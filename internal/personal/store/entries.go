package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppendEntries appends a batch to the session log in one transaction and
// returns the stored entries with their assigned seq (contiguous, per session).
func (s *Store) AppendEntries(ctx context.Context, sessionID string, in ...NewEntry) ([]Entry, error) {
	if len(in) == 0 {
		return nil, nil
	}
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()
	// Write first: takes the write lock before MAX(seq) is read, so two
	// processes appending to one session cannot compute the same seq.
	res, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID)
	if err != nil {
		return nil, err
	}
	if err := requireAffected(res, ErrSessionNotFound); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(in))
	for _, e := range in {
		if e.Type == "" {
			return nil, errors.New("entry type is required")
		}
		body := e.JSON
		if len(body) == 0 {
			body = json.RawMessage("{}")
		}
		var seq int64
		err := tx.QueryRowContext(ctx, `INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
			SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ? FROM entries WHERE session_id = ?
			RETURNING seq`, sessionID, e.TurnID, e.Type, string(body), formatTime(now), sessionID).Scan(&seq)
		if err != nil {
			return nil, fmt.Errorf("append entry: %w", err)
		}
		// Same transaction: the entry and its full-text row commit together
		// (skipped by the SQL for non-indexed types and empty text, see fts.go).
		if _, err := tx.ExecContext(ctx, ftsInsertEntrySQL, sessionID, seq); err != nil {
			return nil, fmt.Errorf("index entry: %w", err)
		}
		out = append(out, Entry{SessionID: sessionID, Seq: seq, TurnID: e.TurnID, Type: e.Type, JSON: body, CreatedAt: now.UTC()})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanEntry(r rowScanner) (Entry, error) {
	var (
		e       Entry
		body    string
		created string
	)
	if err := r.Scan(&e.SessionID, &e.Seq, &e.TurnID, &e.Type, &body, &created); err != nil {
		return Entry{}, err
	}
	e.JSON, e.CreatedAt = json.RawMessage(body), parseTime(created)
	return e, nil
}

const entryCols = `session_id, seq, turn_id, type, json, created_at`

// ListEntries returns entries ordered by seq, narrowed by f.
func (s *Store) ListEntries(ctx context.Context, sessionID string, f EntryFilter) ([]Entry, error) {
	q := `SELECT ` + entryCols + ` FROM entries WHERE session_id = ? AND seq > ?`
	args := []any{sessionID, f.SinceSeq}
	if f.TurnID != "" {
		q += ` AND turn_id = ?`
		args = append(args, f.TurnID)
	}
	if len(f.Types) > 0 {
		q += ` AND type IN (` + placeholders(len(f.Types)) + `)`
		for _, t := range f.Types {
			args = append(args, t)
		}
	}
	q += ` ORDER BY seq ASC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEntryRefs returns the link fields of every entry of a session, oldest
// first, with the bodies left in the store (see EntryRef).
func (s *Store) ListEntryRefs(ctx context.Context, sessionID string) ([]EntryRef, error) {
	// The call id of a message lives under ToolCall (the call) or ToolResult
	// (the result); other entry types carry none. json_valid guards the
	// extraction: a foreign row must not fail the whole page.
	q := `SELECT seq, turn_id, type, created_at, CASE WHEN json_valid(json)
		THEN COALESCE(json_extract(json, '$.ToolCall.CallID'), json_extract(json, '$.ToolResult.CallID')) END
		FROM entries WHERE session_id = ? ORDER BY seq ASC`
	rows, err := s.db.QueryContext(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list entry refs: %w", err)
	}
	defer rows.Close()
	out := []EntryRef{}
	for rows.Next() {
		var (
			r       EntryRef
			callID  sql.NullString
			created string
		)
		if err := rows.Scan(&r.Seq, &r.TurnID, &r.Type, &created, &callID); err != nil {
			return nil, err
		}
		r.CreatedAt, r.CallID = parseTime(created), callID.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestEntry returns the highest-seq entry of the given type, or (nil, nil)
// when the session has none.
func (s *Store) LatestEntry(ctx context.Context, sessionID, typ string) (*Entry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+entryCols+` FROM entries WHERE session_id = ? AND type = ? ORDER BY seq DESC LIMIT 1`, sessionID, typ)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
