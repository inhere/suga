package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gookit/goutil/x/assert"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// streamOnlyServer answers only requests with stream=true (like a gateway in
// front of a subscription model) and rejects the others the way such a
// gateway does. A streamed request gets a whole completion, which the provider
// handles as "gateway ignored stream=true".
func streamOnlyServer(t *testing.T, result string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if streaming, _ := body["stream"].(bool); !streaming {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"stream must be true"}}`)
			return
		}
		_, _ = io.WriteString(w, result)
	}))
	t.Cleanup(server.Close)
	return server, &bodies
}

// TestStreamRequiredRoutesGenerateThroughStream: with StreamRequired set, the
// non-streamed Generate call goes through the streaming request shape
// (stream=true), so a stream-only endpoint can serve it.
func TestStreamRequiredRoutesGenerateThroughStream(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}

	t.Run("openai", func(t *testing.T) {
		server, bodies := streamOnlyServer(t, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
		p := NewOpenAIProvider(OpenAIConfig{BaseURL: server.URL, StreamRequired: true})
		res, err := p.Generate(context.Background(), req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "ok", res.Content)
		assert.Eq(t, true, (*bodies)[0]["stream"])
		assert.Eq(t, true, (*bodies)[0]["stream_options"].(map[string]any)["include_usage"])
	})

	t.Run("responses", func(t *testing.T) {
		server, bodies := streamOnlyServer(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
		p := NewResponsesProvider(ResponsesConfig{OpenAIConfig: OpenAIConfig{BaseURL: server.URL, StreamRequired: true}})
		res, err := p.Generate(context.Background(), req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "ok", res.Content)
		assert.Eq(t, true, (*bodies)[0]["stream"])
	})

	t.Run("claude", func(t *testing.T) {
		server, bodies := streamOnlyServer(t, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
		p := NewClaudeProvider(ClaudeConfig{BaseURL: server.URL, StreamRequired: true})
		res, err := p.Generate(context.Background(), req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "ok", res.Content)
		assert.Eq(t, true, (*bodies)[0]["stream"])
	})

	t.Run("a stream-only endpoint rejects the plain Generate", func(t *testing.T) {
		server, bodies := streamOnlyServer(t, `{}`)
		_, err := NewOpenAIProvider(OpenAIConfig{BaseURL: server.URL}).Generate(context.Background(), req)
		assert.Err(t, err) // proves the server above really is stream-only
		assert.Eq(t, false, (*bodies)[0]["stream"])
	})
}
