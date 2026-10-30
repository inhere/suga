package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

func TestCheckCorruptedToolCalls(t *testing.T) {
	call := func(args map[string]any) []agentcoreapi.ToolCall {
		return []agentcoreapi.ToolCall{{CallID: "c", Name: "file_read", Arguments: args}}
	}
	t.Run("harmony text is flagged", func(t *testing.T) {
		for name, v := range map[string]string{
			"analysis garbage": "a.go ว񟟿?}]}#+#+#+#+assistant,analysis 代 ... invalid JSON? Need correct. typo. Let's call.",
			"commentary":       "a.go (commentary) ... to=functions.file_read code",
			"channel":          "a.go<|channel|>analysis",
			"message":          "a.go<|message|>x",
		} {
			err := checkCorruptedToolCalls(call(map[string]any{"path": v}))
			assert.Err(t, err, name)
			assert.True(t, strings.Contains(err.Error(), "reasoning-channel"), name)
		}
		// nested values are scanned too
		assert.Err(t, checkCorruptedToolCalls(call(map[string]any{"opts": []any{map[string]any{"p": "x to=functions.y"}}})))
	})
	t.Run("normal arguments pass", func(t *testing.T) {
		for _, v := range []string{"docs/#notes.md", "文档/说明.md", "a#b#c.go", "## heading\n# x", "assistant, analysis of data"} {
			assert.NoErr(t, checkCorruptedToolCalls(call(map[string]any{"path": v})))
		}
	})
}

func TestStreamRetriesCorruptedToolArgs(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	sseCall := func(path string) string {
		args, _ := json.Marshal(map[string]any{"path": path})
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "c1",
				"function": map[string]any{"name": "file_read", "arguments": string(args)}}}},
			"finish_reason": "tool_calls"}}})
		return "data: " + string(chunk) + "\ndata: [DONE]\n"
	}
	bad := sseCall("a.go ว񟟿?}]}#+#+#+#+assistant,analysis 代 ... invalid JSON? Need correct. typo. Let's call.")
	bad2 := sseCall("a.go (commentary) ... to=functions.file_read code")
	good := sseCall("docs/#notes.md")
	serve := func(t *testing.T, hits *atomic.Int32, bodies ...string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			n := int(hits.Add(1))
			if n > len(bodies) {
				n = len(bodies)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, bodies[n-1])
		}))
		t.Cleanup(s.Close)
		return s
	}
	openai := func(url string) *OpenAIProvider {
		return NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: url, Model: "m", Retry: fastRetry})
	}

	t.Run("corrupted then clean: retried, retry event recorded", func(t *testing.T) {
		var hits atomic.Int32
		server := serve(t, &hits, bad, good)
		var events []agentcoreapi.StreamEvent
		res, err := openai(server.URL).Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(2), hits.Load())
		assert.Eq(t, "docs/#notes.md", res.ToolCalls[0].Arguments["path"])
		retries := 0
		for _, ev := range events {
			if ev.Kind == agentcoreapi.StreamEventRetry {
				retries++
				assert.Eq(t, corruptedOutputReason, ev.Delta)
			}
		}
		assert.Eq(t, 1, retries)
	})
	t.Run("keeps corrupted: ends with a clear error", func(t *testing.T) {
		var hits atomic.Int32
		server := serve(t, &hits, bad, bad2)
		res, err := openai(server.URL).Stream(context.Background(), req, nil)
		assert.Nil(t, res)
		assert.ErrMsgContains(t, err, "reasoning-channel text")
		assert.Eq(t, ErrorKindBadResponse, ErrorKind(err))
		assert.Eq(t, int32(fastRetry.MaxAttempts), hits.Load())
	})
	t.Run("clean arguments with # run at once", func(t *testing.T) {
		var hits atomic.Int32
		server := serve(t, &hits, good)
		_, err := openai(server.URL).Stream(context.Background(), req, nil)
		assert.NoErr(t, err)
		assert.Eq(t, int32(1), hits.Load())
	})
}

func TestResponsesCorruptedToolArgs(t *testing.T) {
	p := NewResponsesProvider(ResponsesConfig{OpenAIConfig: OpenAIConfig{APIKey: "k", BaseURL: "http://x", Model: "m"}})
	body := `{"status":"completed","output":[{"type":"function_call","call_id":"c","name":"file_read","arguments":"{\"path\":\"a.go (commentary) to=functions.file_read\"}"}]}`
	_, err := p.parseResponsesBody([]byte(body))
	var ce *corruptedOutputError
	assert.True(t, errors.As(err, &ce))
}
