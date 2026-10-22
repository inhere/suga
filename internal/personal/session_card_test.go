package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// sessionCardFixture builds the runner and the small store helpers the card tests
// share: a session, its entries and its turns, all written through the store.
type sessionCardFixture struct {
	t      *testing.T
	ctx    context.Context
	runner *Runner
	repo   *store.Store
}

func newSessionCardFixture(t *testing.T) *sessionCardFixture {
	t.Helper()
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(), Model: "m", ProviderName: "p"})
	assert.NoErr(t, err)
	return &sessionCardFixture{t: t, ctx: context.Background(), runner: runner, repo: repo}
}

// session creates a session with the given meta (nil for none) and returns it as
// stored (the created_at/updated_at the store assigned).
func (f *sessionCardFixture) session(id, title string, meta map[string]any) store.Session {
	f.t.Helper()
	model := store.Session{ID: id, Title: title, Status: store.SessionOpen, Meta: meta}
	assert.NoErr(f.t, f.repo.CreateSession(f.ctx, model))
	stored, err := f.repo.GetSession(f.ctx, id)
	assert.NoErr(f.t, err)
	return stored
}

// entry appends one entry whose body is the JSON of body.
func (f *sessionCardFixture) entry(sessionID, typ string, body any) store.Entry {
	f.t.Helper()
	raw, err := json.Marshal(body)
	assert.NoErr(f.t, err)
	es, err := f.repo.AppendEntries(f.ctx, sessionID, store.NewEntry{Type: typ, JSON: raw})
	assert.NoErr(f.t, err)
	return es[0]
}

// turn records a completed turn.
func (f *sessionCardFixture) turn(sessionID, id string) {
	f.t.Helper()
	assert.NoErr(f.t, f.repo.CreateTurn(f.ctx, store.Turn{ID: id, SessionID: sessionID}))
	assert.NoErr(f.t, f.repo.UpdateTurn(f.ctx, store.Turn{ID: id, Status: store.TurnCompleted}))
}

// card reads the card and requires no error.
func (f *sessionCardFixture) card(sessionID string) SessionCard {
	f.t.Helper()
	card, err := f.runner.SessionCard(f.ctx, sessionID)
	assert.NoErr(f.t, err)
	return card
}

