package acpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/skills"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// policyRow is one tool of the ACP policy table. Outcomes: "run" (allowed
// without a permission request), "ask" (RequestPermission is sent) or "deny"
// (refused without a request). The table locks the current behavior before
// the tool kind and metadata refactor and must stay unchanged through it.
type policyRow struct {
	name string
	tool func(t *testing.T, cwd string) agentcore.Tool
	// kind is the kind announced by BeforeTool; diff whether the
	// announcement carries a diff for a file argument.
	kind acp.ToolKind
	diff bool
	// readOnly and normal are the Decide outcomes in read_only and default
	// mode; nilTool is the default-mode outcome when Decide gets no tool.
	readOnly, normal, nilTool string
}

func policyCatalogTool(name string) func(t *testing.T, cwd string) agentcore.Tool {
	return func(t *testing.T, cwd string) agentcore.Tool {
		t.Helper()
		for _, spec := range native.Catalog() {
			if spec.Name == name {
				tool := spec.New(native.Env{Workspace: cwd, SerpAPIKey: "test-key", TrashDir: t.TempDir()})
				assert.Require(t, assert.NotNil(t, tool))
				return tool
			}
		}
		t.Fatalf("%s is not in the native catalog", name)
		return nil
	}
}

func policyRows() []policyRow {
	allowed := func(name string, kind acp.ToolKind) policyRow {
		return policyRow{name: name, tool: policyCatalogTool(name), kind: kind, readOnly: "run", normal: "run", nilTool: "run"}
	}
	gated := func(name string, kind acp.ToolKind, diff bool) policyRow {
		return policyRow{name: name, tool: policyCatalogTool(name), kind: kind, diff: diff, readOnly: "deny", normal: "ask", nilTool: "ask"}
	}
	wired := func(name string, kind acp.ToolKind, tool func(t *testing.T, cwd string) agentcore.Tool) policyRow {
		return policyRow{name: name, tool: tool, kind: kind, readOnly: "run", normal: "run", nilTool: "run"}
	}
	mcpRow := func(server, name string) policyRow {
		return policyRow{name: name, tool: func(*testing.T, string) agentcore.Tool {
			return &tableMCPTool{countingTool{name: name, mcp: true}, server}
		},
			kind: acp.ToolKindOther, readOnly: "deny", normal: "ask", nilTool: "ask"}
	}
	return []policyRow{
		allowed("file_read", acp.ToolKindRead), allowed("file_list", acp.ToolKindRead),
		allowed("file_grep", acp.ToolKindSearch), allowed("file_find", acp.ToolKindSearch),
		allowed("file_symbols", acp.ToolKindRead), allowed("file_refs", acp.ToolKindSearch),
		allowed("web_search", acp.ToolKindSearch), allowed("web_fetch", acp.ToolKindFetch),
		gated("file_write", acp.ToolKindEdit, true), gated("file_edit", acp.ToolKindEdit, true),
		gated("shell_exec", acp.ToolKindExecute, false), gated("file_remove", acp.ToolKindDelete, false),
		wired("update_plan", acp.ToolKindThink, func(*testing.T, string) agentcore.Tool {
			return native.NewUpdatePlanTool(personal.NewPlanRecorder())
		}),
		wired("tool_search", acp.ToolKindSearch, func(*testing.T, string) agentcore.Tool {
			return native.NewToolSearchTool(tools.NewVisibility(tools.NewRegistry(), tools.VisibilityOptions{}))
		}),
		wired("skill", acp.ToolKindRead, func(*testing.T, string) agentcore.Tool { return native.NewSkillTool(skills.NewRegistry(), 0) }),
		wired("session_search", acp.ToolKindSearch, func(*testing.T, string) agentcore.Tool {
			return native.NewSessionSearchTool(personal.NewLedger(""))
		}),
		wired("recall", acp.ToolKindSearch, func(*testing.T, string) agentcore.Tool {
			return native.NewRecallTool(personal.NewLedger(""), 0)
		}),
		wired("suga_manifest", acp.ToolKindRead, func(*testing.T, string) agentcore.Tool { return wiring.NewManifestTool(nil) }),
		// Unknown tools require permission and are refused in read_only.
		{name: "future_tool", tool: func(*testing.T, string) agentcore.Tool { return &countingTool{name: "future_tool"} },
			kind: acp.ToolKindOther, readOnly: "deny", normal: "ask", nilTool: "ask"},
		// Agent-state tools run without a request, even in read_only mode.
		// (A nil tool gets no exemption: the call is announced by name only, fail closed.)
		{name: "agent_state_tool", tool: func(*testing.T, string) agentcore.Tool {
			return &countingTool{name: "agent_state_tool", agentState: true}
		}, kind: acp.ToolKindOther, readOnly: "run", normal: "run", nilTool: "ask"},
		// An MCP tool declaring AgentState is still an MCP tool.
		{name: "memory", tool: func(*testing.T, string) agentcore.Tool {
			return &tableMCPTool{countingTool{name: "memory", mcp: true, agentState: true}, "evil"}
		}, kind: acp.ToolKindOther, readOnly: "deny", normal: "ask", nilTool: "ask"},
		mcpRow("srv", "srv_query"),
		// An MCP tool named like a built-in read tool never takes the read fast path.
		mcpRow("evil", "file_read"),
		mcpRow("evil", "file_write"),
	}
}

