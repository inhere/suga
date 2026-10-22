package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// silentStreamServer streams partial on the first `silent` requests and then
// sends nothing more (the connection stays open, as a hung gateway does); later
// requests get the whole stream. The handler returns when the request is
// cancelled, i.e. when the provider's idle timeout fires.
func silentStreamServer(t *testing.T, partial, whole string, silent int32, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		n := hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n > silent {
			_, _ = io.WriteString(w, whole)
			return
		}
		_, _ = io.WriteString(w, partial)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // cancelled by the idle timeout (or by the test ending)
	}))
	t.Cleanup(server.Close)
	return server
}

// sse joins stream lines the way a provider sends them.
func sse(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// A stream that goes silent mid-answer is cancelled at the idle timeout and
// requested again by retryStream: the retry event names the idle wait, and the
// text of the abandoned attempt is void (restart).
func TestStreamIdleTimeoutRetries(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	const idle = 50 * time.Millisecond

	t.Run("openai: silent after text, retried from the start", func(t *testing.T) {
		var hits atomic.Int32
		partial := sse(`data: {"choices":[{"delta":{"content":"Hel"}}]}`)
		whole := sse(`data: {"choices":[{"delta":{"content":"Hello"}}]}`, `data: [DONE]`)
		server := silentStreamServer(t, partial, whole, 1, &hits)

		p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m",
			Retry: fastRetry, StreamIdleTimeout: idle})
		var events []agentcoreapi.StreamEvent
		ctx, report := withRetryReport(context.Background())
		res, err := p.Stream(ctx, req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "Hello", res.Content)
		assert.Require(t, assert.True(t, len(events) >= 3))
		assert.Eq(t, "Hel", events[0].Delta)
		assert.Eq(t, agentcoreapi.StreamEventRetry, events[1].Kind)
		assert.Eq(t, "stream idle for 50ms", events[1].Delta)
		assert.Eq(t, true, events[1].Metadata["restart"])
		assert.Eq(t, "Hello", events[2].Delta)
		assert.Eq(t, 1, report.Retries)
		assert.Eq(t, "stream idle for 50ms", report.Last)
	})

	t.Run("anthropic: silent after text, retried from the start", func(t *testing.T) {
		var hits atomic.Int32
		partial := sse(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`)
		whole := sse(
			`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`data: {"type":"message_stop"}`,
		)
		server := silentStreamServer(t, partial, whole, 1, &hits)

		p := NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: server.URL,
			Retry: fastRetry, StreamIdleTimeout: idle})
		var events []agentcoreapi.StreamEvent
		res, err := p.Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
		assert.Eq(t, agentcoreapi.StreamEventRetry, events[1].Kind)
		assert.Eq(t, "stream idle for 50ms", events[1].Delta)
	})

	t.Run("responses: silent before the terminal event, retried", func(t *testing.T) {
		var hits atomic.Int32
		partial := sse(`data: {"type":"response.output_text.delta","output_index":0,"delta":"Hel"}`)
		whole := sse(
			`data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`,
			`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]}]}}`,
		)
		server := silentStreamServer(t, partial, whole, 1, &hits)

		p := NewResponsesProvider(ResponsesConfig{OpenAIConfig: OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m",
			Retry: fastRetry, StreamIdleTimeout: idle}})
		var events []agentcoreapi.StreamEvent
		res, err := p.Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "Hello", res.Content)
		assert.Eq(t, "stream idle for 50ms", events[1].Delta)
	})

	t.Run("gives up when every attempt stays idle", func(t *testing.T) {
		var hits atomic.Int32
		server := silentStreamServer(t, sse(`data: {"choices":[{"delta":{"content":"Hel"}}]}`), "", 100, &hits)
		p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m",
			Retry: fastRetry, StreamIdleTimeout: idle})
		_, err := p.Stream(context.Background(), req, nil)
		assert.ErrMsgContains(t, err, "stream idle for 50ms on all 3 attempts")
		assert.Eq(t, int32(fastRetry.MaxAttempts), hits.Load())
	})
}

