package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// approvalTool counts executions.
type approvalTool struct{ runs int }

func (*approvalTool) Name() string           { return "act" }
func (*approvalTool) Description() string    { return "act" }
func (*approvalTool) Schema() api.ToolSchema { return api.ToolSchema{Name: "act"} }
func (*approvalTool) Kind() Kind             { return KindNative }
func (t *approvalTool) Execute(context.Context, api.ToolCall) (*api.ToolResult, error) {
	t.runs++
	return &api.ToolResult{Content: "ok"}, nil
}

func TestApproval(t *testing.T) {
	call := api.ToolCall{CallID: "c1", Name: "act"}
	build := func(decide func() error) (*Approval, *approvalTool) {
		tool := &approvalTool{}
		reg := NewRegistry()
		reg.Register(tool)
		var approver Approver
		if decide != nil {
			approver = ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return decide() })
		}
		return NewApproval(reg, approver), tool
	}

	t.Run("allow runs the tool", func(t *testing.T) {
		a, tool := build(func() error { return nil })
		res, err := a.ExecuteTool(context.Background(), call)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "ok", res.Content)
		assert.Eq(t, 1, tool.runs)
		got, err := a.Get("act")
		assert.NoErr(t, err)
		assert.Eq(t, "act", got.Name())
	})
	t.Run("denial is returned as is", func(t *testing.T) {
		a, tool := build(func() error { return Denied("no") })
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrDenied))
		assert.Eq(t, false, errors.Is(err, ErrAborted))
		assert.Eq(t, "no", err.Error())
		assert.Eq(t, 0, tool.runs)
	})
	t.Run("other errors abort and keep the cause", func(t *testing.T) {
		a, tool := build(func() error { return context.Canceled })
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrAborted))
		assert.Eq(t, true, errors.Is(err, context.Canceled))
		assert.Eq(t, false, errors.Is(err, ErrDenied))
		assert.Eq(t, 0, tool.runs)
	})
	t.Run("nil approver fails closed", func(t *testing.T) {
		a, tool := build(nil)
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrDenied))
		assert.Eq(t, 0, tool.runs)
	})
	t.Run("unknown tool is not sent to the approver", func(t *testing.T) {
		asked := false
		reg := NewRegistry()
		a := NewApproval(reg, ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { asked = true; return nil }))
		_, err := a.ExecuteTool(context.Background(), api.ToolCall{Name: "missing"})
		assert.Err(t, err)
		assert.Eq(t, false, asked)
	})
}

// approvalRewriter is a CallRewriter with a scripted behaviour.
type approvalRewriter struct {
	rewrite func(api.ToolCall) (api.ToolCall, error)
}

func (r approvalRewriter) Rewrite(_ context.Context, call api.ToolCall, _ api.Tool) (api.ToolCall, error) {
	return r.rewrite(call)
}

