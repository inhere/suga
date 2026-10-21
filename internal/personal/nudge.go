package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	axconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// The rules that detect a turn that stopped too early (design
// docs/design/2026-10-22-nudge-design.md). They are pure rules over what a
// finished turn left behind: the order of its tool calls, the session plan and
// the end of its answer. Each host decides what to do with a finding: run pushes
// the model back, chat only hints, ACP records it in the trace.

// The rule names a finding and the trace event carry.
const (
	// NudgeRuleUnverified is N1: work was not checked or not finished - a write
	// tool ran and no shell_exec followed it, or the session plan still has open
	// steps.
	NudgeRuleUnverified = "N1"
	// NudgeRulePromise is N2: the answer ends with "and now I will ..." but no
	// tool call follows it.
	NudgeRulePromise = "N2"
	// The actions a host reports for a finding.
	NudgeActionPushed   = "pushed"
	NudgeActionHinted   = "hinted"
	NudgeActionRecorded = "recorded"
)

// nudgeTailChars is how much of the end of an answer is searched for a promise
// (about 300 characters, counted in runes).
const nudgeTailChars = 300

// nudgeWriteTools are the tools whose successful call changed the workspace.
var nudgeWriteTools = map[string]bool{"file_edit": true, "file_write": true, "file_remove": true}

// nudgeCheckTool is the tool that validates a change.
const nudgeCheckTool = "shell_exec"

// NudgeToolCall is one tool call of the turn, in execution order.
type NudgeToolCall struct {
	Name string
	// OK is false for a refused or failed call: only a call that went through
	// counts as "the workspace changed" or "the change was checked".
	OK bool
	// Targets are the files or directories the call was about (trace attr target).
	Targets []string
}

// NudgeFinding is one hit rule with the evidence the model (or the trace) is
// told about.
type NudgeFinding struct {
	Rule     string
	Evidence string
}

// nudgePromisePatterns are the "I will continue" phrasings of N2. A match has to
// sit in the answer's last paragraph and the answer must not end with a question:
// asking whether to go on leaves the decision to the person.
var nudgePromisePatterns = []*regexp.Regexp{
	regexp.MustCompile(`接下来(我)?(会|将|来|去|先|再)`),
	regexp.MustCompile(`(我)?(先|再|现在|马上|这就)(去)?(运行|执行|跑|检查|验证|测试|修改|更新|修复|继续)`),
	regexp.MustCompile(`让我(先|再)?(运行|执行|跑|检查|验证|测试|修改|更新|修复)`),
	regexp.MustCompile(`下一步(我)?(将|会|来|要)`),
	regexp.MustCompile(`(?i)\bI('ll| will| am going to|'m going to)\s+(now\s+)?(run|check|fix|update|add|write|test|verify|continue)`),
	regexp.MustCompile(`(?i)\bNext,?\s+I('ll|’ll| will)`),
	regexp.MustCompile(`(?i)\bLet me\s+(now\s+)?(run|check|fix|update|add|write|test|verify|try)`),
}

// CheckNudge evaluates N1 and N2 for one finished turn and returns the findings
// in rule order. completed says the turn ended with a plain-text answer (the
// final assistant message carried no tool call); plan is the session's latest
// plan, nil when the session never made one.
func CheckNudge(calls []NudgeToolCall, plan *native.Plan, finalText string, completed bool) []NudgeFinding {
	var findings []NudgeFinding
	if evidence := unverifiedEvidence(calls, plan); evidence != "" {
		findings = append(findings, NudgeFinding{Rule: NudgeRuleUnverified, Evidence: evidence})
	}
	if completed {
		if evidence := promiseEvidence(finalText); evidence != "" {
			findings = append(findings, NudgeFinding{Rule: NudgeRulePromise, Evidence: evidence})
		}
	}
	return findings
}

// unverifiedEvidence is N1's evidence, or "" when neither condition holds: after
// the last successful write no shell_exec ran, or the session plan still has open
// steps.
func unverifiedEvidence(calls []NudgeToolCall, plan *native.Plan) string {
	var parts []string
	if target, ok := uncheckedWrite(calls); ok {
		parts = append(parts, fmt.Sprintf("最后修改 %s 之后没有运行 %s", target, nudgeCheckTool))
	}
	if open := openPlanSteps(plan); len(open) > 0 {
		parts = append(parts, fmt.Sprintf("计划还有 %d 项未完成：%s", len(open), strings.Join(open, "；")))
	}
	return strings.Join(parts, "；")
}

// nudgeOneLine collapses text to one line, so a plan step name or the quoted
// answer cannot break the one-line evidence a hint or a stderr line prints.
func nudgeOneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// uncheckedWrite reports the target of the last successful write tool when no
// shell_exec call followed it.
func uncheckedWrite(calls []NudgeToolCall) (string, bool) {
	lastWrite := -1
	for i, call := range calls {
		if call.OK && nudgeWriteTools[call.Name] {
			lastWrite = i
		}
	}
	if lastWrite < 0 {
		return "", false
	}
	for _, call := range calls[lastWrite+1:] {
		if call.Name == nudgeCheckTool {
			return "", false
		}
	}
	target := "文件"
	if targets := calls[lastWrite].Targets; len(targets) > 0 {
		target = strings.Join(targets, ", ")
	}
	return target, true
}

