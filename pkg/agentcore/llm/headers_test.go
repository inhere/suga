package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gookit/goutil/x/assert"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

func TestOpenAIProviderSendsConfiguredHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Headers: map[string]string{
		"x-opencode-session": SessionIDPlaceholder,
		"X-Route":            "conv-" + SessionIDPlaceholder,
		"X-Static":           "v",
	}})
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}

	t.Run("session id substituted", func(t *testing.T) {
		_, err := p.Generate(agentcoreapi.WithSessionID(context.Background(), "sess-1"), req)
		assert.NoErr(t, err)
		assert.Eq(t, "sess-1", got.Get("X-Opencode-Session"))
		assert.Eq(t, "conv-sess-1", got.Get("X-Route"))
		assert.Eq(t, "v", got.Get("X-Static"))
		assert.Eq(t, DefaultUserAgent, got.Get("User-Agent"))
		assert.Eq(t, "Bearer k", got.Get("Authorization"))
	})
	t.Run("no session skips placeholder headers", func(t *testing.T) {
		_, err := p.Generate(context.Background(), req)
		assert.NoErr(t, err)
		assert.Eq(t, "", got.Get("X-Opencode-Session"))
		assert.Eq(t, "", got.Get("X-Route"))
		assert.Eq(t, "v", got.Get("X-Static"))
	})
	t.Run("user agent can be overridden", func(t *testing.T) {
		custom := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Headers: map[string]string{"User-Agent": "my-agent/1.0"}})
		_, err := custom.Generate(context.Background(), req)
		assert.NoErr(t, err)
		assert.Eq(t, "my-agent/1.0", got.Get("User-Agent"))
	})
}

// The deprecated llm helpers forward to api: a ctx marked either way carries
// the same session id.
func TestDeprecatedSessionIDHelpers(t *testing.T) {
	assert.Eq(t, "s1", SessionIDFrom(agentcoreapi.WithSessionID(context.Background(), "s1")))
	assert.Eq(t, "s1", agentcoreapi.SessionIDFrom(WithSessionID(context.Background(), "s1")))
}

// TestHeaderPlaceholders: providers.<id>.headers expands the session, turn,
// model and provider placeholders; a header that needs a value this request
// cannot fill is left out, while a value without placeholders always goes.
func TestHeaderPlaceholders(t *testing.T) {
	vars := headerVars{Model: "gpt-6.1-sol", Provider: "openai-codex"}

	t.Run("every placeholder", func(t *testing.T) {
		ctx := agentcoreapi.WithSessionID(context.Background(), "sg_1")
		ctx = agentcoreapi.WithTurnID(ctx, "t_9")
		req, _ := http.NewRequest(http.MethodPost, "https://gw.example/v1/responses", nil)
		applyHeaders(ctx, req, map[string]string{
			"Session-Id": SessionIDPlaceholder,
			"X-Turn":     "turn-" + TurnIDPlaceholder,
			"X-Model":    ModelPlaceholder,
			"x-provider": ProviderPlaceholder,
			"X-Combined": ProviderPlaceholder + "/" + ModelPlaceholder,
			"X-Static":   "v",
		}, vars)
		assert.Eq(t, "sg_1", req.Header.Get("Session-Id"))
		assert.Eq(t, "turn-t_9", req.Header.Get("X-Turn"))
		assert.Eq(t, "gpt-6.1-sol", req.Header.Get("X-Model"))
		assert.Eq(t, "openai-codex", req.Header.Get("X-Provider"))
		assert.Eq(t, "openai-codex/gpt-6.1-sol", req.Header.Get("X-Combined"))
		assert.Eq(t, "v", req.Header.Get("X-Static"))
	})

	t.Run("a placeholder without a value skips its header", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://gw.example/v1/responses", nil)
		applyHeaders(context.Background(), req, map[string]string{
			"Session-Id": SessionIDPlaceholder,
			"X-Turn":     TurnIDPlaceholder,
			"X-Model":    ModelPlaceholder,
			"X-Static":   "v",
		}, vars)
		assert.Eq(t, "", req.Header.Get("Session-Id"), "no session, no header")
		assert.Eq(t, "", req.Header.Get("X-Turn"), "no turn, no header")
		assert.Eq(t, "gpt-6.1-sol", req.Header.Get("X-Model"))
		assert.Eq(t, "v", req.Header.Get("X-Static"))
	})

	t.Run("a provider without a model id leaves the model header out", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://gw.example/v1/responses", nil)
		applyHeaders(context.Background(), req, map[string]string{"X-Model": ModelPlaceholder}, headerVars{Provider: "p"})
		assert.Eq(t, "", req.Header.Get("X-Model"))
	})
}
