package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// maskFixture writes sessions straight through the store: turns, entries and
// rewind records, the way T5 will, without any rewind execution logic.
type maskFixture struct {
	t      *testing.T
	ctx    context.Context
	repo   *store.Store
	runner *Runner
}

func newMaskFixture(t *testing.T) *maskFixture {
	t.Helper()
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(), Model: "m", ProviderName: "p"})
	assert.Require(t, assert.NoErr(t, err))
	return &maskFixture{t: t, ctx: context.Background(), repo: repo, runner: runner}
}

func (f *maskFixture) session(id string) {
	f.t.Helper()
	assert.Require(f.t, assert.NoErr(f.t, f.repo.CreateSession(f.ctx, store.Session{ID: id})))
}

func (f *maskFixture) turn(sid, id, status string) {
	f.t.Helper()
	assert.NoErr(f.t, f.repo.CreateTurn(f.ctx, store.Turn{ID: id, SessionID: sid}))
	assert.NoErr(f.t, f.repo.UpdateTurn(f.ctx, store.Turn{ID: id, Status: status}))
}

// add appends one entry and returns its seq.
func (f *maskFixture) add(sid, turn, typ string, body any) int64 {
	f.t.Helper()
	raw, err := json.Marshal(body)
	assert.Require(f.t, assert.NoErr(f.t, err))
	got, err := f.repo.AppendEntries(f.ctx, sid, store.NewEntry{TurnID: turn, Type: typ, JSON: raw})
	assert.Require(f.t, assert.NoErr(f.t, err))
	return got[0].Seq
}

func (f *maskFixture) msg(sid, turn, role, text string) int64 {
	typ := store.EntryUser
	if role == "assistant" {
		typ = store.EntryAssistant
	}
	return f.add(sid, turn, typ, agentcore.Message{Role: role, Content: text})
}

// rewind appends a rewind record that keeps everything up to cut.
func (f *maskFixture) rewind(sid string, cut int64) int64 {
	f.t.Helper()
	return f.add(sid, "", store.EntryRewind, map[string]any{"cut_seq": cut, "target_turn": "x", "scope": "conversation"})
}

func itemSeqs(v historyView) []int64 {
	out := []int64{}
	for _, it := range v.items {
		out = append(out, it.seq)
	}
	return out
}

// threeTurns is t1 (1,2), t2 (3,4), t3 (5,6), all completed.
func (f *maskFixture) threeTurns(sid string) {
	f.session(sid)
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("t%d", i)
		f.turn(sid, id, store.TurnCompleted)
		f.msg(sid, id, "user", fmt.Sprintf("request number %d please", i))
		f.msg(sid, id, "assistant", fmt.Sprintf("answer %d", i))
	}
}

func TestMaskedRanges(t *testing.T) {
	f := newMaskFixture(t)
	f.threeTurns("s")
	got, err := f.runner.maskedRanges(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, 0, len(got))
	f.rewind("s", 2) // seq 7: masks 3..6
	f.rewind("s", 1) // seq 8: nested, masks 2..7
	got, err = f.runner.maskedRanges(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, store.SeqRanges{{Lo: 1, Hi: 8}}, got)
}

