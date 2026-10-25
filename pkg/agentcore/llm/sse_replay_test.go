package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// The fixtures of testdata/sse are raw SSE bodies recorded from the real
// gateways (see scripts/sserecord): these tests replay them byte for byte
// through the real provider parsers and assert what comes out - the recorded
// chunk fragmentation, usage fields, reasoning text and tool-call argument
// fragments have to survive unchanged.

// replayFixture returns the recorded body of one fixture.
func replayFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sse", name))
	assert.Require(t, assert.NoErr(t, err))
	return string(data)
}

// replayServer serves one recorded body as the streamed response of any path
// and records every request's JSON body. bodies serves them in order (the
// two-step conversation replays two requests against one server).
func replayServer(t *testing.T, bodies []string, gotRequests *[]map[string]any) *httptest.Server {
	t.Helper()
	n := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqData, _ := io.ReadAll(r.Body)
		if gotRequests != nil {
			req := map[string]any{}
			_ = json.Unmarshal(reqData, &req)
			*gotRequests = append(*gotRequests, req)
		}
		i := n
		n++
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, bodies[i])
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSseReplayClaude(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}},
		Tools: []agentcoreapi.ToolSchema{{Name: "list_files", Description: "List the files of a directory"}}}

	t.Run("text: thinking, signature and usage", func(t *testing.T) {
		fixture := replayFixture(t, "command-code-claude-haiku-5-5-text.sse")
		server := replayServer(t, []string{fixture}, nil)
		var events []agentcoreapi.StreamEvent
		res, err := NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: server.URL}).Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "A good short answer states the key point directly, includes only what's essential, and is easy to understand at a glance.", res.Content)
		assert.Eq(t, "I need to craft exactly one concise sentence, something like defining a good short answer as direct, complete, and free of unnecessary detail.\n\n", res.Reasoning)
		assert.Eq(t, agentcoreapi.FinishFinal, res.FinishReason)
		assert.Eq(t, 57, res.Usage.InputTokens)
		assert.Eq(t, 204, res.Usage.OutputTokens)
		assert.Eq(t, 261, res.Usage.TotalTokens)
		assert.Eq(t, false, res.Truncated)
		// The trailing text deltas plus the message_stop.
		assert.Eq(t, agentcoreapi.StreamEventMessageStop, events[len(events)-1].Kind)

		// The thinking block carries the recorded signature byte for byte.
		blocks := claudeThinkingBlocks(agentcoreapi.Message{Metadata: res.Metadata})
		assert.Require(t, assert.Eq(t, 1, len(blocks)))
		assert.Eq(t, fixtureSignature(t, fixture), blocks[0]["signature"])
		assert.StrContains(t, blocks[0]["thinking"].(string), "craft exactly one concise sentence")
	})

	t.Run("tool call assembled from fragmented arguments", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "command-code-claude-haiku-5-5-tool1.sse")}, nil)
		res, err := NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: server.URL}).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, agentcoreapi.FinishToolCalls, res.FinishReason)
		assert.Eq(t, "", res.Content)
		assert.Eq(t, "I should just call list_files on \"fixtures\" directly to see what's there, keeping my reasoning brief.\n\n", res.Reasoning)
		assert.Eq(t, 1, len(res.ToolCalls))
		assert.Eq(t, "toolu_016tNffeNRdCJ37ydm1xUV9A", res.ToolCalls[0].CallID)
		assert.Eq(t, "list_files", res.ToolCalls[0].Name)
		assert.Eq(t, "fixtures", res.ToolCalls[0].Arguments["directory"])
		assert.Eq(t, 435, res.Usage.InputTokens)
		assert.Eq(t, 162, res.Usage.OutputTokens)
	})

	t.Run("tool result message answered", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "command-code-claude-haiku-5-5-tool2.sse")}, nil)
		var events []agentcoreapi.StreamEvent
		res, err := NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: server.URL}).Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "The `fixtures` directory contains three files: `notes.md`, `main.go`, and `go.mod`.", res.Content)
		assert.Eq(t, "I found three files matching what I needed, so I can move forward and answer with a brief response based on them.\n\n", res.Reasoning)
		assert.Eq(t, agentcoreapi.FinishFinal, res.FinishReason)
		assert.Eq(t, 497, res.Usage.InputTokens)
		assert.Eq(t, 98, res.Usage.OutputTokens)
		assert.Eq(t, agentcoreapi.StreamEventMessageStop, events[len(events)-1].Kind)
	})
}

