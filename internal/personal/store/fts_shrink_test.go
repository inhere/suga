package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// Tool results are not indexed any more (the write path skips them), and the
// `suga data shrink-fts` maintenance command deletes the rows the historical
// backfill wrote for them (design 0.3: in-place, batched, resumable). These
// tests cover the write path, the search consequences and the command.

// markedBody builds a body of n characters whose head, middle and tail hold
// distinct keywords (HEADER, MIDDLE, FOOTER), so a search can tell which part
// of it was indexed.
func markedBody(prefix string, n int, fill string) string {
	const markers = 6 + 6 + 6 // HEADER, MIDDLE, FOOTER
	inner := n - len([]rune(prefix)) - markers
	if inner < 0 {
		inner = 0
	}
	head, tail := inner/2, inner-inner/2
	return prefix + "HEADER" + strings.Repeat(fill, head) + "MIDDLE" + strings.Repeat(fill, tail) + "FOOTER"
}

func TestFTSWritePathSkipsToolResults(t *testing.T) {
	ctx := context.Background()

	t.Run("a tool result is not indexed, the conversation is", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		appendAll(t, s, "s1",
			user(t, "user needle"),
			toolMsg(t, "tool needle", "tool needle"),
			assistant(t, "assistant needle"))

		rows := indexedRows(t, s, "s1")
		assert.Require(t, assert.Len(t, rows, 2))
		assert.Eq(t, EntryUser, rows[0].Role)
		assert.Eq(t, EntryAssistant, rows[1].Role)
		assert.Eq(t, []string{"s1/1", "s1/3"}, hitKeys(search(t, s, EntrySearch{Query: "needle"})))
		// Even an explicit tool kind has nothing to match on a new ledger.
		assert.Empty(t, hitKeys(search(t, s, EntrySearch{Query: "needle", Kinds: []string{EntryTool}})))
	})

	t.Run("the full tool output stays in the ledger for recall", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		body := markedBody("tool ", 3000, "x")
		appendAll(t, s, "s1", toolMsg(t, body, body))

		entries, err := s.ListEntries(ctx, "s1", EntryFilter{Types: []string{EntryTool}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, entries, 1))
		assert.StrContains(t, string(entries[0].JSON), "MIDDLE")
	})

	t.Run("compaction and handoff summaries stay searchable", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		appendAll(t, s, "s1",
			NewEntry{Type: EntryCompaction, JSON: []byte(`{"summary":"压缩摘要：检索方案"}`)},
			NewEntry{Type: EntryHandoff, JSON: []byte(`{"source":"x","summary":"交接摘要：检索方案"}`)})
		assert.Eq(t, []string{"s1/1", "s1/2"}, hitKeys(search(t, s, EntrySearch{Query: "检索方案"})))
	})

	t.Run("CJK short and three-rune terms still work", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		body := "工具 开头标记" + strings.Repeat("結", 2000) + "中部标记" + strings.Repeat("結", 2000) + "结尾标记"
		appendAll(t, s, "s1", user(t, body), toolMsg(t, body, body))

		// Two runes take the LIKE path, three or more the trigram MATCH path.
		assert.Eq(t, []string{"s1/1"}, hitKeys(search(t, s, EntrySearch{Query: "中部"})))
		assert.Eq(t, []string{"s1/1"}, hitKeys(search(t, s, EntrySearch{Query: "中部标"})))
		assert.Eq(t, []string{"s1/1"}, hitKeys(search(t, s, EntrySearch{Query: "结尾标记"})))
		assert.Empty(t, hitKeys(search(t, s, EntrySearch{Query: "中部", Kinds: []string{EntryTool}})))
	})

	t.Run("a session with only tool entries has no searchable history", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "tools", "/w")
		appendAll(t, s, "tools", toolMsg(t, "only tool output", "only tool output"))
		ok, err := s.HasSearchableEntries(ctx, EntrySearch{SessionIDs: []string{"tools"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, ok, "tool output is read with recall, not searched")

		appendAll(t, s, "tools", user(t, "now there is prose"))
		ok, err = s.HasSearchableEntries(ctx, EntrySearch{SessionIDs: []string{"tools"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, ok)
	})
}

// legacySession is one session of a ledger written before tool results left
// the index: its entries are written before the FTS migration, which then
// indexes the tool results in FULL.
type legacySession struct {
	id      string
	entries []NewEntry
}

// openLegacyLedger opens a ledger whose entries predate the FTS migration and
// migrates it, so entries_fts holds the full text of every tool result --
// exactly the state `suga data shrink-fts` has to repair.
func openLegacyLedger(t *testing.T, sessions ...legacySession) *Store {
	t.Helper()
	s, _ := openAtCoreSchema(t)
	t.Cleanup(func() { _ = s.Close() })
	for _, sess := range sessions {
		writeHistory(t, s, sess.id, sess.entries)
	}
	assert.Require(t, assert.NoErr(t, s.migrate(context.Background())))
	return s
}

// toolEntries builds n long tool entries for a legacy ledger.
func toolEntries(t *testing.T, n int) []NewEntry {
	t.Helper()
	out := make([]NewEntry, 0, n)
	for i := 0; i < n; i++ {
		body := markedBody(fmt.Sprintf("tool %d ", i), 3000, "x")
		out = append(out, toolMsg(t, body, body))
	}
	return out
}

func TestFTSShrinkCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("status before, during and after", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", append(toolEntries(t, 2),
			user(t, markedBody("u ", 5000, "x")))})
		before, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, before.Started)
		assert.False(t, before.Complete)
		assert.Eq(t, int64(2), before.PendingRows)
		assert.True(t, before.Remaining())
		assert.True(t, before.EstimatedReclaimBytes > 0, "an estimate is printed")

		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(2), report.Rows)
		assert.Eq(t, report.HighWater, report.ScannedRowID)
		assert.True(t, report.Optimized, "a pass that deleted rows merges the index once")
		assert.True(t, report.BytesAfter < report.BytesBefore,
			fmt.Sprintf("entries_fts_data: %d -> %d", report.BytesBefore, report.BytesAfter))

		after, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, after.Started)
		assert.True(t, after.Complete)
		assert.False(t, after.Remaining())
		assert.Eq(t, int64(0), after.PendingRows)
		// Only the user row is left in the index, and search agrees.
		rows := indexedRows(t, s, "old")
		assert.Require(t, assert.Len(t, rows, 1))
		assert.Eq(t, EntryUser, rows[0].Role)
		assert.Eq(t, markedBody("u ", 5000, "x"), rows[0].Text)
		assert.Empty(t, hitKeys(search(t, s, EntrySearch{Query: "tool 0"})))
	})

	t.Run("batches delete every tool row and the checkpoint advances", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", append(toolEntries(t, 7), user(t, "keep this"))})

		var seen []int64
		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2, OnBatch: func(p FTSShrinkProgress) {
			seen = append(seen, p.Rows)
		}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(7), report.Rows)
		// 2,000-row batches over 7 rows: four batches of 2 and one of 1.
		assert.Eq(t, []int64{2, 4, 6, 7}, seen)
		assert.Eq(t, 4, report.Batches)
		rows := indexedRows(t, s, "old")
		assert.Require(t, assert.Len(t, rows, 1))
		assert.Eq(t, "keep this", rows[0].Text)
		assert.Eq(t, report.ScannedRowID, ftsCheckpoint(t, s))
	})

	t.Run("an interrupted pass resumes from its checkpoint", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 10)})

		cancelled, cancel := context.WithCancel(ctx)
		var first FTSShrinkProgress
		report, err := s.ShrinkFTSToolOutputs(cancelled, FTSShrinkOptions{Batch: 2, OnBatch: func(p FTSShrinkProgress) {
			first = p
			if p.Rows >= 4 {
				cancel()
			}
		}})
		assert.ErrIs(t, context.Canceled, err)
		assert.Eq(t, int64(4), first.Rows)
		assert.Eq(t, int64(4), report.Rows)
		assert.True(t, report.ScannedRowID < report.HighWater, "the pass stopped before the high-water mark")
		assert.Eq(t, report.ScannedRowID, ftsCheckpoint(t, s))

		midway, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, midway.Complete)
		assert.Eq(t, int64(6), midway.PendingRows)

		var resumed []int64
		rest, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2, OnBatch: func(p FTSShrinkProgress) {
			resumed = append(resumed, p.ScannedRowID)
		}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(6), rest.Rows)
		// The checkpoint skips the rowids the first pass already walked: three
		// batches for the six rows left, starting right after the checkpoint.
		assert.Eq(t, []int64{report.ScannedRowID + 2, report.ScannedRowID + 4, report.ScannedRowID + 6}, resumed)
		assert.Eq(t, 3, rest.Batches)
		left, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, left.Complete)
		assert.Eq(t, int64(0), left.PendingRows)
		assert.Empty(t, indexedRows(t, s, "old"))
	})

	t.Run("abort drops the checkpoint and the next pass starts over", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 6)})

		cancelled, cancel := context.WithCancel(ctx)
		_, err := s.ShrinkFTSToolOutputs(cancelled, FTSShrinkOptions{Batch: 2, OnBatch: func(FTSShrinkProgress) { cancel() }})
		assert.ErrIs(t, context.Canceled, err)
		assert.True(t, ftsCheckpoint(t, s) > 0)

		assert.NoErr(t, s.ResetFTSShrink(ctx))
		assert.Eq(t, int64(0), ftsCheckpoint(t, s))
		status, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, status.Started)

		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2})
		assert.Require(t, assert.NoErr(t, err))
		// Two rows were already deleted before the abort; the remaining four are
		// the ones this pass reports.
		assert.Eq(t, int64(4), report.Rows)
		assert.Empty(t, indexedRows(t, s, "old"))
	})

	t.Run("a checkpoint left by the truncating version restarts from the first rowid", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", append(toolEntries(t, 3), user(t, "keep"))})
		high, err := s.maxFTSRowID(ctx)
		assert.Require(t, assert.NoErr(t, err))
		// What P1's completed pass left behind: complete, checkpoint at the end,
		// and the tool rows still in the index (only truncated).
		assert.Require(t, assert.NoErr(t, s.ensureShrinkState(ctx)))
		_, err = s.db.ExecContext(ctx, `INSERT INTO fts_shrink_state(id, scanned_rowid, complete, shrunk_rows, started_at, updated_at)
            VALUES(1, ?, 1, 3, ?, ?)`, high, formatTime(time.Now()), formatTime(time.Now()))
		assert.Require(t, assert.NoErr(t, err))

		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(3), report.Rows, "a complete checkpoint does not hide the tool rows")
		assert.Eq(t, int64(4), report.ScannedRowID)
	})

	t.Run("appends during the pass are not indexed", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 4)})
		createSession(t, s, "live", "/w")

		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2, OnBatch: func(FTSShrinkProgress) {
			fresh := markedBody("fresh ", 4000, "y")
			appendAll(t, s, "live", user(t, fresh), toolMsg(t, fresh, fresh))
		}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(4), report.Rows)
		rows := indexedRows(t, s, "live")
		assert.Require(t, assert.Len(t, rows, 2), "one user row per batch, no tool rows")
		for _, row := range rows {
			assert.Eq(t, EntryUser, row.Role)
		}
		assert.Eq(t, []string{"live/1", "live/3"},
			hitKeys(search(t, s, EntrySearch{Query: "HEADER", SessionIDs: []string{"live"}})))
	})

	t.Run("a session deleted during the pass leaves no rows behind", func(t *testing.T) {
		s := openLegacyLedger(t,
			legacySession{"old", toolEntries(t, 4)},
			legacySession{"doomed", toolEntries(t, 2)})

		// The deletion happens between two batches, while the pass is running.
		deleted := false
		_, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2, OnBatch: func(FTSShrinkProgress) {
			if !deleted {
				deleted = true
				assert.NoErr(t, s.DeleteSession(ctx, "doomed"))
			}
		}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, indexedRows(t, s, "doomed"), "the delete trigger still runs during the pass")
		assert.Empty(t, indexedRows(t, s, "old"))
	})

	t.Run("a ledger without tool rows is a no-op", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		appendAll(t, s, "s1", toolMsg(t, "short", "short"), user(t, "hi"))
		status, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, status.Remaining())

		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(0), report.Rows)
		assert.False(t, report.Optimized, "nothing was deleted, so nothing is merged")
		assert.Eq(t, int64(1), int64(len(indexedRows(t, s, "s1"))))
	})
}

