package personal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore"
)

func TestForkSeedForCall(t *testing.T) {
	t.Run("uses the current request and places the target call in the middle", func(t *testing.T) {
		tracker := newPrefixTracker()
		base := &agentcore.GenerateRequest{System: "system", Tools: []agentcore.ToolSchema{{Name: "file_read"}}, Messages: []agentcore.Message{{Role: "user", Content: "background"}}}
		tracker.observeRequest("s", base)
		tracker.observeStep("s", "turn-1", &agentcore.GenerateResult{Content: "lead", Reasoning: "think", Metadata: map[string]any{"sig": "x"}, ToolCalls: []agentcore.ToolCall{
			{CallID: "c1", Name: "file_read", Arguments: map[string]any{"path": "a"}},
			{CallID: "c2", Name: "file_read", Arguments: map[string]any{"path": "b"}},
			{CallID: "c3", Name: "file_read", Arguments: map[string]any{"path": "c"}},
		}})
		tracker.observeToolResult("s", "c1", agentcore.ToolResult{CallID: "c1", Name: "file_read", Content: "real result"})
		tr, err := i18n.New(i18n.LanguageEN)
		assert.NoErr(t, err)
		r := &Runner{cfg: Config{Model: "model", ProviderName: "provider", Translator: tr}, rec: &recorder{prefix: tracker}}
		seed, err := r.ForkSeedForCall(context.Background(), "s", "c2", "inspect b")
		assert.NoErr(t, err)
		assert.Eq(t, "turn-1", seed.BaseTurn)
		assert.Eq(t, "call", seed.Source)
		assert.Eq(t, "prefix", seed.Reuse)
		assert.Eq(t, "background", seed.Request.Messages[0].Content)
		assert.Eq(t, "real result", seed.Request.Messages[2].Content)
		assert.True(t, strings.Contains(seed.Request.Messages[4].Content, "inspect b"))
		assert.True(t, strings.Contains(seed.Request.Messages[6].Content, "not run"))
		assert.Eq(t, "background", base.Messages[0].Content)
	})

	t.Run("returns a translated context hint when the seed is too large", func(t *testing.T) {
		tracker := newPrefixTracker()
		tracker.observeRequest("s", &agentcore.GenerateRequest{System: strings.Repeat("x", 100), Messages: []agentcore.Message{{Role: "user", Content: "context"}}})
		tracker.observeStep("s", "turn-1", &agentcore.GenerateResult{ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "read"}}})
		tr, err := i18n.New(i18n.LanguageEN)
		assert.NoErr(t, err)
		r := &Runner{cfg: Config{ContextWindow: 1, Translator: tr}, rec: &recorder{prefix: tracker}}
		_, err = r.ForkSeedForCall(context.Background(), "s", "c1", "task")
		var unavailable *ForkUnavailableError
		assert.True(t, errors.As(err, &unavailable))
		assert.Eq(t, "context", unavailable.Reason)
		assert.NotEmpty(t, unavailable.Hint())
	})
}