// TestApprovalRewrite covers the pre-decision rewriter (a hook rewriting the
// arguments): the decision is always taken on the rewritten call, and a rewriter
// that fails or panics refuses the call instead of letting the original run.
func TestApprovalRewrite(t *testing.T) {
	call := api.ToolCall{CallID: "c1", Name: "act", Arguments: map[string]any{"path": "docs/a.txt"}}
	build := func(rewrite func(api.ToolCall) (api.ToolCall, error), decide func(api.ToolCall) error) (*Approval, *approvalTool) {
		tool := &approvalTool{}
		reg := NewRegistry()
		reg.Register(tool)
		a := NewApproval(reg, ApproverFunc(func(_ context.Context, c api.ToolCall, _ api.Tool) error { return decide(c) }))
		a.SetRewriter(approvalRewriter{rewrite: rewrite})
		return a, tool
	}

	t.Run("the decision sees the rewritten arguments", func(t *testing.T) {
		var decided map[string]any
		a, tool := build(func(c api.ToolCall) (api.ToolCall, error) {
			c.Arguments = map[string]any{"path": "go.mod"}
			return c, nil
		}, func(c api.ToolCall) error {
			decided = c.Arguments
			if c.Arguments["path"] == "go.mod" {
				return Denied("受保护路径")
			}
			return nil
		})
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrDenied))
		assert.Eq(t, "go.mod", decided["path"])
		assert.Eq(t, 0, tool.runs)
	})
	t.Run("a rewriter cannot allow what the approver refuses", func(t *testing.T) {
		a, tool := build(func(c api.ToolCall) (api.ToolCall, error) { return c, nil },
			func(api.ToolCall) error { return Denied("read_only") })
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrDenied))
		assert.Eq(t, "read_only", err.Error())
		assert.Eq(t, 0, tool.runs)
	})
	t.Run("a failing rewriter refuses the call", func(t *testing.T) {
		a, tool := build(func(c api.ToolCall) (api.ToolCall, error) {
			return c, Denied("hook 超时")
		}, func(api.ToolCall) error { return nil })
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrDenied))
		assert.Eq(t, "hook 超时", err.Error())
		assert.Eq(t, 0, tool.runs)
	})
	t.Run("a panicking rewriter refuses the call", func(t *testing.T) {
		a, tool := build(func(api.ToolCall) (api.ToolCall, error) {
			panic("boom")
		}, func(api.ToolCall) error { return nil })
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrDenied))
		assert.Eq(t, false, errors.Is(err, ErrAborted))
		assert.Eq(t, true, strings.Contains(err.Error(), "boom"))
		assert.Eq(t, 0, tool.runs)
	})
	t.Run("the rewritten call is the one that runs", func(t *testing.T) {
		run := &approvalRunTool{}
		reg := NewRegistry()
		reg.Register(run)
		a := NewApproval(reg, ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return nil }))
		a.SetRewriter(approvalRewriter{rewrite: func(c api.ToolCall) (api.ToolCall, error) {
			c.Arguments = map[string]any{"path": "docs/b.txt"}
			return c, nil
		}})
		_, err := a.ExecuteTool(context.Background(), call)
		assert.NoErr(t, err)
		assert.Eq(t, "docs/b.txt", run.args["path"])
	})
}

// approvalRunTool keeps the arguments its call ran with.
type approvalRunTool struct{ args map[string]any }

func (*approvalRunTool) Name() string           { return "act" }
func (*approvalRunTool) Description() string    { return "act" }
func (*approvalRunTool) Schema() api.ToolSchema { return api.ToolSchema{Name: "act"} }
func (*approvalRunTool) Kind() Kind             { return KindNative }
func (t *approvalRunTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	t.args = call.Arguments
	return &api.ToolResult{Content: "ok"}, nil
}