// tableMCPTool is an MCP tool of a named server.
type tableMCPTool struct {
	countingTool
	server string
}

func (t *tableMCPTool) MCPServer() string { return t.server }

// newTableHook returns a permission hook wired to a client that rejects every
// permission request.
func newTableHook(t *testing.T, cwd string, readOnly bool, client *testACPClient) *permissionHook {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()
	conn := acp.NewAgentSideConnection(New(nil), agentToClientW, clientToAgentR)
	_ = acp.NewClientSideConnection(client, clientToAgentW, agentToClientR)
	t.Cleanup(func() {
		_ = clientToAgentW.Close()
		_ = clientToAgentR.Close()
		_ = agentToClientW.Close()
		_ = agentToClientR.Close()
	})
	return &permissionHook{conn: conn, sessionID: "s", cwd: cwd, readOnly: readOnly}
}

// policyOutcome announces call through BeforeTool, then classifies Decide.
func policyOutcome(t *testing.T, hook *permissionHook, client *testACPClient, call agentcore.ToolCall, tool agentcore.Tool) (string, *pendingCall) {
	t.Helper()
	ctx := context.Background()
	assert.Require(t, assert.NoErr(t, hook.BeforeTool(ctx, &agentcore.ToolEvent{RunID: "r", Call: &call}, nil)))
	hook.mu.Lock()
	pending := *hook.calls[acp.ToolCallId(call.CallID)]
	hook.mu.Unlock()
	client.mu.Lock()
	before := len(client.requests)
	client.mu.Unlock()
	err := hook.Decide(ctx, call, tool)
	client.mu.Lock()
	asked := len(client.requests) > before
	client.mu.Unlock()
	var denied *deniedError
	switch {
	case asked:
		return "ask", &pending
	case err == nil:
		return "run", &pending
	case errors.As(err, &denied):
		return "deny", &pending
	}
	t.Fatalf("%s: unexpected Decide error %v", call.Name, err)
	return "", nil
}

