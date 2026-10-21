package personal

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// foldMetaTool is a test tool that declares only the metadata the fold layer
// reads (ReadOnly / External / Prefetchable), so the tests exercise the same
// decision the real file_read, grep, web_fetch and shell_exec tools get.
type foldMetaTool struct {
	name string
	meta tools.Meta
}

func (t foldMetaTool) Name() string        { return t.name }
func (t foldMetaTool) Description() string { return "test tool" }
func (t foldMetaTool) Kind() tools.Kind    { return tools.KindNative }
func (t foldMetaTool) ToolMeta() tools.Meta {
	return t.meta
}
func (t foldMetaTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: t.name, InputSchema: map[string]any{"type": "object"}}
}
func (t foldMetaTool) Execute(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
	return &agentcore.ToolResult{Content: "ok"}, nil
}

// foldReadTool, foldExecTool and foldFetchTool are the three metadata classes
// the folding decision tells apart: a local re-runnable read, a tool with side
// effects and an external tool.
var (
	foldReadTool  = foldMetaTool{name: "file_read", meta: tools.Meta{Category: tools.CategoryRead, ReadOnly: true, Prefetchable: true}}
	foldExecTool  = foldMetaTool{name: "shell_exec", meta: tools.Meta{Category: tools.CategoryExecute}}
	foldFetchTool = foldMetaTool{name: "web_fetch", meta: tools.Meta{Category: tools.CategoryFetch, ReadOnly: true, External: true}}
)

// foldBuildSetup opens a store and a runner with the folding tools registered
// and a fresh session, and returns a context builder of that session whose
// first turn starts at message 0. BuildMessages then runs the real
// compose/estimate path.
func foldBuildSetup(t *testing.T, cfg Config) (*contextBuilder, *Runner, *store.Store, string) {
	t.Helper()
	repo := openObserveRepo(t)
	registry := tools.NewRegistry()
	for _, tool := range []foldMetaTool{foldReadTool, foldExecTool, foldFetchTool} {
		registry.Register(tool)
	}
	cfg.Repository, cfg.Tools = repo, registry
	if cfg.Provider == nil {
		cfg.Provider = mock.NewProvider(mock.Final("ok"))
	}
	r, err := New(cfg)
	assert.Require(t, assert.NoErr(t, err))
	sess, err := r.NewSession(context.Background(), "fold-size", "t")
	assert.Require(t, assert.NoErr(t, err))
	b := r.newContextBuilder(sess.ID, "turn-1", historyView{}, nil, nil, "")
	return b, r, repo, sess.ID
}

// foldCtx is the turn scope of a builder: runner.Run sets the same one, and
// the fold events are attributed to the session through it.
func foldCtx(sessionID string) context.Context {
	return withObserveScope(context.Background(), sessionID, "turn-1")
}

// foldResultSteps builds a user message plus n assistant tool steps of the
// named tool, each with reasoning, a short path argument and a result of
// resultChars characters.
func foldResultSteps(n, resultChars int, name string) []agentcore.Message {
	msgs := []agentcore.Message{{Role: "user", Content: "task"}}
	result := strings.Repeat("r", resultChars)
	for i := 0; i < n; i++ {
		call := &agentcore.ToolCall{CallID: fmt.Sprintf("c%d", i), Name: name, Arguments: map[string]any{"path": "a.go"}}
		msgs = append(msgs,
			agentcore.Message{Role: "assistant", Reasoning: "think", ToolCall: call},
			agentcore.Message{Role: "tool", Content: result, ToolResult: &agentcore.ToolResult{
				CallID: call.CallID, Name: name, Content: result}})
	}
	return msgs
}

// sizeFoldEvents returns the fold events written by the size trigger.
func sizeFoldEvents(t *testing.T, repo *store.Store, sessionID string) []store.Event {
	t.Helper()
	var out []store.Event
	for _, ev := range eventsOf(t, repo, sessionID, store.EventFold) {
		if ev.Attrs["trigger"] == "size" {
			out = append(out, ev)
		}
	}
	return out
}

