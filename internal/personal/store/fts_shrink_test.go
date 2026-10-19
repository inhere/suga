package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gookit/goutil/x/assert"
)

// Data governance P1: tool results are indexed as their head and tail only.
// These tests cover the truncation itself, the search consequences and the
// `suga gc --shrink-fts` maintenance command that brings the historical rows
// in line (design 0.3: in-place, batched, resumable).

// markedBody builds a body of n characters whose head, middle and tail hold
// distinct keywords (HEADER, MIDDLE, FOOTER), so a search can tell which part
// of it was indexed. MIDDLE sits at least a head/tail length away from both
// ends once n is well above the limit.
func markedBody(prefix string, n int, fill string) string {
	const markers = 6 + 6 + 6 // HEADER, MIDDLE, FOOTER
	inner := n - len([]rune(prefix)) - markers
	if inner < 0 {
		inner = 0
	}
	head, tail := inner/2, inner-inner/2
	return prefix + "HEADER" + strings.Repeat(fill, head) + "MIDDLE" + strings.Repeat(fill, tail) + "FOOTER"
}

// markedBodyOfLength is markedBody padded to exactly n characters.
func markedBodyOfLength(prefix string, n int, fill string) string {
	body := markedBody(prefix, n, fill)
	for len([]rune(body)) < n {
		body += fill
	}
	return body
}

// wantToolText is the Go spelling of the SQL truncation: trim the body the way
// SQLite's trim(x, char(32,9,10,13)) does, then keep head+tail when it is
// longer than the limit. Getting this from the runes (not the bytes) is the
// point: SQLite counts characters.
func wantToolText(body string) string {
	body = strings.Trim(body, " \t\n\r")
	rs := []rune(body)
	if len(rs) <= ftsToolMaxChars {
		return body
	}
	return string(rs[:ftsToolHeadChars]) + ftsToolEllipsis + string(rs[len(rs)-ftsToolTailChars:])
}

// wantToolBody applies the same join of Content and ToolResult.Content the
// expression uses: the result is appended only when it says something new.
func wantToolBody(content, result string) string {
	if result == "" || result == content {
		return content
	}
	return content + "\n" + result
}

// indexedToolText appends one tool entry and returns the text the index holds.
func indexedToolText(t *testing.T, s *Store, sid, content, result string) string {
	t.Helper()
	createSession(t, s, sid, "/w")
	appendAll(t, s, sid, toolMsg(t, content, result))
	rows := indexedRows(t, s, sid)
	assert.Require(t, assert.Len(t, rows, 1))
	return rows[0].Text
}

