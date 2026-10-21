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

// The rule names a finding and the trace event carry; the kinds of those rules
// are nudge_rules.go's NudgeKind* constants.
const (
	// The actions a host reports for a finding.
	NudgeActionPushed   = "pushed"
	NudgeActionHinted   = "hinted"
	NudgeActionRecorded = "recorded"
)

// nudgeTailChars is how much of the end of an answer is searched for a promise
// (about 300 characters, counted in runes).
const nudgeTailChars = 300

// nudgeWriteTools and nudgeCheckTool (the built-in rule defaults) live in
// nudge_rules.go now: the rules carry them per rule.

// NudgeToolCall is one tool call of the turn, in execution order.
type NudgeToolCall struct {
	Name string
	// OK is false for a refused or failed call: only a call that went through
	// counts as "the workspace changed" or "the change was checked".
	OK bool
	// Targets are the files or directories the call was about (trace attr target).
	Targets []string
	// Command is the command a check tool ran (trace attr command, recorded for
	// shell calls), so a write rule's check_command can match it. Empty when
	// the call was not a shell command.
	Command string
}

// NudgeFinding is one hit rule with the evidence the model (or the trace) is
// told about.
type NudgeFinding struct {
	Rule string
	// Kind is the rule's kind (NudgeKind*), so hosts treat every rule kind the
	// same without knowing rule names.
	Kind     string
	Evidence string
}

// CheckNudge evaluates the rules over one finished turn and returns the
// findings in rule order. rules are the enabled ones (LoadNudgeRules);
// completed says the turn ended with a plain-text answer (the final assistant
// message carried no tool call); plan is the session's latest plan, nil when
// the session never made one.
func CheckNudge(rules []NudgeRule, calls []NudgeToolCall, plan *native.Plan, finalText string, completed bool) []NudgeFinding {
	var findings []NudgeFinding
	for i := range rules {
		rule := &rules[i]
		var evidence string
		switch rule.Kind {
		case NudgeKindUnverifiedWrite:
			evidence = unverifiedWriteEvidence(rule, calls)
		case NudgeKindPlanOpen:
			if open := openPlanSteps(plan); len(open) > 0 {
				evidence = fmt.Sprintf("计划还有 %d 项未完成：%s", len(open), strings.Join(open, "；"))
			}
		case NudgeKindPromise:
			if completed {
				evidence = promiseEvidence(rule.patterns, finalText)
			}
		}
		if evidence != "" {
			findings = append(findings, NudgeFinding{Rule: rule.Name, Kind: rule.Kind, Evidence: evidence})
		}
	}
	return findings
}

// unverifiedWriteEvidence is a write rule's evidence, or "" when the last
// successful write was followed by a check: a call of the rule's check tool
// whose command matches its check_command (the old N1's first condition).
func unverifiedWriteEvidence(rule *NudgeRule, calls []NudgeToolCall) string {
	target, ok := uncheckedWrite(rule, calls)
	if !ok {
		return ""
	}
	return fmt.Sprintf("最后修改 %s 之后没有运行 %s", target, defaultCheckTool)
}

// nudgeOneLine collapses text to one line, so a plan step name or the quoted
// answer cannot break the one-line evidence a hint or a stderr line prints.
func nudgeOneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// uncheckedWrite reports the target of the last successful write tool when no
// check followed it: a call of the rule's check tool whose command matches its
// check_command (any command does when the rule names none).
func uncheckedWrite(rule *NudgeRule, calls []NudgeToolCall) (string, bool) {
	lastWrite := -1
	for i, call := range calls {
		if call.OK && rule.writeTools[call.Name] {
			lastWrite = i
		}
	}
	if lastWrite < 0 {
		return "", false
	}
	for _, call := range calls[lastWrite+1:] {
		if !rule.checkTools[call.Name] {
			continue
		}
		if rule.checkCmd != nil && !rule.checkCmd.MatchString(call.Command) {
			continue
		}
		return "", false
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

// promiseEvidence is a promise rule's evidence when the answer's end promises
// more work: the last paragraph matches one of the rule's patterns and does
// not ask a question.
func promiseEvidence(patterns []*regexp.Regexp, finalText string) string {
	tail := nudgeTail(finalText)
	if tail == "" || nudgeQuestionEnd(tail) {
		return ""
	}
	for _, pattern := range patterns {
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
	rules := r.cfg.NudgeRules
	if rules == nil {
		rules = builtinNudgeRuleFunc()
	}
	return CheckNudge(rules(), r.turnToolCalls(ctx, sessionID, result.TurnID), latestPlanModel(sessionID, r.latestPlan(ctx, sessionID)), result.Output, true)
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
		calls = append(calls, NudgeToolCall{Name: event.Name, OK: event.Status == eventOK,
			Targets: eventTargets(event), Command: eventCommand(event)})
	}
	return calls
}

// eventCommand reads a tool event's recorded command (shell calls, see
// addCallAttrs), so a write rule's check_command can match it.
func eventCommand(event store.Event) string {
	command, _ := event.Attrs["command"].(string)
	return command
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

// RecordNudge writes the "nudge" trace event of one finding: attrs rule (the
// rule name), kind (its kind), action (pushed/hinted/recorded) and the evidence
// summary.
func RecordNudge(ctx context.Context, rec nudgeRecorder, sessionID, action string, finding NudgeFinding) {
	rec.RecordEvent(ctx, sessionID, HostEvent{
		Kind: store.EventNudge, Name: "turn", Status: "ok",
		Attrs: map[string]any{"rule": finding.Rule, "kind": finding.Kind, "action": action, "evidence": finding.Evidence},
	})
}
