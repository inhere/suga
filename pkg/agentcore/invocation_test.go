package agentcore

import (
	"context"
	"testing"

	"github.com/inhere/suga/pkg/agentcore/tools"
)

// invocationTool records the invocation id it reads from the execution context,
// i.e. what a host's in-process tool (wiring.Options.ExtraTools) sees.
type invocationTool struct {
	seen []string
}

func (t *invocationTool) Name() string        { return "act" }
func (t *invocationTool) Description() string { return "records its invocation id" }
func (t *invocationTool) Schema() ToolSchema {
	return ToolSchema{Name: t.Name(), InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
}

func (t *invocationTool) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	t.seen = append(t.seen, tools.InvocationID(ctx))
	return &ToolResult{CallID: call.CallID, Name: call.Name, Content: "ok"}, nil
}

// invocationHook records the invocation id at both hook stages of a call.
type invocationHook struct {
	NopHook
	before []string
	after  []string
}

func (h *invocationHook) BeforeTool(ctx context.Context, _ *ToolEvent, _ *Control) error {
	h.before = append(h.before, tools.InvocationID(ctx))
	return nil
}

func (h *invocationHook) AfterTool(ctx context.Context, _ *ToolEvent, _ *Control) error {
	h.after = append(h.after, tools.InvocationID(ctx))
	return nil
}

// TestRuntimeRunInvocationIDPerToolCall: one id per call, the same in BeforeTool,
// in the tool and in AfterTool, and different between two calls.
func TestRuntimeRunInvocationIDPerToolCall(t *testing.T) {
	store := newRuntimeTestStorage()
	reg := tools.NewRegistry()
	tool := &invocationTool{}
	reg.MustRegister(tool)
	hook := &invocationHook{}
	provider := &runtimeTestProvider{results: []*GenerateResult{
		{
			ToolCalls: []ToolCall{
				{CallID: "call-1", Name: "act", Arguments: map[string]any{"path": "a.txt"}},
				{CallID: "call-2", Name: "act", Arguments: map[string]any{"path": "b.txt"}},
			},
			FinishReason: FinishToolCalls,
		},
		{Content: "done", FinishReason: FinishFinal},
	}}

	_, err := New(Config{
		Provider:       provider,
		Storage:        store,
		Tools:          reg,
		PromptBuilder:  runtimeTestPromptBuilder{},
		ContextBuilder: runtimeTestContextBuilder{},
		Hooks:          []Hook{hook},
	}).Run(context.Background(), RunInput{RunID: "turn-9", AgentID: "agent-1", Instruction: "act twice"})
	if err != nil {
		t.Fatalf("expected the run to complete, got %v", err)
	}

	if len(tool.seen) != 2 {
		t.Fatalf("expected two tool executions, got %#v", tool.seen)
	}
	if tool.seen[0] == "" || tool.seen[1] == "" {
		t.Fatalf("expected every call to carry an invocation id, got %#v", tool.seen)
	}
	if tool.seen[0] == tool.seen[1] {
		t.Fatalf("expected a different invocation id per call, got %#v", tool.seen)
	}
	if tool.seen[0] != "turn-9/call-1" || tool.seen[1] != "turn-9/call-2" {
		t.Fatalf("expected the run id and the provider call id in the invocation id, got %#v", tool.seen)
	}
	if len(hook.before) != 2 || len(hook.after) != 2 {
		t.Fatalf("expected two tool hook rounds, got before=%#v after=%#v", hook.before, hook.after)
	}
	for i := range tool.seen {
		if hook.before[i] != tool.seen[i] || hook.after[i] != tool.seen[i] {
			t.Fatalf("expected one id for the whole call %d, got before=%q tool=%q after=%q",
				i, hook.before[i], tool.seen[i], hook.after[i])
		}
	}
}
