package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// seedReportEvents writes a deliberately varied event set: several providers
// and models, error status, estimated runs, ttft only sometimes, token and cost
// attrs in every shape the aggregation reads, tool/denial rows, compaction and
// tool_search rows, and a second session (also a sub-agent of the first).
func seedReportEvents(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	createSession(t, s, "a", "/w")
	createSession(t, s, "b", "/w")
	createSession(t, s, "sub", "/w")
	assert.Require(t, assert.NoErr(t, s.PatchSessionMeta(ctx, "sub", map[string]any{MetaKind: KindAgent, MetaParentSession: "a"})))

	base := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	add := func(sid string, minutes int, e Event) {
		t.Helper()
		e.SessionID = sid
		e.StartedAt = base.Add(time.Duration(minutes) * time.Minute)
		_, err := s.AppendEvent(ctx, e)
		assert.Require(t, assert.NoErr(t, err))
	}
	// Models: two providers, two models, one model with a single call (p50=p95).
	for i := 0; i < 20; i++ {
		add("a", i, Event{Kind: EventModel, Name: "p1", DurationMs: int64(100 + i*7), TTFTMs: int64(i % 3 * 10),
			InputTokens: 100, OutputTokens: 40, CacheReadTokens: int64(i), CacheWriteTokens: 5,
			Status: statusFor(i), Attrs: map[string]any{"model": "m1", "cost_usd": 0.5, "estimated": i%4 == 0}})
	}
	for i := 0; i < 6; i++ {
		add("a", 30+i, Event{Kind: EventModel, Name: "p2", DurationMs: int64(300 + i*11),
			InputTokens: 10, OutputTokens: 2, Attrs: map[string]any{"model": "m2", "cost_usd": 0.25}})
	}
	// A single call of a third model: nearest-rank p50 = p95 = that value.
	add("b", 5, Event{Kind: EventModel, Name: "p1", DurationMs: 42, InputTokens: 3, OutputTokens: 1,
		Attrs: map[string]any{"model": "m3"}})
	// A model event whose attrs are not the usual shape (name as a number, cost
	// as a string): AggregateEvents counts it as model "" without a cost.
	add("b", 6, Event{Kind: EventModel, Name: "p1", DurationMs: 5, Attrs: map[string]any{"model": 7, "cost_usd": "0.9"}})
	// Tokens with cache beyond input: the per-event floor at 0 applies.
	add("b", 7, Event{Kind: EventModel, Name: "p2", DurationMs: 9, InputTokens: 3, CacheReadTokens: 10,
		Attrs: map[string]any{"model": "m1", "cost_usd": 1.5}})
	// Tools: shell and read, one error, denial for a tool never executed.
	for i := 0; i < 10; i++ {
		add("a", 40+i, Event{Kind: EventTool, Name: "shell", DurationMs: int64(20 + i*3), Status: statusFor(i),
			Attrs: map[string]any{"raw_bytes": float64(1000 + i), "inline_bytes": 400, "spilled": i == 3}})
	}
	add("a", 50, Event{Kind: EventTool, Name: "read", DurationMs: 7, Attrs: map[string]any{"raw_bytes": 12}})
	add("a", 51, Event{Kind: EventDenial, Name: "shell"})
	add("a", 52, Event{Kind: EventDenial, Name: "never_run"})
	// Sub-agent session: only in the tree report.
	add("sub", 60, Event{Kind: EventModel, Name: "p1", DurationMs: 11, InputTokens: 5, Attrs: map[string]any{"model": "m1", "cost_usd": 0.01}})
	add("sub", 61, Event{Kind: EventTool, Name: "shell", DurationMs: 3, Attrs: map[string]any{"raw_bytes": 1}})
	add("a", 70, Event{Kind: EventCompaction})
	add("a", 71, Event{Kind: EventCompaction})
	add("a", 72, Event{Kind: EventToolSearch})
	// Kinds the report ignores, also under a tool name.
	add("a", 73, Event{Kind: EventTurn, Name: "shell", DurationMs: 999})
	add("a", 74, Event{Kind: EventHook, Name: "shell"})
}

func statusFor(i int) string {
	if i%5 == 0 {
		return "error"
	}
	return "ok"
}

// TestBuildReportMatchesAggregateEvents: the SQL aggregation of BuildReport must
// produce exactly what the previous implementation produced (ListEvents into
// AggregateEvents), for a session tree, for one session and for the whole store
// with a since bound.
func TestBuildReportMatchesAggregateEvents(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedReportEvents(t, s)
	old := func(t *testing.T, f EventFilter) Report {
		t.Helper()
		f.Kinds = reportKinds
		events, err := s.ListEvents(ctx, f)
		assert.Require(t, assert.NoErr(t, err))
		return AggregateEvents(events)
	}
	since := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

	cases := []struct {
		name string
		f    EventFilter
	}{
		{"whole store", EventFilter{}},
		{"one session", EventFilter{SessionID: "a"}},
		{"session tree", EventFilter{SessionID: "a", WithAgents: true}},
		{"sub-agent session", EventFilter{SessionID: "sub"}},
		{"since bound", EventFilter{Since: since}},
		{"session tree since", EventFilter{SessionID: "a", WithAgents: true, Since: since}},
		{"a session without events", EventFilter{SessionID: "b", TurnID: "missing"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.BuildReport(ctx, c.f)
			assert.Require(t, assert.NoErr(t, err))
			want := old(t, c.f)
			if !reflect.DeepEqual(want, got) {
				wj, _ := json.MarshalIndent(want, "", " ")
				gj, _ := json.MarshalIndent(got, "", " ")
				t.Fatalf("report differs\nwant: %s\ngot:  %s", wj, gj)
			}
			// The rendered JSON (TracePort, stats --json) must stay byte for byte.
			wj, wErr := json.Marshal(want)
			gj, gErr := json.Marshal(got)
			assert.Require(t, assert.NoErr(t, wErr))
			assert.Require(t, assert.NoErr(t, gErr))
			assert.Eq(t, string(wj), string(gj))
		})
	}

	t.Run("the values themselves are the expected ones", func(t *testing.T) {
		rep, err := s.BuildReport(ctx, EventFilter{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, rep.Compactions)
		assert.Eq(t, 1, rep.ToolSearch)
		byName := map[string]ToolStat{}
		for _, st := range rep.Tools {
			byName[st.Name] = st
		}
		shell := byName["shell"]
		assert.Eq(t, 11, shell.Calls) // 10 in a, 1 in sub
		assert.Eq(t, 2, shell.Errors)
		assert.Eq(t, 1, shell.Denials)
		assert.Eq(t, int64(1000+1001+1002+1003+1004+1005+1006+1007+1008+1009+1), shell.RawBytes)
		assert.Eq(t, 1, shell.Spilled)
		assert.Eq(t, 0, byName["never_run"].Calls)
		assert.Eq(t, 1, byName["never_run"].Denials)
		providers := map[string]ModelStat{}
		for _, st := range rep.Models {
			providers[st.Provider+"/"+st.Model] = st
		}
		assert.Eq(t, 21, providers["p1/m1"].Calls)             // 20 in a, 1 in sub
		assert.Eq(t, 4, providers["p1/m1"].Errors)             // i%5 == 0
		assert.Eq(t, int64(0), providers["p2/m1"].InputTokens) // 3 - 10 floored at 0
		assert.Eq(t, 1.5, providers["p2/m1"].CostUSD)
		assert.Eq(t, int64(42), providers["p1/m3"].P50Ms) // one call: p50 = p95
		assert.Eq(t, int64(42), providers["p1/m3"].P95Ms)
	})
}
