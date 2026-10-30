package output

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/llm"
	"github.com/inhere/suga/pkg/agentcore/redact"
)

// WithModel adds the "model" field ("provider/model") to a stream-json line;
// an empty model leaves the line as it is.
func WithModel(line map[string]any, model string) map[string]any {
	if model != "" {
		line["model"] = model
	}
	return line
}

// SessionLine is the first stream-json line of a run.
func SessionLine(sessionID string) map[string]any {
	return map[string]any{"type": "session", "id": sessionID, "session_id": sessionID}
}

// EventLine maps a runner event to its stream-json line; ok is false for
// events that have no stream-json representation.
func EventLine(event personal.Event) (line map[string]any, ok bool) {
	data := map[string]any{"session_id": event.SessionID, "run_id": event.RunID}
	switch event.Kind {
	case "run_started":
		data["type"] = "turn_start"
	case "run_finished":
		data["type"], data["status"] = "turn_end", event.State
		if event.Usage != nil {
			data["usage"] = personal.ReportRunUsage(event.Usage.Run)
			if event.Usage.Estimated {
				data["estimated"] = true
			}
			addBudget(data, event.Usage.Budget)
			// The context object comes with the turn's last request only; a turn
			// that made none leaves it out.
			addContextOccupancy(data, event.Usage.Context)
		}
	case "usage":
		// Run-cumulative usage after each model call; the last one equals the
		// turn_end/result "usage" unless tool calls followed it.
		if event.Usage == nil {
			return nil, false
		}
		data["type"], data["usage"] = "usage", personal.ReportRunUsage(event.Usage.Run)
		if event.Usage.Estimated {
			data["estimated"] = true
		}
		addBudget(data, event.Usage.Budget)
	case "budget":
		data["type"] = "budget"
		if event.Text != "" {
			data["message"] = event.Text
		}
		addBudget(data, event.Budget)
	case string(agentcore.StreamEventTextDelta):
		data["type"], data["text"] = "message_delta", event.Text
	case string(agentcore.StreamEventRetry):
		// restart: the message_delta text since the last message start is
		// void; the message is streamed again from its start.
		data["type"], data["reason"] = "retry", event.Text
		if event.Retry != nil {
			data["attempt"], data["wait_ms"], data["restart"] = event.Retry.Attempt, event.Retry.Wait.Milliseconds(), event.Retry.Restart
		}
	case "plan":
		// Same shape as the ACP plan update: the full list, replaced each time.
		entries := make([]map[string]string, 0, len(event.Plan.Steps))
		for _, step := range event.Plan.Steps {
			entries = append(entries, map[string]string{"step": step.Step, "status": step.Status})
		}
		data["type"], data["explanation"], data["entries"] = "plan", event.Plan.Explanation, entries
	case "tool_call":
		// Text is already the redacted, truncated argument summary (toolEventHook).
		data["type"], data["name"], data["args"] = "tool_call", event.ToolName, event.Text
	case "tool_result":
		data["type"], data["name"], data["is_error"] = "tool_result", event.ToolName, event.IsError
		data["summary"] = ToolResultSummary(event.Text)
	case "agent_progress":
		// A sub-agent's tool call (agent tool); agent is its description.
		data["type"], data["agent"], data["name"], data["args"] = "agent_progress", event.Agent, event.ToolName, event.Text
	default:
		return nil, false
	}
	return data, true
}

// toolSummaryLimit caps the tool_result summary, in runes.
const toolSummaryLimit = 200

// VerifyLine is the stream-json line of one --verify check: round counts from
// 0 (the check that follows the first turn), command is the caller's command
// (secrets masked) and passed is what its exit code says.
func VerifyLine(round int, command string, exitCode int, passed bool, duration time.Duration) map[string]any {
	return map[string]any{
		"type": "verify", "round": round, "command": command, "exit_code": exitCode,
		"passed": passed, "duration_ms": duration.Milliseconds(),
	}
}

// NudgeLine is the stream-json line of one hit rule of the "stopped too early"
// check: rule is the rule's name, kind one of promise/unverified_write/
// plan_open, action says what happened (pushed, hinted, recorded) and evidence
// is the one-line reason.
func NudgeLine(rule, kind, action, evidence string) map[string]any {
	return map[string]any{"type": "nudge", "rule": rule, "kind": kind, "action": action, "evidence": evidence}
}

// WithVerify adds the run's --verify outcome to the closing result line: how
// many checks ran and whether the last one passed. A run without --verify
// leaves the line exactly as it was.
func WithVerify(line map[string]any, passed bool, rounds int) map[string]any {
	line["verify"] = map[string]any{"passed": passed, "rounds": rounds}
	return line
}

