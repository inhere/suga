package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
)

// toolLine is what chat shows for one tool call: the tool_call event goes
// through the renderer, so the test covers the line the person reads.
func toolLine(t *testing.T, workspace, tool, args string) string {
	t.Helper()
	var out bytes.Buffer
	newRenderer(rendererOptions{Out: &out, Workspace: workspace}).Handle(personal.Event{
		Kind: "tool_call", ToolName: tool, Text: args})
	return strings.TrimSuffix(out.String(), "\n")
}

// TestToolCallSummaryLines: every tool with a summary of its own shows its
// arguments the way a person reads them, paths of the workspace relative to it.
func TestToolCallSummaryLines(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")

	for _, tc := range []struct {
		name, tool, args, want string
	}{
		{name: "file_read path", tool: "file_read", args: `{"path":"a.go"}`,
			want: "⏺ file_read a.go"},
		{name: "file_read absolute path inside the workspace", tool: "file_read",
			args: fmt.Sprintf(`{"path":%q}`, filepath.Join(ws, "a.go")), want: "⏺ file_read a.go"},
		{name: "file_read line window", tool: "file_read", args: `{"path":"a.go","offset":10,"limit":20}`,
			want: "⏺ file_read a.go:10-29"},
		{name: "file_read offset only", tool: "file_read", args: `{"path":"a.go","offset":10}`,
			want: "⏺ file_read a.go:10-"},
		{name: "file_read limit only starts at line 1", tool: "file_read", args: `{"path":"a.go","limit":40}`,
			want: "⏺ file_read a.go:1-40"},
		{name: "file_read several paths", tool: "file_read", args: `{"paths":["a.go","internal/b.go","c.go"]}`,
			want: "⏺ file_read a.go, internal/b.go, c.go"},
		{name: "file_read paths sent as JSON text", tool: "file_read", args: `{"paths":"[\"a.go\",\"b.go\"]"}`,
			want: "⏺ file_read a.go, b.go"},
		{name: "file_read path outside the workspace stays absolute", tool: "file_read",
			args: fmt.Sprintf(`{"path":%q}`, outside), want: "⏺ file_read " + outside},
		{name: "file_write path with size", tool: "file_write", args: `{"path":"a.go","content":"one\ntwo"}`,
			want: "⏺ file_write a.go (7 字节, 2 行)"},
		{name: "file_edit several replacements", tool: "file_edit",
			args: `{"path":"a.go","edits":[{"old_string":"a","new_string":"b"},{"old_string":"c","new_string":"d"}]}`,
			want: "⏺ file_edit a.go (2 处)"},
		{name: "file_edit one replacement", tool: "file_edit", args: `{"path":"a.go","old_string":"a","new_string":"b"}`,
			want: "⏺ file_edit a.go (1 处)"},
		{name: "file_remove path", tool: "file_remove", args: `{"path":"tmp/x.go"}`,
			want: "⏺ file_remove tmp/x.go"},
		{name: "file_list defaults to the workspace root", tool: "file_list", args: `{}`,
			want: "⏺ file_list ."},
		{name: "file_list directory and depth", tool: "file_list", args: `{"dir":"internal","depth":3}`,
			want: "⏺ file_list internal (depth 3)"},
		{name: "file_find glob and path", tool: "file_find", args: `{"pattern":"*.go","path":"internal/chat"}`,
			want: "⏺ file_find *.go in internal/chat"},
		{name: "file_grep pattern, path and glob", tool: "file_grep",
			args: `{"pattern":"needle","path":"internal/chat","glob":["*.go","!*_test.go"]}`,
			want: `⏺ file_grep "needle" internal/chat *.go !*_test.go`},
		{name: "file_grep pattern only", tool: "file_grep", args: `{"pattern":"x<y>&z"}`,
			want: `⏺ file_grep "x<y>&z"`},
		{name: "file_symbols name and path", tool: "file_symbols", args: `{"path":"svc/svc.go","name":"Load"}`,
			want: "⏺ file_symbols Load in svc/svc.go"},
		{name: "file_symbols path only", tool: "file_symbols", args: `{"path":"svc"}`,
			want: "⏺ file_symbols svc"},
		{name: "file_refs name and path", tool: "file_refs", args: `{"name":"Load","path":"svc/svc.go"}`,
			want: "⏺ file_refs Load svc/svc.go"},
		{name: "file_refs name only", tool: "file_refs", args: `{"name":"Load","path":"."}`,
			want: "⏺ file_refs Load"},
		{name: "view_image path", tool: "view_image", args: `{"path":"images/a.png"}`,
			want: "⏺ view_image images/a.png"},
		{name: "shell_exec command", tool: "shell_exec", args: `{"command":"echo hi"}`,
			want: "⏺ shell_exec echo hi"},
		{name: "shell_exec first line with notes", tool: "shell_exec",
			args: `{"command":"go test ./...\n  go vet ./...","timeout":300,"background":true}`,
			want: "⏺ shell_exec go test ./... … (timeout 300s, background)"},
		{name: "shell_exec watch", tool: "shell_exec",
			args: `{"command":"go test ./x","background":true,"every":30,"watch_paths":["**/*.go","./docs"]}`,
			want: "⏺ shell_exec go test ./x (background, every 30s, watch 2 个路径)"},
		{name: "web_fetch url", tool: "web_fetch", args: `{"url":"https://example.com/a?b=1&c=2"}`,
			want: "⏺ web_fetch https://example.com/a?b=1&c=2"},
		{name: "web_fetch urls", tool: "web_fetch",
			args: `{"urls":["https://example.com/a","https://example.com/b","https://example.com/c"]}`,
			want: "⏺ web_fetch 3 个 URL：https://example.com/a, …"},
		{name: "web_search query", tool: "web_search", args: `{"query":"go worktree"}`,
			want: "⏺ web_search go worktree"},
		{name: "agent type and task", tool: "agent", args: `{"type":"research","description":"find Foo","prompt":"x"}`,
			want: "⏺ agent research find Foo"},
		{name: "agent defaults to explore and keeps the prompt first line", tool: "agent",
			args: `{"prompt":"trace the callers\nof Foo"}`, want: "⏺ agent explore trace the callers …"},
		{name: "session_message list", tool: "session_message", args: `{"action":"list"}`,
			want: "⏺ session_message list"},
		{name: "session_message send", tool: "session_message",
			args: `{"action":"send","to":"suga-k7","text":"请核对\n第二轮"}`, want: `⏺ session_message → suga-k7 "请核对 …"`},
		{name: "session_search query", tool: "session_search", args: `{"query":"worktree 事故"}`,
			want: "⏺ session_search worktree 事故"},
		{name: "session_search ref", tool: "session_search", args: `{"query":"","ref":"out:s:abc"}`,
			want: "⏺ session_search out:s:abc"},
		{name: "recall ref", tool: "recall", args: `{"ref":"suga-k7/12"}`,
			want: "⏺ recall suga-k7/12"},
		{name: "skill name", tool: "skill", args: `{"name":"tdd"}`,
			want: "⏺ skill tdd"},
		{name: "skill query", tool: "skill", args: `{"query":"browser"}`,
			want: `⏺ skill query "browser"`},
		{name: "tool_search query", tool: "tool_search", args: `{"query":"select:file_read"}`,
			want: "⏺ tool_search select:file_read"},
		{name: "memory add", tool: "memory", args: `{"action":"add","title":"构建约定"}`,
			want: "⏺ memory add 构建约定"},
		{name: "memory search", tool: "memory", args: `{"action":"search","query":"worktree"}`,
			want: "⏺ memory search worktree"},
		{name: "memory get", tool: "memory", args: `{"action":"get","id":"build-rules"}`,
			want: "⏺ memory get build-rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, toolLine(t, ws, tc.tool, tc.args))
		})
	}
}

