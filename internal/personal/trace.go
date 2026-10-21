package personal

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// traceEvent is the jsonl shape of one timeline event.
type traceEvent struct {
	Time         string         `json:"time"`
	Kind         string         `json:"kind"`
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	TurnID       string         `json:"turn_id"`
	DurationMs   int64          `json:"duration_ms"`
	TTFTMs       int64          `json:"ttft_ms,omitempty"`
	InputTokens  int64          `json:"input_tokens,omitempty"`
	OutputTokens int64          `json:"output_tokens,omitempty"`
	CacheRead    int64          `json:"cache_read_tokens,omitempty"`
	CacheWrite   int64          `json:"cache_write_tokens,omitempty"`
	Error        string         `json:"error,omitempty"`
	Attrs        map[string]any `json:"attrs,omitempty"`
}

// tracePlan is the jsonl shape of one update_plan record in the timeline.
type tracePlan struct {
	Time        string     `json:"time"`
	Kind        string     `json:"kind"`
	TurnID      string     `json:"turn_id"`
	Explanation string     `json:"explanation,omitempty"`
	Plan        []planStep `json:"plan"`
}

type planStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

// planSymbol maps a plan step status to its marker.
func planSymbol(status string) string {
	switch status {
	case native.PlanCompleted:
		return "✔"
	case native.PlanInProgress:
		return "▶"
	default:
		return "○"
	}
}

// traceRow is one timeline item: an event or a plan entry, ordered by time.
type traceRow struct {
	at    time.Time
	event *store.Event
	plan  *store.Entry
}

// mergeTrace interleaves events (already oldest first) with plan entries
// (ordered by seq, so also by creation time) by timestamp. On equal times the
// event goes first, so a plan update follows the tool event that produced it.
func mergeTrace(events []store.Event, plans []store.Entry) []traceRow {
	rows := make([]traceRow, 0, len(events)+len(plans))
	i, j := 0, 0
	for i < len(events) || j < len(plans) {
		if j >= len(plans) || (i < len(events) && !plans[j].CreatedAt.Before(events[i].StartedAt)) {
			rows = append(rows, traceRow{at: events[i].StartedAt, event: &events[i]})
			i++
			continue
		}
		rows = append(rows, traceRow{at: plans[j].CreatedAt, plan: &plans[j]})
		j++
	}
	return rows
}

