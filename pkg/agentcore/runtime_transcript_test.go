package agentcore

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

type charExpandingRegistry struct{ *tools.Registry }

type charRawStore struct {
	*runtimeTestStorage
	raw []Message
}

func (s *charRawStore) AppendMessages(ctx context.Context, runID string, msgs ...Message) error {
	s.raw = append(s.raw, msgs...)
	return s.runtimeTestStorage.AppendMessages(ctx, runID, msgs...)
}

func (r charExpandingRegistry) ExpandCall(_ context.Context, call ToolCall) (ToolCall, bool) {
	if call.Name != "tool_call" {
		return call, false
	}
	name, _ := call.Arguments["name"].(string)
	args, _ := call.Arguments["arguments"].(map[string]any)
	return ToolCall{CallID: call.CallID, Name: name, Arguments: args}, true
}

func TestRuntime_Run_ToolTranscriptLeadOnFirstCall(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(&runtimeTestTool{name: "file_read"})
	reg.MustRegister(&runtimeTestToolWithSchema{runtimeTestTool: runtimeTestTool{name: "grep"},
		inputSchema: map[string]any{"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "integer"}}}})
	reg.MustRegister(&runtimeTestTool{name: "tool_call"})
	provider := &runtimeTestProvider{results: []*GenerateResult{
		{Content: "lead", Reasoning: "think", Metadata: map[string]any{"sig": "s1"}, ToolCalls: []ToolCall{
			{CallID: "c1", Name: "file_read", Arguments: map[string]any{"path": "a"}},
			{CallID: "c2", Name: "tool_call", Arguments: map[string]any{"name": "file_read", "arguments": map[string]any{"path": "b"}}},
			{CallID: "c3", Name: "grep", Arguments: map[string]any{"limit": "5"}},
		}, FinishReason: FinishToolCalls},
		{Content: "done", FinishReason: FinishFinal},
	}}
	store := &charRawStore{runtimeTestStorage: newRuntimeTestStorage()}
	res, err := New(Config{Provider: provider, Storage: store, Tools: charExpandingRegistry{reg},
		PromptBuilder: runtimeTestPromptBuilder{}, ContextBuilder: runtimeTestContextBuilder{}}).
		Run(context.Background(), RunInput{RunID: "r", AgentID: "a", Instruction: "x"})
	assert.NoErr(t, err)
	assert.NotNil(t, res)
	assert.Eq(t, "done", res.Output)

	var as []Message
	for _, m := range store.raw {
		if m.Role == "assistant" && m.ToolCall != nil {
			as = append(as, m)
		}
	}
	assert.Eq(t, 3, len(as))
	assert.Eq(t, "lead", as[0].Content)
	assert.Eq(t, "think", as[0].Reasoning)
	assert.Eq(t, "s1", as[0].Metadata["sig"])
	for i, m := range as {
		assert.Eq(t, "c1", m.Metadata[ToolBatchMetaKey])
		if i > 0 {
			assert.Empty(t, m.Content)
			assert.Empty(t, m.Reasoning)
			assert.Nil(t, m.Metadata["sig"])
		}
	}
	assert.Eq(t, "file_read", as[1].ToolCall.Name)
	v, ok := as[2].ToolCall.Arguments["limit"].(float64)
	assert.True(t, ok)
	assert.Eq(t, float64(5), v)
}
