package wiring

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// nativeStubTool is a native tool that records whether it ran.
type nativeStubTool struct {
	name string
	runs int
	// agentState makes the stub declare Meta.AgentState.
	agentState bool
}

func (t *nativeStubTool) Name() string        { return t.name }
func (t *nativeStubTool) Description() string { return "native stub tool" }
func (t *nativeStubTool) Kind() tools.Kind    { return tools.KindNative }

// ToolMeta gives the stub the metadata of the built-in tool it poses as;
// other names (a tool added later) declare nothing.
func (t *nativeStubTool) ToolMeta() tools.Meta {
	if t.agentState {
		return tools.Meta{Category: tools.CategoryOther, AgentState: true}
	}
	builtins := native.BuildTools(native.Env{SerpAPIKey: "test-key"}, true)
	builtins = append(builtins, native.NewUpdatePlanTool(nil), native.NewToolSearchTool(nil), native.NewSkillTool(nil, 0))
	for _, tool := range builtins {
		if tool.Name() == t.name {
			return tools.MetaOf(tool)
		}
	}
	return tools.Meta{Category: tools.CategoryOther}
}
func (t *nativeStubTool) Schema() agentcoreapi.ToolSchema {
	return agentcoreapi.ToolSchema{Name: t.name}
}
func (t *nativeStubTool) Execute(_ context.Context, call agentcoreapi.ToolCall) (*agentcoreapi.ToolResult, error) {
	t.runs++
	return &agentcoreapi.ToolResult{CallID: call.CallID, Name: call.Name, Content: "ran"}, nil
}

// scriptedAsker answers questions from a script and records them.
type scriptedAsker struct {
	answers   []string
	err       error
	questions []tools.Question
}

func (a *scriptedAsker) Ask(_ context.Context, q tools.Question) (string, error) {
	a.questions = append(a.questions, q)
	if a.err != nil {
		return "", a.err
	}
	if len(a.answers) == 0 {
		return "", nil
	}
	answer := a.answers[0]
	a.answers = a.answers[1:]
	return answer, nil
}

// testChatApprover builds a chat approver over an existing added-directory set,
// as the older tests used: the outside-directory question stays off (they do not
// wire the session handle) and only the other rules are exercised.
func testChatApprover(gate *cliMCPGate, auto bool, asker tools.Asker, workspace string, dirs *native.AddDirs) *chatApprover {
	return newChatApprover(chatApproverOptions{gate: gate, auto: auto, asker: asker, workspace: workspace, dirs: dirs})
}

// newChatApproval builds the chat approval layer over stub tools.
func newChatApproval(t *testing.T, auto bool, asker tools.Asker) (*tools.Approval, map[string]*nativeStubTool) {
	t.Helper()
	registry := tools.NewRegistry()
	stubs := map[string]*nativeStubTool{}
	for _, name := range []string{"file_read", "file_list", "update_plan", "tool_search", "file_write", "shell_exec", "web_search", "file_grep", "file_find", "web_fetch", "future_tool"} {
		stubs[name] = &nativeStubTool{name: name}
		registry.Register(stubs[name])
	}
	for _, name := range []string{"gofer_issue_list", "gofer_issue_close"} {
		registry.Register(newServerStubTool("gofer", name))
	}
	gate, err := newCLIMCPGate(false)
	assert.Require(t, assert.NoErr(t, err))
	return tools.NewApproval(registry, testChatApprover(gate, auto, asker, "/work", nil)), stubs
}

func runTool(approval *tools.Approval, name string, args map[string]any) error {
	_, err := approval.ExecuteTool(context.Background(), agentcore.ToolCall{CallID: "c", Name: name, Arguments: args})
	return err
}

