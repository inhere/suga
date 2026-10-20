package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Tool results are no longer indexed (ftsIndexedTypes), yet a ledger written by
// v0.35 or older still holds the rows the historical backfill wrote for them.
// The delete pass (`suga data shrink-fts`, removed in v0.41.0) is gone; what
// stays is the notice a search carries while such a ledger is in use, so a
// search that only covers conversation rows does not read as "not found".

// FTSShrinkPending reports whether the ledger still holds tool rows in the
// index: the search notice tells the model that a search covers user and AI
// messages only once those rows are gone. It is asked once per store handle
// (see noteShrinkPending), so the row scan is not run on every search.
func (s *Store) FTSShrinkPending(ctx context.Context) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM entries_fts WHERE role = 'tool' LIMIT 1`).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil // no tool row left: a fresh ledger or a cleaned one
	case err != nil:
		return false, fmt.Errorf("check tool rows in entries_fts: %w", err)
	}
	return true, nil
}

// FTSShrinkPendingNotice is the note a search result carries while the ledger
// still holds tool rows in the index: a search also matches tool output that the
// new index no longer covers, so "not found" would be read too strongly.
const FTSShrinkPendingNotice = "历史 tool 输出仍在索引中（用 v0.40 的 suga data shrink-fts 可清理），清理后检索只覆盖用户与 AI 消息"

// noteShrinkPending returns FTSShrinkPendingNotice while entries_fts still
// holds tool rows, plain text on a ledger that never indexed one. The answer is
// computed once per store handle: only another process's maintenance command
// can change it, and a search must not pay for the check every time.
func (s *Store) noteShrinkPending(ctx context.Context) string {
	s.shrinkMu.Lock()
	defer s.shrinkMu.Unlock()
	if s.shrinkPending == nil {
		pending, err := s.FTSShrinkPending(ctx)
		if err != nil {
			return "" // a notice is never worth failing a search for
		}
		s.shrinkPending = &pending
	}
	if *s.shrinkPending {
		return FTSShrinkPendingNotice
	}
	return ""
}

// tableExists reports whether the schema holds that table.
func (s *Store) tableExists(ctx context.Context, name string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("look up table %s: %w", name, err)
	}
	return true, nil
}
