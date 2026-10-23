package personal

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// TestToolEventHookCarriesCallID: chat tracks a running sub-agent by the tool
// call it belongs to, so both the tool_call and its tool_result event carry the
// call's id, and a background agent's summary names the task it started.
func TestToolEventHookCarriesCallID(t *testing.T) {
	var events []Event
	hook := toolEventHook{sink: func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	}}

	t.Run("tool call", func(t *testing.T) {
		events = nil
		call := &api.ToolCall{CallID: "c7", Name: "agent", Arguments: map[string]any{"description": "survey"}}
		assert.NoErr(t, hook.BeforeTool(context.Background(), &agentcore.ToolEvent{Call: call}, nil))
		assert.Require(t, assert.Eq(t, 1, len(events)))
		assert.Eq(t, "tool_call", events[0].Kind)
		assert.Eq(t, "c7", events[0].CallID)
	})

	t.Run("tool result of a background agent", func(t *testing.T) {
		events = nil
		result := &api.ToolResult{CallID: "c9", Name: "agent", Content: "[background agent task t3 started: survey]",
			Metadata: map[string]any{"task_id": "t3", "background": true, "description": "survey"}}
		assert.NoErr(t, hook.AfterTool(context.Background(), &agentcore.ToolEvent{Result: result}, nil))
		assert.Require(t, assert.Eq(t, 1, len(events)))
		assert.Eq(t, "c9", events[0].CallID)
		assert.Require(t, assert.NotNil(t, events[0].AgentSummary))
		assert.Eq(t, "t3", events[0].AgentSummary.TaskID)
		assert.True(t, events[0].AgentSummary.Background)
	})
}

// TestChildProgressCarriesParentCall: a sub-agent's progress events name the
// agent tool call that started it, so chat counts them on that call's entry
// instead of on a description that two children could share.
func TestChildProgressCarriesParentCall(t *testing.T) {
	var events []Event
	emit := func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	}
	progress := childProgress(ChildRequest{Description: "survey", ParentCall: "c7", Emit: emit})
	assert.Require(t, assert.NotNil(t, progress))
	assert.NoErr(t, progress(context.Background(), Event{Kind: "tool_call", ToolName: "file_grep", Text: `{"pattern":"x"}`}))
	assert.Require(t, assert.Eq(t, 1, len(events)))
	assert.Eq(t, "agent_progress", events[0].Kind)
	assert.Eq(t, "c7", events[0].CallID)
	assert.Eq(t, "survey", events[0].Agent)
	assert.Eq(t, "file_grep", events[0].ToolName)

	// Anything but a tool call of the child stays out of the parent's stream.
	assert.NoErr(t, progress(context.Background(), Event{Kind: "text_delta", Text: "hello"}))
	assert.Eq(t, 1, len(events))
}