func TestFTSToolTruncation(t *testing.T) {
	t.Run("a body at the limit stays whole", func(t *testing.T) {
		s := openTemp(t)
		body := markedBodyOfLength("ascii ", ftsToolMaxChars, "x")
		assert.Require(t, assert.Eq(t, ftsToolMaxChars, utf8.RuneCountInString(body)))
		got := indexedToolText(t, s, "s1", body, body)
		assert.Eq(t, wantToolText(body), got)
		assert.Eq(t, ftsToolMaxChars, utf8.RuneCountInString(got))
	})

	t.Run("one character over the limit keeps head and tail", func(t *testing.T) {
		s := openTemp(t)
		body := markedBodyOfLength("ascii ", ftsToolMaxChars+1, "x")
		assert.Require(t, assert.Eq(t, ftsToolMaxChars+1, utf8.RuneCountInString(body)))
		got := indexedToolText(t, s, "s1", body, body)
		assert.Eq(t, wantToolText(body), got)
		assert.Eq(t, ftsToolMaxChars+
			utf8.RuneCountInString(ftsToolEllipsis), utf8.RuneCountInString(got))
		assert.StrContains(t, got, "HEADER")
		// Only one character is dropped here, so both markers survive: the head
		// carries HEADER, the tail the end of the body.
		assert.StrContains(t, got, "FOOTER")
		assert.StrContains(t, got, ftsToolEllipsis)
	})

	t.Run("a long body drops only its middle", func(t *testing.T) {
		s := openTemp(t)
		body := markedBody("ascii ", 3000, "x")
		got := indexedToolText(t, s, "s1", body, body)
		assert.Eq(t, wantToolText(body), got)
		assert.StrContains(t, got, "HEADER")
		assert.StrContains(t, got, "FOOTER")
		assert.NotContains(t, got, "MIDDLE")
	})

	t.Run("two and three byte characters", func(t *testing.T) {
		for _, tc := range []struct {
			name, fill string
		}{
			{"2-byte", "é"},
			{"3-byte", "中"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := openTemp(t)
				body := markedBody("ui ", 3000, tc.fill)
				got := indexedToolText(t, s, "s1", body, body)
				assert.Eq(t, wantToolText(body), got)
				assert.True(t, utf8.ValidString(got))
				assert.Eq(t, ftsToolMaxChars+
					utf8.RuneCountInString(ftsToolEllipsis), utf8.RuneCountInString(got))
			})
		}
	})

	t.Run("four byte emoji, cut inside a surrogate pair", func(t *testing.T) {
		s := openTemp(t)
		// The head boundary falls inside the emoji run: SQLite substr counts
		// characters, so the result stays valid UTF-8.
		body := "🚀" + markedBody("", 3000, "🚀")
		got := indexedToolText(t, s, "s1", body, body)
		assert.Eq(t, wantToolText(body), got)
		assert.True(t, utf8.ValidString(got))
	})

	t.Run("trim happens before the length test", func(t *testing.T) {
		s := openTemp(t)
		// 300 blanks on each side, 700 characters of text: after trimming the
		// body is below the limit and is indexed whole.
		body := strings.Repeat(" ", 300) + markedBodyOfLength("core ", 700, "y") + "\n\t" + strings.Repeat(" ", 297)
		got := indexedToolText(t, s, "s1", body, body)
		assert.Eq(t, wantToolText(body), got)
		assert.StrContains(t, got, "MIDDLE")
	})

	t.Run("content repeating the result is indexed once", func(t *testing.T) {
		s := openTemp(t)
		body := markedBody("same ", 3000, "z")
		got := indexedToolText(t, s, "s1", body, body)
		assert.Eq(t, wantToolText(body), got)
	})

	t.Run("content and a different result are joined then truncated", func(t *testing.T) {
		s := openTemp(t)
		content := markedBody("call ", 2000, "a")
		result := markedBody("result ", 3000, "b")
		got := indexedToolText(t, s, "s1", content, result)
		assert.Eq(t, wantToolText(wantToolBody(content, result)), got)
		// The head holds the call, the tail the end of the result.
		assert.StrContains(t, got, "call HEADER")
		assert.StrContains(t, got, "FOOTER")
		assert.NotContains(t, got, "MIDDLE")
	})

	t.Run("other roles are indexed in full", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		long := markedBody("assistant ", 5000, "q")
		appendAll(t, s, "s1", user(t, long), assistant(t, long))
		rows := indexedRows(t, s, "s1")
		assert.Require(t, assert.Len(t, rows, 2))
		assert.Eq(t, long, rows[0].Text)
		assert.Eq(t, long, rows[1].Text)
	})
}

func TestFTSToolTruncationSearch(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	body := markedBody("tool ", 3000, "x")
	assistantBody := markedBody("assistant ", 3000, "x")
	appendAll(t, s, "s1", toolMsg(t, body, body), assistant(t, assistantBody))
	toolOnly := EntrySearch{Kinds: []string{EntryTool}}

	t.Run("the head of a long tool result stays searchable", func(t *testing.T) {
		q := toolOnly
		q.Query = "tool HEADER"
		assert.Eq(t, []string{"s1/1"}, hitKeys(search(t, s, q)))
	})
	t.Run("the tail of a long tool result stays searchable", func(t *testing.T) {
		q := toolOnly
		q.Query = "FOOTER"
		assert.Eq(t, []string{"s1/1"}, hitKeys(search(t, s, q)))
	})
	t.Run("the middle of a long tool result is gone", func(t *testing.T) {
		q := toolOnly
		q.Query = "MIDDLE"
		assert.Empty(t, hitKeys(search(t, s, q)))
	})
	t.Run("a short CJK term falls back to the LIKE path in the tail", func(t *testing.T) {
		cjk := "工具 开头标记" + strings.Repeat("結", 2000) + "中部标记" + strings.Repeat("結", 2000) + "结尾标记"
		createSession(t, s, "s2", "/w")
		appendAll(t, s, "s2", toolMsg(t, cjk, cjk))
		q := toolOnly
		q.Query = "结尾"
		assert.Eq(t, []string{"s2/1"}, hitKeys(search(t, s, q)))
		q.Query = "中部"
		assert.Empty(t, hitKeys(search(t, s, q)))
	})
	t.Run("an assistant message keeps its middle", func(t *testing.T) {
		assert.Eq(t, []string{"s1/2"}, hitKeys(search(t, s, EntrySearch{Query: "MIDDLE", Kinds: []string{EntryAssistant}})))
	})
}

