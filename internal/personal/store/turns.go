package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const turnCols = `id, session_id, status, stop_reason, state_json, started_at, ended_at`

func scanTurn(r rowScanner) (Turn, error) {
	var (
		t              Turn
		state          string
		started, ended string
	)
	if err := r.Scan(&t.ID, &t.SessionID, &t.Status, &t.StopReason, &state, &started, &ended); err != nil {
		return Turn{}, err
	}
	if state != "" {
		t.StateJSON = []byte(state)
	}
	t.StartedAt, t.EndedAt = parseTime(started), parseTime(ended)
	return t, nil
}

// CreateTurn inserts a turn. Status defaults to running, StartedAt to now.
func (s *Store) CreateTurn(ctx context.Context, in Turn) error {
	if in.ID == "" || in.SessionID == "" {
		return errors.New("turn id and session id are required")
	}
	if in.Status == "" {
		in.Status = TurnRunning
	}
	if in.StartedAt.IsZero() {
		in.StartedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO turns(`+turnCols+`) VALUES (?,?,?,?,?,?,?)`,
		in.ID, in.SessionID, in.Status, in.StopReason, string(in.StateJSON), formatTime(in.StartedAt), formatTime(in.EndedAt))
	if err != nil {
		if _, gerr := s.GetSession(ctx, in.SessionID); errors.Is(gerr, ErrSessionNotFound) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("create turn %q: %w", in.ID, err)
	}
	return nil
}

// GetTurn returns the turn or ErrTurnNotFound.
func (s *Store) GetTurn(ctx context.Context, id string) (Turn, error) {
	t, err := scanTurn(s.db.QueryRowContext(ctx, `SELECT `+turnCols+` FROM turns WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Turn{}, ErrTurnNotFound
	}
	return t, err
}

// ListTurns returns a session's turns oldest first, optionally filtered by status.
func (s *Store) ListTurns(ctx context.Context, sessionID string, statuses ...string) ([]Turn, error) {
	q := `SELECT ` + turnCols + ` FROM turns WHERE session_id = ?`
	args := []any{sessionID}
	if len(statuses) > 0 {
		q += ` AND status IN (` + placeholders(len(statuses)) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	// Ties on started_at are broken by rowid (the insert order), so turns
	// created within the same stored instant keep their creation order even
	// though ids are random.
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY started_at ASC, rowid ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list turns: %w", err)
	}
	defer rows.Close()
	out := []Turn{}
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTurn writes status, stop_reason and ended_at (state_json is owned by
// UpsertTurnState and left untouched).
func (s *Store) UpdateTurn(ctx context.Context, t Turn) error {
	res, err := s.db.ExecContext(ctx, `UPDATE turns SET status=?, stop_reason=?, ended_at=? WHERE id=?`,
		t.Status, t.StopReason, formatTime(t.EndedAt), t.ID)
	if err != nil {
		return fmt.Errorf("update turn %q: %w", t.ID, err)
	}
	return requireAffected(res, ErrTurnNotFound)
}

// UpsertTurnState stores the agentcore RunState snapshot of an existing turn.
func (s *Store) UpsertTurnState(ctx context.Context, turnID string, stateJSON []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE turns SET state_json = ? WHERE id = ?`, string(stateJSON), turnID)
	if err != nil {
		return fmt.Errorf("save turn state %q: %w", turnID, err)
	}
	return requireAffected(res, ErrTurnNotFound)
}