// TestToolCallSummaryFallsBackToJSON: a tool without a summary, a call whose
// arguments do not decode and a summary that would be empty keep the compact
// JSON of the call arguments, exactly as before the summaries existed.
func TestToolCallSummaryFallsBackToJSON(t *testing.T) {
	ws := t.TempDir()
	for _, tc := range []struct {
		name, tool, args, want string
	}{
		{name: "update_plan keeps the JSON (its own plan display follows)", tool: "update_plan",
			args: `{"plan":[{"step":"read","status":"in_progress"}]}`,
			want: `⏺ update_plan {"plan":[{"status":"in_progress","step":"read"}]}`},
		{name: "an MCP tool", tool: "mcp__github__create_issue", args: `{"title":"x","body":"y"}`,
			want: `⏺ mcp__github__create_issue {"body":"y","title":"x"}`},
		{name: "a tool without summary", tool: "task", args: `{"action":"list"}`,
			want: `⏺ task {"action":"list"}`},
		{name: "arguments that are not JSON", tool: "file_read", args: "a.go",
			want: "⏺ file_read a.go"},
		{name: "a truncated argument summary", tool: "file_read", args: `{"path":"a.go","con`,
			want: `⏺ file_read {"path":"a.go","con`},
		{name: "a missing path", tool: "file_read", args: `{"offset":3}`,
			want: `⏺ file_read {"offset":3}`},
		{name: "no arguments", tool: "file_read", args: "",
			want: "⏺ file_read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, toolLine(t, ws, tc.tool, tc.args))
		})
	}

	t.Run("without a workspace every path stays as the call wrote it", func(t *testing.T) {
		abs := filepath.Join(ws, "a.go")
		assert.Eq(t, "⏺ file_read "+abs, toolLine(t, "", "file_read", fmt.Sprintf(`{"path":%q}`, abs)))
	})
}

