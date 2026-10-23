package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

func TestClaudeThinkingRequest(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	body := func(model, level string, maxTokens int) map[string]any {
		r := *req
		r.MaxTokens = maxTokens
		raw, _ := json.Marshal(NewClaudeProvider(ClaudeConfig{Thinking: level}).buildRequestBody(&r, model))
		var out map[string]any
		assert.NoErr(t, json.Unmarshal(raw, &out))
		return out
	}
	t.Run("adaptive with effort on current models", func(t *testing.T) {
		b := body("claude-opus-5-5", "high", 0)
		assert.Eq(t, map[string]any{"type": "adaptive", "display": "summarized"}, b["thinking"])
		assert.Eq(t, map[string]any{"effort": "high"}, b["output_config"])
		assert.Eq(t, float64(claudeDefaultMaxOutputTokens), b["max_tokens"])
		assert.Eq(t, map[string]any{"effort": "low"}, body("claude-sonnet-4-6", "minimal", 0)["output_config"])
	})
	t.Run("budget on models before adaptive thinking", func(t *testing.T) {
		b := body("claude-sonnet-4-5-20250929", "medium", 0)
		assert.Eq(t, map[string]any{"type": "enabled", "budget_tokens": float64(8192)}, b["thinking"])
		assert.Nil(t, b["output_config"])
		// budget_tokens stays below an explicit max_tokens
		assert.Eq(t, float64(3999), body("claude-haiku-4-5", "high", 4000)["thinking"].(map[string]any)["budget_tokens"])

		hdr := http.Header{}
		setThinkingBeta(&http.Request{Header: hdr}, map[string]any{"thinking": map[string]any{"type": "enabled"}})
		assert.Eq(t, claudeInterleavedBeta, hdr.Get("anthropic-beta"))
		hdr = http.Header{}
		setThinkingBeta(&http.Request{Header: hdr}, map[string]any{"thinking": map[string]any{"type": "adaptive"}})
		assert.Eq(t, "", hdr.Get("anthropic-beta"))
	})
	t.Run("off, auto and unset send nothing", func(t *testing.T) {
		for _, level := range []string{"", "auto", "off"} {
			b := body("claude-opus-5-5", level, 0)
			assert.Nil(t, b["thinking"])
			assert.Nil(t, b["output_config"])
			assert.Eq(t, float64(claudeDefaultMaxOutputTokens), b["max_tokens"])
		}
	})
	t.Run("a limit too small for a budget drops thinking", func(t *testing.T) {
		// Anthropic needs 1024 <= budget_tokens < max_tokens: a 1024-token
		// request has no room, so no thinking parameters are sent.
		b := body("claude-sonnet-4-5-20250929", "max", 1024)
		assert.Nil(t, b["thinking"])
		assert.Eq(t, float64(1024), b["max_tokens"])
		// One token more makes the smallest budget fit below the limit.
		assert.Eq(t, float64(1024), body("claude-sonnet-4-5-20250929", "max", 1025)["thinking"].(map[string]any)["budget_tokens"])
	})
}

