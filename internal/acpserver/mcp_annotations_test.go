package acpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

// hint is a *bool for an MCP annotation field.
func hint(v bool) *bool { return &v }

// annotatedMCPTool is the fake MCP server tool of the ACP annotations tests: an
// MCP tool of server "gofer" plus the annotations its tools/list declared.
type annotatedMCPTool struct {
	tableMCPTool
	annotations mcp.Annotations
}

func (t *annotatedMCPTool) MCPAnnotations() mcp.Annotations { return t.annotations }

func newAnnotatedMCPTool(name string, annotations mcp.Annotations) *annotatedMCPTool {
	return &annotatedMCPTool{tableMCPTool: tableMCPTool{countingTool: countingTool{name: name, mcp: true}, server: "gofer"}, annotations: annotations}
}

// TestToolKindMCPAnnotations pins the announced kind: only a non-destructive
// readOnlyHint makes an MCP tool kind=read, and never a read-only tool for the
// approval layer.
func TestToolKindMCPAnnotations(t *testing.T) {
	readTool := policyCatalogTool("file_read")(t, t.TempDir())
	cases := []struct {
		name     string
		tool     agentcore.Tool
		kind     acp.ToolKind
		readOnly bool
	}{
		{"readOnlyHint", newAnnotatedMCPTool("gofer_work_list", mcp.Annotations{ReadOnlyHint: hint(true)}), acp.ToolKindRead, false},
		{"readOnlyHint with title", newAnnotatedMCPTool("gofer_today_list", mcp.Annotations{Title: "Today", ReadOnlyHint: hint(true)}), acp.ToolKindRead, false},
		{"destructive readOnlyHint", newAnnotatedMCPTool("purge", mcp.Annotations{ReadOnlyHint: hint(true), DestructiveHint: hint(true)}), acp.ToolKindOther, false},
		{"readOnlyHint false", newAnnotatedMCPTool("write", mcp.Annotations{ReadOnlyHint: hint(false)}), acp.ToolKindOther, false},
		{"no annotations", newAnnotatedMCPTool("srv_query", mcp.Annotations{}), acp.ToolKindOther, false},
		{"mcp tool without the interface", &tableMCPTool{countingTool: countingTool{name: "file_read", mcp: true}, server: "evil"}, acp.ToolKindOther, false},
		{"non MCP tool unchanged", readTool, acp.ToolKindRead, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, readOnly := toolKind(tc.tool)
			assert.Eq(t, tc.kind, kind)
			assert.Eq(t, tc.readOnly, readOnly)
		})
	}
}

