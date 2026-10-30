package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AppendEvent inserts one event and returns it with its assigned ID.
func (s *Store) AppendEvent(ctx context.Context, e Event) (Event, error) {
	if e.Kind == "" {
		return e, errors.New("event kind is required")
	}
	if e.StartedAt.IsZero() {
		e.StartedAt = time.Now()
	}
	attrs := "{}"
	if e.Attrs != nil {
		b, err := json.Marshal(e.Attrs)
		if err != nil {
			return e, err
		}
		attrs = string(b)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO events(session_id, turn_id, kind, name, status, started_at, duration_ms, ttft_ms,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, error, attrs_json) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.SessionID, e.TurnID, e.Kind, e.Name, e.Status, formatTime(e.StartedAt), e.DurationMs, e.TTFTMs,
		e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheWriteTokens, e.Error, attrs)
	if err != nil {
		return e, fmt.Errorf("append event: %w", err)
	}
	e.ID, _ = res.LastInsertId()
	return e, nil
}

// ListEvents returns events oldest first (started_at, id), narrowed by f.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	q := `SELECT id, session_id, turn_id, kind, name, status, started_at, duration_ms, ttft_ms,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, error, attrs_json FROM events WHERE 1=1`
	var args []any
	if f.SessionID != "" && f.WithAgents {
		q += ` AND (session_id = ? OR session_id IN (SELECT id FROM sessions WHERE json_extract(meta_json, '$.` + MetaParentSession + `') = ?))`
		args = append(args, f.SessionID, f.SessionID)
	} else if f.SessionID != "" {
		q += ` AND session_id = ?`
		args = append(args, f.SessionID)
	}
	if f.TurnID != "" {
		q += ` AND turn_id = ?`
		args = append(args, f.TurnID)
	}
	if len(f.Kinds) > 0 {
		q += ` AND kind IN (` + placeholders(len(f.Kinds)) + `)`
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if !f.Since.IsZero() {
		q += ` AND started_at >= ?`
		args = append(args, formatTime(f.Since))
	}
	// id is the events rowid (INTEGER PRIMARY KEY AUTOINCREMENT), so a tie on
	// started_at is ordered by creation, never by a random turn or session id.
	if f.AfterID > 0 {
		q += ` AND id > ?`
		args = append(args, f.AfterID)
	}
	if f.ByID || f.AfterID > 0 {
		q += ` ORDER BY id ASC`
	} else {
		q += ` ORDER BY started_at ASC, id ASC`
	}
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var (
			e       Event
			started string
			attrs   string
		)
		if err := rows.Scan(&e.ID, &e.SessionID, &e.TurnID, &e.Kind, &e.Name, &e.Status, &started, &e.DurationMs, &e.TTFTMs,
			&e.InputTokens, &e.OutputTokens, &e.CacheReadTokens, &e.CacheWriteTokens, &e.Error, &attrs); err != nil {
			return nil, err
		}
		e.StartedAt = parseTime(started)
		if attrs != "" && attrs != "{}" {
			if err := json.Unmarshal([]byte(attrs), &e.Attrs); err != nil {
				return nil, fmt.Errorf("decode event attrs: %w", err)
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateEventEntrySeq fills the sequence of a pending injected entry after the
// next turn stores it. The event is written when undo completes, while the
// entry itself is deliberately deferred until the user starts another turn.
func (s *Store) UpdateEventEntrySeq(ctx context.Context, id, seq int64) error {
	if id <= 0 || seq <= 0 {
		return errors.New("event and entry sequence are required")
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT attrs_json FROM events WHERE id = ?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	attrs := map[string]any{}
	if raw != "" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
			return fmt.Errorf("decode event attrs: %w", err)
		}
	}
	attrs["entry_seq"] = seq
	encoded, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE events SET attrs_json = ? WHERE id = ?`, string(encoded), id)
	if err != nil {
		return fmt.Errorf("update event %d: %w", id, err)
	}
	return requireAffected(res, sql.ErrNoRows)
}

// SumUsageTree is SumUsage over the session and its sub-agent sessions
// (meta parent_session_id = sessionID): what the session spent in all, as its
// cumulative usage reports it.
func (s *Store) SumUsageTree(ctx context.Context, sessionID string) (Usage, error) {
	var u Usage
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0),
		COALESCE(SUM(json_extract(attrs_json, '$.cost_usd')),0) FROM events WHERE kind = ? AND (session_id = ?
		OR session_id IN (SELECT id FROM sessions WHERE json_extract(meta_json, '$.`+MetaParentSession+`') = ?))`,
		EventModel, sessionID, sessionID).Scan(&u.Calls, &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens, &u.CostUSD)
	return u, err
}

// SumUsage totals tokens over kind=model events only; turn/tool events carry
// no per-call usage of their own, so nothing is counted twice. Cost is the sum
// of the optional attrs.cost_usd of those events.
func (s *Store) SumUsage(ctx context.Context, sessionID string) (Usage, error) {
	var u Usage
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0),
		COALESCE(SUM(json_extract(attrs_json, '$.cost_usd')),0) FROM events WHERE session_id = ? AND kind = ?`,
		sessionID, EventModel).Scan(&u.Calls, &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens, &u.CostUSD)
	return u, err
}
