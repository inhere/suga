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

// hardDangerChecker is a dangerChecker that also says whether the target is one
// the person may not confirm (native.DangerousDeleteKind).
type hardDangerChecker interface {
	DangerousDeleteKind(command string) (reason string, hard bool)
}

// dangerGuard stops a shell command that looks like a catastrophic delete
// (native.DangerousDelete) before the approver sees it. In chat the person
// confirms it every time, by typing "yes", even with tools.approval: auto or
// an earlier "always"; everywhere else (run, ACP) it is refused. A delete aimed
// at the home directory, the file system root, a parent of the workspace or a
// path outside the workspace is refused everywhere, chat included: typing "yes"
// does not run it.
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
	var reason string
	var hard bool
	if hc, ok := tool.(hardDangerChecker); ok {
		reason, hard = hc.DangerousDeleteKind(command)
	} else {
		reason = checker.DangerousDelete(command)
	}
	if reason == "" {
		return g.inner.Decide(ctx, call, tool)
	}
	tools.ReportDanger(ctx, reason)
	if hard {
		return tools.Denied(hardDeleteRefusal(reason))
	}
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

// hardDeleteRefusal is the error of a delete nobody may confirm: it names the
// target (inside reason) and the cause, and points the person to running the
// command themselves.
func hardDeleteRefusal(reason string) string {
	return "已拒绝危险删除（不可确认放行，聊天里输入 yes 也不会执行）：" + reason +
		"。目标是用户主目录、文件系统根、工作区的上级目录或工作区外的路径；删除工作区内的文件请用 file_remove（可恢复），" +
		"确实需要删除这个目标时请用户自己在终端执行。"
}