// TestMCPReadOnlyHintStillAsks is the ACP contract of the annotations: an MCP
// tool declaring readOnlyHint is announced and requested as kind=read, but the
// permission request is still sent and read_only mode still refuses it.
func TestMCPReadOnlyHintStillAsks(t *testing.T) {
	call := agentcore.ToolCall{CallID: "mcp-1", Name: "gofer_work_list", Arguments: map[string]any{"status": "open"}}

	// outcome runs BeforeTool then Decide and reports the announced kind, the
	// requested kind and whether RequestPermission was sent.
	outcome := func(t *testing.T, readOnly bool, tool agentcore.Tool) (acp.ToolKind, *acp.ToolKind, bool, error) {
		t.Helper()
		client := &testACPClient{selected: "reject"}
		hook := newTableHook(t, t.TempDir(), readOnly, client)
		hook.lookup = func(string) agentcore.Tool { return tool }
		ctx := context.Background()
		assert.Require(t, assert.NoErr(t, hook.BeforeTool(ctx, &agentcore.ToolEvent{RunID: "r", Call: &call}, nil)))
		hook.mu.Lock()
		announced := *hook.calls[acp.ToolCallId(call.CallID)]
		hook.mu.Unlock()
		err := hook.Decide(ctx, call, tool)
		client.mu.Lock()
		defer client.mu.Unlock()
		if len(client.requests) == 0 {
			return announced.kind, nil, false, err
		}
		return announced.kind, client.requests[0].ToolCall.Kind, true, err
	}

	t.Run("read only hint still asks with kind read", func(t *testing.T) {
		tool := newAnnotatedMCPTool("gofer_work_list", mcp.Annotations{ReadOnlyHint: hint(true)})
		announced, requested, asked, err := outcome(t, false, tool)
		assert.Eq(t, acp.ToolKindRead, announced)
		assert.True(t, asked, "the permission request is still sent")
		assert.Require(t, assert.NotNil(t, requested))
		assert.Eq(t, acp.ToolKindRead, *requested)
		var denied *deniedError
		assert.True(t, errors.As(err, &denied), "the rejected request denies the call")
	})

	t.Run("read only hint is still refused in read_only mode", func(t *testing.T) {
		tool := newAnnotatedMCPTool("gofer_work_list", mcp.Annotations{ReadOnlyHint: hint(true)})
		announced, _, asked, err := outcome(t, true, tool)
		assert.Eq(t, acp.ToolKindRead, announced)
		assert.False(t, asked, "read_only refuses before asking")
		var denied *deniedError
		assert.Require(t, assert.True(t, errors.As(err, &denied)))
		assert.True(t, errors.Is(err, ErrReadOnly))
	})

	t.Run("destructive tool stays other", func(t *testing.T) {
		tool := newAnnotatedMCPTool("purge", mcp.Annotations{ReadOnlyHint: hint(true), DestructiveHint: hint(true)})
		announced, requested, asked, _ := outcome(t, false, tool)
		assert.Eq(t, acp.ToolKindOther, announced)
		assert.True(t, asked)
		assert.Require(t, assert.NotNil(t, requested))
		assert.Eq(t, acp.ToolKindOther, *requested)
	})

	t.Run("tool without annotations stays other", func(t *testing.T) {
		tool := newAnnotatedMCPTool("srv_query", mcp.Annotations{})
		announced, requested, asked, _ := outcome(t, false, tool)
		assert.Eq(t, acp.ToolKindOther, announced)
		assert.True(t, asked)
		assert.Require(t, assert.NotNil(t, requested))
		assert.Eq(t, acp.ToolKindOther, *requested)
	})
}

// TestMCPReadOnlyHintKeepsApproverRules pins that the hint changes nothing but
// the reported kind: the ACP policy still asks (and never allows) the call.
func TestMCPReadOnlyHintKeepsApproverRules(t *testing.T) {
	tool := newAnnotatedMCPTool("gofer_work_list", mcp.Annotations{ReadOnlyHint: hint(true)})
	policyAsked := false
	client := &testACPClient{selected: "allow"}
	hook := newTableHook(t, t.TempDir(), false, client)
	hook.lookup = func(string) agentcore.Tool { return tool }
	hook.policy = PolicyDecision(func(agentcore.ToolCall, agentcore.Tool) (string, string, string) {
		policyAsked = true
		return "ask", "", "mcp:gofer/gofer_work_list"
	})
	call := agentcore.ToolCall{CallID: "mcp-2", Name: "gofer_work_list"}
	ctx := context.Background()
	assert.Require(t, assert.NoErr(t, hook.BeforeTool(ctx, &agentcore.ToolEvent{RunID: "r", Call: &call}, nil)))
	assert.NoErr(t, hook.Decide(ctx, call, tool))
	assert.True(t, policyAsked)
	client.mu.Lock()
	defer client.mu.Unlock()
	assert.Len(t, client.requests, 1)
	assert.Eq(t, acp.ToolKindRead, *client.requests[0].ToolCall.Kind)
}

// TestMCPAnnotationsDoNotLeakIntoMeta pins that the tools.Meta of an MCP tool
// stays untouched: other approval layers (chat, run, sub-agents) keep refusing
// or asking regardless of the server's declaration.
func TestMCPAnnotationsDoNotLeakIntoMeta(t *testing.T) {
	tool := newAnnotatedMCPTool("gofer_work_list", mcp.Annotations{ReadOnlyHint: hint(true)})
	meta := tools.MetaOf(tool)
	assert.False(t, meta.ReadOnly)
	assert.Eq(t, tools.CategoryOther, meta.Category)
}
