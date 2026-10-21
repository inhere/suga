package personal

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// 循环调用检测（loop_guard，tools.loop_guard）：模型在一个回合里反复以相同
// 参数调用同一工具（典型：同一文件反复 file_read、同一命令反复 shell_exec 得到
// 同样结果）时，在该次工具结果末尾追加一段提示，让它换方法或停下说明原因。
// 纯规则，不调用模型；触发时不拦截调用。
//
// 规则（与任务约定一致，写成常量）：
//   - R1 连续重复：本回合内连续 loopGuardRule1Calls 次调用同一工具且参数规范化后相同。
//   - R2 累计重复：本回合内同一工具、同一参数累计 loopGuardRule2Calls 次（不要求连续）。
//   - 同一（工具, 参数）每回合最多提示 loopGuardMaxHints 次（第 3 次及以后同组重复
//     不再追加，避免刷屏）。
//
// 计数按回合清零：hook 是 turnHooks 里按回合新建的实例，状态天然不跨回合。
const (
	loopGuardRule1Calls = 3 // R1：连续相同调用的触发次数
	loopGuardRule2Calls = 4 // R2：累计相同调用的触发次数
	loopGuardMaxHints   = 2 // 同一组重复每回合最多提示的次数
)

// loopGuardSeq separates the tool name from its canonical arguments in a
// loop-guard counter key, so a tool name can never merge with an argument.
const loopGuardSeq = "\x00"

// loopGuardExcluded are the tools that never count as a repeated call: they do
// not return information the model is polling for, and calling them repeatedly
// is normal work (update_plan rewrites the list, agent runs sub-tasks,
// session_message talks to other sessions).
var loopGuardExcluded = map[string]bool{"update_plan": true, "session_message": true, "agent": true}

// loopGuardHook watches the tool calls of one turn (a fresh instance per turn,
// see turnHooks) and appends the reminder text to the repeated call's result.
// It runs after toolEventHook in the hook chain, so chat and the other callers
// keep seeing the original result on their tool_result events; the runtime
// persists what the hook event carries (AfterTool mutations persist, see
// hooks_mutation_test.go), so the reminder only reaches the model's context.
type loopGuardHook struct {
	agentcore.NopHook
	sink      EmitFunc
	sessionID string
	turnID    string
	runID     string
	// record is the turn's event recorder (recorder.record via turnHooks),
	// best-effort like every event; nil disables tracing.
	record func(context.Context, store.Event)
	// counts counts every call of one (tool, canonical arguments) group this
	// turn; hints counts the reminders already appended for it.
	counts map[string]int
	hints  map[string]int
	// last and streak track the current run of consecutive identical calls.
	last   string
	streak int
}

// AfterTool counts the call and, on R1 or R2, appends the reminder to the
// result the runtime is about to persist. The hook never fails the turn: a
// reminder that cannot be recorded or shown is no reason to stop.
func (h *loopGuardHook) AfterTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event == nil || event.Call == nil || event.Result == nil || loopGuardExcluded[event.Call.Name] {
		return nil
	}
	key := event.Call.Name + loopGuardSeq + loopGuardArgs(event.Call.Arguments)
	if key == h.last {
		h.streak++
	} else {
		h.last, h.streak = key, 1
	}
	h.counts[key]++
	total := h.counts[key]
	rule, n := loopGuardRule(total, h.streak)
	if rule == "" || h.hints[key] >= loopGuardMaxHints {
		return nil
	}
	h.hints[key]++
	hint := loopGuardHint(event.Call.Name, n)
	event.Result.Content = appendLoopGuardHint(event.Result.Content, hint)
	h.trace(ctx, event.Call.Name, rule, n)
	h.show(ctx, event.Call.Name, n)
	return nil
}

// loopGuardRule reports the rule a call triggers ("" = none) together with the
// count to mention: R2 counts first, so a call that is both the 4th of a group
// and part of a run of three is reported as the cumulative rule.
func loopGuardRule(total, streak int) (string, int) {
	switch {
	case total >= loopGuardRule2Calls:
		return "R2", total
	case streak >= loopGuardRule1Calls:
		return "R1", streak
	default:
		return "", 0
	}
}

// loopGuardHint is the text appended to the repeated call's result (one to two
// lines) and the wording of chat's gray loop_guard line.
func loopGuardHint(tool string, n int) string {
	return fmt.Sprintf("↻ 这是本回合第 %d 次以相同参数调用 %s，得到的结果与上次相同。请检查结果是否真的有变化，换一种方法；若确实无变化，请停下并说明原因。", n, tool)
}

// appendLoopGuardHint appends hint at the end of a tool result, separated from
// the original text by a blank line so the model tells the two apart.
func appendLoopGuardHint(content, hint string) string {
	if content == "" {
		return hint
	}
	return content + "\n\n" + hint
}

// trace writes the loop_guard trace event (kind loop_guard, name = tool):
// attrs carry which rule fired and the count of the group, so `suga inspect
// trace` can see every reminder. Best-effort like every event.
func (h *loopGuardHook) trace(ctx context.Context, tool, rule string, n int) {
	if h.record == nil {
		return
	}
	h.record(ctx, store.Event{
		Kind: store.EventLoopGuard, Name: tool, Status: eventOK,
		Attrs: map[string]any{"rule": rule, "count": n},
	})
}

// show shows one gray line in chat (that the model was reminded) through the
// turn's event sink; an emit failure is only logged, like every other display
// event, and turn-less callers without a sink never call this.
func (h *loopGuardHook) show(ctx context.Context, tool string, n int) {
	if h.sink == nil {
		return
	}
	if err := emit(ctx, h.sink, Event{Kind: "loop_guard", ToolName: tool, SessionID: h.sessionID, TurnID: h.turnID, RunID: h.runID,
		Text: fmt.Sprintf("↻ 重复调用 %s ×%d，已提醒模型", tool, n)}); err != nil {
		turnLogger(ctx).Warn("emit loop_guard event", "tool", tool, "err", err)
	}
}

// loopGuardArgs builds the canonical form of a call's arguments for comparing
// calls of the same tool. It is the same comparison fold_supersede uses
// (reflect.DeepEqual on the decoded argument maps, same package); JSON
// encoding is its canonical serialization because encoding/json sorts map
// keys, and the arguments here are decoded from JSON (numbers as float64), so
// the encoding is stable. Arguments the provider could not decode never
// produce a call result, so undecodable forms do not need a key.
func loopGuardArgs(args map[string]any) string {
	data, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(data)
}
