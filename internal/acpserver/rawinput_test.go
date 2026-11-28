package acpserver

import (
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// runShellCall runs one shell_exec call with args through the ACP server and
// returns the client with its recorded tool_call updates and permission requests.
func runShellCall(t *testing.T, args map[string]any) *testACPClient {
	t.Helper()
	t.Setenv("SUGA_HOME", t.TempDir())
	t.Setenv("SUGA_SESSION_ID", "")
	cwd := t.TempDir()
	provider := mock.NewProvider(mock.ToolCall("sh-1", "shell_exec", args), mock.Final("done"))
	client := &testACPClient{selected: "allow"}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, &countingTool{name: "shell_exec"}), client)
	ctx := testCtx(t)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("run")}})
	assert.NoErr(t, err)
	return client
}

func TestToolCallCarriesRawInput(t *testing.T) {
	client := runShellCall(t, map[string]any{"command": "go test ./...", "cwd": "sub"})
	client.mu.Lock()
	defer client.mu.Unlock()

	t.Run("tool_call update", func(t *testing.T) {
		var start *acp.SessionUpdateToolCall
		for _, n := range client.updates {
			if n.Update.ToolCall != nil {
				start = n.Update.ToolCall
			}
		}
		assert.NotNil(t, start)
		raw, ok := start.RawInput.(map[string]any)
		assert.True(t, ok)
		assert.Eq(t, "go test ./...", raw["command"])
		assert.Eq(t, "sub", raw["cwd"])
		// existing fields are unchanged
		assert.Eq(t, "shell_exec", start.Title)
		assert.Eq(t, acp.ToolCallStatusPending, start.Status)
		assert.Eq(t, acp.ToolCallId("sh-1"), start.ToolCallId)
	})

	t.Run("request_permission toolCall", func(t *testing.T) {
		assert.Eq(t, 1, len(client.requests))
		call := client.requests[0].ToolCall
		raw, ok := call.RawInput.(map[string]any)
		assert.True(t, ok)
		assert.Eq(t, "go test ./...", raw["command"])
		assert.Eq(t, "sub", raw["cwd"])
		assert.Eq(t, "shell_exec", *call.Title)
		assert.Eq(t, acp.ToolCallStatusPending, *call.Status)
		assert.Eq(t, 4, len(client.requests[0].Options))
	})

	t.Run("status updates stay status only", func(t *testing.T) {
		for _, n := range client.updates {
			if u := n.Update.ToolCallUpdate; u != nil {
				assert.Nil(t, u.RawInput)
			}
		}
	})
}

func TestToolRawInputRedactsSecrets(t *testing.T) {
	client := runShellCall(t, map[string]any{
		"command":   "curl -H 'Authorization: Bearer abc123secret' https://x.test && export API_KEY=hunter2",
		"api_token": "plain-secret-value",
	})
	client.mu.Lock()
	defer client.mu.Unlock()
	assert.Eq(t, 1, len(client.requests))
	for name, v := range map[string]any{"request": client.requests[0].ToolCall.RawInput, "tool_call": toolCallStart(client).RawInput} {
		t.Run(name, func(t *testing.T) {
			raw, ok := v.(map[string]any)
			assert.True(t, ok)
			text := raw["command"].(string)
			assert.NotContains(t, text, "abc123secret")
			assert.NotContains(t, text, "hunter2")
			assert.Contains(t, text, "https://x.test")
			assert.Eq(t, "[REDACTED]", raw["api_token"])
		})
	}
}

func toolCallStart(c *testACPClient) *acp.SessionUpdateToolCall {
	for _, n := range c.updates {
		if n.Update.ToolCall != nil {
			return n.Update.ToolCall
		}
	}
	return &acp.SessionUpdateToolCall{}
}

func TestToolRawInputBounds(t *testing.T) {
	t.Run("long string is cut", func(t *testing.T) {
		raw := toolRawInput(map[string]any{"content": strings.Repeat("字", maxRawInputString+50)}).(map[string]any)
		text := raw["content"].(string)
		assert.True(t, strings.HasSuffix(text, "…[truncated]"))
		assert.Eq(t, maxRawInputString, len([]rune(strings.TrimSuffix(text, "…[truncated]"))))
	})
	t.Run("nested values are redacted", func(t *testing.T) {
		raw := toolRawInput(map[string]any{"env": map[string]any{"password": "p"}, "argv": []string{"a", "token=zzz"}}).(map[string]any)
		assert.Eq(t, "[REDACTED]", raw["env"].(map[string]any)["password"])
		assert.NotContains(t, raw["argv"].([]any)[1], "zzz")
	})
	t.Run("no arguments gives untyped nil", func(t *testing.T) {
		assert.Nil(t, toolRawInput(nil))
		assert.Nil(t, toolRawInput(map[string]any{}))
	})
}