func TestLoadHistoryMasked(t *testing.T) {
	t.Run("rewound turns leave the context, later turns come back", func(t *testing.T) {
		f := newMaskFixture(t)
		f.threeTurns("s")
		f.rewind("s", 2) // 7
		f.turn("s", "t4", store.TurnCompleted)
		f.msg("s", "t4", "user", "request number 4 please") // 8
		f.msg("s", "t4", "assistant", "answer 4")           // 9
		v, err := f.runner.loadHistory(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, []int64{1, 2, 8, 9}, itemSeqs(v))
	})
	t.Run("nested rewind", func(t *testing.T) {
		f := newMaskFixture(t)
		f.threeTurns("s")
		f.rewind("s", 4) // 7: masks 5,6
		f.turn("s", "t4", store.TurnCompleted)
		f.msg("s", "t4", "user", "request number 4 please") // 8
		f.msg("s", "t4", "assistant", "answer 4")           // 9
		f.rewind("s", 2)                                    // 10: masks 3..9
		v, err := f.runner.loadHistory(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, []int64{1, 2}, itemSeqs(v))
	})
	t.Run("rewind earlier than a compaction and a clear record drops their summaries", func(t *testing.T) {
		for _, reason := range []string{"threshold", segmentReasonClear} {
			f := newMaskFixture(t)
			f.threeTurns("s")
			f.add("s", "t3", store.EntryCompaction, compactionRecord{Summary: "SECRET-SUMMARY", FirstKeptSeq: 5, Reason: reason}) // 7
			f.rewind("s", 2)                                                                                                      // 8
			v, err := f.runner.loadHistory(f.ctx, "s")
			assert.NoErr(t, err)
			assert.Eq(t, "", v.summary)
			assert.Eq(t, []int64{1, 2}, itemSeqs(v))
		}
	})
	t.Run("a rewind after a compaction keeps its summary", func(t *testing.T) {
		f := newMaskFixture(t)
		f.session("s")
		f.turn("s", "t1", store.TurnCompleted)
		f.msg("s", "t1", "user", "request number 1 please")                                         // 1
		f.add("s", "t1", store.EntryCompaction, compactionRecord{Summary: "KEPT", FirstKeptSeq: 1}) // 2
		f.turn("s", "t2", store.TurnCompleted)
		f.msg("s", "t2", "user", "request number 2 please") // 3
		f.rewind("s", 2)                                    // 4
		v, err := f.runner.loadHistory(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, "KEPT", v.summary)
		assert.Eq(t, []int64{1}, itemSeqs(v))
	})
	t.Run("interrupted turn and context update inside the interval", func(t *testing.T) {
		f := newMaskFixture(t)
		f.session("s")
		f.turn("s", "t1", store.TurnCompleted)
		f.msg("s", "t1", "user", "request number 1 please") // 1
		f.msg("s", "t1", "assistant", "answer 1")           // 2
		f.turn("s", "t2", store.TurnFailed)
		f.msg("s", "t2", "user", "request number 2 please")                                   // 3
		f.add("s", "t2", store.EntryContextUpdate, contextUpdateRecord{RulesHash: "newrule"}) // 4
		before, err := f.runner.loadHistory(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, true, len(itemSeqs(before)) > 2) // the failed turn's prefix is replayed
		f.rewind("s", 2)                              // 5
		after, err := f.runner.loadHistory(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, []int64{1, 2}, itemSeqs(after))
		assert.Eq(t, 0, len(after.updates))
	})
	t.Run("SegmentHistory is the same masked view", func(t *testing.T) {
		f := newMaskFixture(t)
		f.threeTurns("s")
		f.rewind("s", 2)
		h, err := f.runner.SegmentHistory(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, 2, len(h.Items))
	})
}

func TestHandoffReadersMasked(t *testing.T) {
	f := newMaskFixture(t)
	f.session("s")
	f.turn("s", "t1", store.TurnCompleted)
	f.msg("s", "t1", "user", "first long request")                                                           // 1
	f.add("s", "t1", store.EntryPlan, json.RawMessage(`{"plan":[{"step":"keep step","status":"pending"}]}`)) // 2
	f.turn("s", "t2", store.TurnCompleted)
	f.msg("s", "t2", "user", "second long request")                                                          // 3
	f.add("s", "t2", store.EntryPlan, json.RawMessage(`{"plan":[{"step":"drop step","status":"pending"}]}`)) // 4
	reqs, err := f.runner.recentRequests(f.ctx, "s", "", 5)
	assert.NoErr(t, err)
	assert.Eq(t, []string{"first long request", "second long request"}, reqs)
	assert.Eq(t, true, strings.Contains(f.runner.latestPlan(f.ctx, "s"), "drop step"))

	f.rewind("s", 2) // 5
	reqs, err = f.runner.recentRequests(f.ctx, "s", "", 5)
	assert.NoErr(t, err)
	assert.Eq(t, []string{"first long request"}, reqs)
	plan := f.runner.latestPlan(f.ctx, "s")
	assert.Eq(t, true, strings.Contains(plan, "keep step"))
	assert.Eq(t, false, strings.Contains(plan, "drop step"))
}