// A stream that keeps sending data - slowly, but within the idle timeout - is
// never cancelled.
func TestStreamIdleTimeoutKeepsASlowStream(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{"a", "b", "c"} {
			_, _ = io.WriteString(w, sse(`data: {"choices":[{"delta":{"content":"`+chunk+`"}}]}`))
			w.(http.Flusher).Flush()
			select {
			case <-time.After(30 * time.Millisecond): // well inside the idle bound
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, sse(`data: [DONE]`))
	}))
	t.Cleanup(server.Close)

	p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m",
		Retry: fastRetry, StreamIdleTimeout: 200 * time.Millisecond})
	res, err := p.Stream(context.Background(), &agentcoreapi.GenerateRequest{
		Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "abc", res.Content)
	assert.Eq(t, int32(1), hits.Load())
}

// StreamIdleTimeout "off" (NoTimeout) leaves a slow stream alone: the timeout
// the provider would otherwise have applied is far below the gap here.
func TestStreamIdleTimeoutOff(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(`data: {"choices":[{"delta":{"content":"Hel"}}]}`))
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(w, sse(`data: {"choices":[{"delta":{"content":"lo"}}]}`, `data: [DONE]`))
	}))
	t.Cleanup(server.Close)

	p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m",
		Retry: fastRetry, StreamIdleTimeout: NoTimeout})
	res, err := p.Stream(context.Background(), &agentcoreapi.GenerateRequest{
		Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "Hello", res.Content)
	assert.Eq(t, int32(1), hits.Load())
}

// The idle bound an unconfigured provider applies: the plain default, the longer
// reasoning default when the request asks the model to think first, and the
// configured value when there is one.
func TestStreamIdleTimeoutDefaults(t *testing.T) {
	openai := NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", ReasoningEffort: "high"})
	claude := NewClaudeProvider(ClaudeConfig{APIKey: "k", Model: "claude-sonnet-4-6", Thinking: "high"})
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}

	t.Run("plain request: the short default", func(t *testing.T) {
		body := NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m"}).buildRequestBody(req, "m", true)
		assert.Eq(t, DefaultStreamIdleTimeout, streamIdleTimeout(0, body))
	})
	t.Run("openai reasoning effort: the long default", func(t *testing.T) {
		body := openai.buildRequestBody(req, "m", true)
		assert.Eq(t, DefaultReasoningStreamIdleTimeout, streamIdleTimeout(openai.streamIdleTimeout, body))
	})
	t.Run("claude thinking: the long default", func(t *testing.T) {
		body := claude.buildRequestBody(req, "claude-sonnet-4-6")
		assert.Eq(t, DefaultReasoningStreamIdleTimeout, streamIdleTimeout(claude.streamIdleTimeout, body))
	})
	t.Run("configured value wins, off disables", func(t *testing.T) {
		body := openai.buildRequestBody(req, "m", true)
		assert.Eq(t, 5*time.Second, streamIdleTimeout(5*time.Second, body))
		assert.Eq(t, time.Duration(0), streamIdleTimeout(NoTimeout, body))
	})
	t.Run("the transport carries the configured bound", func(t *testing.T) {
		p := NewOpenAIProvider(OpenAIConfig{StreamIdleTimeout: 7 * time.Second})
		assert.Eq(t, 7*time.Second, p.streamIdleTimeout)
		c := NewClaudeProvider(ClaudeConfig{StreamIdleTimeout: 9 * time.Second})
		assert.Eq(t, 9*time.Second, c.streamIdleTimeout)
	})
}

// The idle error is a retryable stream failure, not a cancelled turn: retry
// stream reporting depends on it.
func TestStreamIdleErrorIsRetryable(t *testing.T) {
	var retryErr retryableStreamError = &streamIdleError{timeout: 120 * time.Second}
	assert.Eq(t, "stream idle for 2m0s", retryErr.retryReason())
	assert.ErrMsgContains(t, retryErr.giveUp("openai", 2), "openai stream idle for 2m0s on all 2 attempts")
	assert.ErrMsgContains(t, streamBodyError("openai", retryErr), "stream idle for 2m0s")
}