func TestFTSShrinkPendingNotice(t *testing.T) {
	ctx := context.Background()

	t.Run("a legacy ledger announces the tool rows in its index", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 1)})
		assert.StrContains(t, searchNotice(t, s, "HEADER"), FTSShrinkPendingNotice)
		// The historical row still matches in full until the pass deletes it.
		assert.Eq(t, []string{"old/1"}, hitKeys(search(t, s, EntrySearch{Query: "MIDDLE"})))
	})

	t.Run("after the pass the notice is gone", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 1)})
		_, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, searchNotice(t, s, "HEADER"))
		assert.Empty(t, hitKeys(search(t, s, EntrySearch{Query: "MIDDLE"})))
	})

	t.Run("a ledger that never indexed a tool result says nothing", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		appendAll(t, s, "s1", user(t, "short history"), toolMsg(t, "short output", "short output"))
		assert.Empty(t, searchNotice(t, s, "short"))
	})
}

// TestFTSShrinkLargeLedger is the benchmark of the delete pass: 20,000+ entries
// with the length distribution of a real ledger, a tool row still in the index
// for most of them, and the pass timed. The measured numbers are logged (the
// report quotes them).
func TestFTSShrinkLargeLedger(t *testing.T) {
	ctx := context.Background()
	const (
		toolRows = 20000
		// Real tool results: 2-8KB of text, the majority around 3KB.
		minBytes = 2000
		maxBytes = 8000
	)
	entries := make([]NewEntry, 0, toolRows+1000)
	for i := 0; i < toolRows; i++ {
		size := minBytes + (i*7919)%(maxBytes-minBytes)
		body := markedBody(fmt.Sprintf("tool %d ", i), size, "x")
		entries = append(entries, toolMsg(t, body, body))
	}
	for i := 0; i < 500; i++ {
		size := 200 + (i*104729)%1800
		entries = append(entries, user(t, markedBody(fmt.Sprintf("user %d ", i), size, "中")),
			assistant(t, markedBody(fmt.Sprintf("assistant %d ", i), size, "中")))
	}
	// A ledger whose entries were written before the FTS migration: the backfill
	// indexes them in FULL, which is exactly the state `suga data shrink-fts`
	// has to repair.
	start := time.Now()
	s := openLegacyLedger(t, legacySession{"big", entries})
	backfill := time.Since(start)

	sizeBefore, err := s.ftsDataBytes(ctx)
	assert.Require(t, assert.NoErr(t, err))
	before, err := s.FTSShrinkStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(toolRows), before.PendingRows)

	start = time.Now()
	report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{})
	shrink := time.Since(start)
	assert.Require(t, assert.NoErr(t, err))
	sizeAfter, err := s.ftsDataBytes(ctx)
	assert.Require(t, assert.NoErr(t, err))

	assert.Eq(t, int64(toolRows), report.Rows)
	assert.True(t, sizeAfter < sizeBefore, fmt.Sprintf("index did not shrink: %d -> %d", sizeBefore, sizeAfter))
	for _, row := range indexedRows(t, s, "big") {
		assert.True(t, row.Role == EntryUser || row.Role == EntryAssistant,
			"only conversation rows remain, got "+row.Role)
	}
	t.Logf("legacy index+backfill: %s; delete of %d rows in %d batches: %s; entries_fts_data %d -> %d bytes (-%.1f%%); optimized: %v",
		backfill, report.Rows, report.Batches, shrink, sizeBefore, sizeAfter,
		100*float64(sizeBefore-sizeAfter)/float64(sizeBefore), report.Optimized)
	t.Logf("pending estimate before the pass: %d bytes", before.EstimatedReclaimBytes)
}