// TestFoldBySize: folding starts only once the pre-fold estimate of a request
// passes context.fold_at × the compaction threshold, and then stays on for the
// segment (design 2026-10-21, D1-D3).
func TestFoldBySize(t *testing.T) {
	// window 4000 -> reserve 800 -> compaction threshold 3200, so folding
	// starts above 1600 tokens with the default fold_at 0.5. The big view below
	// is about 2400 tokens of tool text, the small one a few tokens.
	base := Config{ContextWindow: 4000, FoldAt: 0.5, FoldEvery: 2, FoldKeep: 1}

	t.Run("below the threshold nothing is folded", func(t *testing.T) {
		b, _, repo, id := foldBuildSetup(t, base)
		state := &agentcore.RunState{Messages: foldResultSteps(4, 2, "file_read")}
		view, err := b.BuildMessages(foldCtx(b.sessionID), state)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, len(sizeFoldEvents(t, repo, id)))
		for _, m := range view {
			assert.False(t, m.Role == "assistant" && folded(m), "a step was folded below the threshold")
		}
	})

	t.Run("passing the threshold turns folding on and records it", func(t *testing.T) {
		b, _, repo, id := foldBuildSetup(t, base)
		state := &agentcore.RunState{Messages: foldResultSteps(8, 1200, "file_read")}
		view, err := b.BuildMessages(foldCtx(b.sessionID), state)
		assert.Require(t, assert.NoErr(t, err))

		events := sizeFoldEvents(t, repo, id)
		assert.Require(t, assert.Eq(t, 1, len(events)))
		tokens, _ := events[0].Attrs["tokens"].(float64)
		threshold, _ := events[0].Attrs["threshold"].(float64)
		assert.True(t, tokens > 1600, "trigger size %v did not pass fold_at x threshold", tokens)
		assert.Eq(t, float64(3200), threshold)
		assert.Eq(t, 0.5, events[0].Attrs["at"])

		// The trigger turns folding on for this very request: 8 steps with
		// every 2 / keep 1 fold 6 steps.
		foldedSteps := 0
		for _, m := range view {
			if m.Role == "assistant" && folded(m) {
				foldedSteps++
			}
		}
		assert.Eq(t, 6, foldedSteps)
		assert.StrContains(t, view[2].Content, "[已折叠的 file_read 结果：原 1200 字符")

		sess, err := repo.GetSession(context.Background(), id)
		assert.NoErr(t, err)
		assert.Eq(t, true, metaBool(sess.Meta[metaFoldActive]))
	})

	t.Run("the segment keeps folding after the view shrank", func(t *testing.T) {
		b, _, repo, id := foldBuildSetup(t, base)
		big := &agentcore.RunState{Messages: foldResultSteps(8, 1200, "file_read")}
		if _, err := b.BuildMessages(foldCtx(b.sessionID), big); err != nil {
			t.Fatalf("first request: %v", err)
		}
		// A later request of the same segment is far below the trigger again.
		small := &agentcore.RunState{Messages: foldResultSteps(4, 2, "file_read")}
		view, err := b.BuildMessages(foldCtx(b.sessionID), small)
		assert.Require(t, assert.NoErr(t, err))
		foldedSteps := 0
		for _, m := range view {
			if m.Role == "assistant" && folded(m) {
				foldedSteps++
			}
		}
		assert.True(t, foldedSteps > 0, "folding stopped after the view shrank")
		assert.Eq(t, 1, len(sizeFoldEvents(t, repo, id))) // recorded once
	})

	t.Run("a new segment resets the trigger", func(t *testing.T) {
		b, r, _, id := foldBuildSetup(t, base)
		big := &agentcore.RunState{Messages: foldResultSteps(8, 1200, "file_read")}
		_, err := b.BuildMessages(foldCtx(b.sessionID), big)
		assert.Require(t, assert.NoErr(t, err))
		// A segment break writes segmentMeta (fold_active false) and the next
		// turn's builder reads the flag back like runner.Run does.
		meta := segmentMeta(&segmentContext{})
		assert.Eq(t, false, metaBool(meta[metaFoldActive]))
		next := r.newContextBuilder(id, "turn-2", historyView{}, nil, nil, "")
		next.foldActive = metaBool(meta[metaFoldActive])
		view, err := next.BuildMessages(foldCtx(b.sessionID), &agentcore.RunState{Messages: foldResultSteps(4, 2, "file_read")})
		assert.Require(t, assert.NoErr(t, err))
		for _, m := range view {
			assert.False(t, m.Role == "assistant" && folded(m), "folding survived the segment break")
		}
	})

	t.Run("the default cadence after the trigger is every 10 / keep 6", func(t *testing.T) {
		// fold_every unset: the size trigger alone turns folding on with its own
		// cadence (design Q3). window 8000 -> threshold 6400, trigger 3200; 17
		// steps of 1200 chars are about 5100 tokens.
		foldedCfg := Config{ContextWindow: 8000, FoldAt: 0.5}
		b, _, repo, id := foldBuildSetup(t, foldedCfg)
		state := &agentcore.RunState{Messages: foldResultSteps(17, 1200, "file_read")}
		view, err := b.BuildMessages(foldCtx(b.sessionID), state)
		assert.Require(t, assert.NoErr(t, err))
		foldedSteps := 0
		for _, m := range view {
			if m.Role == "assistant" && folded(m) {
				foldedSteps++
			}
		}
		assert.Eq(t, 10, foldedSteps) // (17 - 6) / 10 * 10
		var boundary float64
		for _, ev := range eventsOf(t, repo, id, store.EventFold) {
			if v, ok := ev.Attrs["boundary"].(float64); ok {
				boundary = v
			}
		}
		assert.Eq(t, float64(10), boundary)
	})

	t.Run("fold_every alone keeps the old behaviour", func(t *testing.T) {
		// fold_at 0: the configured cadence folds from the first step, whatever
		// the size, and no size event is written.
		b, _, repo, id := foldBuildSetup(t, Config{ContextWindow: 4000, FoldEvery: 2, FoldKeep: 1})
		view, err := b.BuildMessages(foldCtx(b.sessionID), &agentcore.RunState{Messages: foldResultSteps(4, 2, "file_read")})
		assert.Require(t, assert.NoErr(t, err))
		foldedSteps := 0
		for _, m := range view {
			if m.Role == "assistant" && folded(m) {
				foldedSteps++
			}
		}
		assert.Eq(t, 2, foldedSteps)
		assert.Eq(t, 0, len(sizeFoldEvents(t, repo, id)))
	})

	t.Run("fold_at 0 disables folding when no cadence is set", func(t *testing.T) {
		b, _, repo, id := foldBuildSetup(t, Config{ContextWindow: 4000, FoldAt: 0})
		view, err := b.BuildMessages(foldCtx(b.sessionID), &agentcore.RunState{Messages: foldResultSteps(8, 1200, "file_read")})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, len(sizeFoldEvents(t, repo, id)))
		for _, m := range view {
			assert.False(t, m.Role == "assistant" && folded(m), "a step was folded with fold_at 0")
		}
	})
}