// A handoff built after a rewind, and a compaction written after one, carry
// neither the rewound request nor its plan.
func TestHandoffPackageAfterRewind(t *testing.T) {
	repo, runner, prov, src := handoffSource(t)
	ctx := context.Background()
	es, err := repo.ListEntries(ctx, src, store.EntryFilter{})
	assert.NoErr(t, err)
	cut := es[len(es)-1].Seq // the plan of the first turn
	_, err = runner.Run(ctx, RunRequest{SessionID: src, Prompt: "second work, rewound later"})
	assert.NoErr(t, err)
	_, err = repo.AppendEntries(ctx, src, store.NewEntry{Type: store.EntryPlan, JSON: json.RawMessage(`{"plan":[{"step":"rewound step","status":"pending"}]}`)})
	assert.NoErr(t, err)
	_, err = repo.AppendEntries(ctx, src, store.NewEntry{Type: store.EntryRewind,
		JSON: json.RawMessage(fmt.Sprintf(`{"cut_seq":%d,"target_turn":"x","scope":"conversation"}`, cut))})
	assert.NoErr(t, err)

	t.Run("handoff", func(t *testing.T) {
		_, err := runner.Handoff(ctx, HandoffRequest{SourceID: src, NewID: "child-r"})
		assert.NoErr(t, err)
		in := prov.sumReqs[len(prov.sumReqs)-1].Messages[0].Content
		assert.Eq(t, true, strings.Contains(in, "source work"))
		assert.Eq(t, false, strings.Contains(in, "second work"))
		assert.Eq(t, false, strings.Contains(in, "rewound step"))
		kids, err := repo.ListEntries(ctx, "child-r", store.EntryFilter{Types: []string{store.EntryHandoff}})
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(kids))
		var rec handoffRecord
		assert.NoErr(t, json.Unmarshal(kids[0].JSON, &rec))
		assert.Eq(t, false, strings.Contains(rec.Summary, "second work"))
		assert.Eq(t, true, strings.Contains(string(rec.Plan), "ship T08"))
		assert.Eq(t, false, strings.Contains(string(rec.Plan), "rewound step"))
	})
	t.Run("compaction", func(t *testing.T) {
		_, err := runner.Compact(ctx, src)
		assert.NoErr(t, err)
		rec := latestSegment(t, repo, src)
		assert.Eq(t, false, strings.Contains(strings.Join(rec.RecentRequests, "|"), "second work"))
		assert.Eq(t, true, strings.Contains(strings.Join(rec.RecentRequests, "|"), "source work"))
		for _, m := range prov.sumReqs[len(prov.sumReqs)-1].Messages {
			assert.Eq(t, false, strings.Contains(m.Content, "rewound step"))
		}
	})
}

func TestLastReplyMasked(t *testing.T) {
	setLast := func(f *maskFixture, id string) {
		sess, err := f.repo.GetSession(f.ctx, "s")
		assert.NoErr(t, err)
		sess.LastTurnID = id
		assert.NoErr(t, f.repo.UpdateSessionFields(f.ctx, sess))
	}
	t.Run("a rewound failed turn does not flag the visible one", func(t *testing.T) {
		f := newMaskFixture(t)
		f.session("s")
		f.turn("s", "t1", store.TurnCompleted)
		f.msg("s", "t1", "user", "request number 1 please")
		f.msg("s", "t1", "assistant", "answer 1") // 2
		f.turn("s", "t2", store.TurnFailed)
		f.msg("s", "t2", "user", "request number 2 please")
		f.add("s", "t2", store.EntryAssistant, agentcore.Message{Role: "assistant", ToolCall: &agentcore.ToolCall{CallID: "c", Name: "file_read"}})
		setLast(f, "t2")
		text, incomplete, err := f.runner.LastReply(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, "answer 1", text)
		assert.Eq(t, true, incomplete)

		f.rewind("s", 2)
		text, incomplete, err = f.runner.LastReply(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, "answer 1", text)
		assert.Eq(t, false, incomplete)
	})
	t.Run("the reply of a rewound turn is not shown", func(t *testing.T) {
		f := newMaskFixture(t)
		f.threeTurns("s")
		setLast(f, "t3")
		f.rewind("s", 2)
		text, incomplete, err := f.runner.LastReply(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, "answer 1", text)
		assert.Eq(t, false, incomplete)
	})
}

func TestSessionCardMasked(t *testing.T) {
	f := newMaskFixture(t)
	f.session("s")
	f.turn("s", "t1", store.TurnCompleted)
	f.msg("s", "t1", "user", "request number 1 please")                                              // 1
	f.add("s", "t1", store.EntryCompaction, compactionRecord{Summary: "OLD", Reason: "threshold"})   // 2
	f.add("s", "t1", store.EntryPlan, json.RawMessage(`{"plan":[{"step":"a","status":"pending"}]}`)) // 3
	f.turn("s", "t2", store.TurnCompleted)
	f.msg("s", "t2", "user", "request number 2 please")                                                                                                              // 4
	f.add("s", "t2", store.EntryCompaction, compactionRecord{Summary: "NEW", Reason: "threshold"})                                                                   // 5
	f.add("s", "t2", store.EntryPlan, json.RawMessage(`{"plan":[{"step":"a","status":"pending"},{"step":"b","status":"pending"},{"step":"c","status":"pending"}]}`)) // 6
	f.turn("s", "t3", store.TurnCompleted)                                                                                                                           // no entries: counted as before

	card, err := f.runner.SessionCard(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, "NEW", card.LatestSummary)
	assert.Eq(t, 2, card.SegmentCount)
	assert.Eq(t, 3, card.Turns)
	assert.Eq(t, 3, len(card.OpenSteps))

	f.rewind("s", 3) // masks 4..6
	card, err = f.runner.SessionCard(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, "OLD", card.LatestSummary)
	assert.Eq(t, 1, card.SegmentCount)
	assert.Eq(t, 2, card.Turns) // t2 has all its entries masked
	assert.Eq(t, 1, len(card.OpenSteps))
}

