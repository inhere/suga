package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// gcCount returns the row count of a table (or of a query).
func gcCount(t *testing.T, s *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	assert.Require(t, assert.NoErr(t, s.DB().QueryRow(query, args...).Scan(&n)))
	return n
}

// gcAgeSessions moves every session of the ledger to one timestamp: AppendEntries
// bumps updated_at to now, so a fixture ages its sessions afterwards.
func gcAgeSessions(t *testing.T, s *Store, at time.Time) {
	t.Helper()
	_, err := s.DB().Exec(`UPDATE sessions SET updated_at = ?`, formatTime(at))
	assert.Require(t, assert.NoErr(t, err))
}

// gcMakeSessions creates n sessions with a tool entry each (one bulk insert, so
// the fixture stays fast) and ages them; it returns their ids.
func gcMakeSessions(t *testing.T, s *Store, n int, age time.Duration) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, gcSessionID(i))
	}
	_, err := s.DB().ExecContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < ?)
		INSERT INTO sessions(id, title, cwd, status, last_turn_id, created_at, updated_at, meta_json)
		SELECT 's' || printf('%04d', x), '', '', 'open', '', 't', 't', '{}' FROM c`, n)
	assert.Require(t, assert.NoErr(t, err))
	_, err = s.DB().ExecContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < ?)
		INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
		SELECT 's' || printf('%04d', x), 1, '', 'tool', '{"Role":"tool","Content":"' || printf('%s %06d', '`+
		strings.Repeat("lorem ipsum dolor sit amet ", 40)+`', x) || '"}', 't' FROM c`, n)
	assert.Require(t, assert.NoErr(t, err))
	// One bulk pass instead of one indexed write per row.
	_, err = s.DB().ExecContext(ctx, `INSERT INTO entries_fts(session_id, seq, role, text) `+ftsSourceSelect(""))
	assert.Require(t, assert.NoErr(t, err))
	gcAgeSessions(t, s, time.Now().Add(-age))
	return ids
}

// gcSessionID is the id of the i-th session of the fixtures.
func gcSessionID(i int) string { return fmt.Sprintf("s%04d", i) }

// TestCollectGarbageBatchesSessions: a cleanup of hundreds of sessions runs in
// short transactions of gcBatchSessions sessions and can be interrupted: a
// committed batch stays, a later run removes the rest. Every committed batch
// holds a whole number of batches, which is what "可靠中断" means here.
func TestCollectGarbageBatchesSessions(t *testing.T) {
	ctx := context.Background()
	const total = 3 * gcBatchSessions
	cutoff := time.Now().Add(-24 * time.Hour)

	t.Run("interrupted run keeps whole committed batches, the rest stays for the next", func(t *testing.T) {
		s := openTemp(t)
		defer s.Close()
		gcMakeSessions(t, s, total, 48*time.Hour)

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		// Observe the first committed batch on a second handle and stop the run
		// between two batches, the way Ctrl-C does.
		watcher, err := Open(s.Path())
		assert.Require(t, assert.NoErr(t, err))
		defer watcher.Close()
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			for {
				select {
				case <-runCtx.Done():
					return
				case <-time.After(time.Millisecond):
				}
				if gcCount(t, s, `SELECT COUNT(*) FROM sessions`) < total {
					cancel()
					return
				}
			}
		}()
		_, err = s.CollectGarbage(runCtx, cutoff, nil, true)
		<-stopped
		if err != nil {
			assert.ErrIs(t, err, context.Canceled)
		}
		left := gcCount(t, s, `SELECT COUNT(*) FROM sessions`)
		t.Logf("interrupted with %d of %d sessions left", left, total)
		assert.Eq(t, int64(0), left%gcBatchSessions, "only whole batches may be committed")
		assert.True(t, left <= total)
		// The index never keeps a row of a session that is gone: what a batch
		// deleted, it deleted everywhere.
		orphans := gcCount(t, s, `SELECT COUNT(*) FROM entries_fts WHERE session_id NOT IN (SELECT id FROM sessions)`)
		assert.Eq(t, int64(0), orphans)

		report, err := s.CollectGarbage(ctx, cutoff, nil, true)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, report.Applied)
		assert.Eq(t, int64(0), gcCount(t, s, `SELECT COUNT(*) FROM sessions`))
		assert.Eq(t, int64(0), gcCount(t, s, `SELECT COUNT(*) FROM entries_fts`))
		assert.Eq(t, left, int64(len(report.Sessions)), "the second run reports exactly what was left")
	})

	t.Run("a dry run writes nothing", func(t *testing.T) {
		s := openTemp(t)
		defer s.Close()
		gcMakeSessions(t, s, 2*gcBatchSessions, 48*time.Hour)

		report, err := s.CollectGarbage(ctx, cutoff, nil, false)
		assert.NoErr(t, err)
		assert.Eq(t, false, report.Applied)
		assert.Eq(t, 2*gcBatchSessions, len(report.Sessions))
		assert.Eq(t, int64(2*gcBatchSessions), gcCount(t, s, `SELECT COUNT(*) FROM sessions`))
	})
}