func TestChatApprover(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"gofer/gofer_*_list"}))

	t.Run("read-only tools run without asking", func(t *testing.T) {
		asker := &scriptedAsker{}
		approval, stubs := newChatApproval(t, false, asker)
		for _, name := range []string{"file_read", "file_list", "update_plan", "tool_search", "web_search", "file_grep", "file_find", "web_fetch"} {
			assert.NoErr(t, runTool(approval, name, nil))
			assert.Eq(t, 1, stubs[name].runs)
		}
		assert.Len(t, asker.questions, 0)
	})

	t.Run("answers", func(t *testing.T) {
		for _, tc := range []struct {
			answer string
			allow  bool
		}{
			{"y", true}, {"yes", true}, {" Y ", true}, {"YES", true},
			{"n", false}, {"no", false}, {"", false}, {"ok", false}, {"yy", false},
		} {
			asker := &scriptedAsker{answers: []string{tc.answer}}
			approval, stubs := newChatApproval(t, false, asker)
			err := runTool(approval, "shell_exec", map[string]any{"command": "echo hi"})
			if tc.allow {
				assert.NoErr(t, err, tc.answer)
				assert.Eq(t, 1, stubs["shell_exec"].runs)
				continue
			}
			assert.ErrIs(t, err, tools.ErrDenied, tc.answer)
			assert.Eq(t, "用户拒绝执行 shell_exec", err.Error())
			assert.Eq(t, 0, stubs["shell_exec"].runs)
		}
	})

	t.Run("always allows the same scope for the process only", func(t *testing.T) {
		asker := &scriptedAsker{answers: []string{"a", "", "y"}}
		approval, stubs := newChatApproval(t, false, asker)
		assert.NoErr(t, runTool(approval, "shell_exec", map[string]any{"command": "ls"}))
		// pwd is a different command prefix, so the scoped rule asks again.
		assert.NoErr(t, runTool(approval, "shell_exec", map[string]any{"command": "pwd"}))
		assert.Eq(t, 2, stubs["shell_exec"].runs)
		assert.Len(t, asker.questions, 3) // approval, persistence, approval
		// Another tool is still asked.
		assert.ErrIs(t, runTool(approval, "file_write", map[string]any{"path": "a", "content": "x"}), tools.ErrDenied)
		assert.Len(t, asker.questions, 4)
		// A new approver (a new chat process) asks again.
		asker2 := &scriptedAsker{answers: []string{"always", ""}}
		approval2, _ := newChatApproval(t, false, asker2)
		assert.NoErr(t, runTool(approval2, "shell_exec", map[string]any{"command": "ls"}))
		assert.Len(t, asker2.questions, 2)
	})

	t.Run("future tools are asked too", func(t *testing.T) {
		asker := &scriptedAsker{answers: []string{"n"}}
		approval, _ := newChatApproval(t, false, asker)
		assert.ErrIs(t, runTool(approval, "future_tool", map[string]any{"x": 1}), tools.ErrDenied)
		assert.Require(t, assert.Len(t, asker.questions, 1))
		assert.Eq(t, []string{`参数：{"x":1}`}, asker.questions[0].Details)
	})

	t.Run("auto runs without asking", func(t *testing.T) {
		asker := &scriptedAsker{}
		approval, stubs := newChatApproval(t, true, asker)
		for _, name := range []string{"shell_exec", "file_write", "web_search"} {
			assert.NoErr(t, runTool(approval, name, nil))
			assert.Eq(t, 1, stubs[name].runs)
		}
		assert.Len(t, asker.questions, 0)
	})

	t.Run("unbound asker refuses with the auto hint", func(t *testing.T) {
		approval, stubs := newChatApproval(t, false, tools.NewBindableAsker())
		err := runTool(approval, "shell_exec", map[string]any{"command": "ls"})
		assert.ErrIs(t, err, tools.ErrDenied)
		assert.StrContains(t, err.Error(), "tools.approval: auto")
		assert.Eq(t, 0, stubs["shell_exec"].runs)
	})

	t.Run("an asker error aborts the turn", func(t *testing.T) {
		for _, cause := range []error{io.EOF, context.Canceled} {
			approval, stubs := newChatApproval(t, false, &scriptedAsker{err: cause})
			err := runTool(approval, "shell_exec", map[string]any{"command": "ls"})
			assert.ErrIs(t, err, tools.ErrAborted)
			assert.ErrIs(t, err, cause)
			assert.False(t, errors.Is(err, tools.ErrDenied))
			assert.Eq(t, 0, stubs["shell_exec"].runs)
		}
	})

	t.Run("MCP rules are unchanged and never asked", func(t *testing.T) {
		asker := &scriptedAsker{answers: []string{"y", "y"}}
		approval, _ := newChatApproval(t, false, asker)
		assert.NoErr(t, runTool(approval, "gofer_issue_list", nil))
		err := runTool(approval, "gofer_issue_close", nil)
		assert.True(t, deniedCall(err, "gofer_issue_close"))
		assert.Len(t, asker.questions, 0)
	})
}

