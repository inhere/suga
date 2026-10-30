package wiring

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

func TestDangerGuard(t *testing.T) {
	ws := t.TempDir()
	shell := native.NewShellExecTool(ws, 0)
	call := func(command string) agentcore.ToolCall {
		return agentcore.ToolCall{CallID: "c1", Name: "shell_exec", Arguments: map[string]any{"command": command}}
	}
	// A delete of the whole workspace in the OS default shell (PowerShell on
	// Windows): dangerous, but one the person may still confirm.
	danger := "rm -rf ."
	// Deletes of a hard target: nobody may confirm them.
	hardTargets := []string{"rm -rf ~", "rm -rf /", "rm -rf ..", "rm -rf /var/lib/x"}
	if runtime.GOOS == "windows" {
		danger = "Remove-Item -Recurse -Force ."
		hardTargets = []string{"Remove-Item -Recurse -Force ~", `Remove-Item -Recurse -Force C:\`, "Remove-Item -Recurse -Force ..",
			`Remove-Item -Recurse -Force C:\Windows\Temp\x`}
	}
	innerRuns := 0
	inner := tools.ApproverFunc(func(context.Context, agentcore.ToolCall, agentcore.Tool) error {
		innerRuns++
		return nil
	})

	t.Run("ordinary commands go to the approver", func(t *testing.T) {
		innerRuns = 0
		assert.NoErr(t, guardDangerous(inner).Decide(context.Background(), call("go test ./..."), shell))
		assert.Eq(t, 1, innerRuns)
		assert.Nil(t, guardDangerous(nil)) // no approver stays deny-all
	})
	t.Run("run and ACP refuse, with the reason in the trace", func(t *testing.T) {
		innerRuns = 0
		ctx, report := tools.WithApprovalReport(context.Background())
		err := guardDangerous(inner).Decide(ctx, call(danger), shell)
		assert.True(t, errors.Is(err, tools.ErrDenied))
		assert.ErrMsgContains(t, err, "已拒绝危险删除："+danger+"：删除整个工作区")
		assert.ErrMsgContains(t, err, "file_remove")
		assert.Eq(t, 0, innerRuns)
		assert.StrContains(t, report.Danger, "整个工作区")
	})
	t.Run("chat asks even with approval auto; only yes runs", func(t *testing.T) {
		asker := &scriptedAsker{answers: []string{"y", "YES"}}
		guard := guardDangerous(testChatApprover(nil, true, asker, ws, nil))
		ctx, report := tools.WithApprovalReport(context.Background())
		err := guard.Decide(ctx, call(danger), shell)
		assert.ErrMsgContains(t, err, "用户拒绝执行危险删除") // "y" is not enough
		assert.Eq(t, tools.AskedAndDenied, report.Source)
		ctx, report = tools.WithApprovalReport(context.Background())
		assert.NoErr(t, guard.Decide(ctx, call(danger), shell))
		assert.Eq(t, tools.ApprovedByUser, report.Source)
		assert.Require(t, assert.Len(t, asker.questions, 2))
		assert.StrContains(t, asker.questions[0].Details[1], "⚠ 危险删除："+danger+"：删除整个工作区")
		assert.StrContains(t, asker.questions[0].Prompt, "输入 yes 执行")
	})
	t.Run("chat without a terminal refuses", func(t *testing.T) {
		guard := guardDangerous(testChatApprover(nil, false, &scriptedAsker{err: tools.ErrCannotAsk}, ws, nil))
		err := guard.Decide(context.Background(), call(danger), shell)
		assert.ErrMsgContains(t, err, "无法向用户确认")
	})
	t.Run("hard targets are refused even in chat, without asking", func(t *testing.T) {
		for _, command := range hardTargets {
			asker := &scriptedAsker{answers: []string{"yes", "yes"}}
			guard := guardDangerous(testChatApprover(nil, true, asker, ws, nil))
			innerRuns = 0
			ctx, report := tools.WithApprovalReport(context.Background())
			err := guard.Decide(ctx, call(command), shell)
			assert.True(t, errors.Is(err, tools.ErrDenied), command)
			assert.ErrMsgContains(t, err, "不可确认放行")
			assert.ErrMsgContains(t, err, command)
			assert.ErrMsgContains(t, err, "用户自己在终端执行")
			assert.Len(t, asker.questions, 0)
			assert.NotEq(t, "", report.Danger, command)
		}
		// run / ACP (no chat approver) refuse them the same way
		err := guardDangerous(inner).Decide(context.Background(), call(hardTargets[0]), shell)
		assert.ErrMsgContains(t, err, "不可确认放行")
	})
	t.Run("a delete inside the workspace goes on to the approver", func(t *testing.T) {
		innerRuns = 0
		cmd := "rm -rf ./build"
		if runtime.GOOS == "windows" {
			cmd = "Remove-Item -Recurse -Force ./build"
		}
		assert.NoErr(t, guardDangerous(inner).Decide(context.Background(), call(cmd), shell))
		assert.Eq(t, 1, innerRuns)
	})
}
