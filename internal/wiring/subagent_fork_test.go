package wiring

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// forkWriteTool is a write tool the fork's registry holds on purpose (through
// extraTools) and the parent policy allows: the whitelist of forkDenyTools is
// then the only barrier between the model and the file.
type forkWriteTool struct{ dir string }

func (t *forkWriteTool) Name() string        { return "file_write" }
func (t *forkWriteTool) Description() string { return "write a file" }
func (t *forkWriteTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryEdit}
}
func (t *forkWriteTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: "file_write", Description: t.Description(),
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}
}
func (t *forkWriteTool) Execute(_ context.Context, call agentcore.ToolCall) (*agentcore.ToolResult, error) {
	name, _ := call.Arguments["path"].(string)
	if err := os.WriteFile(filepath.Join(t.dir, name), []byte("x"), 0o644); err != nil {
		return nil, err
	}
	return &agentcore.ToolResult{CallID: call.CallID, Name: "file_write", Content: "written"}, nil
}

// forkTestSeed is a seed whose request declares tools a fork must not run.
func forkTestSeed(model string) personal.ForkSeed {
	var schemas []agentcore.ToolSchema
	for _, name := range []string{"file_read", "file_write", "agent", "task", "tool_search", "tool_call", "mcp__x__y", "memory"} {
		schemas = append(schemas, agentcore.ToolSchema{Name: name, Description: name,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}})
	}
	return personal.ForkSeed{
		Request: &agentcore.GenerateRequest{System: "main system", Tools: schemas, Messages: []agentcore.Message{
			{Role: "user", Content: "earlier question"}, {Role: "assistant", Content: "earlier answer"},
			{Role: "user", Content: "fork directive"},
		}},
		Model: model, ProviderName: "openai", BaseTurn: "turn-7", Source: "call", Reuse: "prefix",
	}
}

type forkRun struct {
	report  native.AgentReport
	bodies  []string
	dir     string
	session string
}

// runFork runs a fork against a scripted model that makes one call per entry
// of calls (tool name, path argument) and then answers "fork answer".
func runFork(t *testing.T, model string, calls [][2]string, rules []approvalrules.Rule) forkRun {
	t.Helper()
	workspace := manifestConfig(t)
	t.Setenv("SUGA_HOME", t.TempDir())
	t.Setenv("SUGA_SESSION_ID", "")
	var script []string
	for i, c := range calls {
		script = append(script, childToolCall("c"+string(rune('1'+i)), c[0], map[string]any{"path": c[1]}))
	}
	srv, bodies := scriptedChildServer(t, script)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k", "base_url": srv.URL}})
	policy := approvalPolicy{Mode: approvalrules.ModeRun, Workspace: workspace,
		Defaults: defaultApprovalDefaults(approvalrules.ModeRun, false, tools.ApprovalModeDefault), Rules: rules}
	s := &subAgents{workspace: workspace, ruleCWD: workspace, settings: appconfig.AgentSettings{MaxSteps: 12},
		policySnapshot: func() approvalPolicy { return policy }, extraTools: []agentcore.Tool{&forkWriteTool{dir: workspace}}}
	seed := forkTestSeed(model)
	rep, err := s.RunAgent(context.Background(), native.AgentRequest{Type: native.AgentFork, Description: "d", Prompt: "do it",
		Background: true, CallID: "call-1", Seed: seed})
	assert.NoErr(t, err)
	return forkRun{report: rep, bodies: *bodies, dir: workspace, session: rep.SessionID}
}

func TestForkRunner(t *testing.T) {
	t.Run("three writes in a row still end with the answer", func(t *testing.T) {
		out := runFork(t, "gpt-4o", [][2]string{{"file_write", "a.txt"}, {"file_write", "b.txt"}, {"file_write", "c.txt"}}, nil)
		assert.Eq(t, "final", out.report.Stop)
		assert.StrContains(t, out.report.Report, "child done")
		for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
			_, err := os.Stat(filepath.Join(out.dir, name))
			assert.True(t, os.IsNotExist(err), name)
		}
		last := out.bodies[len(out.bodies)-1]
		assert.Eq(t, 3, strings.Count(last, "分身只读：file_write 在分身中不可用"))
	})
	t.Run("ask rule on a read fails closed as an ordinary error", func(t *testing.T) {
		rules := []approvalrules.Rule{{Source: approvalrules.SourceUser, Index: 1, Tool: "file_read", Action: approvalrules.Ask}}
		out := runFork(t, "gpt-4o", [][2]string{{"file_read", "a.txt"}, {"file_read", "a.txt"}, {"file_read", "a.txt"}}, rules)
		assert.Eq(t, "final", out.report.Stop)
		assert.StrContains(t, out.report.Report, "child done")
		last := out.bodies[len(out.bodies)-1]
		assert.Eq(t, 3, strings.Count(last, "分身只读：file_read 在分身中不可用"))
	})
	t.Run("every name outside the whitelist gets the same hint", func(t *testing.T) {
		out := runFork(t, "gpt-4o", [][2]string{{"agent", "x"}, {"tool_call", "x"}, {"mcp__x__y", "x"}, {"task", "x"}, {"memory", "x"}, {"tool_search", "x"}}, nil)
		assert.Eq(t, "final", out.report.Stop)
		last := out.bodies[len(out.bodies)-1]
		for _, name := range []string{"agent", "tool_call", "mcp__x__y", "task", "memory", "tool_search"} {
			assert.StrContains(t, last, "分身只读："+name+" 在分身中不可用，不要重试；需要改动或命令时写进报告由主会话执行")
		}
		assert.NotContains(t, last, "告诉用户")
	})
	t.Run("the request model follows the seed", func(t *testing.T) {
		out := runFork(t, "gpt-4.1", nil, nil)
		assert.StrContains(t, out.bodies[0], `"model":"gpt-4.1"`)
	})
	t.Run("session meta fork_base", func(t *testing.T) {
		out := runFork(t, "gpt-4o", nil, nil)
		path, err := appconfig.ResolveCoreStorePath()
		assert.NoErr(t, err)
		repo, err := store.Open(path)
		assert.NoErr(t, err)
		defer func() { _ = repo.Close() }()
		sess, err := repo.GetSession(context.Background(), out.session)
		assert.NoErr(t, err)
		assert.Eq(t, "turn-7|call", sess.Meta["fork_base"])
		assert.Eq(t, "fork", sess.Meta["agent_type"])
	})
	t.Run("an unbound host has no seed", func(t *testing.T) {
		s := &subAgents{fork: &forkHost{}}
		_, err := s.ForkSeed(context.Background(), native.AgentRequest{})
		var unavailable *personal.ForkUnavailableError
		assert.True(t, errors.As(err, &unavailable))
	})
}
