package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ForkInput is the complete payload of a forked session (session rewind design
// 0.2, R6). The caller maps everything beforehand: new turn ids, entries
// renumbered 1..n with their original CreatedAt, compaction and clear records
// pointing at the new seqs.
type ForkInput struct {
	Session Session
	// Turns are stored with their own StartedAt/EndedAt/status; an empty
	// SessionID is filled with Session.ID.
	Turns []Turn
	// Entries must carry Seq 1..n in order and a CreatedAt.
	Entries []NewEntry
}

// CreateForkedSession writes the session, its turns and its entries in one write
// transaction. Entries go through the same in-transaction path as AppendEntries
// (insertEntryTx), so a fork row is indexed in entries_fts and a big tool or
// assistant body moves to the new session's body file exactly as it would in a
// normal append, with the original created_at kept. Any failure rolls the
// transaction back and drops the body file this call created, so a failed fork
// leaves neither rows nor files behind. The seq check (1..n, contiguous) runs
// before the transaction opens.
func (s *Store) CreateForkedSession(ctx context.Context, in ForkInput) error {
	if in.Session.ID == "" {
		return errors.New("session id is required")
	}
	for i, e := range in.Entries {
		if e.Seq != int64(i+1) {
			return fmt.Errorf("fork entry %d has seq %d, want %d", i, e.Seq, i+1)
		}
		if e.CreatedAt.IsZero() {
			return fmt.Errorf("fork entry seq %d has no created_at", e.Seq)
		}
	}
	sess := in.Session
	now := time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = sess.CreatedAt
	}
	if sess.Status == "" {
		sess.Status = SessionOpen
	}
	meta, err := encodeMeta(sess.Meta)
	if err != nil {
		return err
	}

	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	batch := &entryBatch{dropFileIfNew: true}
	committed := false
	defer func() {
		if !committed {
			_ = batch.writer.Close(false)
		}
	}()

	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(`+sessionCols+`) VALUES (?,?,?,?,?,?,?,?)`,
		sess.ID, sess.Title, sess.Cwd, sess.Status, sess.LastTurnID, formatTime(sess.CreatedAt), formatTime(sess.UpdatedAt), meta); err != nil {
		return fmt.Errorf("create session %q: %w", sess.ID, err)
	}
	for _, t := range in.Turns {
		if t.ID == "" {
			return errors.New("fork turn id is required")
		}
		if t.SessionID == "" {
			t.SessionID = sess.ID
		}
		if t.SessionID != sess.ID {
			return fmt.Errorf("fork turn %q belongs to session %q", t.ID, t.SessionID)
		}
		if t.Status == "" {
			t.Status = TurnRunning
		}
		if t.StartedAt.IsZero() {
			t.StartedAt = now
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO turns(`+turnCols+`) VALUES (?,?,?,?,?,?,?)`,
			t.ID, t.SessionID, t.Status, t.StopReason, string(t.StateJSON), formatTime(t.StartedAt), formatTime(t.EndedAt)); err != nil {
			return fmt.Errorf("create turn %q: %w", t.ID, err)
		}
	}
	for _, e := range in.Entries {
		if _, err := s.insertEntryTx(ctx, tx, sess.ID, batch, e, e.Seq, e.CreatedAt); err != nil {
			return err
		}
	}
	if err := s.swapMovedBodiesTx(ctx, tx, sess.ID, batch); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	_ = batch.writer.Close(true)
	return nil
}