// TestClaudeThinkingBlocksRoundTrip: the signed thinking of a tool-use reply
// is parsed (stream and plain), stored with the call and sent back unchanged
// before the tool_use; a folded message sends none.
func TestClaudeThinkingBlocksRoundTrip(t *testing.T) {
	want := []any{
		map[string]any{"type": "thinking", "thinking": "need the file", "signature": "sig-1"},
		map[string]any{"type": "redacted_thinking", "data": "opaque"},
	}
	check := func(t *testing.T, res *agentcoreapi.GenerateResult) {
		t.Helper()
		assert.Eq(t, "need the file", res.Reasoning)
		// As the runtime stores it: the result metadata on the call message,
		// through a JSON round trip (the session store).
		raw, _ := json.Marshal(res.Metadata)
		var meta map[string]any
		assert.NoErr(t, json.Unmarshal(raw, &meta))
		assert.Eq(t, want, meta[ClaudeThinkingMetaKey])

		call := agentcoreapi.Message{Role: "assistant", Content: "reading", Reasoning: res.Reasoning, Metadata: meta, ToolCall: &res.ToolCalls[0]}
		result := agentcoreapi.Message{Role: "tool", ToolResult: &agentcoreapi.ToolResult{CallID: "t1", Content: "x"}}
		p := NewClaudeProvider(ClaudeConfig{Thinking: "high"})
		msgs := p.buildRequestBody(&agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "go"}, call, result}}, "claude-opus-5-5")["messages"].([]map[string]any)
		blocks := msgs[1]["content"].([]map[string]any)
		assert.Require(t, assert.Eq(t, 4, len(blocks)))
		assert.Eq(t, want[0], any(blocks[0]))
		assert.Eq(t, want[1], any(blocks[1]))
		assert.Eq(t, "text", blocks[2]["type"])
		assert.Eq(t, "tool_use", blocks[3]["type"])

		call.Metadata = map[string]any{ClaudeThinkingMetaKey: meta[ClaudeThinkingMetaKey], ReasoningFoldedMetaKey: true}
		msgs = p.buildRequestBody(&agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "go"}, call, result}}, "claude-opus-5-5")["messages"].([]map[string]any)
		assert.Eq(t, 2, len(msgs[1]["content"].([]map[string]any))) // text + tool_use
	}
	t.Run("stream", func(t *testing.T) {
		server := claudeServer(t, 200, "text/event-stream", sseLines(
			`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"need "}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"the file"}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque"}}`,
			`data: {"type":"content_block_stop","index":1}`,
			`data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"reading"}}`,
			`data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"t1","name":"file_read"}}`,
			`data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a\"}"}}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
			`data: {"type":"message_stop"}`,
		), nil, nil, nil)
		res, err := NewClaudeProvider(ClaudeConfig{BaseURL: server.URL}).Stream(context.Background(),
			&agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "go"}}}, nil)
		assert.Require(t, assert.NoErr(t, err))
		check(t, res)
	})
	t.Run("plain response", func(t *testing.T) {
		res, err := NewClaudeProvider(ClaudeConfig{}).parseResponse([]byte(`{"content":[
			{"type":"thinking","thinking":"need the file","signature":"sig-1"},
			{"type":"redacted_thinking","data":"opaque"},
			{"type":"text","text":"reading"},
			{"type":"tool_use","id":"t1","name":"file_read","input":{"path":"a"}}],"stop_reason":"tool_use"}`))
		assert.Require(t, assert.NoErr(t, err))
		check(t, res)
	})
}

// TestClaudeToolUseInputReplay: a call whose arguments were all dropped replays
// an "input" object and never null, which /v1/messages rejects with "Invalid
// input at messages.2.content" (2026-10-23 claude-haiku-5-5 via a gateway).
func TestClaudeToolUseInputReplay(t *testing.T) {
	p := NewClaudeProvider(ClaudeConfig{})
	result := agentcoreapi.Message{Role: "tool", ToolResult: &agentcoreapi.ToolResult{CallID: "t1", Content: "x"}}
	for name, args := range map[string]map[string]any{
		"nil after coercion": nil,
		"empty object":       {},
		"kept arguments":     {"path": "a.txt"},
	} {
		t.Run(name, func(t *testing.T) {
			call := agentcoreapi.Message{Role: "assistant", ToolCall: &agentcoreapi.ToolCall{CallID: "t1", Name: "file_list", Arguments: args}}
			msgs := p.buildRequestBody(&agentcoreapi.GenerateRequest{
				Messages: []agentcoreapi.Message{{Role: "user", Content: "go"}, call, result},
			}, "claude-opus-5-5")["messages"].([]map[string]any)
			raw, err := json.Marshal(msgs[1]["content"].([]map[string]any)[0]["input"])
			assert.Require(t, assert.NoErr(t, err))
			var input any
			assert.Require(t, assert.NoErr(t, json.Unmarshal(raw, &input)))
			obj, ok := input.(map[string]any)
			assert.Require(t, ok) // an object, not null
			if len(args) == 0 {
				assert.Eq(t, 0, len(obj))
			} else {
				assert.Eq(t, args, obj)
			}
		})
	}
}