// TestCollectGarbageFTSRows: removing a session removes its index rows (through
// the batch path and through the single DeleteSession trigger), while the other
// sessions keep their own rows and stay searchable.
func TestCollectGarbageFTSRows(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	defer s.Close()
	old := time.Now().Add(-48 * time.Hour)
	for _, id := range []string{"gone", "stays", "single"} {
		assert.Require(t, assert.NoErr(t, s.CreateSession(ctx, Session{ID: id, CreatedAt: old, UpdatedAt: old})))
		appendAll(t, s, id, NewEntry{Type: EntryTool, JSON: []byte(`{"Role":"tool","Content":"needle-of-` + id + `"}`)})
	}
	// Only two of the three are stale; "stays" keeps its rows (and is searched
	// while the others are gone).
	_, err := s.DB().Exec(`UPDATE sessions SET updated_at = ? WHERE id IN ('gone', 'single')`, formatTime(old))
	assert.Require(t, assert.NoErr(t, err))
	trigger := gcTriggerSQL(t, s)

	report, err := s.CollectGarbage(ctx, time.Now().Add(-24*time.Hour), nil, true)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 2, len(report.Sessions)) // "gone" and "single" are stale
	assert.Eq(t, int64(0), gcCount(t, s, `SELECT COUNT(*) FROM entries_fts WHERE session_id = ?`, "gone"))
	assert.Eq(t, int64(1), gcCount(t, s, `SELECT COUNT(*) FROM entries_fts WHERE session_id = ?`, "stays"))
	hits, err := s.SearchEntries(ctx, EntrySearch{Query: "needle-of-gone"})
	assert.NoErr(t, err)
	assert.Empty(t, hits)
	hits, err = s.SearchEntries(ctx, EntrySearch{Query: "needle-of-stays"})
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(hits))

	// The delete trigger is back in place, byte for byte, after the batches that
	// suspended it.
	assert.Eq(t, trigger, gcTriggerSQL(t, s))
	triggerCount := gcCount(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, ftsSessionDeleteTrigger)
	assert.Eq(t, int64(1), triggerCount)

	// The single-session path (the trigger) cleans the index too.
	assert.NoErr(t, s.DeleteSession(ctx, "stays"))
	assert.Eq(t, int64(0), gcCount(t, s, `SELECT COUNT(*) FROM entries_fts WHERE session_id = ?`, "stays"))
}

// gcTriggerSQL returns the stored definition of the session delete trigger.
func gcTriggerSQL(t *testing.T, s *Store) string {
	t.Helper()
	var ddl string
	err := s.DB().QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, ftsSessionDeleteTrigger).Scan(&ddl)
	assert.Require(t, assert.NoErr(t, err))
	return ddl
}

