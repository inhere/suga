package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	memorySearchDefaultLimit = 10
	memorySearchMaxLimit     = 50
)

// MemorySource is one files-backed entry projected into the derived FTS index.
// Text is deliberately supplied by the caller: Markdown remains the source of
// truth and the store only owns the rebuildable SQLite projection.
type MemorySource struct {
	ID      string
	Text    string
	Updated time.Time
}

// MemoryHit is a ranked hit from memory_fts. The adapter fills the entry's
// title and kind from the same enumeration used to build the source rows.
type MemoryHit struct {
	ID      string
	Snippet string
	Updated time.Time
}

// SyncMemoryIndex incrementally makes memory_fts match sources for scope. A
// single transaction makes a search see either the old projection or the new
// one; changed rows are deleted before insertion because FTS5 has no ordinary
// UPDATE semantics for its UNINDEXED metadata columns.
func (s *Store) SyncMemoryIndex(ctx context.Context, scope string, sources []MemorySource) (added, removed int, err error) {
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin memory index sync: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
		scope UNINDEXED,
		entry_id UNINDEXED,
		text,
		content_hash UNINDEXED,
		updated UNINDEXED,
		tokenize='trigram'
	)`); err != nil {
		return 0, 0, fmt.Errorf("ensure memory index: %w", err)
	}

	type existingRow struct {
		hash    string
		updated string
	}
	existing := map[string]existingRow{}
	rows, err := tx.QueryContext(ctx, `SELECT entry_id, content_hash, updated FROM memory_fts WHERE scope = ?`, scope)
	if err != nil {
		return 0, 0, fmt.Errorf("read memory index: %w", err)
	}
	for rows.Next() {
		var id, hash, updated string
		if err := rows.Scan(&id, &hash, &updated); err != nil {
			_ = rows.Close()
			return 0, 0, fmt.Errorf("read memory index row: %w", err)
		}
		existing[id] = existingRow{hash: hash, updated: updated}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, fmt.Errorf("read memory index rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, fmt.Errorf("close memory index rows: %w", err)
	}

	seen := make(map[string]bool, len(sources))
	for _, src := range sources {
		if seen[src.ID] {
			continue
		}
		seen[src.ID] = true
		hash := memorySourceHash(src.Text)
		updated := formatTime(src.Updated)
		old, ok := existing[src.ID]
		if ok && old.hash == hash && old.updated == updated {
			continue
		}
		if ok {
			if _, err := tx.ExecContext(ctx, `DELETE FROM memory_fts WHERE scope = ? AND entry_id = ?`, scope, src.ID); err != nil {
				return 0, 0, fmt.Errorf("replace memory index row %s: %w", src.ID, err)
			}
			removed++
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_fts(scope, entry_id, text, content_hash, updated) VALUES (?, ?, ?, ?, ?)`,
			scope, src.ID, src.Text, hash, updated); err != nil {
			return 0, 0, fmt.Errorf("insert memory index row %s: %w", src.ID, err)
		}
		added++
	}
	for id := range existing {
		if seen[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_fts WHERE scope = ? AND entry_id = ?`, scope, id); err != nil {
			return 0, 0, fmt.Errorf("remove memory index row %s: %w", id, err)
		}
		removed++
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit memory index sync: %w", err)
	}
	return added, removed, nil
}

func memorySourceHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// SearchMemoryIndex searches one scope with the same trigram/short-term split
// as session search. Long terms use FTS5 bm25 ranking; one- and two-rune terms
// remain LIKE filters so CJK and short identifiers are still searchable.
func (s *Store) SearchMemoryIndex(ctx context.Context, scope, q string, limit int) ([]MemoryHit, error) {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return nil, errors.New("search query is required")
	}
	if limit <= 0 {
		limit = memorySearchDefaultLimit
	}
	if limit > memorySearchMaxLimit {
		limit = memorySearchMaxLimit
	}

	matchTerms := make([]string, 0, len(terms))
	likeTerms := make([]string, 0, len(terms))
	for _, term := range terms {
		if runeCount(term) >= minMatchRunes {
			matchTerms = append(matchTerms, term)
		} else {
			likeTerms = append(likeTerms, term)
		}
	}

	where := `scope = ?`
	args := []any{scope}
	columns, order := `text`, `updated DESC, entry_id`
	if len(matchTerms) > 0 {
		columns = `snippet(memory_fts, 2, '', '', '…', 64)`
		where = joinWhere(where, `memory_fts MATCH ?`)
		args = append(args, ftsMatchExpr(matchTerms))
		order = `bm25(memory_fts), updated DESC, entry_id`
	}
	for _, term := range likeTerms {
		where = joinWhere(where, `text LIKE ? ESCAPE char(92)`)
		args = append(args, "%"+escapeLike(term)+"%")
	}
	args = append(args, limit)
	query := `SELECT entry_id, ` + columns + `, updated FROM memory_fts WHERE ` + where + ` ORDER BY ` + order + ` LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search memory index: %w", err)
	}
	defer rows.Close()

	out := []MemoryHit{}
	for rows.Next() {
		var hit MemoryHit
		var updated string
		if err := rows.Scan(&hit.ID, &hit.Snippet, &updated); err != nil {
			return nil, fmt.Errorf("scan memory index hit: %w", err)
		}
		hit.Updated = parseTime(updated)
		if len(matchTerms) == 0 {
			hit.Snippet = likeSnippet(hit.Snippet, terms[0])
		}
		out = append(out, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read memory index hits: %w", err)
	}
	return out, nil
}

func runeCount(s string) int { return len([]rune(s)) }
