package axconfig

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/llm"
)

// TestStreamRequired covers providers.<id>[.models.<model>].stream_required:
// the model-level key wins, and a stream-only endpoint gets stream=true even
// from the non-streamed Generate path (summaries, for example).
func TestStreamRequired(t *testing.T) {
	loadYAML(t, `
model: gw/fast
providers:
  gw:
    type: openai
    stream_required: true
    models:
      fast: {}
      plain:
        stream_required: false
  resp:
    type: openai-responses
    stream_required: true
  claude:
    type: anthropic
    stream_required: true
`)

	t.Run("model over provider, unset keeps the provider value", func(t *testing.T) {
		s, err := ReadModelSettings("")
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, StreamRequiredFor(s.Main))
		plain, err := ReadModelSettings("gw/plain")
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, StreamRequiredFor(plain.Main)) // explicit model-level false
	})

	t.Run("Generate sends stream=true on every protocol", func(t *testing.T) {
		for _, ref := range []string{"gw/fast", "resp/m", "claude/m"} {
			s, err := ReadModelSettings(ref)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, true, sentStreamBody(t, s.Main)["stream"], ref)
		}
	})

	t.Run("without the flag the request stays non-streamed", func(t *testing.T) {
		loadYAML(t, "providers:\n  gw:\n    type: openai\n")
		s, err := ReadModelSettings("gw/m")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, false, sentStreamBody(t, s.Main)["stream"])
	})
}

// sentStreamBody runs one Generate request of spec's provider against a fake
// endpoint and returns the request body. The reply is a whole (non-SSE)
// completion for every protocol, which is what a gateway answers when it
// ignores stream=true.
func sentStreamBody(t *testing.T, spec ModelSpec) map[string]any {
	t.Helper()
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/responses"):
			_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
		}
	}))
	defer srv.Close()
	spec.Provider.BaseURL = srv.URL
	p, err := BuildProvider(spec)
	assert.Require(t, assert.NoErr(t, err))
	res, err := p.Generate(context.Background(), &llm.GenerateRequest{
		Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}},
	})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "ok", res.Content)
	return sent
}
