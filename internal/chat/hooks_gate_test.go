package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// gateTestTool records how it was executed; the PreToolUse tests read the
// arguments the call really ran with.
type gateTestTool struct {
	runs int
	call agentcore.ToolCall
}

func (*gateTestTool) Name() string        { return "file_write" }
func (*gateTestTool) Description() string { return "write a file" }
func (*gateTestTool) Schema() agentcoreapi.ToolSchema {
	return agentcoreapi.ToolSchema{Name: "file_write"}
}
func (*gateTestTool) Kind() tools.Kind { return tools.KindNative }

func (t *gateTestTool) Execute(_ context.Context, call agentcoreapi.ToolCall) (*agentcoreapi.ToolResult, error) {
	t.runs++
	t.call = call
	return &agentcoreapi.ToolResult{Content: "ok"}, nil
}

// gateTestClient is a ToolGate with a scripted answer.
type gateTestClient struct {
	tool     string
	response sugahooks.Response
	err      error
	events   []sugahooks.Event
}

func (c *gateTestClient) PreToolName(tool string) (string, bool) { return tool, tool == c.tool }

func (c *gateTestClient) PreTool(_ context.Context, event sugahooks.Event) (sugahooks.Response, error) {
	c.events = append(c.events, event)
	return c.response, c.err
}

// gateTestRecorder captures the hook events the gate writes for trace.
type gateTestRecorder struct{ events []personal.HostEvent }

func (r *gateTestRecorder) RecordEvent(_ context.Context, _ string, event personal.HostEvent) {
	r.events = append(r.events, event)
}

// TestToolGate covers the three hard rules of a call hook: a rewritten call is
// approved on the new arguments, a hook allow never skips the approval policy,
// and a hook that fails or denies refuses the call (with the reason in trace).
func TestToolGate(t *testing.T) {
	call := agentcore.ToolCall{CallID: "c1", Name: "file_write", Arguments: map[string]any{"path": "docs/a.txt"}}
	build := func(gate *gateTestClient, decide func(agentcore.ToolCall) error) (*tools.Approval, *gateTestTool, *gateTestRecorder) {
		tool := &gateTestTool{}
		reg := tools.NewRegistry()
		reg.Register(tool)
		approval := tools.NewApproval(reg, tools.ApproverFunc(func(_ context.Context, c agentcore.ToolCall, _ agentcore.Tool) error {
			return decide(c)
		}))
		recorder := &gateTestRecorder{}
		approval.SetRewriter(newToolGate(gate, recorder, nil))
		return approval, tool, recorder
	}
	ctx := agentcoreapi.WithSessionID(context.Background(), "sess-1")

	t.Run("the decision sees the arguments the hook rewrote", func(t *testing.T) {
		gate := &gateTestClient{tool: "file_write", response: sugahooks.Response{
			UpdatedInput: map[string]any{"path": "go.mod"}}}
		asked := map[string]any{}
		approval, tool, recorder := build(gate, func(c agentcore.ToolCall) error {
			asked = c.Arguments
			if c.Arguments["path"] == "go.mod" {
				return tools.Denied("受保护路径")
			}
			return nil
		})
		_, err := approval.ExecuteTool(ctx, call)
		assert.Eq(t, true, errors.Is(err, tools.ErrDenied))
		assert.Eq(t, "go.mod", asked["path"])
		assert.Eq(t, 0, tool.runs)
		assert.Require(t, assert.Eq(t, 1, len(gate.events)))
		assert.Eq(t, "PreToolUse", gate.events[0].Name)
		assert.Eq(t, "docs/a.txt", gate.events[0].ToolInput["path"])
		assert.Require(t, assert.Eq(t, 1, len(recorder.events)))
		assert.Eq(t, "hook", recorder.events[0].Kind)
		assert.Eq(t, true, recorder.events[0].Attrs["rewritten_args"])
	})

	t.Run("the rewritten call is the one that runs", func(t *testing.T) {
		gate := &gateTestClient{tool: "file_write", response: sugahooks.Response{
			UpdatedInput: map[string]any{"path": "docs/b.txt", "content": "hi"}}}
		approval, tool, _ := build(gate, func(agentcore.ToolCall) error { return nil })
		_, err := approval.ExecuteTool(ctx, call)
		assert.NoErr(t, err)
		assert.Eq(t, 1, tool.runs)
		assert.Eq(t, "docs/b.txt", tool.call.Arguments["path"])
		assert.Eq(t, "hi", tool.call.Arguments["content"])
		// The rewritten call keeps the identity of the original.
		assert.Eq(t, "c1", tool.call.CallID)
		assert.Eq(t, "file_write", tool.call.Name)
	})

	t.Run("a hook allow does not skip the approval policy", func(t *testing.T) {
		gate := &gateTestClient{tool: "file_write", response: sugahooks.Response{}}
		approval, tool, recorder := build(gate, func(agentcore.ToolCall) error {
			return tools.Denied("read_only 模式不允许写入")
		})
		_, err := approval.ExecuteTool(ctx, call)
		assert.Eq(t, true, errors.Is(err, tools.ErrDenied))
		assert.Eq(t, "read_only 模式不允许写入", err.Error())
		assert.Eq(t, 0, tool.runs)
		assert.Require(t, assert.Eq(t, 1, len(recorder.events)))
		assert.Eq(t, "ok", recorder.events[0].Status)
	})

	t.Run("a hook deny refuses the call and records the reason", func(t *testing.T) {
		gate := &gateTestClient{tool: "file_write", response: sugahooks.Response{Decision: "deny", Reason: "不要写 go.mod"}}
		approval, tool, recorder := build(gate, func(agentcore.ToolCall) error { return nil })
		_, err := approval.ExecuteTool(ctx, call)
		assert.Eq(t, true, errors.Is(err, tools.ErrDenied))
		assert.Eq(t, true, strings.Contains(err.Error(), "不要写 go.mod"))
		assert.Eq(t, 0, tool.runs)
		assert.Require(t, assert.Eq(t, 1, len(recorder.events)))
		assert.Eq(t, "deny", recorder.events[0].Attrs["decision"])
		assert.Eq(t, len("不要写 go.mod"), recorder.events[0].Attrs["reason_chars"])
	})

	t.Run("a failing hook refuses the call", func(t *testing.T) {
		gate := &gateTestClient{tool: "file_write", err: errors.New("hook command timed out after 3s")}
		approval, tool, recorder := build(gate, func(agentcore.ToolCall) error { return nil })
		_, err := approval.ExecuteTool(ctx, call)
		assert.Eq(t, true, errors.Is(err, tools.ErrDenied))
		assert.Eq(t, true, strings.Contains(err.Error(), "hook command timed out"))
		assert.Eq(t, 0, tool.runs)
		assert.Require(t, assert.Eq(t, 1, len(recorder.events)))
		assert.Eq(t, "error", recorder.events[0].Status)
		assert.Eq(t, true, strings.Contains(recorder.events[0].Error, "timed out"))
	})

	t.Run("an uncovered tool is left alone", func(t *testing.T) {
		gate := &gateTestClient{tool: "shell_exec"}
		asked := map[string]any{}
		approval, tool, recorder := build(gate, func(c agentcore.ToolCall) error { asked = c.Arguments; return nil })
		_, err := approval.ExecuteTool(ctx, call)
		assert.NoErr(t, err)
		assert.Eq(t, "docs/a.txt", asked["path"])
		assert.Eq(t, 1, tool.runs)
		assert.Eq(t, 0, len(gate.events))
		assert.Eq(t, 0, len(recorder.events))
	})
}
