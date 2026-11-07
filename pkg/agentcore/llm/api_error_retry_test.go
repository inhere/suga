package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// overloadMessage is what the API of the report answered (issue suga-h2c8): no
// code and no type, only this text, and the turn failed right away.
const overloadMessage = "Our servers are currently overloaded. Please try again later."

// scriptedServer answers the i-th request with bodies[i] (the last body repeats
// for every later request) and counts the requests, so a case can send a
// refusal and then a whole answer.
func scriptedServer(t *testing.T, contentType string, bodies ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		n := min(int(hits.Add(1))-1, len(bodies)-1)
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, bodies[n])
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// The whole answer each protocol streams once the server stopped refusing.
const (
	openaiWhole    = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	claudeWhole    = "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\ndata: {\"type\":\"message_stop\"}\n"
	responsesWhole = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n"
)

// TestIsTransientAPIError: the classification of an API error object.
func TestIsTransientAPIError(t *testing.T) {
	cases := []struct {
		name      string
		code, typ string
		msg       string
		transient bool
	}{
		{"openai overload code", "server_is_overloaded", "", overloadMessage, true},
		{"openai server error code", "server_error", "", "internal error", true},
		{"openai rate limit code", "rate_limit_exceeded", "", "slow down", true},
		{"openai slow down code", "slow_down", "", "", true},
		{"anthropic overloaded type", "", "overloaded_error", "Overloaded", true},
		{"anthropic api type", "", "api_error", "Internal server error", true},
		{"anthropic rate limit type", "", "rate_limit_error", "", true},
		{"message only: overloaded", "", "", "The server is Overloaded", true},
		{"message only: try again later", "", "", "Please TRY AGAIN LATER.", true},
		{"message only: upstream stream failed", "", "", "Upstream HTTP/2 stream failed", true},
		{"a bad model stays permanent", "invalid_request_error", "", "The model does not exist", false},
		{"a bad key stays permanent", "invalid_api_key", "authentication_error", "Incorrect API key provided", false},
		// Only the two phrases above are taken as a message hint: a text that
		// merely mentions a rate limit without the API's own code or type is left
		// as a bad response (see transientAPIErrorPhrases).
		{"a bare rate limit text stays permanent", "", "", "rate limited", false},
		{"a context length text stays permanent", "", "", "Upstream stream failed: prompt is too long", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Eq(t, c.transient, isTransientAPIError(c.code, c.typ, c.msg))
		})
	}
}