func TestChatApproverQuestion(t *testing.T) {
	approver := testChatApprover(nil, false, nil, filepath.FromSlash("/work"), nil)
	t.Run("shell command is full and redacted", func(t *testing.T) {
		long := "echo start-" + strings.Repeat("x", 500) + "-end"
		q := approver.question(agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{
			"command": long + " && curl -H 'Authorization: Bearer sk-secret-token' https://e.x",
		}})
		assert.Eq(t, "shell_exec", q.Tool)
		assert.Require(t, assert.Len(t, q.Details, 1))
		assert.StrContains(t, q.Details[0], "命令："+long) // never truncated
		assert.NotContains(t, q.Details[0], "sk-secret-token")
		assert.Eq(t, "允许执行 shell_exec？[y]是 [N]否 [a]本会话总是允许该工具：", q.Prompt)
	})
	t.Run("file_write shows the full path and the content size", func(t *testing.T) {
		q := approver.question(agentcore.ToolCall{Name: "file_write", Arguments: map[string]any{"path": "dir/a.txt", "content": "你好"}})
		assert.Eq(t, []string{"路径：" + filepath.Join(filepath.FromSlash("/work"), "dir/a.txt"), "内容：6 字节"}, q.Details)
	})
	t.Run("file_remove shows the path and what would be removed", func(t *testing.T) {
		ws := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(ws, "out"), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, "out", "a.txt"), []byte("abc"), 0o644))
		assert.NoErr(t, os.MkdirAll(filepath.Join(ws, "out", "node_modules", "m"), 0o755))
		ra := testChatApprover(nil, false, nil, ws, nil)
		q := ra.question(agentcore.ToolCall{Name: "file_remove", Arguments: map[string]any{"path": "out", "recursive": true}})
		assert.Eq(t, []string{"路径：" + filepath.Join(ws, "out"), "目录：1 个文件，3 字节，移入回收区保留 7 天",
			"直接永久删除 1 个被忽略的目录：out/node_modules/"}, q.Details)
		q = ra.question(agentcore.ToolCall{Name: "file_remove", Arguments: map[string]any{"path": "."}})
		assert.StrContains(t, q.Details[1], "检查：refusing to remove the workspace root")
	})
	t.Run("file_remove under an added :rw directory is planned like the workspace", func(t *testing.T) {
		ws, shared := t.TempDir(), t.TempDir()
		assert.NoErr(t, os.WriteFile(filepath.Join(shared, "a.txt"), []byte("abc"), 0o644))
		dirs := native.NewAddDirs()
		dirs.Set([]native.AddDir{{Path: shared, Writable: true}})
		ra := testChatApprover(nil, false, nil, ws, dirs)
		q := ra.question(agentcore.ToolCall{Name: "file_remove", Arguments: map[string]any{"path": filepath.Join(shared, "a.txt")}})
		assert.Eq(t, []string{"路径：" + filepath.Join(shared, "a.txt"), "文件：3 字节，移入回收区保留 7 天"}, q.Details)
		// The directory itself is still refused, like the workspace root.
		q = ra.question(agentcore.ToolCall{Name: "file_remove", Arguments: map[string]any{"path": shared, "recursive": true}})
		assert.StrContains(t, q.Details[1], "检查：refusing to remove the root of an added directory")
	})
	t.Run("file_edit previews old and new text", func(t *testing.T) {
		long := strings.Repeat("line\n", 30)
		q := approver.question(agentcore.ToolCall{Name: "file_edit", Arguments: map[string]any{
			"path": "a.go", "old_string": "b := 2", "new_string": long, "replace_all": true}})
		assert.Eq(t, "路径："+filepath.Join(filepath.FromSlash("/work"), "a.go"), q.Details[0])
		assert.Eq(t, "原文：\nb := 2", q.Details[1])
		assert.StrContains(t, q.Details[2], "…（共 31 行）")
		assert.Eq(t, "替换全部匹配处", q.Details[3])
	})
	t.Run("file_edit previews every edit", func(t *testing.T) {
		q := approver.question(agentcore.ToolCall{Name: "file_edit", Arguments: map[string]any{"path": "a.go", "edits": []any{
			map[string]any{"old_string": "a", "new_string": "b"},
			map[string]any{"old_string": "c", "new_string": "d", "replace_all": true},
		}}})
		assert.Eq(t, []string{"路径：" + filepath.Join(filepath.FromSlash("/work"), "a.go"),
			"第 1 处原文：\na", "第 1 处改为：\nb", "第 2 处原文：\nc", "第 2 处改为：\nd", "第 2 处替换全部匹配处"}, q.Details)
	})
	t.Run("web_search shows the query", func(t *testing.T) {
		q := approver.question(agentcore.ToolCall{Name: "web_search", Arguments: map[string]any{"query": "golang tcflush"}})
		assert.Eq(t, []string{"查询：golang tcflush"}, q.Details)
	})
	t.Run("control characters are removed before display", func(t *testing.T) {
		q := approver.question(agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": "rm -rf ~\r\x1b[2Kecho safe\nls\t-l"}})
		assert.Eq(t, "命令：rm -rf ~echo safe\nls\t-l", q.Details[0])
	})
}

func TestToolsApprovalConfig(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	for _, tc := range []struct {
		name  string
		value any
		auto  bool
		err   bool
	}{
		{name: "unset is ask", value: nil},
		{name: "ask", value: "ask"},
		{name: "auto", value: "auto", auto: true},
		{name: "unknown word", value: "always", err: true},
		{name: "upper case", value: "AUTO", err: true},
		{name: "empty", value: "", err: true},
		{name: "boolean", value: true, err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gkconfig.Reset()
			if tc.value != nil {
				assert.NoErr(t, gkconfig.Set("tools.approval", tc.value))
			}
			auto, err := toolsApprovalAuto()
			if tc.err {
				assert.Err(t, err)
				assert.StrContains(t, err.Error(), `tools.approval must be "ask" or "auto"`)
				return
			}
			assert.NoErr(t, err)
			assert.Eq(t, tc.auto, auto)
		})
	}
}

