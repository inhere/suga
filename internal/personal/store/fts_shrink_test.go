package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// Tool results are not indexed any more (the write path skips them), yet a
// ledger written by v0.35 or older still holds the rows the historical backfill
// wrote for them. These tests cover the write path, the search consequences and
// the notice such a legacy ledger carries (the delete pass itself was removed in
// v0.41.0).

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
// exactly the state a pre-v0.36 ledger is in.
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

func TestFTSShrinkPendingNotice(t *testing.T) {
	t.Run("a legacy ledger announces the tool rows in its index", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 1)})
		assert.StrContains(t, searchNotice(t, s, "HEADER"), FTSShrinkPendingNotice)
		// The historical row still matches in full until it is deleted.
		assert.Eq(t, []string{"old/1"}, hitKeys(search(t, s, EntrySearch{Query: "MIDDLE"})))
	})

	t.Run("a ledger cleaned by v0.40 says nothing", func(t *testing.T) {
		s := openLegacyLedger(t, legacySession{"old", toolEntries(t, 1)})
		// What `suga data shrink-fts` (removed in v0.41.0) left behind.
		_, err := s.db.Exec(`DELETE FROM entries_fts WHERE role = 'tool'`)
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

// searchNotice runs one search and returns the notice of the result.
func searchNotice(t *testing.T, s *Store, query string) string {
	t.Helper()
	result, err := s.SearchEntriesPage(context.Background(), EntrySearch{Query: query})
	assert.Require(t, assert.NoErr(t, err))
	return result.Notice
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
