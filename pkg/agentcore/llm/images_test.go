package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// imageTestPart is one small PNG-ish part; the bytes only need to be stable.
func imageTestPart() agentcoreapi.ImagePart {
	return agentcoreapi.ImagePart{
		MediaType: "image/png", Ref: "images/ab.png", SHA256: "ab", Width: 4, Height: 2,
		Data: []byte("png-bytes"),
	}
}

// imageTestMessages is a turn with a user picture and a tool result picture.
func imageTestMessages() []agentcoreapi.Message {
	return []agentcoreapi.Message{
		{Role: "user", Content: "what is this", Images: []agentcoreapi.ImagePart{imageTestPart()}},
		{Role: "assistant", ToolCall: &agentcoreapi.ToolCall{CallID: "c1", Name: "view_image", Arguments: map[string]any{}}},
		{Role: "tool", Content: "image ok", ToolCall: &agentcoreapi.ToolCall{CallID: "c1", Name: "view_image"},
			ToolResult: &agentcoreapi.ToolResult{CallID: "c1", Name: "view_image", Content: "image ok",
				Images: []agentcoreapi.ImagePart{imageTestPart()}}},
	}
}

// TestClaudeImageMapping pins the Anthropic mapping: image blocks before the
// text in a user message, and an image block inside the tool_result content.
func TestClaudeImageMapping(t *testing.T) {
	body := NewClaudeProvider(ClaudeConfig{}).buildRequestBody(&agentcoreapi.GenerateRequest{Messages: imageTestMessages()}, "m")
	raw, err := json.Marshal(body)
	assert.Require(t, assert.NoErr(t, err))
	text := string(raw)

	t.Run("the user picture is an image block before the text", func(t *testing.T) {
		assert.True(t, strings.Contains(text, `{"source":{"data":"cG5nLWJ5dGVz","media_type":"image/png","type":"base64"},"type":"image"}`))
		assert.True(t, strings.Index(text, `"type":"image"`) < strings.Index(text, "what is this"))
	})
	t.Run("the tool result carries the image block", func(t *testing.T) {
		assert.True(t, strings.Contains(text, `"tool_use_id":"c1"`))
		toolResult := claudeResultContent("image ok", []agentcoreapi.ImagePart{imageTestPart()}, false)
		blocks, ok := toolResult.([]map[string]any)
		assert.True(t, ok)
		assert.Require(t, assert.Eq(t, 2, len(blocks)))
		assert.Eq(t, "image", blocks[0]["type"])
		assert.Eq(t, "text", blocks[1]["type"])
	})
	t.Run("a model without vision gets a note instead", func(t *testing.T) {
		body := NewClaudeProvider(ClaudeConfig{NoImages: true}).buildRequestBody(&agentcoreapi.GenerateRequest{Messages: imageTestMessages()}, "m")
		raw, err := json.Marshal(body)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, strings.Contains(string(raw), "cG5nLWJ5dGVz"))
		assert.True(t, strings.Contains(string(raw), "当前模型不支持看图"))
	})
}

// TestOpenAIImageMapping pins the Chat Completions mapping: image_url parts in
// the user message, and one user message with the pictures after the tool batch.
func TestOpenAIImageMapping(t *testing.T) {
	p := NewOpenAIProvider(OpenAIConfig{})
	body := p.buildRequestBody(&agentcoreapi.GenerateRequest{Messages: imageTestMessages()}, "m", false)
	msgs, ok := body["messages"].([]map[string]any)
	assert.Require(t, assert.True(t, ok))
	assert.Require(t, assert.Eq(t, 4, len(msgs)))

	t.Run("the tool message stays text", func(t *testing.T) {
		assert.Eq(t, "tool", msgs[2]["role"])
		assert.Eq(t, "image ok", msgs[2]["content"])
	})
	t.Run("the pictures follow the whole batch in one user message", func(t *testing.T) {
		assert.Eq(t, "user", msgs[3]["role"])
		parts, ok := msgs[3]["content"].([]map[string]any)
		assert.Require(t, assert.True(t, ok))
		assert.Require(t, assert.Eq(t, 2, len(parts)))
		assert.Eq(t, "text", parts[0]["type"])
		assert.True(t, strings.Contains(parts[0]["text"].(string), "view_image"))
		assert.Eq(t, "image_url", parts[1]["type"])
	})
	t.Run("a model without vision gets a note instead", func(t *testing.T) {
		body := NewOpenAIProvider(OpenAIConfig{NoImages: true}).buildRequestBody(&agentcoreapi.GenerateRequest{Messages: imageTestMessages()}, "m", false)
		raw, err := json.Marshal(body)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, strings.Contains(string(raw), "data:image/png;base64"))
		assert.True(t, strings.Contains(string(raw), "当前模型不支持看图"))
	})
}

// TestResponsesImageMapping pins the Responses mapping: input_image items in a
// user message and, because function_call_output is text only, a user item with
// the pictures after the tool batch. A picture-only user message is kept.
func TestResponsesImageMapping(t *testing.T) {
	p := NewResponsesProvider(ResponsesConfig{})

	t.Run("the user picture is an input_image item", func(t *testing.T) {
		items := p.buildInput([]agentcoreapi.Message{{Role: "user", Images: []agentcoreapi.ImagePart{imageTestPart()}}}, false)
		assert.Require(t, assert.Eq(t, 1, len(items)))
		parts, ok := items[0]["content"].([]map[string]any)
		assert.Require(t, assert.True(t, ok))
		assert.Require(t, assert.Eq(t, 1, len(parts)))
		assert.Eq(t, "input_image", parts[0]["type"])
	})
	t.Run("the tool pictures follow the batch in a user item", func(t *testing.T) {
		items := p.buildInput(imageTestMessages(), false)
		assert.Require(t, assert.Eq(t, 4, len(items)))
		assert.Eq(t, "function_call_output", items[2]["type"])
		assert.Eq(t, "image ok", items[2]["output"])
		assert.Eq(t, "user", items[3]["role"])
		parts, ok := items[3]["content"].([]map[string]any)
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, "input_image", parts[len(parts)-1]["type"])
	})
	t.Run("a model without vision gets a note instead", func(t *testing.T) {
		items := NewResponsesProvider(ResponsesConfig{OpenAIConfig: OpenAIConfig{NoImages: true}}).buildInput(imageTestMessages(), false)
		raw, err := json.Marshal(items)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, strings.Contains(string(raw), "data:image/png;base64"))
		assert.True(t, strings.Contains(string(raw), "当前模型不支持看图"))
	})
}
