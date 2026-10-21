package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/inhere/suga/internal/output"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

// The "stopped too early" check of a run (personal.CheckNudge, design
// docs/design/2026-10-22-nudge-design.md): a turn that looks finished although
// the work is not is pushed back with an injected turn, the same way
// finishBackgroundTasks and the --verify loop give a turn one more chance.
const (
	// maxNudgePromisePushes is N2's per-run limit: asking twice is enough, a
	// model that keeps promising is not helped by a third request.
	maxNudgePromisePushes = 2
	// maxNudgeUnverifiedPushes is N1's per-run limit: it points at a missing check
	// once; whether a change needs one is the model's call.
	maxNudgeUnverifiedPushes = 1
)

// nudgeRunner is the slice of *personal.Runner the check needs: a push is an
// ordinary injected turn, and the findings come from the turn's own events.
type nudgeRunner interface {
	Run(ctx context.Context, req personal.RunRequest) (personal.RunResult, error)
	NudgeFindings(ctx context.Context, sessionID string, result personal.RunResult) []personal.NudgeFinding
	RecordEvent(ctx context.Context, sessionID string, ev personal.HostEvent)
}

// nudgeAfterTurn re-runs the model while the finished turn hits a rule, at most
// maxNudgePromisePushes (N2) and maxNudgeUnverifiedPushes (N1) pushes per run.
// skipUnverified drops N1 because a --verify command already checked the
// delivery. It returns the last turn's result; an error is returned only when a
// push could not run or the context ended.
func nudgeAfterTurn(ctx context.Context, nr nudgeRunner, sessionID string, result personal.RunResult,
	skipUnverified bool, emit personal.EmitFunc, live *output.LiveText, encoder *json.Encoder, stderr io.Writer) (personal.RunResult, error) {
	pushed := map[string]int{}
	for {
		hits := nudgePushes(nr.NudgeFindings(ctx, sessionID, result), pushed, skipUnverified)
		if len(hits) == 0 {
			return result, nil
		}
		if live != nil {
			live.Finish() // end the streamed answer line before the pushed turn starts
		}
		for _, finding := range hits {
			pushed[finding.Rule]++
			personal.RecordNudge(ctx, nr, sessionID, personal.NudgeActionPushed, finding)
			if err := emitNudge(encoder, stderr, finding); err != nil {
				return result, err
			}
		}
		next, err := nr.Run(ctx, personal.RunRequest{
			SessionID: sessionID, Prompt: nudgePrompt(hits), Injected: true, Emit: emit,
		})
		result = next
		if err != nil {
			return result, err
		}
		// Only a normally finished turn is checked again (a failed, yielded,
		// cancelled or wrapped-up one is not): the loop ends here.
		if result.State != personal.RunCompleted || result.Status == agentcore.RunYielded || result.WrapUp != "" {
			return result, nil
		}
	}
}

// nudgePushes keeps the findings that may still be pushed: their per-run limit is
// not reached and N1 is not covered by --verify.
func nudgePushes(findings []personal.NudgeFinding, pushed map[string]int, skipUnverified bool) []personal.NudgeFinding {
	var hits []personal.NudgeFinding
	for _, finding := range findings {
		switch finding.Rule {
		case personal.NudgeRuleUnverified:
			if skipUnverified || pushed[finding.Rule] >= maxNudgeUnverifiedPushes {
				continue
			}
		default:
			if pushed[finding.Rule] >= maxNudgePromisePushes {
				continue
			}
		}
		hits = append(hits, finding)
	}
	return hits
}

// nudgePrompt is the injected turn: what the rules found, and the two ways out -
// finish the work, or say why stopping is right.
func nudgePrompt(hits []personal.NudgeFinding) string {
	var b strings.Builder
	b.WriteString("[收工检查] 上一回合看起来提前停下了，请继续完成，或说明为什么不需要继续/为什么必须停下。\n")
	for _, finding := range hits {
		fmt.Fprintf(&b, "- %s：%s\n", finding.Rule, finding.Evidence)
	}
	b.WriteString("不要重复已经完成的工作；确实交付完毕时，直接说明原因后结束。")
	return b.String()
}

// emitNudge reports one push: text mode writes its one stderr line (stdout stays
// the answer), stream-json its "nudge" line.
func emitNudge(encoder *json.Encoder, stderr io.Writer, finding personal.NudgeFinding) error {
	if encoder == nil {
		fmt.Fprintf(stderr, "nudge: %s %s\n", finding.Rule, finding.Evidence)
		return nil
	}
	return encoder.Encode(output.NudgeLine(finding.Rule, personal.NudgeActionPushed, finding.Evidence))
}
