package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// searchExcludeFixture: session cur holds 45 "page needle" hits, seq 11..30 are
// masked (a rewind to seq 10); session other holds 5 more hits.
func searchExcludeFixture(t *testing.T) (*Store, SeqRanges, string) {
	s := openTemp(t)
	ws := t.TempDir()
	createSession(t, s, "cur", ws)
	createSession(t, s, "other", ws)
	in := make([]NewEntry, 45)
	for i := range in {
		in[i] = user(t, fmt.Sprintf("page needle %02d", i+1))
	}
	appendAll(t, s, "cur", in...)
	for i := range in[:5] {
		appendAll(t, s, "other", user(t, fmt.Sprintf("page needle other %d", i)))
	}
	return s, NewSeqRanges(SeqRange{Lo: 10, Hi: 31}), ws
}

func collectPages(t *testing.T, s *Store, q EntrySearch) []EntryHit {
	t.Helper()
	var all []EntryHit
	for {
		page, err := s.SearchEntriesPage(context.Background(), q)
		assert.Require(t, assert.NoErr(t, err))
		all = append(all, page.Hits...)
		if page.NextCursor == "" {
			return all
		}
		q.Cursor = page.NextCursor
	}
}

func TestSearchEntriesPageExcludeSession(t *testing.T) {
	s, masked, ws := searchExcludeFixture(t)
	ex := ExcludeSession{SessionID: "cur", Ranges: masked}

	check := func(t *testing.T, q EntrySearch, wantCur, wantOther int) {
		q.ExcludeSession = ex
		hits := collectPages(t, s, q)
		cur, other := 0, 0
		seen := map[string]bool{}
		for _, h := range hits {
			key := fmt.Sprintf("%s/%d", h.SessionID, h.Seq)
			assert.False(t, seen[key], "duplicate hit "+key)
			seen[key] = true
			if h.SessionID == "cur" {
				cur++
				assert.False(t, masked.Hidden(h.Seq), "masked hit "+key)
			} else {
				other++
			}
		}
		assert.Eq(t, wantCur, cur)
		assert.Eq(t, wantOther, other)
	}
	t.Run("all scope pages across the interval", func(t *testing.T) {
		// 45 - 20 masked = 25 visible in cur; the other session is untouched.
		check(t, EntrySearch{Query: "page needle", Limit: 7}, 25, 5)
	})
	t.Run("current session scope", func(t *testing.T) {
		check(t, EntrySearch{Query: "page needle", CurrentSessionID: "cur", Limit: 10}, 25, 0)
	})
	t.Run("workspace scope", func(t *testing.T) {
		check(t, EntrySearch{Query: "page needle", WorkspaceDir: ws, CurrentSessionID: "cur", Limit: 10}, 25, 5)
	})
	t.Run("short term uses the like path", func(t *testing.T) {
		check(t, EntrySearch{Query: "ne", Limit: 100}, 25, 5)
	})
	t.Run("unsorted overlapping ranges mean the same exclusion", func(t *testing.T) {
		q := EntrySearch{Query: "page needle", Limit: 100}
		q.ExcludeSession = ExcludeSession{SessionID: "cur", Ranges: SeqRanges{{Lo: 20, Hi: 31}, {Lo: 10, Hi: 25}}}
		got := collectPages(t, s, q)
		assert.Len(t, got, 30)
	})
	t.Run("cursor does not carry over to another option", func(t *testing.T) {
		q := EntrySearch{Query: "page needle", Limit: 5, ExcludeSession: ex}
		first, err := s.SearchEntriesPage(context.Background(), q)
		assert.Require(t, assert.NoErr(t, err))
		assert.NotEmpty(t, first.NextCursor)
		// Same cursor, no exclusion: the snapshot is stale for it, the search restarts.
		q.ExcludeSession = ExcludeSession{}
		q.Cursor = first.NextCursor
		restarted, err := s.SearchEntriesPage(context.Background(), q)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, restarted.Notice, searchCursorRestartNote)
		// Same cursor, another interval: also a restart.
		q.ExcludeSession = ExcludeSession{SessionID: "cur", Ranges: NewSeqRanges(SeqRange{Lo: 1, Hi: 4})}
		restarted, err = s.SearchEntriesPage(context.Background(), q)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, restarted.Notice, searchCursorRestartNote)
		// The same option continues the snapshot without a notice.
		q.ExcludeSession, q.Cursor = ex, first.NextCursor
		next, err := s.SearchEntriesPage(context.Background(), q)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "", next.Notice)
	})
}