// TestSessionCard covers what a session card reports: the identity and state of
// the session, its newest handoff/compaction summary, the open plan steps, the
// cumulative usage, activity times and counts.
func TestSessionCard(t *testing.T) {
	t.Run("an empty session", func(t *testing.T) {
		f := newSessionCardFixture(t)
		stored := f.session("empty", "空会话", nil)
		card := f.card("empty")
		assert.Eq(t, "empty", card.SessionID)
		assert.Eq(t, "空会话", card.Title)
		assert.Eq(t, "", card.ShortName)
		assert.Eq(t, store.SessionOpen, card.State)
		assert.Eq(t, "", card.Origin) // created before meta.origin, or without one
		assert.Eq(t, stored.CreatedAt.UTC(), card.CreatedAt.UTC())
		// No entry: the session's own update time stands in for the activity.
		assert.Eq(t, stored.UpdatedAt.UTC(), card.LastActiveAt.UTC())
		assert.Eq(t, "", card.LatestSummary)
		assert.Eq(t, "", card.SummaryKind)
		assert.True(t, card.SummaryAt.IsZero())
		assert.True(t, card.PlanUpdatedAt.IsZero())
		assert.Len(t, card.OpenSteps, 0)
		assert.Eq(t, SessionCardUsage{}, card.Usage)
		assert.Eq(t, 0, card.Turns)
		assert.Eq(t, 0, card.SegmentCount)
	})

	t.Run("a session with only conversation: name, origin, turns and usage", func(t *testing.T) {
		f := newSessionCardFixture(t)
		f.session("talk", "对话", map[string]any{metaShortName: " talk-k7 ", store.MetaOrigin: store.OriginChat})
		f.turn("talk", "t1")
		f.entry("talk", store.EntryUser, map[string]any{"Role": "user", "Content": "开始"})
		f.turn("talk", "t2")
		f.entry("talk", store.EntryAssistant, map[string]any{"Role": "assistant", "Content": "好的"})
		// One model call, plus a sub-agent session of this one whose call counts
		// in the tree total.
		f.session("talk-child", "子任务", map[string]any{store.MetaKind: store.KindAgent, store.MetaParentSession: "talk"})
		_, err := f.repo.AppendEvent(f.ctx, store.Event{SessionID: "talk", Kind: store.EventModel,
			InputTokens: 100, OutputTokens: 20, Attrs: map[string]any{"cost_usd": 0.25}})
		assert.NoErr(t, err)
		_, err = f.repo.AppendEvent(f.ctx, store.Event{SessionID: "talk-child", Kind: store.EventModel,
			InputTokens: 50, OutputTokens: 10})
		assert.NoErr(t, err)

		card := f.card("talk")
		assert.Eq(t, "talk-k7", card.ShortName)
		assert.Eq(t, store.OriginChat, card.Origin)
		assert.Eq(t, 2, card.Turns)
		assert.Eq(t, 0, card.SegmentCount)
		assert.Eq(t, "", card.LatestSummary)
		assert.Eq(t, SessionCardUsage{Calls: 2, InputTokens: 150, OutputTokens: 30, CostUSD: 0.25}, card.Usage)
		// The last entry is the assistant one, so the card is active after creation.
		assert.False(t, card.LastActiveAt.Before(card.CreatedAt))
	})

	t.Run("the newest compaction summary wins", func(t *testing.T) {
		f := newSessionCardFixture(t)
		f.session("compact", "压缩", nil)
		f.entry("compact", store.EntryCompaction, compactionRecord{Reason: segmentReasonStart})
		first := f.entry("compact", store.EntryCompaction, compactionRecord{Summary: "第一份交接包", Reason: segmentReasonThreshold})
		time.Sleep(2 * time.Millisecond) // distinct stored timestamps
		second := f.entry("compact", store.EntryCompaction, compactionRecord{Summary: "第二份交接包", Reason: segmentReasonManual})

		card := f.card("compact")
		assert.Eq(t, "第二份交接包", card.LatestSummary)
		assert.Eq(t, SessionCardSummaryCompaction, card.SummaryKind)
		assert.Eq(t, segmentReasonManual, card.SummaryReason)
		assert.Eq(t, second.CreatedAt.UTC(), card.SummaryAt.UTC())
		assert.True(t, card.SummaryAt.After(first.CreatedAt))
		// start record + the two breaks
		assert.Eq(t, 3, card.SegmentCount)
	})

	t.Run("a handoff summary, also when an empty start record is newer", func(t *testing.T) {
		f := newSessionCardFixture(t)
		f.session("handoff", "交接", map[string]any{store.MetaOrigin: store.OriginRun})
		f.entry("handoff", store.EntryHandoff, handoffRecord{Source: "src", Summary: "从 src 交接过来的摘要"})
		// A start record written after it carries no summary: it must not hide it.
		f.entry("handoff", store.EntryCompaction, compactionRecord{Reason: segmentReasonStart})

		card := f.card("handoff")
		assert.Eq(t, "从 src 交接过来的摘要", card.LatestSummary)
		assert.Eq(t, SessionCardSummaryHandoff, card.SummaryKind)
		assert.Eq(t, "", card.SummaryReason)
		assert.False(t, card.SummaryAt.IsZero())
		assert.Eq(t, store.OriginRun, card.Origin)
	})

	t.Run("a plan that is partly done", func(t *testing.T) {
		f := newSessionCardFixture(t)
		f.session("plan", "计划", nil)
		f.entry("plan", store.EntryPlan, native.Plan{Steps: []native.PlanStep{
			{Step: "旧步骤", Status: native.PlanPending},
		}})
		latest := f.entry("plan", store.EntryPlan, native.Plan{Explanation: "做到一半", Steps: []native.PlanStep{
			{Step: "读代码", Status: native.PlanCompleted},
			{Step: "写实现", Status: native.PlanInProgress},
			{Step: "跑测试\n并汇报", Status: native.PlanPending},
			{Step: "无需做", Status: native.PlanCompleted},
		}})

		card := f.card("plan")
		// Only the open steps, in plan order, collapsed to one line each.
		assert.Eq(t, []string{"写实现", "跑测试 并汇报"}, card.OpenSteps)
		assert.Eq(t, latest.CreatedAt.UTC(), card.PlanUpdatedAt.UTC())
	})

	t.Run("a plan whose steps are all done has no open steps but keeps its time", func(t *testing.T) {
		f := newSessionCardFixture(t)
		f.session("done", "做完", nil)
		f.entry("done", store.EntryPlan, native.Plan{Steps: []native.PlanStep{
			{Step: "甲", Status: native.PlanCompleted},
		}})
		card := f.card("done")
		assert.Len(t, card.OpenSteps, 0)
		assert.False(t, card.PlanUpdatedAt.IsZero())
	})

	t.Run("an unknown session is a not-found error", func(t *testing.T) {
		f := newSessionCardFixture(t)
		_, err := f.runner.SessionCard(f.ctx, "nope")
		assert.ErrIs(t, err, ErrSessionNotFound)
		var notFound SessionNotFoundError
		assert.True(t, errors.As(err, &notFound))
		assert.Eq(t, "nope", notFound.ID)
	})

	t.Run("reading a card writes nothing", func(t *testing.T) {
		f := newSessionCardFixture(t)
		f.session("ro", "只读", map[string]any{metaShortName: "ro-k7"})
		f.turn("ro", "t1")
		f.entry("ro", store.EntryUser, map[string]any{"Role": "user", "Content": "开始"})
		f.entry("ro", store.EntryPlan, native.Plan{Steps: []native.PlanStep{{Step: "甲", Status: native.PlanInProgress}}})
		f.entry("ro", store.EntryCompaction, compactionRecord{Summary: "交接包", Reason: segmentReasonIdle})

		entriesBefore, err := f.repo.ListEntries(f.ctx, "ro", store.EntryFilter{})
		assert.NoErr(t, err)
		turnsBefore, err := f.repo.ListTurns(f.ctx, "ro")
		assert.NoErr(t, err)
		metaBefore, err := f.repo.GetSession(f.ctx, "ro")
		assert.NoErr(t, err)
		eventsBefore, err := f.repo.ListEvents(f.ctx, store.EventFilter{SessionID: "ro"})
		assert.NoErr(t, err)

		f.card("ro")

		entriesAfter, err := f.repo.ListEntries(f.ctx, "ro", store.EntryFilter{})
		assert.NoErr(t, err)
		turnsAfter, err := f.repo.ListTurns(f.ctx, "ro")
		assert.NoErr(t, err)
		metaAfter, err := f.repo.GetSession(f.ctx, "ro")
		assert.NoErr(t, err)
		eventsAfter, err := f.repo.ListEvents(f.ctx, store.EventFilter{SessionID: "ro"})

		assert.Eq(t, len(entriesBefore), len(entriesAfter))
		assert.Eq(t, entriesBefore, entriesAfter)
		assert.Eq(t, len(turnsBefore), len(turnsAfter))
		assert.Eq(t, turnsBefore, turnsAfter)
		assert.Eq(t, metaBefore.Meta, metaAfter.Meta)
		assert.Eq(t, metaBefore.UpdatedAt.UTC(), metaAfter.UpdatedAt.UTC())
		assert.Eq(t, metaBefore.Status, metaAfter.Status)
		assert.NoErr(t, err)
		assert.Eq(t, eventsBefore, eventsAfter)
	})

	t.Run("a read-only store answers the same card", func(t *testing.T) {
		// A store opened with OpenReadOnly fails on any write, so a card that
		// builds from it proves the read path writes nothing at all.
		path := newStorePath(t)
		ctx := context.Background()
		rw, err := store.Open(path)
		assert.NoErr(t, err)
		assert.NoErr(t, rw.CreateSession(ctx, store.Session{ID: "ro-store", Title: "只读库"}))
		raw, err := json.Marshal(compactionRecord{Summary: "交接包", Reason: segmentReasonIdle})
		assert.NoErr(t, err)
		_, err = rw.AppendEntries(ctx, "ro-store", store.NewEntry{Type: store.EntryCompaction, JSON: raw})
		assert.NoErr(t, err)
		assert.NoErr(t, rw.Close())

		ro, err := store.OpenReadOnly(path)
		assert.NoErr(t, err)
		t.Cleanup(func() { _ = ro.Close() })
		runner, err := New(Config{Repository: ro, Provider: mock.NewProvider()})
		assert.NoErr(t, err)
		card, err := runner.SessionCard(context.Background(), "ro-store")
		assert.NoErr(t, err)
		assert.Eq(t, "只读库", card.Title)
		assert.Eq(t, "交接包", card.LatestSummary)
		assert.Eq(t, 1, card.SegmentCount)
	})
}