// fixtureSignature extracts the signature_delta value of a recorded stream, so
// the claim that the parser passes it through byte for byte is not comparing
// the parser against itself.
func fixtureSignature(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok || !strings.Contains(payload, "signature_delta") {
			continue
		}
		var ev claudeStreamEvent
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(payload), &ev)))
		return ev.Delta.Signature
	}
	t.Fatal("no signature_delta in the fixture")
	return ""
}

// TestSseReplayClaudeTwoStep is the v0.47 regression net: the parsed first
// step (tool call with its thinking blocks) is built into the second request,
// and the request the provider sends must carry the thinking blocks unchanged
// first and a tool_use.input that is always an object.
func TestSseReplayClaudeTwoStep(t *testing.T) {
	fixture1 := replayFixture(t, "command-code-claude-haiku-5-5-tool1.sse")
	fixture2 := replayFixture(t, "command-code-claude-haiku-5-5-tool2.sse")
	var gotRequests []map[string]any
	server := replayServer(t, []string{fixture1, fixture2}, &gotRequests)
	p := NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: server.URL})

	// First step: the model asks for the tool.
	res1, err := p.Stream(context.Background(), &agentcoreapi.GenerateRequest{
		Messages: []agentcoreapi.Message{{Role: "user", Content: "Call the list_files tool to list the files of the directory named fixtures."}},
		Tools:    []agentcoreapi.ToolSchema{{Name: "list_files", Description: "List the files of a directory"}},
	}, nil)
	assert.Require(t, assert.NoErr(t, err))
	call := res1.ToolCalls[0]

	// Second step: the same reply with its thinking blocks, followed by the
	// tool result, is sent back to ask for the final answer. A call whose
	// arguments were empty must replay as an input object, never null.
	emptyCall := newToolCall("toolu_empty", "list_files", nil)
	req2 := &agentcoreapi.GenerateRequest{
		Messages: []agentcoreapi.Message{
			{Role: "user", Content: "Call the list_files tool to list the files of the directory named fixtures."},
			{Role: "assistant", Metadata: res1.Metadata, ToolCall: &call},
			{Role: "tool", ToolCall: &call, ToolResult: &agentcoreapi.ToolResult{CallID: call.CallID, Name: call.Name, Content: "notes.md\nmain.go\ngo.mod"}},
			{Role: "assistant", Metadata: map[string]any{ClaudeThinkingMetaKey: []any{
				map[string]any{"type": "thinking", "thinking": "decided without arguments", "signature": fixtureSignature(t, fixture1)},
			}}, ToolCall: &emptyCall},
			{Role: "tool", ToolCall: &emptyCall, ToolResult: &agentcoreapi.ToolResult{CallID: "toolu_empty", Name: "list_files", Content: "notes.md"}},
		},
		Tools: []agentcoreapi.ToolSchema{{Name: "list_files", Description: "List the files of a directory"}},
	}
	res2, err := p.Stream(context.Background(), req2, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "The `fixtures` directory contains three files: `notes.md`, `main.go`, and `go.mod`.", res2.Content)
	assert.Require(t, assert.Eq(t, 2, len(gotRequests)))

	sent := gotRequests[1]
	messages, _ := sent["messages"].([]any)
	assert.Require(t, assert.True(t, len(messages) >= 5))
	// The reply's thinking block goes back first, unchanged: the recorded
	// signature must survive the round trip byte for byte. The assistant
	// message is [thinking, tool_use].
	first, _ := messages[1].(map[string]any)
	blocks, _ := first["content"].([]any)
	thinking, _ := blocks[0].(map[string]any)
	assert.Eq(t, "thinking", thinking["type"])
	assert.Eq(t, fixtureSignature(t, fixture1), thinking["signature"])
	thinkingText, _ := thinking["thinking"].(string)
	assert.StrContains(t, thinkingText, "I should just call list_files")

	// The call with recorded arguments replays intact; the empty call's input
	// is an object, not the null that /v1/messages rejects.
	toolUse, _ := blocks[1].(map[string]any)
	input, _ := toolUse["input"].(map[string]any)
	assert.Eq(t, "fixtures", input["directory"])
	assert.Eq(t, "toolu_016tNffeNRdCJ37ydm1xUV9A", toolUse["id"])
	third, _ := messages[3].(map[string]any)
	blocks3, _ := third["content"].([]any)
	thinking3, _ := blocks3[0].(map[string]any)
	assert.Eq(t, "thinking", thinking3["type"])
	emptyUse, _ := blocks3[1].(map[string]any)
	emptyInput, _ := emptyUse["input"].(map[string]any)
	assert.Eq(t, 0, len(emptyInput)) // an object with no fields, not null
}