// TestStreamRetriesTransientAPIErrors: an API error the provider reports inside
// the stream (or in a body without a stream) that names a temporary server side
// failure is a retry like a broken stream - the turn is requested again instead
// of failing - and its retries ran out as provider_transient. A permanent error
// (invalid_request_error) fails the turn at once.
//
// Reverse check: with isTransientAPIError always false every retry case here
// fails after one request with provider_bad_response; with it always true the
// invalid_request_error cases retry instead of failing at once.
func TestStreamRetriesTransientAPIErrors(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	openai := func(url string) *OpenAIProvider {
		return NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: url, Model: "m", Retry: fastRetry})
	}
	claude := func(url string) *ClaudeProvider {
		return NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: url, Retry: fastRetry})
	}
	responses := func(url string) *ResponsesProvider {
		return NewResponsesProvider(ResponsesConfig{OpenAIConfig: OpenAIConfig{APIKey: "k", BaseURL: url, Model: "m", Retry: fastRetry}})
	}
	openaiOverload := "data: {\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"" + overloadMessage + "\"}}\n"
	claudeOverload := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n"
	responsesOverload := "data: {\"type\":\"error\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"" + overloadMessage + "\"}}\n"

	t.Run("openai: an overload chunk is requested again", func(t *testing.T) {
		server, hits := scriptedServer(t, "text/event-stream", openaiOverload, openaiWhole)
		var events []agentcoreapi.StreamEvent
		ctx, report := withRetryReport(context.Background())
		res, err := openai(server.URL).Stream(ctx, req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
		assert.Require(t, assert.True(t, len(events) >= 3))
		assert.Eq(t, agentcoreapi.StreamEventRetry, events[0].Kind)
		assert.Eq(t, apiTransientReason, events[0].Delta)
		assert.Eq(t, false, events[0].Metadata["restart"]) // nothing was streamed yet
		assert.Eq(t, 1, events[0].Metadata["attempt"])
		assert.Eq(t, "ok", events[1].Delta)
		assert.Eq(t, 1, report.Retries)
		assert.Eq(t, apiTransientReason, report.Last)
	})
	t.Run("responses: an upstream stream failure is requested again", func(t *testing.T) {
		// suga-ryy0: the gateway reported a broken upstream HTTP/2 stream as an
		// error event; it is temporary, so the turn is requested again.
		body := "data: {\"type\":\"error\",\"error\":{\"message\":\"Upstream HTTP/2 stream failed (request-id: x)\"}}\n"
		server, hits := scriptedServer(t, "text/event-stream", body, responsesWhole)
		res, err := responses(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
	})
	t.Run("responses: a message-only upstream stream failure is requested again", func(t *testing.T) {
		body := "data: {\"type\":\"error\",\"message\":\"Upstream HTTP/2 stream failed\"}\n"
		server, hits := scriptedServer(t, "text/event-stream", body, responsesWhole)
		res, err := responses(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
	})
	t.Run("openai: the message alone is enough (no code or type)", func(t *testing.T) {
		body := "data: {\"error\":{\"message\":\"" + overloadMessage + "\"}}\n"
		server, hits := scriptedServer(t, "text/event-stream", body, openaiWhole)
		res, err := openai(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
	})
	t.Run("openai: an overload after some text restarts the reply", func(t *testing.T) {
		// The refused attempt already streamed text: it is void, so the retry is
		// announced with restart=true and the message is streamed again.
		body := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n" + openaiOverload
		server, hits := scriptedServer(t, "text/event-stream", body, openaiWhole)
		var events []agentcoreapi.StreamEvent
		res, err := openai(server.URL).Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content) // nothing of the refused attempt is kept
		assert.Require(t, assert.True(t, len(events) >= 3))
		assert.Eq(t, "Hel", events[0].Delta)
		assert.Eq(t, agentcoreapi.StreamEventRetry, events[1].Kind)
		assert.Eq(t, apiTransientReason, events[1].Delta)
		assert.Eq(t, true, events[1].Metadata["restart"])
		assert.Eq(t, "ok", events[2].Delta)
	})
	t.Run("openai: giving up names the overload and reports provider_transient", func(t *testing.T) {
		server, hits := scriptedServer(t, "text/event-stream", openaiOverload)
		_, err := openai(server.URL).Stream(context.Background(), req, nil)
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
		assert.ErrMsgContains(t, err, "服务端过载，已重试 3 次")
		assert.ErrMsgContains(t, err, overloadMessage) // the API's own wording stays readable
		assert.Eq(t, int32(fastRetry.MaxAttempts), hits.Load())
	})
	t.Run("openai: an invalid request error is not retried", func(t *testing.T) {
		body := "data: {\"error\":{\"code\":\"invalid_request_error\",\"message\":\"bad model\"}}\n"
		server, hits := scriptedServer(t, "text/event-stream", body)
		_, err := openai(server.URL).Stream(context.Background(), req, nil)
		assert.Eq(t, int32(1), hits.Load())
		assert.Eq(t, ErrorKindBadResponse, ErrorKind(err))
		assert.Eq(t, "openai api error: bad model", err.Error())
	})
	t.Run("openai: a JSON error body instead of a stream is retried too", func(t *testing.T) {
		// A gateway ignoring stream=true: the 200 body is the error object.
		body := `{"error":{"code":"rate_limit_exceeded","message":"Rate limit reached"}}`
		whole := `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`
		server, hits := scriptedServer(t, "application/json", body, whole)
		res, err := openai(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
	})

	t.Run("claude: an overloaded error event is requested again", func(t *testing.T) {
		server, hits := scriptedServer(t, "text/event-stream", claudeOverload, claudeWhole)
		var events []agentcoreapi.StreamEvent
		res, err := claude(server.URL).Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
		assert.Eq(t, agentcoreapi.StreamEventRetry, events[0].Kind)
		assert.Eq(t, apiTransientReason, events[0].Delta)
	})
	t.Run("claude: giving up names the overload and reports provider_transient", func(t *testing.T) {
		server, hits := scriptedServer(t, "text/event-stream", claudeOverload)
		_, err := claude(server.URL).Stream(context.Background(), req, nil)
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
		assert.ErrMsgContains(t, err, "服务端过载，已重试 3 次")
		assert.ErrMsgContains(t, err, "claude api error: Overloaded")
		assert.Eq(t, int32(fastRetry.MaxAttempts), hits.Load())
	})
	t.Run("claude: an invalid request error is not retried", func(t *testing.T) {
		body := "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"bad model\"}}\n"
		server, hits := scriptedServer(t, "text/event-stream", body)
		_, err := claude(server.URL).Stream(context.Background(), req, nil)
		assert.Eq(t, int32(1), hits.Load())
		assert.Eq(t, ErrorKindBadResponse, ErrorKind(err))
		assert.Eq(t, "claude api error: bad model", err.Error())
	})

	t.Run("responses: an overload event is requested again", func(t *testing.T) {
		server, hits := scriptedServer(t, "text/event-stream", responsesOverload, responsesWhole)
		var events []agentcoreapi.StreamEvent
		res, err := responses(server.URL).Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
		assert.Eq(t, agentcoreapi.StreamEventRetry, events[0].Kind)
		assert.Eq(t, apiTransientReason, events[0].Delta)
	})
	t.Run("responses: a failed response with a temporary code is requested again", func(t *testing.T) {
		failed := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"internal error\"}}}\n"
		server, hits := scriptedServer(t, "text/event-stream", failed, responsesWhole)
		res, err := responses(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
	})
	t.Run("responses: giving up names the overload and reports provider_transient", func(t *testing.T) {
		server, hits := scriptedServer(t, "text/event-stream", responsesOverload)
		_, err := responses(server.URL).Stream(context.Background(), req, nil)
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
		assert.ErrMsgContains(t, err, "服务端过载，已重试 3 次")
		assert.Eq(t, int32(fastRetry.MaxAttempts), hits.Load())
	})
	t.Run("responses: an invalid request error is not retried", func(t *testing.T) {
		body := "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request_error\",\"message\":\"bad effort\"}}\n"
		server, hits := scriptedServer(t, "text/event-stream", body)
		_, err := responses(server.URL).Stream(context.Background(), req, nil)
		assert.Eq(t, int32(1), hits.Load())
		assert.Eq(t, ErrorKindBadResponse, ErrorKind(err))
		assert.Eq(t, "responses api error: bad effort", err.Error())
	})
	t.Run("responses: an error body without a stream is retried too", func(t *testing.T) {
		body := `{"error":{"code":"server_is_overloaded","message":"` + overloadMessage + `"}}`
		whole := `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`
		server, hits := scriptedServer(t, "application/json", body, whole)
		res, err := responses(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "ok", res.Content)
	})
}

// TestAPIBodyErrorKinds: an error object in a 200 response body is classified
// like one inside a stream. The non-streamed Generate paths have no retries of
// their own (the streamed ones are covered above), so what is checked here is
// the error kind a host sees.
func TestAPIBodyErrorKinds(t *testing.T) {
	t.Run("responses", func(t *testing.T) {
		p := NewResponsesProvider(ResponsesConfig{OpenAIConfig: OpenAIConfig{APIKey: "k", BaseURL: "https://gw.example/v1", Model: "m"}})
		_, err := p.parseResponsesBody([]byte(`{"status":"failed","error":{"code":"server_is_overloaded","message":"` + overloadMessage + `"}}`))
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
		assert.Eq(t, "responses api error: "+overloadMessage, err.Error())
		_, err = p.parseResponsesBody([]byte(`{"status":"failed","error":{"code":"invalid_request_error","message":"bad effort"}}`))
		assert.Eq(t, ErrorKindBadResponse, ErrorKind(err))
	})
	t.Run("openai", func(t *testing.T) {
		p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: "https://gw.example/v1", Model: "m"})
		_, err := p.parseResponse([]byte(`{"error":{"code":"server_error","message":"internal error"}}`))
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
		_, err = p.parseResponse([]byte(`{"choices":[],"error":{"code":"invalid_request_error","message":"bad model"}}`))
		assert.Eq(t, ErrorKindBadResponse, ErrorKind(err))
	})
	t.Run("claude", func(t *testing.T) {
		p := NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: "https://api.anthropic.example", Model: "m"})
		_, err := p.parseResponse([]byte(`{"error":{"type":"overloaded_error","message":"Overloaded"}}`))
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
		_, err = p.parseResponse([]byte(`{"error":{"type":"invalid_request_error","message":"bad model"}}`))
		assert.Eq(t, ErrorKindBadResponse, ErrorKind(err))
	})
}
