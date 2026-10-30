package wiring

import (
	"context"
	"errors"
	"fmt"
	"time"

	gkconfig "github.com/gookit/config/v2"

	chatreply "github.com/inhere/suga/internal/chat"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// askOnExternalOrigin reads tools.ask_on_external_origin (default true).
func askOnExternalOrigin() bool { return gkconfig.Bool("tools.ask_on_external_origin", true) }

// originObserver records the text of every external tool result (web pages,
// search results, MCP replies, memory backends that flag external=true) into
// the session's ExternalOrigin set. It wraps the approval layer, so what it
// sees is the result of a call that was allowed and ran.
type originObserver struct {
	inner interface {
		agentcore.ToolRegistry
		tools.Lookup
	}
	origin *tools.ExternalOrigin
}

func (o *originObserver) ListTools(ctx context.Context) ([]agentcore.ToolSchema, error) {
	return o.inner.ListTools(ctx)
}

func (o *originObserver) Get(name string) (agentcore.Tool, error) { return o.inner.Get(name) }

func (o *originObserver) ExecuteTool(ctx context.Context, call agentcore.ToolCall) (*agentcore.ToolResult, error) {
	res, err := o.inner.ExecuteTool(ctx, call)
	if res == nil {
		return res, err
	}
	tool, _ := o.inner.Get(call.Name)
	external, _ := res.Metadata["external"].(bool)
	if external || tools.MetaOf(tool).External {
		o.origin.Record(api.SessionIDFrom(ctx), call.Name, res.Content)
	}
	return res, err
}

// originGuard stops a write or exec call whose arguments carry a verbatim piece
// (12 bytes or more) of an external tool result, even with tools.approval: auto.
// In chat the person is asked once for that call; everywhere else nobody can be
// asked, so the call is refused with the reason. Read-only and agent-state tools
// are not checked. The decision then goes on to the wrapped approver, so rules
// and the usual prompt still apply.
type originGuard struct {
	inner  tools.Approver
	chat   *chatApprover // nil outside chat
	origin *tools.ExternalOrigin
}

// guardExternalOrigin wraps approver (which may already be a wrapper of the
// chat approver chat; nil outside chat); a nil approver stays nil (deny all)
// and the guard is off with tools.ask_on_external_origin: false.
func guardExternalOrigin(approver tools.Approver, chat *chatApprover, origin *tools.ExternalOrigin) tools.Approver {
	if approver == nil || !askOnExternalOrigin() {
		return approver
	}
	return &originGuard{inner: approver, chat: chat, origin: origin}
}

func (g *originGuard) Decide(ctx context.Context, call agentcore.ToolCall, tool agentcore.Tool) error {
	meta := tools.MetaOf(tool)
	if meta.ReadOnly || meta.AgentState {
		return g.inner.Decide(ctx, call, tool)
	}
	hit, ok := g.origin.Find(api.SessionIDFrom(ctx), call.Arguments)
	if !ok {
		return g.inner.Decide(ctx, call, tool)
	}
	snippet := displaySafe(redact.Secrets(hit.Snippet))
	reason := fmt.Sprintf("参数里有逐字复制自 %s 结果的文本：%q", hit.Tool, snippet)
	tools.ReportDanger(ctx, reason)
	if g.chat == nil {
		return tools.Denied(fmt.Sprintf("已拒绝 %s：%s。外部内容里的文字可能是提示注入，不会自动执行；"+
			"请改为自己写出参数，或让用户确认后再做。", call.Name, reason))
	}
	if err := g.confirm(ctx, call, hit.Tool, snippet, reason); err != nil {
		return err
	}
	return g.inner.Decide(ctx, call, tool)
}

// confirm asks the chat user; only an allowing reply lets the call go on.
func (g *originGuard) confirm(ctx context.Context, call agentcore.ToolCall, source, snippet, reason string) error {
	a := g.chat
	a.mu.Lock()
	defer a.mu.Unlock()
	asked := time.Now()
	t := approvalTr(a.translator)
	answer, err := a.asker.Ask(ctx, tools.Question{
		Tool: call.Name,
		Details: []string{
			t.T("host.origin.source", map[string]any{"tool": displaySafe(source)}),
			t.T("host.origin.snippet", map[string]any{"snippet": snippet}),
		},
		Prompt: t.T("host.origin.prompt", map[string]any{"tool": call.Name}),
	})
	if errors.Is(err, tools.ErrCannotAsk) {
		return tools.Denied("已拒绝（无法向用户确认）：" + call.Name + "：" + reason)
	}
	if err != nil {
		return err
	}
	wait := time.Since(asked)
	if chatreply.ParseApprovalReply(answer) == chatreply.ReplyAllow {
		tools.ReportApproval(ctx, tools.ApprovedByUser, wait)
		return nil
	}
	tools.ReportApproval(ctx, tools.AskedAndDenied, wait)
	return tools.Denied("用户拒绝执行 " + call.Name + "：" + reason)
}

// chatApproverOf is the chat approver behind approver, nil for any other.
func chatApproverOf(approver tools.Approver) *chatApprover {
	chat, _ := approver.(*chatApprover)
	return chat
}
