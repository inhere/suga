package acpserver

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// trustPolicy models the wiring closure: every write asks; a target under .git
// is a protected-path downgrade (clamped and pathProtected), like ClampAllow.
func trustPolicy() PolicyDecision {
	return func(call agentcore.ToolCall, _ agentcore.Tool) (string, string, string, bool, bool) {
		path, _ := call.Arguments["path"].(string)
		protected := strings.HasPrefix(path, ".git/")
		return "ask", "", "file_write:**", protected, protected
	}
}

func connectWithOptions(t *testing.T, open OpenRuntime, client acp.Client, opts ...ServeOption) *acp.ClientSideConnection {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()
	agent := New(open, false, opts...)
	agent.SetConnection(acp.NewAgentSideConnection(agent, agentToClientW, clientToAgentR))
	conn := acp.NewClientSideConnection(client, clientToAgentW, agentToClientR)
	t.Cleanup(func() {
		_ = clientToAgentW.Close()
		_ = clientToAgentR.Close()
		_ = agentToClientW.Close()
		_ = agentToClientR.Close()
	})
	return conn
}

// TestACPTrustThroughServeOption drives real prompts: the tier set by
// WithApprovalMode reaches the permission hook of each prompt.
func TestACPTrustThroughServeOption(t *testing.T) {
	run := func(t *testing.T, tier tools.ApprovalMode, mode acp.SessionModeId) (*testACPClient, *countingTool) {
		t.Helper()
		tool := &countingTool{}
		provider := mock.NewProvider(
			mock.ToolCall("call-1", "file_write", map[string]any{"path": "notes.txt"}),
			mock.ToolCall("call-2", "file_write", map[string]any{"path": ".git/hooks/x"}),
			mock.Final("done"),
		)
		client := &testACPClient{selected: "reject"}
		open := testACPRuntimeWith(t, provider, tool, acpTestOptions{policy: trustPolicy()})
		conn := connectWithOptions(t, open, client, WithApprovalMode(tier))
		ctx := testCtx(t)
		created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
		assert.Require(t, assert.NoErr(t, err))
		if mode != "" {
			_, err = conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: created.SessionId, ModeId: mode})
			assert.Require(t, assert.NoErr(t, err))
		}
		_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("go")}})
		assert.Require(t, assert.NoErr(t, err))
		return client, tool
	}

	t.Run("trust runs an unprotected ask and still asks for .git", func(t *testing.T) {
		client, tool := run(t, tools.ApprovalModeTrust, "")
		client.mu.Lock()
		defer client.mu.Unlock()
		assert.Require(t, assert.Len(t, client.requests, 1))
		assert.Eq(t, int32(1), tool.calls.Load()) // notes.txt ran; the .git write was rejected
	})
	t.Run("no tier keeps asking for both", func(t *testing.T) {
		client, tool := run(t, "", "")
		client.mu.Lock()
		defer client.mu.Unlock()
		assert.Len(t, client.requests, 2)
		assert.Eq(t, int32(0), tool.calls.Load())
	})
	t.Run("auto and ask do not release asks", func(t *testing.T) {
		for _, tier := range []tools.ApprovalMode{tools.ApprovalModeAuto, tools.ApprovalModeAsk} {
			client, _ := run(t, tier, "")
			client.mu.Lock()
			assert.Len(t, client.requests, 2)
			client.mu.Unlock()
		}
	})
	t.Run("read_only mode still refuses writes under trust", func(t *testing.T) {
		client, tool := run(t, tools.ApprovalModeTrust, "read_only")
		client.mu.Lock()
		defer client.mu.Unlock()
		assert.Len(t, client.requests, 0)
		assert.Eq(t, int32(0), tool.calls.Load())
	})
}

func TestACPTrustHookDecide(t *testing.T) {
	decide := func(t *testing.T, policy PolicyDecision, hook *permissionHook, path string) (error, *tools.ApprovalReport) {
		t.Helper()
		call := agentcore.ToolCall{CallID: "c-" + path, Name: "file_write", Arguments: map[string]any{"path": path}}
		assert.Require(t, assert.NoErr(t, hook.BeforeTool(context.Background(), &agentcore.ToolEvent{RunID: "run", Call: &call}, nil)))
		ctx, report := tools.WithApprovalReport(context.Background())
		return hook.Decide(ctx, call, nil), report
	}
	t.Run("the approval is recorded as trust and no request is sent", func(t *testing.T) {
		client := &testACPClient{selected: "reject"}
		hook := newTableHook(t, t.TempDir(), false, client)
		hook.policy, hook.approval = trustPolicy(), tools.ApprovalModeTrust
		err, report := decide(t, hook.policy, hook, "notes.txt")
		assert.NoErr(t, err)
		assert.Eq(t, tools.ApprovedByTrust, report.Source)
		client.mu.Lock()
		assert.Len(t, client.requests, 0)
		client.mu.Unlock()
	})
	t.Run("an always allow rule on a protected target does not release it", func(t *testing.T) {
		client := &testACPClient{selected: "reject"}
		hook := newTableHook(t, t.TempDir(), false, client)
		hook.policy, hook.approval, hook.session = trustPolicy(), tools.ApprovalModeTrust, newSessionPolicy()
		hook.session.add("file_write:**", "allow")
		assert.Error(t, func() error { err, _ := decide(t, hook.policy, hook, ".git/config"); return err }())
		client.mu.Lock()
		assert.Len(t, client.requests, 1)
		client.mu.Unlock()
	})
	t.Run("a deny from the policy is not released", func(t *testing.T) {
		client := &testACPClient{selected: "allow"}
		hook := newTableHook(t, t.TempDir(), false, client)
		hook.approval = tools.ApprovalModeTrust
		hook.policy = func(agentcore.ToolCall, agentcore.Tool) (string, string, string, bool, bool) {
			return "deny", "rule", "file_write:**", false, false
		}
		err, _ := decide(t, hook.policy, hook, "notes.txt")
		assert.Error(t, err)
	})
	t.Run("memory confirmation is lifted by trust only", func(t *testing.T) {
		client := &testACPClient{selected: "reject"}
		hook := newTableHook(t, t.TempDir(), false, client)
		assert.Error(t, hook.ConfirmMemory(context.Background(), memory.Scope{}, memory.Op{}))
		hook.approval = tools.ApprovalModeTrust
		assert.NoErr(t, hook.ConfirmMemory(context.Background(), memory.Scope{}, memory.Op{}))
		client.mu.Lock()
		assert.Len(t, client.requests, 1) // only the untrusted attempt asked
		client.mu.Unlock()
	})
}