// TestToolCallSummaryLimits: the summary is one line cut at callArgsLimit, like
// the JSON was, and it is redacted like every other chat line.
func TestToolCallSummaryLimits(t *testing.T) {
	t.Run("a long command is cut at the call width", func(t *testing.T) {
		command := "deploy " + strings.Repeat("x", 200)
		got := toolLine(t, "", "shell_exec", fmt.Sprintf(`{"command":%q}`, command))
		assert.Eq(t, "⏺ shell_exec "+string([]rune(command)[:callArgsLimit-1])+"…", got)
	})

	t.Run("a credential in the command is redacted", func(t *testing.T) {
		got := toolLine(t, "", "shell_exec", `{"command":"deploy --api_key=abc123 rest"}`)
		assert.Eq(t, "⏺ shell_exec deploy --api_key=[REDACTED] rest", got)
		assert.StrNotContains(t, got, "abc123")
	})
}

// TestToolCallArgsUseTheWholeArguments: Text carries the arguments as JSON cut
// at 200 runes, too short to decode a long command or a file_write; the event's
// whole arguments (personal.Event ToolArgs) still give the tool's own summary.
func TestToolCallArgsUseTheWholeArguments(t *testing.T) {
	long := "go test ./internal/chat/ -run '" + strings.Repeat("TestSomething|", 30) + "'"
	full := map[string]any{"command": long}
	data, _ := json.Marshal(full)
	cut := string([]rune(string(data))[:200]) // what the event's Text holds

	t.Run("whole arguments give the summary", func(t *testing.T) {
		got := toolCallArgs("shell_exec", cut, full, "")
		assert.True(t, strings.HasPrefix(got, "go test ./internal/chat/"), got)
		assert.NotContains(t, got, `{"command"`)
	})
	t.Run("without them a cut call falls back to JSON", func(t *testing.T) {
		got := toolCallArgs("shell_exec", cut, nil, "")
		assert.StrContains(t, got, `{"command"`)
	})
}
