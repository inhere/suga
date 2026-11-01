package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func rewindEntry(t *testing.T, cut int64, target, scope string) NewEntry {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"cut_seq": cut, "target_turn": target, "scope": scope, "files": 0})
	assert.Require(t, assert.NoErr(t, err))
	return NewEntry{Type: EntryRewind, JSON: raw}
}

func seqsOf(es []Entry) []int64 {
	out := []int64{}
	for _, e := range es {
		out = append(out, e.Seq)
	}
	return out
}

func TestSeqRanges(t *testing.T) {
	t.Run("normalize", func(t *testing.T) {
		assert.Eq(t, SeqRanges(nil), NewSeqRanges())
		// An interval that masks no integer is dropped.
		assert.Eq(t, SeqRanges(nil), NewSeqRanges(SeqRange{3, 4}, SeqRange{5, 5}, SeqRange{9, 2}))
		// Overlapping and nested intervals merge, input order does not matter.
		assert.Eq(t, SeqRanges{{1, 9}}, NewSeqRanges(SeqRange{4, 9}, SeqRange{1, 5}, SeqRange{2, 3}))
		// Intervals sharing only an endpoint stay apart: that seq is masked by neither.
		assert.Eq(t, SeqRanges{{1, 5}, {5, 9}}, NewSeqRanges(SeqRange{5, 9}, SeqRange{1, 5}))
	})
	t.Run("hidden boundaries", func(t *testing.T) {
		r := NewSeqRanges(SeqRange{2, 6}, SeqRange{6, 10}, SeqRange{20, 22})
		want := map[int64]bool{1: false, 2: false, 3: true, 5: true, 6: false, 7: true, 9: true, 10: false, 19: false, 20: false, 21: true, 22: false, 23: false}
		for seq, hidden := range want {
			assert.Eq(t, hidden, r.Hidden(seq), fmt.Sprintf("seq %d", seq))
		}
		assert.False(t, SeqRanges(nil).Hidden(5))
	})
	t.Run("visible segments", func(t *testing.T) {
		assert.Eq(t, [][2]int64{{1, 1<<63 - 1}}, SeqRanges(nil).visibleSegments())
		got := NewSeqRanges(SeqRange{2, 6}, SeqRange{6, 10}).visibleSegments()
		assert.Eq(t, [][2]int64{{1, 2}, {6, 6}, {10, 1<<63 - 1}}, got)
		// cut 0 masks everything before the record: no segment before it.
		assert.Eq(t, [][2]int64{{5, 1<<63 - 1}}, NewSeqRanges(SeqRange{0, 5}).visibleSegments())
	})
}

func TestRewindRecords(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "a", "/w")
	createSession(t, s, "b", "/w")
	t.Run("none", func(t *testing.T) {
		got, err := s.RewindRecords(ctx, "a")
		assert.NoErr(t, err)
		assert.Empty(t, got)
	})
	appendAll(t, s, "a", user(t, "q1"), assistant(t, "r1"), user(t, "q2"), rewindEntry(t, 2, "t2", "conversation"),
		user(t, "q2b"), rewindEntry(t, 2, "t2b", "both"))
	appendAll(t, s, "b", user(t, "other"), rewindEntry(t, 0, "tb", "files"))
	t.Run("records of one session in seq order", func(t *testing.T) {
		got, err := s.RewindRecords(ctx, "a")
		assert.NoErr(t, err)
		assert.Eq(t, []RewindRecord{{Seq: 4, CutSeq: 2, TargetTurn: "t2", Scope: "conversation"}, {Seq: 6, CutSeq: 2, TargetTurn: "t2b", Scope: "both"}}, got)
		// The interval of a record is (cut, record): the cut entry and the record stay visible.
		r := RangesOf(got)
		assert.Eq(t, SeqRanges{{2, 6}}, r)
		assert.False(t, r.Hidden(2))
		assert.True(t, r.Hidden(3))
		assert.True(t, r.Hidden(4)) // the first record is inside the nested rewind
		assert.True(t, r.Hidden(5))
		assert.False(t, r.Hidden(6))
	})
	t.Run("an undecodable record is an error", func(t *testing.T) {
		appendAll(t, s, "b", NewEntry{Type: EntryRewind, JSON: json.RawMessage(`not json`)})
		_, err := s.RewindRecords(ctx, "b")
		assert.Err(t, err)
	})
}

// seedLinear writes n user entries (seq 1..n) to session id.
func seedLinear(t *testing.T, s *Store, id string, n int) {
	t.Helper()
	createSession(t, s, id, "/w")
	in := make([]NewEntry, n)
	for i := range in {
		if i%3 == 2 {
			in[i] = assistant(t, fmt.Sprintf("a%d", i+1))
		} else {
			in[i] = user(t, fmt.Sprintf("u%d", i+1))
		}
	}
	appendAll(t, s, id, in...)
}