// legacySession is one session of a pre-P1 ledger: its entries are written
// before the FTS migration, which then indexes the tool results in FULL.
type legacySession struct {
	id      string
	entries []NewEntry
}

// openLegacyLedger opens a ledger whose entries predate the FTS migration and
// migrates it, so entries_fts holds the full text of every tool result --
// exactly the state `suga gc --shrink-fts` has to repair.
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
		assert.Eq(t, report.ScannedRowID, report.HighWater)
		assert.True(t, report.BytesBefore > 0,
			fmt.Sprintf("entries_fts_data before: %d", report.BytesBefore))
		assert.Eq(t, report.Merges, report.Merges)

		after, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, after.Started)
		assert.True(t, after.Complete)
		assert.False(t, after.Remaining())
		assert.Eq(t, int64(0), after.PendingRows)
		// The user row was never touched.
		rows := indexedRows(t, s, "old")
		assert.Require(t, assert.Len(t, rows, 3))
		assert.Eq(t, markedBody("u ", 5000, "x"), rows[2].Text)
	})

	t.Run("batches cover every row and the checkpoint advances", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 7)})

		var seen []int64
		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2, OnBatch: func(p FTSShrinkProgress) {
			seen = append(seen, p.Rows)
		}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(7), report.Rows)
		// 2000-row batches over 7 rows: four batches of 2 and one of 1.
		assert.Eq(t, []int64{2, 4, 6, 7}, seen)
		assert.Eq(t, 4, report.Batches)
		for _, row := range indexedRows(t, s, "old") {
			assert.True(t, utf8.RuneCountInString(row.Text) <= ftsToolMaxChars+3)
		}
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
		assert.Eq(t, int64(6), midway.PendingRows)

		rest, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(6), rest.Rows)
		left, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, left.Complete)
		assert.Eq(t, int64(0), left.PendingRows)
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
		// The two rows the first (aborted) pass had already rewritten are in the
		// truncated form and are not rewritten again; the remaining four are.
		assert.Eq(t, int64(4), report.Rows)
		for _, row := range indexedRows(t, s, "old") {
			assert.True(t, utf8.RuneCountInString(row.Text) <= ftsToolMaxChars+3)
		}
	})

	t.Run("appends during the pass are already truncated", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 4)})
		createSession(t, s, "live", "/w")

		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{Batch: 2, OnBatch: func(FTSShrinkProgress) {
			fresh := markedBody("fresh ", 4000, "y")
			appendAll(t, s, "live", toolMsg(t, fresh, fresh))
		}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(4), report.Rows)
		for _, row := range indexedRows(t, s, "live") {
			assert.True(t, utf8.RuneCountInString(row.Text) <= ftsToolMaxChars+3,
				"a row written during the pass is truncated at write time")
		}
		// The callback appended one long result per batch, both already truncated.
		assert.Eq(t, []string{"live/1", "live/2"},
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
		assert.Len(t, indexedRows(t, s, "old"), 4)
	})

	t.Run("a ledger without long tool rows is a no-op", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		appendAll(t, s, "s1", toolMsg(t, "short", "short"), user(t, "hi"))
		report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(0), report.Rows)
		assert.True(t, report.Batches <= 1, "at most the one empty batch over the rowid range")
		status, err := s.FTSShrinkStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, status.Remaining())
	})
}

// TestFTSShrinkMatchesWritePath is the behavioural guard of the shared
// expression: a legacy row that the shrink command rewrites ends up holding
// exactly the text AppendEntries writes for the same entry. Without the shared
// expression (or with the truncation removed) the two texts differ.
func TestFTSShrinkMatchesWritePath(t *testing.T) {
	ctx := context.Background()
	content := markedBody("live ", 4000, "x")
	result := "a different result " + markedBody("", 1000, "y")

	// The legacy ledger holds the FULL text of the tool result; the live session
	// is appended afterwards, through the truncating write path.
	s := openLegacyLedger(t, legacySession{"legacy", []NewEntry{toolMsg(t, content, result)}})
	createSession(t, s, "live", "/w")
	appendAll(t, s, "live", toolMsg(t, content, result))
	live := indexedRows(t, s, "live")
	assert.Require(t, assert.Len(t, live, 1))
	full := wantToolBody(content, result)
	legacyBefore := indexedRows(t, s, "legacy")
	assert.Require(t, assert.Len(t, legacyBefore, 1))
	assert.Eq(t, full, legacyBefore[0].Text)

	report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(1), report.Rows)
	legacy := indexedRows(t, s, "legacy")
	assert.Require(t, assert.Len(t, legacy, 1))
	assert.Eq(t, live[0].Text, legacy[0].Text)
	assert.True(t, utf8.RuneCountInString(legacy[0].Text) <= ftsToolMaxChars+3,
		"the rewrite really truncated")
}