// seedCardBenchSession fills one session with n entries (a summary record every
// 200 rows, so the newest summary is found near the end) in a single statement:
// the benchmark measures the card, not the seeding.
func seedCardBenchSession(tb testing.TB, repo *store.Store, sessionID string, n int) {
	tb.Helper()
	assert.NoErr(tb, repo.CreateSession(context.Background(), store.Session{ID: sessionID, Title: "bench"}))
	_, err := repo.DB().ExecContext(context.Background(), `WITH RECURSIVE seq(i) AS (
		SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i < ?)
		INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
		SELECT ?, i, '', CASE WHEN i % 200 = 0 THEN 'compaction' ELSE 'assistant' END,
		CASE WHEN i % 200 = 0 THEN '{"summary":"交接包","reason":"threshold"}' ELSE '{"Role":"assistant","Content":"x"}' END,
		'2026-01-01T00:00:00.000000000Z' FROM seq`, n, sessionID)
	assert.NoErr(tb, err)
}

// BenchmarkSessionCard measures the card of a session with 20k entries: every
// query is index-scoped, so reading it costs a few reads rather than a scan of
// the log.
func BenchmarkSessionCard(b *testing.B) {
	repo, err := store.Open(newStoreBenchPath(b))
	assert.NoErr(b, err)
	b.Cleanup(func() { _ = repo.Close() })
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider()})
	assert.NoErr(b, err)
	seedCardBenchSession(b, repo, "big", 20000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		card, err := runner.SessionCard(ctx, "big")
		if err != nil {
			b.Fatalf("session card: %v", err)
		}
		if card.SegmentCount != 100 || card.LatestSummary == "" {
			b.Fatalf("unexpected card: %+v", card)
		}
	}
}

// newStoreBenchPath is newStorePath for benchmarks (b.TempDir).
func newStoreBenchPath(b *testing.B) string {
	b.Helper()
	return fmt.Sprintf("%s/db/sessions.db", b.TempDir())
}