// WithOutputSchema adds the run's --output-schema outcome to the closing result
// line (design 2026-10-22 4.4): checked:false with a reason when the turn's end
// left nothing to judge, else passed/rounds/source plus the errors of a failure
// or the checked value of a pass. A run without --output-schema leaves the line
// exactly as it was.
func WithOutputSchema(line map[string]any, rep personal.ContractReport) map[string]any {
	view := map[string]any{"checked": rep.Checked}
	if !rep.Checked {
		view["reason"] = rep.Reason
		line["output_schema"] = view
		return line
	}
	view["passed"], view["rounds"], view["source"] = rep.Passed, rep.Rounds, rep.Source
	if len(rep.Errors) > 0 {
		errs := make([]map[string]string, 0, len(rep.Errors))
		for _, err := range rep.Errors {
			errs = append(errs, map[string]string{"path": err.Path, "message": err.Message})
		}
		view["errors"] = errs
	}
	if rep.Passed && len(rep.Value) > 0 {
		// The value is the checked JSON text; it goes out as JSON, not as a quoted
		// string, so the caller can read it without decoding twice.
		view["value"] = json.RawMessage(rep.Value)
	}
	line["output_schema"] = view
	return line
}

// ToolResultSummary condenses a tool result for stream-json. Secrets are
// redacted first and the result truncated afterwards, so a cut can never leave
// a partially redacted credential behind.
func ToolResultSummary(content string) string {
	text := strings.Join(strings.Fields(redact.Secrets(content)), " ")
	if r := []rune(text); len(r) > toolSummaryLimit {
		return string(r[:toolSummaryLimit-1]) + "…"
	}
	return text
}

// ErrorLine is the stream-json line for a failed run. error_kind (when the
// failure was a provider error, see personal.RunResult.ErrorKind) tells a
// transient one - a cut stream whose retries ran out - from an unusable
// provider answer, so the caller can decide whether continuing makes sense.
// reset_at is the quota reset time a provider named for an exhausted quota
// (llm.ResetAt), so the caller can say when trying again makes sense.
func ErrorLine(sessionID string, runErr error, result personal.RunResult, budget ...*personal.BudgetSnapshot) map[string]any {
	line := map[string]any{"type": "error", "session_id": sessionID, "error": runErr.Error()}
	if result.ErrorKind != "" {
		line["error_kind"] = result.ErrorKind
	}
	if reset := llm.ResetAt(runErr); reset != "" {
		line["reset_at"] = reset
	}
	return WithUsage(line, result, budget...)
}

// YieldedLine is the input_required line of a run that stopped for more input.
func YieldedLine(sessionID string, result personal.RunResult, budget ...*personal.BudgetSnapshot) map[string]any {
	line := map[string]any{"type": "input_required", "session_id": sessionID, "run_id": result.RunID}
	if len(result.DeniedTools) > 0 {
		line["denied_tools"] = result.DeniedTools
	}
	return WithUsage(line, result, budget...)
}

// DeniedLine is the input_required line of a finished turn that had refused tool calls.
func DeniedLine(sessionID string, result personal.RunResult, budget ...*personal.BudgetSnapshot) map[string]any {
	return WithUsage(map[string]any{"type": "input_required", "session_id": sessionID, "run_id": result.RunID, "denied_tools": result.DeniedTools}, result, budget...)
}

// ResultLine is the final stream-json line of a successful run. A wrap-up
// answer (result.WrapUp, e.g. "max_steps") carries its reason as "wrap_up",
// so a caller can tell an answer cut short by a limit from a finished one; a
// normal answer leaves the field out. An answer cut off at the output token
// limit is marked the same way, with the limit as "output_limit".
func ResultLine(sessionID string, result personal.RunResult, budget ...*personal.BudgetSnapshot) map[string]any {
	line := map[string]any{
		"type": "result", "session_id": sessionID, "run_id": result.RunID,
		"result": result.Output, "status": result.State,
	}
	if result.WrapUp != "" {
		line["wrap_up"] = result.WrapUp
	}
	if result.Truncated {
		line["truncated"] = true
		if result.OutputLimit > 0 {
			line["output_limit"] = result.OutputLimit
		}
	}
	return WithUsage(line, result, budget...)
}

// ResultLineWithContext is ResultLine with the optional context usage object
// used by the CLI's final result event. occupancy is the turn's context
// occupancy of its last request (the same figure turn_end carried).
func ResultLineWithContext(sessionID string, result personal.RunResult, contextWindow int, occupancy *personal.ContextOccupancy, budget ...*personal.BudgetSnapshot) map[string]any {
	return WithContext(ResultLine(sessionID, result, budget...), result, contextWindow, occupancy)
}
