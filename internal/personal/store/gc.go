package store

import (
	"context"
	"fmt"
	"time"
)

// GCReport is what a garbage collection removes, or would remove on a dry run.
type GCReport struct {
	// Sessions last updated before the cutoff and not kept (newest first).
	Sessions []Session
	// Events counts the observability rows removed with those sessions plus
	// older rows whose session no longer exists.
	Events int64
	// SnapshotBytes is the on-disk size of snapshots below the sessions removed
	// by this report. It is populated by the CLI, which owns that file root.
	SnapshotBytes int64
	// Applied is false for a dry run.
	Applied bool
}

// CollectGarbage removes sessions not updated since cutoff (and the sub-agent
// sessions they started), with their turns and entries (ON DELETE CASCADE)
// and their events, plus events older than
// cutoff that belong to no session. keep lists session IDs never removed
// (sessions with a live process). Without apply nothing is written. After a
// removal the file is VACUUMed so the space is returned to the disk.
func (s *Store) CollectGarbage(ctx context.Context, cutoff time.Time, keep map[string]bool, apply bool) (GCReport, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return GCReport{}, err
	}
	var report GCReport
	ids := make([]any, 0)
	// A sub-agent session goes with its parent: removed when the parent is,
	// kept while the parent stays; one whose parent is already gone is
	// judged by its own age.
	exists, removed := map[string]bool{}, map[string]bool{}
	for _, session := range sessions {
		exists[session.ID] = true
	}
	stale := func(session Session) bool { return session.UpdatedAt.Before(cutoff) && !keep[session.ID] }
	for _, session := range sessions {
		if parent := ParentSession(session); IsAgentSession(session) && exists[parent] {
			continue
		}
		if stale(session) {
			removed[session.ID] = true
		}
	}
	for _, session := range sessions {
		parent := ParentSession(session)
		child := IsAgentSession(session) && exists[parent]
		if (child && removed[parent] && !keep[session.ID]) || (!child && removed[session.ID]) {
			report.Sessions = append(report.Sessions, session)
			ids = append(ids, session.ID)
		}
	}
	cut := formatTime(cutoff)
	// Events of the chosen sessions, and stale events of sessions already gone.
	where := `(started_at < ? AND session_id NOT IN (SELECT id FROM sessions))`
	args := []any{cut}
	if len(ids) > 0 {
		where = `(session_id IN (` + placeholders(len(ids)) + `)) OR ` + where
		args = append(append([]any(nil), ids...), cut)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE `+where, args...).Scan(&report.Events); err != nil {
		return GCReport{}, fmt.Errorf("count events: %w", err)
	}
	if !apply || (len(ids) == 0 && report.Events == 0) {
		report.Applied = apply
		return report, nil
	}

	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return GCReport{}, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	// Events first: the orphan clause must still see the sessions being removed.
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE `+where, args...); err != nil {
		return GCReport{}, fmt.Errorf("delete events: %w", err)
	}
	if len(ids) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id IN (`+placeholders(len(ids))+`)`, ids...); err != nil {
			return GCReport{}, fmt.Errorf("delete sessions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return GCReport{}, err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return GCReport{}, fmt.Errorf("vacuum: %w", err)
	}
	report.Applied = true
	return report, nil
}
