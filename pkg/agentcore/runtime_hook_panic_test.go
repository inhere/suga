package agentcore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// panickingHook panics in the tool stage a test names, to prove a host hook
// cannot take the run down and that a broken call hook fails closed.
type panickingHook struct {
	NopHook
	stage string
	seen  *[]string
}

func (h *panickingHook) OnError(_ context.Context, event *ErrorEvent, _ *Control) error {
	if h.seen != nil {
		*h.seen = append(*h.seen, event.Stage+": "+event.Err.Error())
	}
	return nil
}

func (h *panickingHook) BeforeTool(context.Context, *ToolEvent, *Control) error {
	if h.stage == "before_tool" {
		panic("before tool boom")
	}
	return nil
}

func (h *panickingHook) AfterTool(context.Context, *ToolEvent, *Control) error {
	if h.stage == "after_tool" {
		panic("after tool boom")
	}
	return nil
}

// TestRuntime_Run_ToolHookPanic covers rule 2 for a host hook: a panic in
// BeforeTool refuses the call (the tool never runs, the run fails closed and the
// reason reaches the error event), and a panic in AfterTool keeps the tool result
// in the transcript while it is recorded as well.
func TestRuntime_Run_ToolHookPanic(t *testing.T) {
	run := func(t *testing.T, stage, runID string) (*runtimeTestTool, *runtimeTestStorage, *[]string, error) {
		t.Helper()
		tool := &runtimeTestTool{name: "lookup", result: &ToolResult{Content: "tool-output"}}
		reg := tools.NewRegistry()
		reg.MustRegister(tool)
		store := newRuntimeTestStorage()
		var seen []string
		runtime := New(Config{
			Provider: &runtimeTestProvider{results: []*GenerateResult{{
				ToolCalls:    []ToolCall{{CallID: "call-1", Name: "lookup"}},
				FinishReason: FinishToolCalls,
			}}},
			Storage:        store,
			Tools:          reg,
			PromptBuilder:  runtimeTestPromptBuilder{},
			ContextBuilder: runtimeTestContextBuilder{},
			Hooks:          []Hook{&panickingHook{stage: stage, seen: &seen}},
		})
		_, err := runtime.Run(context.Background(), RunInput{RunID: runID, AgentID: "agent-1", Instruction: "go"})
		return tool, store, &seen, err
	}

	t.Run("a panic in BeforeTool refuses the call", func(t *testing.T) {
		tool, store, seen, err := run(t, "before_tool", "run-hook-panic-before")
		var hookErr *HookError
		assert.Require(t, assert.Eq(t, true, errors.As(err, &hookErr)))
		assert.Eq(t, true, strings.Contains(err.Error(), "before tool boom"))
		assert.Eq(t, 0, len(tool.calls))
		assert.Require(t, assert.Eq(t, 1, len(*seen)))
		assert.Eq(t, true, strings.HasPrefix((*seen)[0], "before_tool: "))
		saved := store.runs["run-hook-panic-before"]
		if saved == nil || saved.Status != RunFailed {
			t.Fatalf("expected a failed run state, got %#v", saved)
		}
	})

	t.Run("a panic in AfterTool keeps the tool result and records it", func(t *testing.T) {
		tool, store, seen, err := run(t, "after_tool", "run-hook-panic-after")
		var hookErr *HookError
		assert.Require(t, assert.Eq(t, true, errors.As(err, &hookErr)))
		assert.Eq(t, true, strings.Contains(err.Error(), "after tool boom"))
		assert.Eq(t, 1, len(tool.calls))
		assert.Require(t, assert.Eq(t, 1, len(*seen)))
		assert.Eq(t, true, strings.HasPrefix((*seen)[0], "after_tool: "))
		messages := store.messages["run-hook-panic-after"]
		assert.Require(t, assert.Eq(t, 3, len(messages)))
		assert.Eq(t, "tool-output", messages[2].ToolResult.Content)
	})
}