func TestLedgerMaskedSearchAndRecall(t *testing.T) {
	f := newLedgerFixture(t)
	f.ledger.bindMasked(func(ctx context.Context, sid string) (store.SeqRanges, error) {
		return maskedRangesOf(ctx, f.repo, sid)
	})
	f.session(t, "cur", "/w")
	f.session(t, "other", "/w")
	f.message(t, "cur", "user", "needle kept", "")        // 1
	f.message(t, "cur", "assistant", "needle masked", "") // 2
	f.message(t, "cur", "user", "needle masked too", "")  // 3
	f.message(t, "other", "user", "needle elsewhere", "") // 1
	ctx := withObserveScope(context.Background(), "cur", "t")
	snippets := func(scope string) string {
		res, err := f.ledger.Search(ctx, native.LedgerQuery{Query: "needle", Scope: scope})
		assert.NoErr(t, err)
		var refs []string
		for _, h := range res.Hits {
			refs = append(refs, h.Ref)
		}
		return strings.Join(refs, ",")
	}
	for _, scope := range []string{native.LedgerScopeCurrent, native.LedgerScopeWorkspace, native.LedgerScopeAll} {
		assert.Eq(t, true, strings.Contains(snippets(scope), "cur/2"))
	}
	_, err := f.repo.AppendEntries(context.Background(), "cur", store.NewEntry{Type: store.EntryRewind,
		JSON: json.RawMessage(`{"cut_seq":1,"target_turn":"x","scope":"conversation"}`)}) // 4: masks 2,3
	assert.NoErr(t, err)
	t.Run("search", func(t *testing.T) {
		assert.Eq(t, "cur/1", snippets(native.LedgerScopeCurrent))
		for _, scope := range []string{native.LedgerScopeWorkspace, native.LedgerScopeAll} {
			got := snippets(scope)
			assert.Eq(t, true, strings.Contains(got, "cur/1"))
			assert.Eq(t, true, strings.Contains(got, "other/1"))
			assert.Eq(t, false, strings.Contains(got, "cur/2"))
			assert.Eq(t, false, strings.Contains(got, "cur/3"))
		}
	})
	t.Run("another session's search sees the rows", func(t *testing.T) {
		res, err := f.ledger.Search(withObserveScope(context.Background(), "other", "t"),
			native.LedgerQuery{Query: "masked", Scope: native.LedgerScopeAll})
		assert.NoErr(t, err)
		assert.Eq(t, 2, len(res.Hits))
	})
	t.Run("recall of a ref inside the interval does not exist", func(t *testing.T) {
		_, err := f.ledger.Fetch(ctx, "cur/2", 0, 0)
		assert.Err(t, err)
		_, err = f.ledger.Fetch(ctx, "cur/3", 0, 0)
		assert.Err(t, err)
		// Not the running session: the rows are readable (audit, R9).
		text, err := f.ledger.Fetch(context.Background(), "cur/2", 0, 0)
		assert.NoErr(t, err)
		assert.Eq(t, true, strings.Contains(text, "needle masked"))
	})
}

