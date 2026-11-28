package agentcore

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestExpandToolCalls(t *testing.T) {
	t.Run("single call keeps provider fields on its message", func(t *testing.T) {
		res := &GenerateResult{Content: "lead", Reasoning: "think", Metadata: map[string]any{"sig": "s1"}, ToolCalls: []ToolCall{{CallID: "c1", Name: "read"}}}
		messages := ExpandToolCalls(res)
		assert.Eq(t, 1, len(messages))
		assert.Eq(t, "lead", messages[0].Content)
		assert.Eq(t, "think", messages[0].Reasoning)
		assert.Eq(t, "s1", messages[0].Metadata["sig"])
		assert.Eq(t, "c1", messages[0].ToolCall.CallID)
		res.Metadata["sig"] = "changed"
		assert.Eq(t, "s1", messages[0].Metadata["sig"])
	})

	t.Run("batch metadata is present on every call but lead fields are not", func(t *testing.T) {
		messages := ExpandToolCalls(&GenerateResult{
			Content: "lead", Reasoning: "think", Metadata: map[string]any{"sig": "s1"},
			ToolCalls: []ToolCall{{CallID: "c1", Name: "read"}, {CallID: "c2", Name: "grep"}},
		})
		assert.Eq(t, 2, len(messages))
		for i, message := range messages {
			assert.Eq(t, "c1", message.Metadata[ToolBatchMetaKey])
			if i > 0 {
				assert.Empty(t, message.Content)
				assert.Empty(t, message.Reasoning)
				assert.Nil(t, message.Metadata["sig"])
			}
		}
	})

	t.Run("nil and empty results have no transcript", func(t *testing.T) {
		assert.Nil(t, ExpandToolCalls(nil))
		assert.Nil(t, ExpandToolCalls(&GenerateResult{}))
	})
}
