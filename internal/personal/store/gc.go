package store

import (
	"context"
	"database/sql"
	"errors"
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

const (
	// gcBatchSessions is how many sessions one garbage-collection write
	// transaction removes. The batch keeps the write lock short enough for a
	// concurrent chat to commit between two batches, and every committed batch
	// stays when the run is stopped with Ctrl-C (design 0.3, P3).
	gcBatchSessions = 50
	// gcIDChunk caps one `IN (...)` list. One batch of 50 sessions can own far
	// more FTS rows than SQLite takes parameters in a single statement.
	gcIDChunk = 400
	// ftsSessionDeleteTrigger is the trigger the FTS migration installs to drop
	// the index rows of a deleted session (see migrations/20261010-...).
	ftsSessionDeleteTrigger = "entries_fts_session_delete"
)

// CollectGarbage removes sessions not updated since cutoff (and the sub-agent
// sessions they started), with their turns and entries (ON DELETE CASCADE)
// and their events, plus events older than
// cutoff that belong to no session. keep lists session IDs never removed
// (sessions with a live process). Without apply nothing is written.
//
// The removal runs in batches of gcBatchSessions sessions, each in its own
// short write transaction, so a cleanup of hundreds of sessions can be
// interrupted with Ctrl-C and keeps what it already committed. It does NOT
// VACUUM: returning the freed space to the disk is the explicit
// `suga data vacuum`, which reports separately from the deleted rows
// (design 0.3, P3).
func (s *Store) CollectGarbage(ctx context.Context, cutoff time.Time, keep map[string]bool, apply bool) (GCReport, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return GCReport{}, err
	}
	var report GCReport
	// A session is stale when it was not updated since the cutoff and no live
	// process holds it.
	stale := func(session Session) bool { return session.UpdatedAt.Before(cutoff) && !keep[session.ID] }
	chosen := selectGCSessions(sessions, keep, stale)
	report.Sessions = append(report.Sessions, chosen...)
	ids := sessionIDs(chosen)
	cut := formatTime(cutoff)
	// Events of the chosen sessions, and stale events of sessions already gone.
	where := `(started_at < ? AND session_id NOT IN (SELECT id FROM sessions))`
	args := []any{cut}
	if len(ids) > 0 {
		where = `(session_id IN (` + placeholders(len(ids)) + `)) OR ` + where
		args = append(append([]any(nil), toAny(ids)...), cut)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE `+where, args...).Scan(&report.Events); err != nil {
		return GCReport{}, fmt.Errorf("count events: %w", err)
	}
	if !apply || (len(ids) == 0 && report.Events == 0) {
		report.Applied = apply
		return report, nil
	}
	if err := s.deleteSessionsBatched(ctx, ids); err != nil {
		return report, err
	}
	if err := s.deleteOrphanEvents(ctx, cutoff); err != nil {
		return report, err
	}
	report.Applied = true
	return report, nil
}

// selectGCSessions returns the sessions a garbage collection removes: the ones
// remove reports true for, plus the sub-agent sessions of a removed parent.
// A sub-agent session belongs to its parent: it is removed with it and never
// judged on its own while the parent exists; one whose parent is already gone
// is judged by remove like any other session.
func selectGCSessions(sessions []Session, keep map[string]bool, remove func(Session) bool) []Session {
	exists, gone := map[string]bool{}, map[string]bool{}
	for _, session := range sessions {
		exists[session.ID] = true
	}
	for _, session := range sessions {
		if parent := ParentSession(session); IsAgentSession(session) && exists[parent] {
			continue
		}
		if remove(session) {
			gone[session.ID] = true
		}
	}
	out := make([]Session, 0, len(gone))
	for _, session := range sessions {
		parent := ParentSession(session)
		child := IsAgentSession(session) && exists[parent]
		if (child && gone[parent] && !keep[session.ID]) || (!child && gone[session.ID]) {
			out = append(out, session)
		}
	}
	return out
}

// sessionIDs lists the ids of the given sessions.
func sessionIDs(sessions []Session) []string {
	out := make([]string, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, session.ID)
	}
	return out
}

// toAny passes string arguments to a SQL statement.
func toAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

// deleteSessionsBatched removes sessions with their FTS rows and their events,
// gcBatchSessions sessions per write transaction.
//
// The FTS rows are collected with ONE scan per batch and deleted by rowid: the
// session_id column of entries_fts is UNINDEXED, so the delete trigger's
// `DELETE FROM entries_fts WHERE session_id = old.id` scans the whole index
// content once PER SESSION, which is the O(sessions x index rows) stall of
// design 0.3 P3. The trigger is suspended inside the batch transaction and put
// back verbatim before the commit, so a batch of 50 sessions costs one scan
// instead of fifty. The trigger stays in force for every other writer (single
// DeleteSession of any process, older binaries).
//
// A cancelled context stops between batches: every committed batch keeps its
// deletions, the batches that did not run are still there for the next call.
func (s *Store) deleteSessionsBatched(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	hasFTS, err := s.tableExists(ctx, "entries_fts")
	if err != nil {
		return err
	}
	for start := 0; start < len(ids); start += gcBatchSessions {
		if err := ctx.Err(); err != nil {
			return err // resumable: the committed batches stand
		}
		end := min(start+gcBatchSessions, len(ids))
		if err := s.deleteSessionBatch(ctx, ids[start:end], hasFTS); err != nil {
			return err
		}
	}
	return nil
}

// deleteSessionBatch is one short transaction of the loop above: index rows,
// then the sessions (their entries and turns follow by cascade), then their
// events.
func (s *Store) deleteSessionBatch(ctx context.Context, ids []string, hasFTS bool) error {
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if hasFTS {
		triggerDDL, err := suspendFTSSessionDeleteTrigger(ctx, tx)
		if err != nil {
			return err
		}
		rowids, err := ftsRowIDsOfSessions(ctx, tx, ids)
		if err != nil {
			return err
		}
		if err := deleteFTSRows(ctx, tx, rowids); err != nil {
			return err
		}
		if triggerDDL != "" {
			// Put the trigger back before the commit: it must never be missing
			// for a writer outside this transaction (DDL is transactional, so a
			// rollback restores it too).
			if _, err := tx.ExecContext(ctx, triggerDDL); err != nil {
				return fmt.Errorf("restore the %s trigger: %w", ftsSessionDeleteTrigger, err)
			}
		}
	}
	args := toAny(ids)
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id IN (`+placeholders(len(ids))+`)`, args...); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE session_id IN (`+placeholders(len(ids))+`)`, args...); err != nil {
		return fmt.Errorf("delete events of removed sessions: %w", err)
	}
	return tx.Commit()
}

// suspendFTSSessionDeleteTrigger drops the session delete trigger inside tx and
// returns its definition so the caller can restore it before the commit ("" when
// the ledger has no such trigger). The definition is read from sqlite_master
// instead of being spelled out here, so the trigger cannot drift from the
// migration that installs it.
func suspendFTSSessionDeleteTrigger(ctx context.Context, tx *sql.Tx) (string, error) {
	var ddl string
	err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, ftsSessionDeleteTrigger).Scan(&ddl)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("read the %s trigger: %w", ftsSessionDeleteTrigger, err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+ftsSessionDeleteTrigger); err != nil {
		return "", fmt.Errorf("suspend the %s trigger: %w", ftsSessionDeleteTrigger, err)
	}
	return ddl, nil
}

// ftsRowIDsOfSessions collects the entries_fts rowids of these sessions with one
// scan over the index content per gcIDChunk ids (session_id is UNINDEXED, so any
// statement on it is a full scan; one scan per batch instead of one per session
// is the whole point of the batching).
func ftsRowIDsOfSessions(ctx context.Context, tx *sql.Tx, ids []string) ([]int64, error) {
	out := []int64{}
	for start := 0; start < len(ids); start += gcIDChunk {
		end := min(start+gcIDChunk, len(ids))
		rows, err := tx.QueryContext(ctx, `SELECT rowid FROM entries_fts WHERE session_id IN (`+placeholders(end-start)+`)`, toAny(ids[start:end])...)
		if err != nil {
			return nil, fmt.Errorf("collect entries_fts rowids: %w", err)
		}
		for rows.Next() {
			var rowid int64
			if err := rows.Scan(&rowid); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, rowid)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// deleteFTSRows removes index rows by their own rowid, gcIDChunk per statement.
// It also runs FTS5's 'delete' command per row, so the terms of the removed rows
// leave the index with them.
func deleteFTSRows(ctx context.Context, tx *sql.Tx, rowids []int64) error {
	for start := 0; start < len(rowids); start += gcIDChunk {
		end := min(start+gcIDChunk, len(rowids))
		args := make([]any, 0, end-start)
		for _, rowid := range rowids[start:end] {
			args = append(args, rowid)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM entries_fts WHERE rowid IN (`+placeholders(end-start)+`)`, args...); err != nil {
			return fmt.Errorf("delete entries_fts rows: %w", err)
		}
	}
	return nil
}

// deleteOrphanEvents drops the events of sessions that no longer exist and are
// older than cutoff, in its own short transaction. Orphan events belong to no
// session row, so no cascade reaches them.
func (s *Store) deleteOrphanEvents(ctx context.Context, cutoff time.Time) error {
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM events WHERE started_at < ? AND session_id NOT IN (SELECT id FROM sessions)`,
		formatTime(cutoff)); err != nil {
		return fmt.Errorf("delete orphan events: %w", err)
	}
	return tx.Commit()
}