// searchNotice runs one search and returns the notice of the result.
func searchNotice(t *testing.T, s *Store, query string) string {
	t.Helper()
	result, err := s.SearchEntriesPage(context.Background(), EntrySearch{Query: query})
	assert.Require(t, assert.NoErr(t, err))
	return result.Notice
}

// ftsCheckpoint reads the shrink checkpoint (0 when there is none).
func ftsCheckpoint(t *testing.T, s *Store) int64 {
	t.Helper()
	rowid, _, err := s.shrinkCheckpoint(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	return rowid
}

// TestVacuumReturnsSpace covers the --vacuum half of the maintenance command at
// the store level: the file shrinks after deletions and the WAL is truncated.
func TestVacuumReturnsSpace(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	entries := make([]NewEntry, 0, 200)
	for i := 0; i < 200; i++ {
		body := markedBody(fmt.Sprintf("tool %d ", i), 4000, "x")
		entries = append(entries, toolMsg(t, body, body))
	}
	appendAll(t, s, "s1", entries...)
	peak := DatabaseFileBytes(s.path) // db + WAL: everything the ledger holds
	assert.True(t, peak > 0)

	assert.NoErr(t, s.DeleteSession(ctx, "s1"))
	result, err := s.Vacuum(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, result.BytesAfter < peak,
		fmt.Sprintf("VACUUM did not return the deleted space: %d -> %d", peak, result.BytesAfter))
	assert.Eq(t, int64(0), result.WALBytesAfter, "the WAL is truncated")
	assert.Eq(t, DatabaseFileBytes(s.path), result.BytesAfter)
}

// TestFreeDiskSpace covers the pre-check of --vacuum: the free space of the
// volume the database lives on has to be readable (the CLI refuses below twice
// the database size) and the size helper sums the side files.
func TestFreeDiskSpace(t *testing.T) {
	dir := t.TempDir()
	free, err := FreeDiskSpace(filepath.Join(dir, "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, free > 0, "a writable volume reports free space")
	_, err = FreeDiskSpace(filepath.Join(dir, "missing", "sessions.db"))
	assert.Err(t, err)

	assert.Eq(t, int64(0), DatabaseFileBytes(filepath.Join(dir, "sessions.db")))
	assert.True(t, DatabaseFileBytes(filepath.Join(dir, "sessions.db")) < int64(free))
}