// TestOpenCLIChatApproval checks the runtime wiring: chat gets an (unbound)
// asker and rejects an invalid tools.approval at startup; run has no asker.
func TestOpenCLIChatApproval(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	t.Run("chat exposes an unbound asker", func(t *testing.T) {
		rt, err := OpenCLI(context.Background(), Options{ChatMode: true})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		assert.NotNil(t, rt.Asker)
		_, err = rt.Asker.Ask(context.Background(), tools.Question{})
		assert.ErrIs(t, err, tools.ErrCannotAsk)
	})
	t.Run("run has no asker", func(t *testing.T) {
		rt, err := OpenCLI(context.Background(), Options{})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		assert.Nil(t, rt.Asker)
	})
	t.Run("invalid tools.approval fails chat startup", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.approval", "sometimes"))
		_, err := OpenCLI(context.Background(), Options{ChatMode: true})
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "tools.approval")
	})
}

func TestChatApproverApprovedNote(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	// 与 internal/personal/runner.go 的真实装配一致：observe 建报告上下文 ->
	// output 层（含标注） -> approval 层。这里直接用同一顺序的栈，断言模型看到的
	// 结果文案：人批准的那次有注明，记住的 always 与规则放行没有，拒绝不变。
	buildLayer := func(t *testing.T, asker *scriptedAsker) agentcoreapi.ToolRegistry {
		approval, _ := newChatApproval(t, false, asker)
		return tools.NewOutput(approval, tools.OutputConfig{})
	}
	run := func(t *testing.T, layer agentcoreapi.ToolRegistry, call agentcore.ToolCall) (*agentcoreapi.ToolResult, error) {
		ctx, _ := tools.WithApprovalReport(context.Background())
		return layer.ExecuteTool(ctx, call)
	}
	asker := &scriptedAsker{answers: []string{"a"}}
	layer := buildLayer(t, asker)

	t.Run("human answered once equals noted", func(t *testing.T) {
		res, err := run(t, layer, agentcore.ToolCall{CallID: "c1", Name: "shell_exec", Arguments: map[string]any{"command": "echo hi"}})
		assert.NoErr(t, err)
		assert.StrContains(t, res.Content, tools.ApprovedResultNote)
	})
	t.Run("remembered rule stays unnoted", func(t *testing.T) {
		res, err := run(t, layer, agentcore.ToolCall{CallID: "c2", Name: "shell_exec", Arguments: map[string]any{"command": "echo hi"}})
		assert.NoErr(t, err)
		assert.False(t, strings.Contains(res.Content, tools.ApprovedResultNote))
	})
	t.Run("rule allowed read tool stays unnoted", func(t *testing.T) {
		res, err := run(t, layer, agentcore.ToolCall{CallID: "c3", Name: "file_read", Arguments: map[string]any{"path": "a.txt"}})
		assert.NoErr(t, err)
		assert.False(t, strings.Contains(res.Content, tools.ApprovedResultNote))
	})
	t.Run("denied call keeps its refusal", func(t *testing.T) {
		denying := &scriptedAsker{answers: []string{"n"}}
		deniedLayer := buildLayer(t, denying)
		res, err := run(t, deniedLayer, agentcore.ToolCall{CallID: "c4", Name: "shell_exec", Arguments: map[string]any{"command": "echo hi"}})
		assert.ErrIs(t, err, tools.ErrDenied)
		assert.Nil(t, res)
		// 拒绝时结果文案不变：模型看到的仍是拒绝错误，不会有人批准的注明。
		assert.Eq(t, "用户拒绝执行 shell_exec", err.Error())
	})
}

func TestChatApproverReportsSource(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	run := func(approval *tools.Approval, name string) (*tools.ApprovalReport, error) {
		ctx, rep := tools.WithApprovalReport(context.Background())
		_, err := approval.ExecuteTool(ctx, agentcore.ToolCall{CallID: "c", Name: name, Arguments: map[string]any{"command": "echo"}})
		return rep, err
	}
	asker := &scriptedAsker{answers: []string{"a", "", "n"}}
	approval, _ := newChatApproval(t, false, asker)
	rep, err := run(approval, "file_read")
	assert.NoErr(t, err)
	assert.Eq(t, tools.ApprovedByRule, rep.Source) // read-only: never asked
	rep, err = run(approval, "shell_exec")
	assert.NoErr(t, err)
	assert.Eq(t, tools.ApprovedByUser, rep.Source) // answered "always"
	rep, err = run(approval, "shell_exec")
	assert.NoErr(t, err)
	assert.Eq(t, tools.ApprovedAlways, rep.Source) // not asked again for the same scope
	rep, err = run(approval, "file_write")
	assert.Error(t, err)
	assert.Eq(t, tools.AskedAndDenied, rep.Source)
}