// TestFoldToolResults: old results of re-runnable read-only tools are replaced
// by a one-line note; the call arguments stay (design 2026-10-21, 0.2).
func TestFoldToolResults(t *testing.T) {
	refoldable := func(name string) bool { return name == "file_read" }
	long := strings.Repeat("r", foldArgChars+1)

	t.Run("folds only large results of re-runnable read tools", func(t *testing.T) {
		msgs := []agentcore.Message{
			{Role: "user", Content: "task"},
			{Role: "assistant", ToolCall: &agentcore.ToolCall{Name: "file_read", Arguments: map[string]any{"path": "a.go"}}},
			{Role: "tool", Content: long, ToolResult: &agentcore.ToolResult{Name: "file_read", Content: long}},
			{Role: "tool", Content: "short", ToolResult: &agentcore.ToolResult{Name: "file_read", Content: "short"}},
			{Role: "tool", Content: "boom", ToolResult: &agentcore.ToolResult{Name: "file_read", Content: long, IsError: true}},
			{Role: "tool", Content: long, ToolResult: &agentcore.ToolResult{Name: "shell_exec", Content: long}},
			{Role: "tool", Content: long, ToolResult: &agentcore.ToolResult{Name: "web_fetch", Content: long}},
			{Role: "assistant", Content: "done"},
		}
		out, st := foldToolResults(msgs, 1, refoldable)
		assert.Eq(t, 1, st.results)
		assert.Eq(t, foldArgChars+1, st.charsBefore)
		assert.True(t, st.charsAfter < st.charsBefore, "note %d is not shorter than %d", st.charsAfter, st.charsBefore)
		note := out[2].ToolResult.Content
		assert.StrContains(t, note, "file_read")
		assert.StrContains(t, note, fmt.Sprintf("原 %d 字符", foldArgChars+1))
		assert.Eq(t, note, out[2].Content) // the rendered content is the note too
		assert.Eq(t, "a.go", out[1].ToolCall.Arguments["path"])
		assert.Eq(t, "short", out[3].ToolResult.Content)
		assert.Eq(t, long, out[4].ToolResult.Content) // error result kept
		assert.Eq(t, long, out[5].ToolResult.Content) // not read-only
		assert.Eq(t, long, out[6].ToolResult.Content) // external
		assert.Eq(t, long, msgs[2].ToolResult.Content)
	})

	t.Run("only results before the boundary are folded", func(t *testing.T) {
		msgs := foldResultSteps(4, foldArgChars+1, "file_read")
		out, st := foldToolResults(msgs, 2, refoldable)
		assert.Eq(t, 2, st.results)
		// 4 steps, results 1..4: the first two (steps 0 and 1) are before the
		// boundary of 2 folded steps.
		assert.StrContains(t, out[2].ToolResult.Content, "已折叠")
		assert.StrContains(t, out[4].ToolResult.Content, "已折叠")
		assert.Eq(t, msgs[6].ToolResult.Content, out[6].ToolResult.Content)
	})
}