func TestPolicyTable(t *testing.T) {
	cwd := t.TempDir()
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(cwd, "t.txt"), []byte("old\n"), 0o644)))
	args := map[string]any{"path": "t.txt", "content": "new\n", "old_string": "old", "new_string": "new", "command": "true"}

	for i, row := range policyRows() {
		tool := row.tool(t, cwd)
		t.Run(fmt.Sprintf("%02d_%s", i, row.name), func(t *testing.T) {
			lookup := func(string) agentcore.Tool { return tool }
			outcome := func(readOnly bool, tool agentcore.Tool, id string) (string, *pendingCall) {
				client := &testACPClient{selected: "reject"}
				hook := newTableHook(t, cwd, readOnly, client)
				hook.lookup = lookup
				return policyOutcome(t, hook, client, agentcore.ToolCall{CallID: id, Name: row.name, Arguments: args}, tool)
			}

			got, pending := outcome(false, tool, "c1")
			assert.Eq(t, row.normal, got, "default mode")
			assert.Eq(t, row.kind, pending.kind, "announced kind")
			assert.Eq(t, row.diff, len(pending.content) > 0, "diff")
			got, _ = outcome(true, tool, "c2")
			assert.Eq(t, row.readOnly, got, "read_only")
			got, _ = outcome(false, nil, "c3")
			assert.Eq(t, row.nilTool, got, "nil tool")
		})
	}
}

func TestACPPolicyOptionsAndImmediateDecisions(t *testing.T) {
	policy := PolicyDecision(func(agentcore.ToolCall, agentcore.Tool) (string, string, string) {
		return "ask", "", "file_write:docs/**"
	})
	for _, tc := range []struct {
		name     string
		selected acp.PermissionOptionId
		wantErr  bool
		wantAsk  bool
	}{
		{name: "allow once", selected: "allow", wantAsk: true},
		{name: "reject always", selected: "reject_always", wantErr: true, wantAsk: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &testACPClient{selected: tc.selected}
			hook := newTableHook(t, t.TempDir(), false, client)
			hook.policy = policy
			call := agentcore.ToolCall{CallID: "policy-call", Name: "file_write", Arguments: map[string]any{"path": "docs/a.txt"}}
			assert.Require(t, assert.NoErr(t, hook.BeforeTool(context.Background(), &agentcore.ToolEvent{RunID: "run", Call: &call}, nil)))
			err := hook.Decide(context.Background(), call, nil)
			assert.Eq(t, tc.wantErr, err != nil)
			client.mu.Lock()
			assert.Eq(t, tc.wantAsk, len(client.requests) == 1)
			if tc.wantAsk {
				assert.Len(t, client.requests[0].Options, 4)
				assert.Eq(t, acp.PermissionOptionKindAllowOnce, client.requests[0].Options[0].Kind)
				assert.Eq(t, acp.PermissionOptionId("allow"), client.requests[0].Options[0].OptionId)
				assert.Eq(t, acp.PermissionOptionKindAllowAlways, client.requests[0].Options[1].Kind)
				assert.Eq(t, acp.PermissionOptionId("allow_always"), client.requests[0].Options[1].OptionId)
				assert.Eq(t, acp.PermissionOptionKindRejectOnce, client.requests[0].Options[2].Kind)
				assert.Eq(t, acp.PermissionOptionId("reject"), client.requests[0].Options[2].OptionId)
				assert.Eq(t, acp.PermissionOptionKindRejectAlways, client.requests[0].Options[3].Kind)
				assert.Eq(t, acp.PermissionOptionId("reject_always"), client.requests[0].Options[3].OptionId)
			}
			client.mu.Unlock()
		})
	}

	t.Run("allow policy does not ask but still starts the tool", func(t *testing.T) {
		client := &testACPClient{selected: "reject"}
		hook := newTableHook(t, t.TempDir(), false, client)
		hook.policy = func(agentcore.ToolCall, agentcore.Tool) (string, string, string) {
			return "allow", "user#1 file_write action=allow", "file_write:docs/**"
		}
		call := agentcore.ToolCall{CallID: "allow-call", Name: "file_write"}
		assert.Require(t, assert.NoErr(t, hook.BeforeTool(context.Background(), &agentcore.ToolEvent{RunID: "run", Call: &call}, nil)))
		assert.NoErr(t, hook.Decide(context.Background(), call, nil))
		client.mu.Lock()
		assert.Len(t, client.requests, 0)
		client.mu.Unlock()
	})
}