// TestCollectGarbageDoesNotVacuum: `gc --apply` deletes the rows and stops
// there. The file keeps the freed pages (freelist) until `suga gc --vacuum`, so
// the two results are reported separately (design 0.3, P3).
func TestCollectGarbageDoesNotVacuum(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	defer s.Close()
	gcMakeSessions(t, s, 20, 48*time.Hour)
	// Grow the ledger with entries the removal discards, so the file must have
	// free pages afterwards.
	_, err := s.DB().ExecContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 400)
		INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
		SELECT 's0001', 100 + x, '', 'tool', '{"Role":"tool","Content":"' || printf('%s', substr(hex(randomblob(2048)), 1, 2000)) || '"}', 't' FROM c`)
	assert.Require(t, assert.NoErr(t, err))
	gcAgeSessions(t, s, time.Now().Add(-48*time.Hour))
	before := DatabaseFileBytes(s.Path())

	assert.Require(t, assert.NoErr(t, func() error {
		_, err := s.CollectGarbage(ctx, time.Now().Add(-24*time.Hour), nil, true)
		return err
	}()))
	assert.Eq(t, int64(0), gcCount(t, s, `SELECT COUNT(*) FROM sessions`))
	freelist := gcCount(t, s, `PRAGMA freelist_count`)
	t.Logf("file %d bytes before, freelist %d pages after the apply", before, freelist)
	assert.True(t, freelist > 0, "freed pages must still sit in the file: apply does not VACUUM")
	after := DatabaseFileBytes(s.Path())
	assert.True(t, after >= before*9/10, "apply must not rewrite the file: "+fmt.Sprint(before, after))
}

// TestGCLargeLedgerDeleteDuration removes 200 of 300 sessions of a 20,000-entry
// ledger whose index content is 78 MB (the size class of a real ledger after the
// P1 shrink). The rowids of a batch's index rows are collected with one scan and
// the session delete trigger is suspended inside the batch transaction, so the
// cost per batch is one scan instead of one per session (the design's
// O(sessions x index rows)). The bound is the reverse-verification gate: the
// single transaction, per-session-trigger version of v0.31.x exceeds it.
func TestGCLargeLedgerDeleteDuration(t *testing.T) {
	if raceEnabled {
		// The race detector makes the 20k-entry fixture alone take ~40s; the
		// timing budget is checked by the plain test run instead.
		t.Skip("wall-clock budget is not meaningful under -race")
	}
	ctx := context.Background()
	s := openTemp(t)
	defer s.Close()
	const (
		sessions = 300
		kept     = 100
		rows     = 20000
	)
	// 20,000 tool entries spread over 300 sessions (s0001..s0300); the 100 newest
	// (s0201..s0300) stay, the 200 older ones go with the cut-off.
	start := time.Now()
	_, err := s.DB().ExecContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < ?)
		INSERT INTO sessions(id, title, cwd, status, last_turn_id, created_at, updated_at, meta_json)
		SELECT 's' || printf('%04d', x), '', '', 'open', '', 't', 't', '{}' FROM c`, sessions)
	assert.Require(t, assert.NoErr(t, err))
	_, err = s.DB().ExecContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < ?)
		INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
		SELECT 's' || printf('%04d', ((x - 1) % ?) + 1), x, '', 'tool', '{"Role":"tool","Content":"' || printf('%06d %s', x, `+
		`'`+strings.Repeat("lorem ipsum dolor sit amet consectetur ", gcBenchTextRepeat)+`') || '"}', 't' FROM c`, rows, sessions)
	assert.Require(t, assert.NoErr(t, err))
	_, err = s.DB().ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE CAST(substr(id, 2) AS INTEGER) > ?`,
		formatTime(time.Now()), sessions-kept)
	assert.Require(t, assert.NoErr(t, err))
	_, err = s.DB().ExecContext(ctx, `INSERT INTO entries_fts(session_id, seq, role, text) `+ftsSourceSelect(""))
	assert.Require(t, assert.NoErr(t, err))
	fixture := time.Since(start)
	t.Logf("fixture built in %v: %d entries, %d index rows", fixture, gcCount(t, s, `SELECT COUNT(*) FROM entries`),
		gcCount(t, s, `SELECT COUNT(*) FROM entries_fts`))

	start = time.Now()
	report, err := s.CollectGarbage(ctx, time.Now().Add(-24*time.Hour), nil, true)
	elapsed := time.Since(start)
	assert.Require(t, assert.NoErr(t, err))
	t.Logf("removed %d sessions in %v (%d batches of up to %d)", len(report.Sessions), elapsed,
		(len(report.Sessions)+gcBatchSessions-1)/gcBatchSessions, gcBatchSessions)
	assert.Eq(t, sessions-kept, len(report.Sessions))
	assert.Eq(t, int64(kept), gcCount(t, s, `SELECT COUNT(*) FROM sessions`))
	assert.Eq(t, int64(0), gcCount(t, s, `SELECT COUNT(*) FROM entries_fts WHERE session_id NOT IN (SELECT id FROM sessions)`))
	assert.True(t, elapsed < gcLargeLedgerBudget, "removing 200 sessions of a 20k-entry ledger took "+elapsed.String())
}

// gcBenchTextRepeat sizes the tool body of the large-ledger fixture: the text
// repeat times ~39 bytes per repeat, so 100 is about 3.9 KB per entry (78 MB of
// index content over 20,000 rows, the order of the real ledger after the P1
// shrink).
const gcBenchTextRepeat = 100

// gcLargeLedgerBudget is the wall-clock bound of the large-ledger removal. The
// bound leaves room for a slow CI disk and still fails the pre-P3
// implementation, which VACUUMed the file and scanned the index content once
// per session (measured: see the reverse verification in the delivery report).
const gcLargeLedgerBudget = 25 * time.Second