// TestFoldToolResultsByMetadata: the decision comes from the tool registry's
// metadata, in the real compose path (fold_at 0 = fold by cadence from the
// start).
func TestFoldToolResultsByMetadata(t *testing.T) {
	b, _, _, _ := foldBuildSetup(t, Config{ContextWindow: 4000, FoldEvery: 2, FoldKeep: 1})
	msgs := []agentcore.Message{{Role: "user", Content: "task"}}
	long := strings.Repeat("r", foldArgChars+1)
	for _, name := range []string{"file_read", "shell_exec", "web_fetch", "file_read", "unknown_tool", "file_read"} {
		msgs = append(msgs,
			agentcore.Message{Role: "assistant", Reasoning: "think", ToolCall: &agentcore.ToolCall{Name: name, Arguments: map[string]any{"path": "a.go"}}},
			agentcore.Message{Role: "tool", Content: long, ToolResult: &agentcore.ToolResult{Name: name, Content: long}})
	}
	view, err := b.BuildMessages(foldCtx(b.sessionID), &agentcore.RunState{Messages: msgs})
	assert.Require(t, assert.NoErr(t, err))
	// 6 steps with every 2 / keep 1 fold 4 steps: the first four results.
	want := []bool{true, false, false, true}
	for i, wantFolded := range want {
		got := strings.Contains(view[2+2*i].ToolResult.Content, "已折叠")
		assert.Eq(t, wantFolded, got)
	}
	assert.Eq(t, long, view[2+2*4].ToolResult.Content) // unknown tool: metadata unknown
	assert.Eq(t, long, view[2+2*5].ToolResult.Content) // after the boundary
}