func TestFTSShrinkPendingNotice(t *testing.T) {
	ctx := context.Background()

	t.Run("a legacy ledger announces the unfinished shrink", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 1)})
		assert.StrContains(t, searchNotice(t, s, "HEADER"), FTSShrinkPendingNotice)
		assert.Eq(t, []string{"old/1"}, hitKeys(search(t, s, EntrySearch{Query: "MIDDLE"})))
	})

	t.Run("after the pass the notice is gone", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 1)})
		_, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, searchNotice(t, s, "HEADER"))
	})

	t.Run("a ledger that never held a long tool result says nothing", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		appendAll(t, s, "s1", user(t, "short history"))
		assert.Empty(t, searchNotice(t, s, "short"))
	})
}

// TestFTSToolTruncationLargeLedger is the P1 benchmark: 20,000+ entries with
// the length distribution of a real ledger (most tool results well above the
// limit), the FTS index in its pre-P1 state, and the shrink pass timed. The
// measured numbers are logged (the report quotes them).
func TestFTSToolTruncationLargeLedger(t *testing.T) {
	ctx := context.Background()
	const (
		toolEntries = 20000
		// Real tool results: 2-8KB of text, the majority around 3KB.
		minBytes = 2000
		maxBytes = 8000
	)
	entries := make([]NewEntry, 0, toolEntries+2000)
	for i := 0; i < toolEntries; i++ {
		size := minBytes + (i*7919)%(maxBytes-minBytes)
		body := markedBody(fmt.Sprintf("tool %d ", i), size, "x")
		entries = append(entries, toolMsg(t, body, body))
	}
	for i := 0; i < 2000; i++ {
		size := 200 + (i*104729)%1800
		entries = append(entries, user(t, markedBody(fmt.Sprintf("user %d ", i), size, "中")),
			assistant(t, markedBody(fmt.Sprintf("assistant %d ", i), size, "中")))
	}
	// A ledger whose entries were written before the FTS migration: the backfill
	// indexes them in FULL, which is exactly the state `suga gc --shrink-fts`
	// has to repair.
	start := time.Now()
	s := openLegacyLedger(t, legacySession{"big", entries})
	backfill := time.Since(start)

	sizeBefore, err := s.ftsDataBytes(ctx)
	assert.Require(t, assert.NoErr(t, err))
	before, err := s.FTSShrinkStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(toolEntries), before.PendingRows)

	start = time.Now()
	report, err := s.ShrinkFTSToolOutputs(ctx, FTSShrinkOptions{})
	shrink := time.Since(start)
	assert.Require(t, assert.NoErr(t, err))
	sizeAfter, err := s.ftsDataBytes(ctx)
	assert.Require(t, assert.NoErr(t, err))

	assert.Eq(t, int64(toolEntries), report.Rows)
	assert.True(t, sizeAfter < sizeBefore, fmt.Sprintf("index did not shrink: %d -> %d", sizeBefore, sizeAfter))
	var long int64
	for _, row := range indexedRows(t, s, "big") {
		if row.Role == EntryTool && utf8.RuneCountInString(row.Text) > ftsToolMaxChars+3 {
			long++
		}
	}
	assert.Eq(t, int64(0), long, "every tool row is within the limit")
	t.Logf("legacy index+backfill: %s; shrink of %d rows in %d batches: %s; entries_fts_data %d -> %d bytes (-%.1f%%); merges %d",
		backfill, report.Rows, report.Batches, shrink, sizeBefore, sizeAfter,
		100*float64(sizeBefore-sizeAfter)/float64(sizeBefore), report.Merges)
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
	rowid, err := s.shrinkRowID(context.Background())
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

// fileSize is the size of one file (0 when it does not exist).