// openPlanSteps names the plan steps that are not completed yet.
func openPlanSteps(plan *native.Plan) []string {
	if plan == nil {
		return nil
	}
	var open []string
	for _, step := range plan.Steps {
		switch step.Status {
		case native.PlanPending, native.PlanInProgress:
			open = append(open, nudgeOneLine(step.Step))
		}
	}
	return open
}

// promiseEvidence is N2's evidence when the answer's end promises more work:
// the last paragraph matches a continuation phrase and does not ask a question.
func promiseEvidence(finalText string) string {
	tail := nudgeTail(finalText)
	if tail == "" || nudgeQuestionEnd(tail) {
		return ""
	}
	for _, pattern := range nudgePromisePatterns {
		loc := pattern.FindStringIndex(tail)
		if loc == nil {
			continue
		}
		return "结尾说" + truncateRunes(nudgeOneLine(tail[loc[0]:]), 120)
	}
	return ""
}

// nudgeTail is the end of an answer a promise is searched in: at most
// nudgeTailChars runes and only its last paragraph - a promise followed by a
// summary is not where the turn stopped.
func nudgeTail(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > nudgeTailChars {
		runes = runes[len(runes)-nudgeTailChars:]
	}
	s := string(runes)
	if i := strings.LastIndex(s, "\n\n"); i >= 0 {
		s = s[i+2:]
	}
	return strings.TrimSpace(s)
}

// nudgeQuestionEnd reports an answer that ends by asking (中文/英文问号, or 吗/呢):
// whether to continue is then the person's decision.
func nudgeQuestionEnd(tail string) bool {
	t := strings.TrimRight(tail, " \t\r\n。.…~～\"'”’)）]】")
	if t == "" {
		return false
	}
	if strings.HasSuffix(t, "?") || strings.HasSuffix(t, "？") || strings.HasSuffix(t, "吗") || strings.HasSuffix(t, "呢") {
		return true
	}
	return false
}

// NudgeFindings gathers this turn's evidence from the store and evaluates the
// rules. It returns nil when agents.nudge is off or the turn must not be checked
// (a failed, cancelled, yielded or wrapped-up turn).
func (r *Runner) NudgeFindings(ctx context.Context, sessionID string, result RunResult) []NudgeFinding {
	on, err := axconfig.ReadNudgeSetting()
	if err != nil {
		slog.Warn("read agents.nudge", "err", err)
		return nil
	}
	if !on {
		return nil
	}
	if result.State != RunCompleted || result.Status != agentcore.RunCompleted || result.WrapUp != "" {
		return nil
	}
	return CheckNudge(r.turnToolCalls(ctx, sessionID, result.TurnID), latestPlanModel(sessionID, r.latestPlan(ctx, sessionID)), result.Output, true)
}

// latestPlanModel decodes the session's latest plan entry; nil when the session
// never made one. The entry JSON itself is read by latestPlan (segment_handoff.go).
func latestPlanModel(sessionID, raw string) *native.Plan {
	if raw == "" {
		return nil
	}
	var plan native.Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		slog.Warn("decode session plan", "session_id", sessionID, "err", err)
		return nil
	}
	return &plan
}

// turnToolCalls reads the tool events of a turn as the rule sees them.
func (r *Runner) turnToolCalls(ctx context.Context, sessionID, turnID string) []NudgeToolCall {
	events, err := r.cfg.Repository.ListEvents(ctx, store.EventFilter{
		SessionID: sessionID, TurnID: turnID, Kinds: []string{store.EventTool},
	})
	if err != nil {
		slog.Warn("read turn tool events", "session_id", sessionID, "turn_id", turnID, "err", err)
		return nil
	}
	calls := make([]NudgeToolCall, 0, len(events))
	for _, event := range events {
		calls = append(calls, NudgeToolCall{Name: event.Name, OK: event.Status == eventOK, Targets: eventTargets(event)})
	}
	return calls
}

// eventTargets reads a tool event's recorded targets (file tools, see addCallAttrs).
func eventTargets(event store.Event) []string {
	raw, ok := event.Attrs["target"]
	if !ok {
		return nil
	}
	var targets []string
	switch list := raw.(type) {
	case []any:
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				targets = append(targets, s)
			}
		}
	case []string:
		targets = list
	}
	return targets
}

// nudgeRecorder writes the nudge trace event; *Runner implements it.
type nudgeRecorder interface {
	RecordEvent(ctx context.Context, sessionID string, ev HostEvent)
}

// RecordNudge writes the "nudge" trace event of one finding: attrs rule (N1/N2),
// action (pushed/hinted/recorded) and the evidence summary.
func RecordNudge(ctx context.Context, rec nudgeRecorder, sessionID, action string, finding NudgeFinding) {
	rec.RecordEvent(ctx, sessionID, HostEvent{
		Kind: store.EventNudge, Name: "turn", Status: "ok",
		Attrs: map[string]any{"rule": finding.Rule, "action": action, "evidence": finding.Evidence},
	})
}