func TestSseReplayOpenAIChat(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	gptYes := func(base string) *OpenAIProvider {
		return NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: base, Model: "m"})
	}

	t.Run("deepseek text: reasoning_content and usage", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "command-code-deepseek-v4.1-flash-text.sse")}, nil)
		res, err := gptYes(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "Understood.", res.Content)
		assert.Eq(t, "We need answer exactly one short sentence, no tool call. Need infer what user wants? They say \"Reply with exactly one short sentence and no tool call.\" We should comply. Need likely just acknowledge. A short sentence. No tool. Must exactly one short sentence. Could say \"Understood.\" Is that a sentence? Yes. But maybe \"I will reply with exactly one short sentence.\" That is one sentence. Need no extra. Final only. Ensure exactly one short sentence, no tool call.", res.Reasoning)
		assert.Eq(t, agentcoreapi.FinishFinal, res.FinishReason)
		assert.Eq(t, 41, res.Usage.InputTokens)
		assert.Eq(t, 105, res.Usage.OutputTokens)
		assert.Eq(t, 146, res.Usage.TotalTokens)
		assert.Eq(t, false, res.Truncated)
	})

	t.Run("deepseek tool call in argument shards", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "command-code-deepseek-v4.1-flash-tool1.sse")}, nil)
		res, err := gptYes(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, agentcoreapi.FinishToolCalls, res.FinishReason)
		assert.Eq(t, 1, len(res.ToolCalls))
		assert.Eq(t, "call_00_f6lyq89cqmwot84ph7lban0y", res.ToolCalls[0].CallID)
		assert.Eq(t, "list_files", res.ToolCalls[0].Name)
		assert.Eq(t, "fixtures", res.ToolCalls[0].Arguments["directory"])
		assert.Eq(t, 312, res.Usage.InputTokens)
		assert.Eq(t, 39, res.Usage.OutputTokens)
	})

	t.Run("deepseek after tool result", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "command-code-deepseek-v4.1-flash-tool2.sse")}, nil)
		res, err := gptYes(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "The directory `fixtures` contains the following files:\n\n- `notes.md`\n- `main.go`\n- `go.mod`", res.Content)
		assert.Eq(t, "The tool returned the file list. I should just present it.", res.Reasoning)
		assert.Eq(t, agentcoreapi.FinishFinal, res.FinishReason)
		assert.Eq(t, 381, res.Usage.InputTokens)
		assert.Eq(t, 256, res.Usage.CacheReadTokens)
		assert.Eq(t, 421, res.Usage.TotalTokens)
	})

	t.Run("glm text", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "command-code-glm-5.3-flash-text.sse")}, nil)
		res, err := gptYes(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "Hello! How can I help you today?", res.Content)
		assert.Eq(t, "No tool call — I don't have tools anyway. One short sentence.", res.Reasoning)
		assert.Eq(t, 23, res.Usage.InputTokens)
		assert.Eq(t, 26, res.Usage.OutputTokens)
	})

	t.Run("glm tool call", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "command-code-glm-5.3-flash-tool1.sse")}, nil)
		res, err := gptYes(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, agentcoreapi.FinishToolCalls, res.FinishReason)
		assert.Eq(t, 1, len(res.ToolCalls))
		assert.Eq(t, "chatcmpl-tool-a4267870ef856984", res.ToolCalls[0].CallID)
		assert.Eq(t, "fixtures", res.ToolCalls[0].Arguments["directory"])
		assert.Eq(t, 179, res.Usage.InputTokens)
		assert.Eq(t, 12, res.Usage.OutputTokens)
	})

	t.Run("glm after tool result", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "command-code-glm-5.3-flash-tool2.sse")}, nil)
		res, err := gptYes(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "Here are the files in the `fixtures` directory:\n\n- notes.md\n- main.go\n- go.mod", res.Content)
		assert.Eq(t, "", res.Reasoning)
		assert.Eq(t, agentcoreapi.FinishFinal, res.FinishReason)
		assert.Eq(t, 213, res.Usage.InputTokens)
		assert.Eq(t, 24, res.Usage.OutputTokens)
	})
}

