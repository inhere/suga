package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// TestClearContextStartsAnEmptySegment pins what a --clear-ctx break leaves
// behind: the same session with no kept history and no handoff package, an
// empty plan, and a context_cleared event carrying what left the context.
func TestClearContextStartsAnEmptySegment(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(fmt.Sprintf("answer-%d", n))
	}}
	runner := compactRunner(t, repo, prov, nil, nil)
	ctx := context.Background()
	first, err := runner.Run(ctx, RunRequest{Prompt: "remember the plan"})
	assert.NoErr(t, err)
	// The session has a plan of its own, which the clear has to drop.
	plan, _ := json.Marshal(native.Plan{Steps: []native.PlanStep{{Step: "ship it", Status: native.PlanInProgress}}})
	_, err = repo.AppendEntries(ctx, first.SessionID, store.NewEntry{Type: store.EntryPlan, JSON: plan})
	assert.NoErr(t, err)

	cleared, err := runner.ClearContext(ctx, first.SessionID)
	assert.NoErr(t, err)
	assert.Eq(t, 2, cleared.Messages) // the user message and the answer
	assert.Eq(t, true, cleared.Tokens > 0)
	assert.Eq(t, true, cleared.Segment > 0)

	// The next turn's view is empty: no kept messages, no package.
	view, err := runner.SegmentHistory(ctx, first.SessionID)
	assert.NoErr(t, err)
	assert.Eq(t, 0, len(view.Items))
	assert.Eq(t, "", view.Summary)
	// The session itself is untouched (one row, same title).
	sessions, err := repo.ListSessions(ctx)
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(sessions))
	assert.Eq(t, first.SessionID, sessions[0].ID)
	// The plan is empty now, and the trace records the clear.
	latest, err := repo.LatestEntry(ctx, first.SessionID, store.EntryPlan)
	assert.NoErr(t, err)
	assert.Require(t, assert.NotNil(t, latest))
	assert.Eq(t, `{"plan":[]}`, string(latest.JSON))
	events, err := repo.ListEvents(ctx, store.EventFilter{SessionID: first.SessionID, Kinds: []string{store.EventContextCleared}})
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(events))
	assert.Eq(t, 2, attrInt(events[0].Attrs["dropped_messages"]))

	// The next turn sends none of the old messages, and the old ones are still
	// in the log (they are out of the context, not deleted).
	before := len(prov.reqs)
	_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "what is next"})
	assert.NoErr(t, err)
	assert.Eq(t, true, len(prov.reqs) > before)
	for _, m := range prov.reqs[before].Messages {
		assert.Eq(t, false, m.Content == "remember the plan" || m.Content == "answer-1")
	}
	hits, err := repo.SearchEntries(ctx, store.EntrySearch{SessionIDs: []string{first.SessionID}, Query: "remember"})
	assert.NoErr(t, err)
	assert.Eq(t, true, len(hits) > 0)
}

// testLoadedStore is an in-memory tools.LoadedStore for the visibility test.
type testLoadedStore struct {
	names []string
}

func (s *testLoadedStore) LoadedTools(context.Context) ([]string, error) { return s.names, nil }

func (s *testLoadedStore) SaveLoadedTools(_ context.Context, names []string) error {
	s.names = names
	return nil
}

// TestClearLoadedForgetsTheDeferredTools pins the visibility half of the break:
// the persisted set and the segment table are emptied, so a later segment lists
// the deferred tools again as if nothing had been loaded.
func TestClearLoadedForgetsTheDeferredTools(t *testing.T) {
	ctx := context.Background()
	reg := tools.NewRegistry()
	reg.Register(fnTool{name: "later", fn: func() (*agentcore.ToolResult, error) { return &agentcore.ToolResult{Name: "later"}, nil }})
	vis := tools.NewVisibility(reg, tools.VisibilityOptions{Mode: tools.DeferredAlways, NativeTools: []string{"later"}})
	st := &testLoadedStore{names: []string{"later"}}
	vis.Bind(st, nil)
	vis.FoldLoaded(ctx)
	_, loaded := vis.DeferredStats(ctx)
	assert.Eq(t, 1, loaded)

	assert.NoErr(t, vis.ClearLoaded(ctx))
	_, loaded = vis.DeferredStats(ctx)
	assert.Eq(t, 0, loaded)
	assert.Eq(t, 0, len(st.names))
}