func TestLedgerRecallWindows(t *testing.T) {
	// Messages seq 1..8, the rewind record seq 9 (cut 3: masks 4..8), then messages
	// at seq 10 and 11 (labelled by seq).
	build := func(t *testing.T, rewound bool) (*ledgerFixture, context.Context) {
		f := newLedgerFixture(t)
		f.ledger.bindMasked(func(ctx context.Context, sid string) (store.SeqRanges, error) {
			return maskedRangesOf(ctx, f.repo, sid)
		})
		f.session(t, "cur", "/w")
		for i := 1; i <= 8; i++ {
			f.message(t, "cur", "user", fmt.Sprintf("message %d", i), "")
		}
		next := 9
		if rewound {
			_, err := f.repo.AppendEntries(context.Background(), "cur", store.NewEntry{Type: store.EntryRewind,
				JSON: json.RawMessage(`{"cut_seq":3,"target_turn":"x","scope":"conversation"}`)})
			assert.NoErr(t, err)
			next = 10
		}
		f.message(t, "cur", "user", fmt.Sprintf("message %d", next), "")
		f.message(t, "cur", "user", fmt.Sprintf("message %d", next+1), "")
		return f, withObserveScope(context.Background(), "cur", "t")
	}
	t.Run("a window that crosses the interval skips it", func(t *testing.T) {
		f, ctx := build(t, true)
		text, err := f.ledger.Fetch(ctx, "cur/2", 0, 3)
		assert.NoErr(t, err)
		assert.Eq(t, true, strings.Contains(text, "message 2") && strings.Contains(text, "message 3") && strings.Contains(text, "message 10"))
		assert.Eq(t, false, strings.Contains(text, "message 5"))
	})
	t.Run("a window ending right before the interval continues after it", func(t *testing.T) {
		f, ctx := build(t, true)
		text, err := f.ledger.Fetch(ctx, "cur/1", 0, 3) // shows 1,2,3; 4 is masked
		assert.NoErr(t, err)
		assert.Eq(t, true, strings.Contains(text, `recall(ref="cur/10")`))
		next, err := f.ledger.Fetch(ctx, "cur/10", 0, 3)
		assert.NoErr(t, err)
		assert.Eq(t, true, strings.Contains(next, "message 10"))
	})
	t.Run("a continuation that is visible keeps last+1", func(t *testing.T) {
		f, ctx := build(t, true)
		text, err := f.ledger.Fetch(ctx, "cur/1", 0, 2) // shows 1,2; 3 is visible
		assert.NoErr(t, err)
		assert.Eq(t, true, strings.Contains(text, `recall(ref="cur/3")`))
	})
	t.Run("without rewind records the continuation ref is last+1, verbatim", func(t *testing.T) {
		f, ctx := build(t, false)
		for _, c := range []struct {
			ref   string
			limit int
			want  string
		}{{"cur/1", 3, "cur/4"}, {"cur/4", 5, "cur/9"}, {"cur/2", 1, "cur/3"}} {
			text, err := f.ledger.Fetch(ctx, c.ref, 0, c.limit)
			assert.NoErr(t, err)
			assert.Eq(t, true, strings.Contains(text, fmt.Sprintf(`recall(ref=%q)`, c.want)))
		}
	})
}

// A rewind record written through another Store instance is seen by the
// readers of this Runner on their next call: there is no cache.
func TestMaskedRangesVisibleAcrossStores(t *testing.T) {
	path := newStorePath(t)
	repoA, err := store.Open(path)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repoA.Close() })
	repoB, err := store.Open(path)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repoB.Close() })
	runner, err := New(Config{Repository: repoA, Provider: mock.NewProvider(), Model: "m", ProviderName: "p"})
	assert.Require(t, assert.NoErr(t, err))
	f := &maskFixture{t: t, ctx: context.Background(), repo: repoB, runner: runner}
	f.threeTurns("s")

	v, err := runner.loadHistory(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, 6, len(v.items))
	card, err := runner.SessionCard(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, 3, card.Turns)

	f.rewind("s", 2) // through the other instance
	v, err = runner.loadHistory(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, []int64{1, 2}, itemSeqs(v))
	card, err = runner.SessionCard(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, 1, card.Turns)
}

// BenchmarkSessionCardMasked is BenchmarkSessionCard on the same 20k-entry
// session with three disjoint masked intervals of 1000 entries each.
func BenchmarkSessionCardMasked(b *testing.B) {
	repo, err := store.Open(newStoreBenchPath(b))
	assert.NoErr(b, err)
	b.Cleanup(func() { _ = repo.Close() })
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider()})
	assert.NoErr(b, err)
	seedCardBenchSession(b, repo, "big", 20000)
	ctx := context.Background()
	// Three disjoint intervals: rows 6000, 12000 and 18000 become rewind records
	// that mask the 1000 rows before them.
	for _, rec := range [][2]int64{{5000, 6000}, {11000, 12000}, {17000, 18000}} {
		body := fmt.Sprintf(`{"cut_seq":%d,"target_turn":"x","scope":"conversation"}`, rec[0])
		_, err := repo.DB().ExecContext(ctx, `UPDATE entries SET type = 'rewind', json = ? WHERE session_id = 'big' AND seq = ?`, body, rec[1])
		assert.NoErr(b, err)
	}
	recs, err := repo.RewindRecords(ctx, "big")
	assert.NoErr(b, err)
	assert.Eq(b, 3, len(recs))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		card, err := runner.SessionCard(ctx, "big")
		if err != nil {
			b.Fatalf("session card: %v", err)
		}
		if card.LatestSummary == "" {
			b.Fatalf("unexpected card: %+v", card)
		}
	}
}