// TestApprovalOnApproved covers the post-decision callback (SetOnApproved): it
// sees the call that is about to run - the rewritten one - and only when the
// decision let the call through; a refusal or an aborted decision never reaches
// it.
func TestApprovalOnApproved(t *testing.T) {
	call := api.ToolCall{CallID: "c1", Name: "act", Arguments: map[string]any{"path": "docs/a.txt"}}
	build := func(decide func() error) (*Approval, *approvalTool, *[]api.ToolCall) {
		tool := &approvalTool{}
		reg := NewRegistry()
		reg.Register(tool)
		a := NewApproval(reg, ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return decide() }))
		var seen []api.ToolCall
		a.SetOnApproved(func(_ context.Context, c api.ToolCall, _ api.Tool) { seen = append(seen, c) })
		return a, tool, &seen
	}

	t.Run("allowed call: called once, before the tool ran", func(t *testing.T) {
		a, tool, seen := build(func() error { return nil })
		runsAtCall := 0
		a.SetOnApproved(func(_ context.Context, c api.ToolCall, _ api.Tool) {
			runsAtCall = tool.runs
			*seen = append(*seen, c)
		})
		_, err := a.ExecuteTool(context.Background(), call)
		assert.NoErr(t, err)
		assert.Eq(t, 1, tool.runs)
		assert.Eq(t, 0, runsAtCall)
		assert.Require(t, assert.Eq(t, 1, len(*seen)))
		assert.Eq(t, "c1", (*seen)[0].CallID)
	})
	t.Run("denied call: not called", func(t *testing.T) {
		a, tool, seen := build(func() error { return Denied("no") })
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrDenied))
		assert.Eq(t, 0, tool.runs)
		assert.Len(t, *seen, 0)
	})
	t.Run("aborted decision: not called", func(t *testing.T) {
		a, tool, seen := build(func() error { return context.Canceled })
		_, err := a.ExecuteTool(context.Background(), call)
		assert.Eq(t, true, errors.Is(err, ErrAborted))
		assert.Eq(t, 0, tool.runs)
		assert.Len(t, *seen, 0)
	})
	t.Run("the rewritten call is the one recorded", func(t *testing.T) {
		tool := &approvalTool{}
		reg := NewRegistry()
		reg.Register(tool)
		a := NewApproval(reg, ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return nil }))
		a.SetRewriter(approvalRewriter{rewrite: func(c api.ToolCall) (api.ToolCall, error) {
			c.Arguments = map[string]any{"path": "docs/b.txt"}
			return c, nil
		}})
		var recorded api.ToolCall
		a.SetOnApproved(func(_ context.Context, c api.ToolCall, _ api.Tool) { recorded = c })
		_, err := a.ExecuteTool(context.Background(), call)
		assert.NoErr(t, err)
		assert.Eq(t, "docs/b.txt", recorded.Arguments["path"])
	})
}

func TestApprovalReport(t *testing.T) {
	call := api.ToolCall{CallID: "c1", Name: "act"}
	reg := NewRegistry()
	reg.Register(&approvalTool{})
	t.Run("let through without a word is the rule", func(t *testing.T) {
		a := NewApproval(reg, ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return nil }))
		ctx, rep := WithApprovalReport(context.Background())
		_, err := a.ExecuteTool(ctx, call)
		assert.NoErr(t, err)
		assert.Eq(t, ApprovedByRule, rep.Source)
	})
	t.Run("an approver that asked reports it", func(t *testing.T) {
		a := NewApproval(reg, ApproverFunc(func(ctx context.Context, _ api.ToolCall, _ api.Tool) error {
			ReportApproval(ctx, ApprovedByUser, 1500*time.Millisecond)
			return nil
		}))
		ctx, rep := WithApprovalReport(context.Background())
		_, err := a.ExecuteTool(ctx, call)
		assert.NoErr(t, err)
		assert.Eq(t, ApprovalReport{Source: ApprovedByUser, Wait: 1500 * time.Millisecond}, *rep)
	})
	t.Run("rule and scope survive context and serialization", func(t *testing.T) {
		a := NewApproval(reg, ApproverFunc(func(ctx context.Context, _ api.ToolCall, _ api.Tool) error {
			ReportRule(ctx, `user#3 shell_exec command="go test *" action=allow`, `shell_exec:go test`)
			return nil
		}))
		ctx, rep := WithApprovalReport(context.Background())
		_, err := a.ExecuteTool(ctx, call)
		assert.NoErr(t, err)
		assert.Eq(t, `user#3 shell_exec command="go test *" action=allow`, rep.Rule)
		assert.Eq(t, `shell_exec:go test`, rep.Scope)
		assert.Eq(t, rep.Rule, approvalReportOf(ctx).Rule)
		assert.Eq(t, rep.Scope, approvalReportOf(ctx).Scope)
		encoded, err := json.Marshal(rep)
		assert.NoErr(t, err)
		var decoded ApprovalReport
		assert.NoErr(t, json.Unmarshal(encoded, &decoded))
		assert.Eq(t, *rep, decoded)
	})
	t.Run("without a report in the context nothing breaks", func(t *testing.T) {
		ReportApproval(context.Background(), ApprovedByUser, time.Second)
		ReportRule(context.Background(), "ignored", "ignored")
	})
}