// TestExplainSkippingQueries: the interval-aware reads and the exclusion search
// must never scan entries; they search by primary key, by the turn index or by FTS.
func TestExplainSkippingQueries(t *testing.T) {
	s := openTemp(t)
	seedLinear(t, s, "a", 20)
	noScan := func(t *testing.T, plan string) {
		t.Helper()
		// Plan steps are joined by " | "; a table scan reads "SCAN <table or alias>".
		for _, step := range strings.Split(plan, " | ") {
			assert.False(t, step == "SCAN e" || step == "SCAN entries" || strings.HasPrefix(step, "SCAN e ") || strings.HasPrefix(step, "SCAN entries "), step)
		}
		assert.StrContains(t, plan, "SEARCH")
	}
	t.Run("recent and from: segment range by primary key", func(t *testing.T) {
		for _, dir := range []string{"DESC", "ASC"} {
			plan := explainPlan(t, s, `SELECT `+entryCols+` FROM entries WHERE session_id = ? AND seq BETWEEN ? AND ? AND type IN (?) ORDER BY seq `+dir+` LIMIT ?`,
				"a", 1, 10, EntryUser, 5)
			noScan(t, plan)
			assert.StrContains(t, plan, "session_id=? AND seq>? AND seq<?")
			assert.NotContains(t, plan, "TEMP B-TREE")
		}
	})
	t.Run("count by type: segment range", func(t *testing.T) {
		noScan(t, explainPlan(t, s, `SELECT COUNT(1) FROM entries WHERE session_id = ? AND seq BETWEEN ? AND ? AND type = ?`, "a", 1, 10, EntryUser))
	})
	t.Run("turn count: turn index then entry turn index", func(t *testing.T) {
		masked, margs := NewSeqRanges(SeqRange{2, 9}).maskedSQL("e.seq")
		plan := explainPlan(t, s, `SELECT COUNT(1) FROM turns t WHERE t.session_id = ? AND (
		NOT EXISTS (SELECT 1 FROM entries e WHERE e.turn_id = t.id AND e.session_id = t.session_id)
		OR EXISTS (SELECT 1 FROM entries e WHERE e.turn_id = t.id AND e.session_id = t.session_id AND NOT `+masked+`))`, append([]any{"a"}, margs...)...)
		noScan(t, plan)
		assert.StrContains(t, plan, "idx_turns_session")
		assert.StrContains(t, plan, "idx_entries_turn")
	})
	t.Run("rewind records: type index", func(t *testing.T) {
		plan := explainPlan(t, s, rewindRecordsSQL, EntryRewind, "a")
		noScan(t, plan)
		assert.StrContains(t, plan, "idx_entries_type")
	})
	t.Run("exclusion search: FTS drives, entries by primary key", func(t *testing.T) {
		ex := ExcludeSession{SessionID: "a", Ranges: NewSeqRanges(SeqRange{2, 9})}
		for _, q := range []EntrySearch{
			{Query: "needle", ExcludeSession: ex},
			{Query: "needle", CurrentSessionID: "a", ExcludeSession: ex},
			{Query: "needle", WorkspaceDir: "/w", CurrentSessionID: "a", ExcludeSession: ex},
		} {
			sqlText, args, _, _, err := buildEntrySearch(q, 10)
			assert.Require(t, assert.NoErr(t, err))
			plan := explainPlan(t, s, sqlText, args...)
			noScan(t, plan)
			assert.StrContains(t, plan, "SCAN f VIRTUAL TABLE INDEX 0:M")
			assert.StrContains(t, plan, "SEARCH e USING INDEX sqlite_autoindex_entries_1 (session_id=? AND seq=?)")
		}
	})
}