// TestSkippingReadsMatchFilteredReads compares each Skipping read with the plain
// read followed by a Go-side filter, over interval shapes that touch the head, the
// tail, a single seq and adjacent intervals.
func TestSkippingReadsMatchFilteredReads(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedLinear(t, s, "a", 30)
	all, err := s.ListEntries(ctx, "a", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	filter := func(skip SeqRanges, types []string, from int64) []Entry {
		out := []Entry{}
		for _, e := range all {
			ok := e.Seq >= from && !skip.Hidden(e.Seq)
			if ok {
				ok = false
				for _, ty := range types {
					ok = ok || e.Type == ty
				}
			}
			if ok {
				out = append(out, e)
			}
		}
		return out
	}
	cases := map[string]SeqRanges{
		"none":     nil,
		"middle":   NewSeqRanges(SeqRange{5, 12}),
		"tail":     NewSeqRanges(SeqRange{10, 31}),
		"head":     NewSeqRanges(SeqRange{0, 8}),
		"two":      NewSeqRanges(SeqRange{3, 7}, SeqRange{15, 20}),
		"adjacent": NewSeqRanges(SeqRange{3, 10}, SeqRange{10, 14}),
		"all":      NewSeqRanges(SeqRange{0, 31}),
	}
	types := []string{EntryUser, EntryAssistant}
	for name, skip := range cases {
		t.Run(name, func(t *testing.T) {
			want := filter(skip, types, 0)
			for _, limit := range []int{1, 4, 100} {
				got, err := s.RecentEntriesSkipping(ctx, "a", types, skip, limit)
				assert.Require(t, assert.NoErr(t, err))
				exp := []int64{}
				for i := len(want) - 1; i >= 0 && len(exp) < limit; i-- {
					exp = append(exp, want[i].Seq)
				}
				assert.Eq(t, exp, append([]int64{}, seqsOf(got)...), fmt.Sprintf("recent limit %d", limit))
			}
			for _, from := range []int64{0, 6, 13, 40} {
				fw := filter(skip, types, from)
				got, err := s.EntriesFromSkipping(ctx, "a", types, from, skip, 5)
				assert.Require(t, assert.NoErr(t, err))
				exp := seqsOf(fw)
				if len(exp) > 5 {
					exp = exp[:5]
				}
				assert.Eq(t, exp, append([]int64{}, seqsOf(got)...), fmt.Sprintf("from %d", from))
			}
			latest, err := s.LatestEntrySkipping(ctx, "a", EntryAssistant, skip)
			assert.Require(t, assert.NoErr(t, err))
			aw := filter(skip, []string{EntryAssistant}, 0)
			if len(aw) == 0 {
				assert.Nil(t, latest)
			} else {
				assert.Eq(t, aw[len(aw)-1].Seq, latest.Seq)
			}
			n, err := s.CountEntriesByTypeSkipping(ctx, "a", EntryAssistant, skip)
			assert.NoErr(t, err)
			assert.Eq(t, len(aw), n)
		})
	}
	t.Run("empty skip equals the plain reads", func(t *testing.T) {
		plain, err := s.RecentEntries(ctx, "a", types, 7)
		assert.Require(t, assert.NoErr(t, err))
		got, err := s.RecentEntriesSkipping(ctx, "a", types, nil, 7)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, plain, got)
		latest, err := s.LatestEntry(ctx, "a", EntryUser)
		assert.Require(t, assert.NoErr(t, err))
		l2, err := s.LatestEntrySkipping(ctx, "a", EntryUser, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, latest, l2)
		c1, _ := s.CountEntriesByType(ctx, "a", EntryUser)
		c2, _ := s.CountEntriesByTypeSkipping(ctx, "a", EntryUser, nil)
		assert.Eq(t, c1, c2)
	})
	t.Run("empty types or limit read nothing", func(t *testing.T) {
		got, err := s.RecentEntriesSkipping(ctx, "a", nil, nil, 3)
		assert.NoErr(t, err)
		assert.Empty(t, got)
		got, err = s.EntriesFromSkipping(ctx, "a", types, 0, nil, 0)
		assert.NoErr(t, err)
		assert.Empty(t, got)
	})
}

func TestCountTurnsSkipping(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "a", "/w")
	for _, id := range []string{"t1", "t2", "t3", "t4"} {
		assert.Require(t, assert.NoErr(t, s.CreateTurn(ctx, Turn{ID: id, SessionID: "a"})))
	}
	// t1: seq 1-2, t2: seq 3-4, t3: seq 5-6, t4 has no entry.
	var in []NewEntry
	for i, id := range []string{"t1", "t1", "t2", "t2", "t3", "t3"} {
		e := user(t, fmt.Sprintf("m%d", i))
		e.TurnID = id
		in = append(in, e)
	}
	appendAll(t, s, "a", in...)
	count := func(skip SeqRanges) int {
		n, err := s.CountTurnsSkipping(ctx, "a", skip)
		assert.Require(t, assert.NoErr(t, err))
		return n
	}
	assert.Eq(t, 4, count(nil))
	// t2 and t3 are fully masked; the turn without entries still counts.
	assert.Eq(t, 2, count(NewSeqRanges(SeqRange{2, 7})))
	// t2 keeps seq 3 and t3 keeps seq 6: both are visible.
	assert.Eq(t, 4, count(NewSeqRanges(SeqRange{3, 6})))
	// The boundary: masking only seq 3-4 hides t2 alone.
	assert.Eq(t, 3, count(NewSeqRanges(SeqRange{2, 5})))
}