// WriteTrace prints events and plan entries (oldest first) as a timeline: text
// mode is one line per event (and one per plan step), asJSON writes one JSON
// object per line (kind "plan" for a plan entry).
func WriteTrace(w io.Writer, events []store.Event, plans []store.Entry, asJSON bool) error {
	enc := json.NewEncoder(w)
	for _, row := range mergeTrace(events, plans) {
		if row.plan != nil {
			if err := writePlanRow(w, enc, *row.plan, asJSON); err != nil {
				return err
			}
			continue
		}
		e := *row.event
		if asJSON {
			if err := enc.Encode(traceEvent{
				Time: e.StartedAt.UTC().Format(time.RFC3339Nano), Kind: e.Kind, Name: e.Name, Status: e.Status,
				TurnID: e.TurnID, DurationMs: e.DurationMs, TTFTMs: e.TTFTMs,
				InputTokens: e.InputTokens, OutputTokens: e.OutputTokens,
				CacheRead: e.CacheReadTokens, CacheWrite: e.CacheWriteTokens, Error: e.Error, Attrs: e.Attrs,
			}); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(w, traceLine(e)); err != nil {
			return err
		}
	}
	return nil
}

// writePlanRow renders one plan entry; an entry that does not decode is skipped.
func writePlanRow(w io.Writer, enc *json.Encoder, e store.Entry, asJSON bool) error {
	var plan native.Plan
	if err := json.Unmarshal(e.JSON, &plan); err != nil {
		return nil
	}
	if asJSON {
		steps := make([]planStep, len(plan.Steps))
		for i, st := range plan.Steps {
			steps[i] = planStep{Step: st.Step, Status: st.Status}
		}
		return enc.Encode(tracePlan{
			Time: e.CreatedAt.UTC().Format(time.RFC3339Nano), Kind: "plan", TurnID: e.TurnID,
			Explanation: plan.Explanation, Plan: steps,
		})
	}
	at := e.CreatedAt.Local().Format("2006-01-02T15:04:05.000")
	for _, st := range plan.Steps {
		line := fmt.Sprintf("%s plan %s %s", at, planSymbol(st.Status), st.Step)
		if e.TurnID != "" {
			line += " turn=" + e.TurnID
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// traceLine renders: time kind name status duration [ttft] [tokens] [error] turn.
func traceLine(e store.Event) string {
	if e.Kind == store.EventTurnContext {
		return turnContextLine(e)
	}
	parts := []string{
		e.StartedAt.Local().Format("2006-01-02T15:04:05.000"), e.Kind, e.Name, e.Status,
		fmt.Sprintf("%dms", e.DurationMs),
	}
	if e.TTFTMs > 0 {
		parts = append(parts, fmt.Sprintf("ttft=%dms", e.TTFTMs))
	}
	if e.InputTokens > 0 || e.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("in=%d out=%d", e.InputTokens, e.OutputTokens))
		if e.CacheReadTokens > 0 {
			parts = append(parts, fmt.Sprintf("cached=%d", e.CacheReadTokens))
		}
	}
	// A request that broke the previous one's prefix: why, and how much of
	// the earlier content (JSON chars) had to be sent again.
	if reason, _ := e.Attrs["prefix"].(string); reason != "" && reason != prefixAppend {
		parts = append(parts, fmt.Sprintf("prefix=%s(rebuilt=%s)", reason, human(int64(attrInt(e.Attrs["prefix_rebuilt"])))))
	}
	if n, ok := e.Attrs["attempt"]; ok {
		parts = append(parts, fmt.Sprintf("attempt=%v", n))
	}
	// A sub-agent's own tool calls are rendered with its other numbers (see
	// agentTraceParts), not here.
	if n, ok := e.Attrs["tool_calls"]; ok && fmt.Sprint(n) != "0" && !agentEvent(e) {
		parts = append(parts, fmt.Sprintf("calls=%v", n))
	}
	if f, _ := e.Attrs["finish"].(string); f == "truncated" {
		parts = append(parts, "truncated")
		// The output limit that cut the reply off, when a request set one.
		if n := attrInt(e.Attrs["output_limit"]); n > 0 {
			parts = append(parts, fmt.Sprintf("limit=%d", n))
		}
	}
	if n := attrInt(e.Attrs["retries"]); n > 0 {
		parts = append(parts, fmt.Sprintf("retries=%d(%s, waited %.1fs)", n, e.Attrs["retry_reason"], float64(attrInt(e.Attrs["retry_wait_ms"]))/1000))
	}
	// What the pool did with this provider's failure: ask another model, or
	// return the error (llm.Attempt.Fallback).
	if decision, _ := e.Attrs["fallback"].(string); decision != "" {
		parts = append(parts, "fallback="+decision)
	}
	if e.Kind == store.EventBudget {
		parts = append(parts, budgetTraceParts(e)...)
	}
	if e.Kind == store.EventTool || e.Kind == store.EventDenial {
		parts = append(parts, toolTraceParts(e)...)
	}
	if e.Kind == store.EventTask {
		parts = append(parts, taskTraceParts(e)...)
	}
	if e.Kind == store.EventLocalCommand {
		parts = append(parts, localCommandTraceParts(e)...)
	}
	if e.Kind == store.EventVerify {
		parts = append(parts, verifyTraceParts(e)...)
	}
	if e.Kind == store.EventSession {
		parts = append(parts, sessionTraceParts(e)...)
	}
	if e.Kind == "approval_rule_persist" {
		for _, key := range []string{"file", "action", "result"} {
			if value, _ := e.Attrs[key].(string); value != "" {
				parts = append(parts, key+"="+value)
			}
		}
	}
	if e.Kind == store.EventContext {
		parts = append(parts, contextTraceParts(e))
	}
	if e.Kind == store.EventHook {
		if d, _ := e.Attrs["decision"].(string); d != "" {
			parts = append(parts, "decision="+d)
		}
		if n := attrInt(e.Attrs["additional_chars"]); n > 0 {
			parts = append(parts, fmt.Sprintf("context+=%d", n))
		}
		if n := attrInt(e.Attrs["reason_chars"]); n > 0 {
			parts = append(parts, fmt.Sprintf("reason=%d", n))
		}
	}
	if e.Kind == store.EventCompaction && e.Name == "compact" {
		parts = append(parts, segmentTraceParts(e)...)
	}
	if e.Kind == store.EventFold {
		// boundary = folded steps of steps; view chars before -> after folding.
		parts = append(parts, fmt.Sprintf("folded=%v/%v chars=%v->%v",
			e.Attrs["boundary"], e.Attrs["steps"], e.Attrs["chars_before"], e.Attrs["chars_after"]))
	}
	if e.Error != "" {
		msg := strings.Join(strings.Fields(e.Error), " ")
		if r := []rune(msg); len(r) > 120 {
			msg = string(r[:120]) + "..."
		}
		parts = append(parts, "err="+msg)
	}
	if e.TurnID != "" {
		parts = append(parts, "turn="+e.TurnID)
	}
	return strings.Join(parts, " ")
}

// turnContextLine renders a turn_context event briefly: time turn_context
// model mode tools tainted rules/memory hash prefixes turn.
func turnContextLine(e store.Event) string {
	attr := func(key string) string {
		if v, ok := e.Attrs[key]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	short := func(hash string) string {
		if hash == "" {
			return "-"
		}
		return hash[:min(len(hash), 8)]
	}
	parts := []string{
		e.StartedAt.Local().Format("2006-01-02T15:04:05.000"), e.Kind,
		"model=" + attr("provider") + "/" + attr("model"), "mode=" + attr("mode"),
		"tools=" + attr("tools_count") + "#" + short(attr("tools")), "tainted=" + attr("tainted"),
		"rules=" + short(attr("rules_hash")), "memory=" + short(attr("memory_hash")),
	}
	if v := attr("version"); v != "" {
		parts = append(parts, "suga="+v)
	}
	// The preferred tools the environment section listed this turn: names only.
	// Events read back from the store hold the names as a generic slice.
	if v, ok := e.Attrs["preferred_tools"]; ok {
		if names := strings.Join(strings.Fields(strings.Trim(fmt.Sprint(v), "[]")), ","); names != "" {
			parts = append(parts, "preferred="+names)
		}
	}
	if e.TurnID != "" {
		parts = append(parts, "turn="+e.TurnID)
	}
	return strings.Join(parts, " ")
}

// toolTraceParts renders what a tool call did: exit code (shell), the size
// that entered the context, a spill marker and the argument summary last. An
// agent call is rendered by its own parts.
func toolTraceParts(e store.Event) []string {
	if e.Name == "agent" {
		return append(agentTraceParts(e), toolArgPart(e)...)
	}
	var parts []string
	src, _ := e.Attrs["approval"].(string)
	rule, _ := e.Attrs["approval_rule"].(string)
	if src != "" || rule != "" {
		item := ""
		if rule != "" {
			if src == "always" {
				if scope, _ := e.Attrs["approval_scope"].(string); scope != "" {
					item = "approval=always(" + approvalScopeText(scope) + ")"
				}
			}
			if item == "" {
				item = "approval=rule(" + approvalRuleText(rule) + ")"
			}
		} else if src != "rule" {
			item = "approval=" + src
		}
		if item != "" {
			if ms := attrInt(e.Attrs["approval_wait_ms"]); ms > 0 {
				item += fmt.Sprintf("(wait=%.1fs)", float64(ms)/1000)
			}
			parts = append(parts, item)
		}
	}
	if d, _ := e.Attrs["danger"].(string); d != "" {
		parts = append(parts, fmt.Sprintf("danger=%q", d))
	}
	if snapshot, ok := e.Attrs["snapshot"].(map[string]any); ok {
		parts = append(parts, snapshotTracePart(snapshot))
	}
	if code, ok := e.Attrs["exit_code"]; ok {
		parts = append(parts, fmt.Sprintf("exit=%v", code))
	}
	if _, ok := e.Attrs["lines_added"]; ok {
		parts = append(parts, fmt.Sprintf("+%d/-%d", attrInt(e.Attrs["lines_added"]), attrInt(e.Attrs["lines_removed"])))
	}
	if n, ok := e.Attrs["inline_bytes"]; ok {
		parts = append(parts, fmt.Sprintf("bytes=%v", n))
	}
	if spilled, _ := e.Attrs["spilled"].(bool); spilled {
		parts = append(parts, fmt.Sprintf("spilled(raw=%v)", e.Attrs["raw_bytes"]))
	}
	if minimized, _ := e.Attrs["minimized"].(bool); minimized {
		parts = append(parts, fmt.Sprintf("tidied(raw=%v)", e.Attrs["raw_bytes"]))
	}
	return append(parts, toolArgPart(e)...)
}

// approvalRuleText turns Rule.Summary into compact trace notation while
// retaining the source and rule number that explain the decision.
func approvalRuleText(summary string) string {
	core := summary
	if i := strings.LastIndex(core, " action="); i >= 0 {
		core = core[:i]
	}
	i := strings.IndexByte(core, ' ')
	if i < 0 {
		return core
	}
	prefix, detail := core[:i], core[i+1:]
	for _, field := range []string{" command=", " path="} {
		if i := strings.Index(detail, field); i >= 0 {
			detail = detail[:i] + " " + detail[i+len(field):]
			break
		}
	}
	return prefix + ": " + detail
}

// approvalScopeText renders a session scope without exposing its internal
// separator as the human-facing trace explanation.
func approvalScopeText(scope string) string {
	if i := strings.IndexByte(scope, ':'); i >= 0 {
		return scope[:i] + fmt.Sprintf(" %q", scope[i+1:])
	}
	return scope
}

// snapshotTracePart renders the safe snapshot summary without exposing the
// before-image or any filesystem content.
func snapshotTracePart(snapshot map[string]any) string {
	part := fmt.Sprintf("snapshot=%v %v bytes=%v", snapshot["kind"], snapshot["status"], attrInt(snapshot["bytes"]))
	if reason, _ := snapshot["unrecoverable"].(string); reason != "" {
		part += " unrecoverable=" + reason
	}
	return part
}

// toolArgPart is the redacted argument summary of a tool call, rendered last
// and in brackets.
func toolArgPart(e store.Event) []string {
	if arg, ok := e.Attrs["arg"].(string); ok && arg != "" {
		return []string{"[" + arg + "]"}
	}
	return nil
}

// agentTraceParts renders a sub-agent call (the agent tool): the session it
// ran in, its type, the steps it took (with its own tool calls), the tokens it
// spent and why it stopped. A background call returns a task id at once: its
// start event has none of these (they are on the task's end event).
func agentTraceParts(e store.Event) []string {
	var parts []string
	if id, _ := e.Attrs["child_session_id"].(string); id != "" {
		parts = append(parts, "sub="+id)
	}
	if kind, _ := e.Attrs["agent_type"].(string); kind != "" {
		parts = append(parts, "type="+kind)
	}
	if _, ok := e.Attrs["steps"]; ok {
		parts = append(parts, fmt.Sprintf("steps=%d calls=%d", attrInt(e.Attrs["steps"]), attrInt(e.Attrs["tool_calls"])))
	}
	if in, out := attrInt(e.Attrs["input_tokens"]), attrInt(e.Attrs["output_tokens"]); in > 0 || out > 0 {
		item := "in=" + human(int64(in))
		if cached := attrInt(e.Attrs["cache_read_tokens"]); cached > 0 {
			item += "(cache " + human(int64(cached)) + ")"
		}
		parts = append(parts, item, "out="+human(int64(out)))
	}
	if cost := attrFloat(e.Attrs["cost_usd"]); cost > 0 {
		parts = append(parts, fmt.Sprintf("cost=$%.4f", cost))
	}
	if stop, _ := e.Attrs["stop"].(string); stop != "" {
		parts = append(parts, "stop="+stop)
	}
	return parts
}

// agentEvent reports whether e is a sub-agent call: the agent tool event or
// the task event of a background sub-agent. Both render their numbers as one
// group of parts, so the generic calls= part leaves them alone.
func agentEvent(e store.Event) bool {
	switch e.Kind {
	case store.EventTool:
		return e.Name == "agent"
	case store.EventTask:
		kind, _ := e.Attrs["kind"].(string)
		return kind == taskKindAgent
	}
	return false
}

// sessionTraceParts renders a session lifecycle event outside a turn: a move
// to another working directory shows from/to.
func sessionTraceParts(e store.Event) []string {
	if e.Name != sessionCwdChangedEvent && e.Name != "model_switch" {
		return nil
	}
	from, to := "-", "-"
	if v, ok := e.Attrs["from"].(string); ok && v != "" {
		from = v
	}
	if v, ok := e.Attrs["to"].(string); ok && v != "" {
		to = v
	}
	return []string{"from=" + from, "to=" + to}
}

// taskTraceParts renders what a background task ran (a task event spans the
// whole run of one task, its name is the task id): the command and the exit
// code, plus what the task's kind adds. A watch shows how often its command
// ran and how often the result changed, or its last state when no counters
// arrived; a Stop hook shows its summary ("Stop hook"), which names it, in
// place of the command bracket; an agent task shows the steps its sub-agent
// ran. Shell tasks and older events without a kind render as before.
func taskTraceParts(e store.Event) []string {
	kind, _ := e.Attrs["kind"].(string)
	arg, _ := e.Attrs["arg"].(string)
	switch kind {
	case taskKindAgent:
		parts := []string{"agent"}
		if _, ok := e.Attrs["steps"]; ok {
			parts = append(parts, fmt.Sprintf("steps=%d calls=%d", attrInt(e.Attrs["steps"]), attrInt(e.Attrs["tool_calls"])))
		}
		if code, ok := e.Attrs["exit_code"]; ok {
			parts = append(parts, fmt.Sprintf("exit=%v", code))
		}
		if arg != "" {
			parts = append(parts, "["+arg+"]")
		}
		return parts
	case taskKindWatch, taskKindHook:
		var parts []string
		switch {
		case kind == "watch":
			parts = append(parts, "watch")
			if _, ok := e.Attrs["runs"]; ok {
				parts = append(parts, fmt.Sprintf("runs=%d changes=%d",
					attrInt(e.Attrs["runs"]), attrInt(e.Attrs["changes"])))
			} else if p, _ := e.Attrs["progress"].(string); p != "" {
				parts = append(parts, "progress="+p)
			}
		case arg != "":
			parts = append(parts, arg)
		}
		if code, ok := e.Attrs["exit_code"]; ok {
			parts = append(parts, fmt.Sprintf("exit=%v", code))
		}
		if kind == "watch" && arg != "" {
			parts = append(parts, "["+arg+"]")
		}
		return parts
	default:
		return toolTraceParts(e)
	}
}

// localCommandTraceParts renders one local command a person typed at the chat
// prompt (!cmd / !!cmd): the exit code, the output bytes, whether the result
// goes to the model with the next message, how the run ended and the command
// last, in brackets.
func localCommandTraceParts(e store.Event) []string {
	var parts []string
	if code, ok := e.Attrs["exit_code"]; ok {
		parts = append(parts, fmt.Sprintf("exit=%v", code))
	}
	if n, ok := e.Attrs["bytes"]; ok {
		parts = append(parts, fmt.Sprintf("bytes=%v", n))
	}
	if sent, _ := e.Attrs["sent"].(bool); sent {
		parts = append(parts, "model=next-message")
	}
	if cancelled, _ := e.Attrs["cancelled"].(bool); cancelled {
		parts = append(parts, "cancelled")
	}
	if timedOut, _ := e.Attrs["timeout"].(bool); timedOut {
		parts = append(parts, "timeout")
	}
	if command, _ := e.Attrs["command"].(string); command != "" {
		parts = append(parts, "["+command+"]")
	}
	return parts
}

// verifyTraceParts renders one --verify check of a run: the round, whether it
// passed, the exit code, the output bytes and how long it ran.
func verifyTraceParts(e store.Event) []string {
	var parts []string
	if round, ok := e.Attrs["round"]; ok {
		parts = append(parts, fmt.Sprintf("round=%v", round))
	}
	if passed, _ := e.Attrs["passed"].(bool); passed {
		parts = append(parts, "passed")
	} else {
		parts = append(parts, "failed")
	}
	if code, ok := e.Attrs["exit"]; ok {
		parts = append(parts, fmt.Sprintf("exit=%v", code))
	}
	if n, ok := e.Attrs["output_len"]; ok {
		parts = append(parts, fmt.Sprintf("bytes=%v", n))
	}
	if timedOut, _ := e.Attrs["timeout"].(bool); timedOut {
		parts = append(parts, "timeout")
	}
	return parts
}

// segmentTraceParts renders a segment break (compaction "compact" event):
// segment number (the ordinal of its segment record), reason and the token
// estimate before -> after; missing attributes are left out (older events).
func segmentTraceParts(e store.Event) []string {
	var parts []string
	if n, ok := e.Attrs["segment"]; ok && fmt.Sprint(n) != "0" {
		parts = append(parts, fmt.Sprintf("segment=#%v", n))
	}
	if reason, ok := e.Attrs["reason"]; ok {
		parts = append(parts, fmt.Sprintf("reason=%v", reason))
	}
	before, okB := e.Attrs["tokens_before"]
	after, okA := e.Attrs["tokens_after"]
	switch {
	case okB && okA:
		parts = append(parts, fmt.Sprintf("tokens=%v->%v", before, after))
	case okB:
		parts = append(parts, fmt.Sprintf("tokens_before=%v", before))
	}
	return parts
}

// contextParts are the parts of a context event in the order they are sent.
var contextParts = []string{"system", "tools", "environment", "rules", "memory_index", "skills",
	"append_prompt", "deferred_tools", "handoff", "history", "hook_context", "input"}

// contextTraceParts renders a context event: the chars of each non-empty part
// (tools with their count), then the estimated tokens of the window.
func contextTraceParts(e store.Event) string {
	var parts []string
	for _, key := range contextParts {
		n := attrInt(e.Attrs[key])
		if n == 0 {
			continue
		}
		item := key + "=" + human(int64(n))
		if key == "tools" {
			item += fmt.Sprintf("(%d", attrInt(e.Attrs["tools_count"]))
			if mcp := attrInt(e.Attrs["tools_mcp"]); mcp > 0 {
				item += fmt.Sprintf(", %d mcp", mcp)
			}
			item += ")"
		}
		parts = append(parts, item)
	}
	out := "chars " + strings.Join(parts, " ")
	if est, window := attrInt(e.Attrs["est_tokens"]), attrInt(e.Attrs["window"]); est > 0 && window > 0 {
		out += fmt.Sprintf(" est=%s/%s tokens", human(int64(est)), human(int64(window)))
	}
	return out
}
