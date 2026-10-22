package chat

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/pkg/agentcore"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// toolGate is chat's PreToolUse hook as a tools.CallRewriter: the approval layer
// runs it before its own decision, so a call a hook rewrote is approved (or
// refused) on the rewritten arguments, never on the ones it replaced.
//
// The gate only narrows a call: a deny refuses it, a failure (a timeout, a
// non-zero exit, an unreadable answer, a panic) refuses it too (fail closed),
// and an allow is only "no objection" — the ordinary approval policy still
// decides afterwards. A rewrite replaces the arguments and the policy then sees
// the new ones.
type toolGate struct {
	gate     sugahooks.ToolGate
	recorder eventRecorder
	// cwd is the directory the hook command runs for (Event.CWD).
	cwd string
}

func newToolGate(gate sugahooks.ToolGate, recorder eventRecorder) *toolGate {
	cwd, _ := os.Getwd()
	return &toolGate{gate: gate, recorder: recorder, cwd: cwd}
}

// Rewrite runs the PreToolUse hook for the call. It returns the call unchanged
// when the hook does not cover the tool or has no objection.
func (g *toolGate) Rewrite(ctx context.Context, call agentcore.ToolCall, _ agentcore.Tool) (agentcore.ToolCall, error) {
	if g == nil || g.gate == nil {
		return call, nil
	}
	name, send := g.gate.PreToolName(call.Name)
	if !send {
		return call, nil
	}
	event := sugahooks.Event{
		Name: "PreToolUse", SessionID: agentcoreapi.SessionIDFrom(ctx), CWD: g.cwd,
		ToolName: name, ToolInput: call.Arguments,
	}
	start := time.Now()
	response, err := g.gate.PreTool(ctx, event)
	extra := map[string]any{"rewritten_args": len(response.UpdatedInput) > 0}
	recordHookRun(g.recorder, event, sugahooks.Result{Response: response, Err: err}, time.Since(start), extra)
	if err != nil {
		return call, tools.Denied(fmt.Sprintf("调用前 hook 未能确认，已拒绝执行 %s：%s", call.Name, err))
	}
	if denyDecision(response.Decision) {
		reason := strings.TrimSpace(response.Reason)
		if reason == "" {
			reason = "调用前 hook 拒绝执行"
		}
		return call, tools.Denied(fmt.Sprintf("%s（%s）", reason, call.Name))
	}
	if len(response.UpdatedInput) == 0 {
		return call, nil
	}
	rewritten := call
	rewritten.Arguments = make(map[string]any, len(response.UpdatedInput))
	for key, value := range response.UpdatedInput {
		rewritten.Arguments[key] = value
	}
	return rewritten, nil
}

// denyDecision reports whether a PreToolUse answer refuses the call.
func denyDecision(decision string) bool {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "deny", "block", "reject":
		return true
	}
	return false
}
