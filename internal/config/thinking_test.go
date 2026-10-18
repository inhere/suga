package axconfig

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/llm"
)

// sentBody builds spec's provider against a fake server and returns the JSON
// body of one request.
func sentBody(t *testing.T, spec ModelSpec) map[string]any {
	t.Helper()
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	spec.Provider.BaseURL = srv.URL
	p, err := BuildProvider(spec)
	assert.Require(t, assert.NoErr(t, err))
	_, err = p.Generate(context.Background(), &llm.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}})
	assert.Require(t, assert.NoErr(t, err))
	return sent
}

func TestThinkingStyle(t *testing.T) {
	t.Run("parse", func(t *testing.T) {
		for _, v := range ThinkingStyles {
			got, err := ParseThinkingStyle(v)
			assert.NoErr(t, err)
			assert.Eq(t, v, got)
		}
		got, err := ParseThinkingStyle(" Adaptive ")
		assert.NoErr(t, err)
		assert.Eq(t, ThinkingStyleAdaptive, got)
		_, err = ParseThinkingStyle("eager")
		assert.ErrMsgContains(t, err, `unknown thinking style "eager"`)
	})
	t.Run("model over provider, unset keeps the model heuristic", func(t *testing.T) {
		loadYAML(t, `
model: cc/fast
providers:
  cc:
    type: anthropic
    thinking_style: budget
    stream_required: true
    models:
      fast:
        thinking_style: adaptive
      deep: {}
`)
		s, err := ReadModelSettings("")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, ThinkingStyleAdaptive, ThinkingStyleFor(s.Main))
		assert.True(t, StreamRequiredFor(s.Main))

		deep, err := ReadModelSettings("cc/deep")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, ThinkingStyleBudget, ThinkingStyleFor(deep.Main)) // provider value
		assert.True(t, StreamRequiredFor(deep.Main))
	})
	t.Run("unset stays empty", func(t *testing.T) {
		loadYAML(t, "providers:\n  cc:\n    type: openai\n")
		s, err := ReadModelSettings("cc/m")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "", ThinkingStyleFor(s.Main))
		assert.False(t, StreamRequiredFor(s.Main))
	})
	t.Run("invalid config styles are rejected", func(t *testing.T) {
		loadYAML(t, "providers:\n  cc:\n    type: openai\n    thinking_style: eager\n")
		_, err := ReadProviders()
		assert.ErrMsgContains(t, err, "providers.cc.thinking_style: unknown thinking style")
		loadYAML(t, "providers:\n  cc:\n    type: openai\n    models:\n      m:\n        thinking_style: eager\n")
		_, err = ReadProviders()
		assert.ErrMsgContains(t, err, "providers.cc.models.m.thinking_style: unknown thinking style")
	})
}

func TestThinkingStyleIsSentByTheClaudeProvider(t *testing.T) {
	loadYAML(t, `
model: anthropic/sonnet-4-5
providers:
  anthropic:
    type: anthropic
    thinking: high
    models:
      sonnet-4-5: {}
      pinned-adaptive:
        model_id: claude-sonnet-4-5-20250929
        thinking_style: adaptive
      pinned-none:
        model_id: claude-sonnet-4-5-20250929
        thinking_style: none
      pinned-budget:
        model_id: claude-opus-5-5
        thinking_style: budget
`)
	body := func(t *testing.T, ref string) map[string]any {
		t.Helper()
		s, err := ReadModelSettings(ref)
		assert.Require(t, assert.NoErr(t, err))
		return sentClaudeBody(t, s.Main)
	}
	t.Run("adaptive over a model name that would use a budget", func(t *testing.T) {
		got := body(t, "anthropic/pinned-adaptive")
		assert.Eq(t, map[string]any{"type": "adaptive", "display": "summarized"}, got["thinking"])
		assert.Eq(t, map[string]any{"effort": "high"}, got["output_config"])
	})
	t.Run("budget over a model name that would use adaptive", func(t *testing.T) {
		got := body(t, "anthropic/pinned-budget")
		assert.Eq(t, map[string]any{"type": "enabled", "budget_tokens": float64(16384)}, got["thinking"])
		assert.Nil(t, got["output_config"])
	})
	t.Run("none sends no thinking parameters", func(t *testing.T) {
		got := body(t, "anthropic/pinned-none")
		assert.Nil(t, got["thinking"])
		assert.Nil(t, got["output_config"])
	})
	t.Run("unset keeps the model-name heuristic", func(t *testing.T) {
		got := body(t, "")
		assert.Eq(t, map[string]any{"type": "enabled", "budget_tokens": float64(16384)}, got["thinking"])
	})
}

// sentClaudeBody builds spec's Claude provider against a fake /v1/messages and
// returns the JSON body of one request (the reply itself is a fixed text).
func sentClaudeBody(t *testing.T, spec ModelSpec) map[string]any {
	t.Helper()
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	spec.Provider.BaseURL = srv.URL
	p, err := BuildProvider(spec)
	assert.Require(t, assert.NoErr(t, err))
	_, err = p.Generate(context.Background(), &llm.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}})
	assert.Require(t, assert.NoErr(t, err))
	return sent
}

func TestThinking(t *testing.T) {
	doc := `
model: cc/fast
providers:
  cc:
    type: openai
    thinking: HIGH
    models:
      fast:
        thinking: low
      deep: {}
`
	t.Run("parse", func(t *testing.T) {
		for _, v := range ThinkingLevels {
			got, err := ParseThinking(v)
			assert.NoErr(t, err)
			assert.Eq(t, v, got)
		}
		got, err := ParseThinking(" High ")
		assert.NoErr(t, err)
		assert.Eq(t, "high", got)
		_, err = ParseThinking("extreme")
		assert.ErrMsgContains(t, err, `unknown thinking level "extreme"`)
	})
	t.Run("override, then model, then provider", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "low", s.Main.ThinkingLevel())
		s.Main.Thinking = "max"
		assert.Eq(t, "max", s.Main.ThinkingLevel())
		deep, err := ReadModelSettings("cc/deep")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "high", deep.Main.ThinkingLevel()) // provider value, normalized
	})
	t.Run("invalid config levels are rejected", func(t *testing.T) {
		loadYAML(t, "providers:\n  cc:\n    type: openai\n    thinking: huge\n")
		_, err := ReadProviders()
		assert.ErrMsgContains(t, err, "providers.cc.thinking: unknown thinking level")
		loadYAML(t, "providers:\n  cc:\n    type: openai\n    models:\n      m:\n        thinking: huge\n")
		_, err = ReadProviders()
		assert.ErrMsgContains(t, err, "providers.cc.models.m.thinking: unknown thinking level")
	})
	t.Run("sent as reasoning_effort", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("")
		assert.Require(t, assert.NoErr(t, err))
		for level, want := range map[string]any{"high": "high", "xhigh": "xhigh", "off": "off", "minimal": "minimal", "auto": nil} {
			spec := s.Main
			spec.Thinking = level
			assert.Eq(t, want, sentBody(t, spec)["reasoning_effort"], level)
		}
		loadYAML(t, "providers:\n  cc:\n    type: openai\n")
		plain, err := ReadModelSettings("cc/m")
		assert.Require(t, assert.NoErr(t, err))
		_, present := sentBody(t, plain.Main)["reasoning_effort"]
		assert.False(t, present) // nothing configured: the provider default
	})
}