func TestSseReplayResponses(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	provider := func(base string) *ResponsesProvider {
		return NewResponsesProvider(ResponsesConfig{OpenAIConfig: OpenAIConfig{APIKey: "k", BaseURL: base, Model: "gpt-6-luna"}})
	}

	t.Run("text with reasoning summary", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "openai-codex-gpt-6-luna-text.sse")}, nil)
		var events []agentcoreapi.StreamEvent
		res, err := provider(server.URL).Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "How can I help?", res.Content)
		assert.Eq(t, "**Drafting concise user prompt**", res.Reasoning)
		assert.Eq(t, agentcoreapi.FinishFinal, res.FinishReason)
		assert.Eq(t, 17, res.Usage.InputTokens)
		assert.Eq(t, 40, res.Usage.OutputTokens)
		assert.Eq(t, 57, res.Usage.TotalTokens)
		assert.Eq(t, false, res.Truncated)
		assert.Eq(t, agentcoreapi.StreamEventMessageStop, events[len(events)-1].Kind)
	})

	t.Run("function_call item assembled", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "openai-codex-gpt-6-luna-tool1.sse")}, nil)
		res, err := provider(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, agentcoreapi.FinishToolCalls, res.FinishReason)
		assert.Eq(t, 1, len(res.ToolCalls))
		assert.Eq(t, "call_280db3fcd814495d84e96ef1199f2624", res.ToolCalls[0].CallID)
		assert.Eq(t, "list_files", res.ToolCalls[0].Name)
		assert.Eq(t, "fixtures", res.ToolCalls[0].Arguments["directory"])
		assert.Eq(t, 64, res.Usage.InputTokens)
		assert.Eq(t, 18, res.Usage.OutputTokens)
	})

	t.Run("after function_call_output", func(t *testing.T) {
		server := replayServer(t, []string{replayFixture(t, "openai-codex-gpt-6-luna-tool2.sse")}, nil)
		res, err := provider(server.URL).Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "notes.md  \nmain.go  \ngo.mod", res.Content)
		assert.Eq(t, "", res.Reasoning)
		assert.Eq(t, agentcoreapi.FinishFinal, res.FinishReason)
		assert.Eq(t, 114, res.Usage.InputTokens)
		assert.Eq(t, 12, res.Usage.OutputTokens)
	})
}
