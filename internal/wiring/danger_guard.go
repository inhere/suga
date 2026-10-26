package wiring

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// dangerChecker is implemented by shell_exec (native.ShellExecTool).
type dangerChecker interface {
	DangerousDelete(command string) string
}

// dangerGuard stops a shell command that looks like a catastrophic delete
// (native.DangerousDelete) before the approver sees it. In chat the person
// confirms it every time, by typing "yes", even with tools.approval: auto or
// an earlier "always"; everywhere else (run, ACP) it is refused.
type dangerGuard struct {
	inner tools.Approver
	chat  *chatApprover // nil outside chat
}

// guardDangerous wraps approver; a nil approver stays nil (deny all).
func guardDangerous(approver tools.Approver) tools.Approver {
	if approver == nil {
		return nil
	}
	chat, _ := approver.(*chatApprover)
	return &dangerGuard{inner: approver, chat: chat}
}

func (g *dangerGuard) Decide(ctx context.Context, call agentcore.ToolCall, tool agentcore.Tool) error {
	checker, ok := tool.(dangerChecker)
	if !ok {
		return g.inner.Decide(ctx, call, tool)
	}
	command, _ := call.Arguments["command"].(string)
	reason := checker.DangerousDelete(command)
	if reason == "" {
		return g.inner.Decide(ctx, call, tool)
	}
	tools.ReportDanger(ctx, reason)
	if g.chat == nil {
		return tools.Denied("已拒绝危险删除：" + reason + "。这类命令不会自动执行：删除工作区内的文件请用 file_remove（可恢复），" +
			"路径里的变量先确认非空或写成字面路径；确实需要时请让用户自己执行。")
	}
	return g.confirm(ctx, call, reason)
}

// confirm asks the chat user; only "yes" lets the command run.
func (g *dangerGuard) confirm(ctx context.Context, call agentcore.ToolCall, reason string) error {
	a := g.chat
	a.mu.Lock()
	defer a.mu.Unlock()
	command, _ := call.Arguments["command"].(string)
	asked := time.Now()
	t := approvalTr(a.translator)
	answer, err := a.asker.Ask(ctx, tools.Question{
		Tool: call.Name,
		Details: []string{
			t.T("host.danger.command", map[string]any{"command": displaySafe(redact.Secrets(command))}),
			t.T("host.danger.detail", map[string]any{"reason": displaySafe(reason)}),
		},
		Prompt: t.T("host.danger.prompt", map[string]any{"tool": call.Name}),
	})
	if errors.Is(err, tools.ErrCannotAsk) {
		return tools.Denied("已拒绝危险删除（无法向用户确认）：" + reason)
	}
	if err != nil {
		return err
	}
	wait := time.Since(asked)
	if strings.EqualFold(strings.TrimSpace(answer), "yes") {
		tools.ReportApproval(ctx, tools.ApprovedByUser, wait)
		return nil
	}
	tools.ReportApproval(ctx, tools.AskedAndDenied, wait)
	return tools.Denied("用户拒绝执行危险删除：" + reason)
}
