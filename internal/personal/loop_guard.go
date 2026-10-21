package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// 循环调用检测（loop_guard，tools.loop_guard）：模型在一个回合里反复执行同一个
// 动作（典型：同一文件反复 file_read 拿到同样内容、同一条命令反复 shell_exec
// 得到同样结果或一直失败）时，在该次工具结果末尾追加一段提示，让它换方法或停下
// 说明原因。纯规则，不调用模型；触发时不拦截调用。
//
// 规则（与任务约定一致，写成常量）：
//   - R1 连续重复：本回合内连续 loopGuardRule1Calls 次调用的键完全相同（工具、
//     规范化参数、【结果摘要】都一样）。
//   - R2 累计重复：本回合内同一个键累计 loopGuardRule2Calls 次（不要求连续）。
//   - R3 反复失败：同一条 shell 命令（工具、规范化参数相同，不看输出）在本回合
//     内失败 loopGuardRule3Fails 次。
//   - 编辑重置：一次成功的写入类工具调用（file_edit/file_write/file_remove）之后
//     工作区变了，R1/R2 的计数清零——改完再跑同一条命令是正常迭代，不是死循环。
//     R3 的失败计数不重置：中间改了还失败，才是真正的死路。
//   - 同一组（工具, 参数）每回合最多提示 loopGuardMaxHints 次，避免刷屏。
//
// 计数按回合清零：hook 是 turnHooks 里按回合新建的实例，状态天然不跨回合。
const (
	loopGuardRule1Calls = 3 // R1：连续相同调用（含结果）的触发次数
	loopGuardRule2Calls = 4 // R2：累计相同调用（含结果）的触发次数
	loopGuardRule3Fails = 3 // R3：同一条 shell 命令失败的触发次数
	loopGuardMaxHints   = 2 // 同一组重复每回合最多提示的次数
)

// loopGuardShellTool is the one tool R3 watches: a command that keeps failing
// is a dead end even when an edit came in between.
const loopGuardShellTool = "shell_exec"

// loopGuardWriteTools are the tools whose successful call changes the
// workspace; after one, repeating a call is legitimate work again, so R1/R2
// start over. This build has no apply_patch-style tool (see
// pkg/agentcore/tools/native/catalog.go), so the list matches fold_supersede's.
var loopGuardWriteTools = map[string]bool{"file_edit": true, "file_write": true, "file_remove": true}

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
	// counts counts every call of one key this turn (tool, canonical arguments
	// and the result summary, see loopGuardKey); hints counts the reminders
	// already appended for one group (tool and arguments).
	counts map[string]int
	hints  map[string]int
	// last and streak track the current run of consecutive identical keys.
	last   string
	streak int
	// fails counts the failed calls of one shell command (tool and canonical
	// arguments, without the result) for R3.
	fails map[string]int
}

// AfterTool counts the call and, on R1 or R2, appends the reminder to the
// result the runtime is about to persist. The hook never fails the turn: a
// reminder that cannot be recorded or shown is no reason to stop.
func (h *loopGuardHook) AfterTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event == nil || event.Call == nil || event.Result == nil || loopGuardExcluded[event.Call.Name] {
		return nil
	}
	h.ensureMaps()
	group := event.Call.Name + loopGuardSeq + loopGuardArgs(event.Call.Arguments)
	failed := loopGuardFailed(event.Result)
	// 一次成功的写入调用改变了工作区：之后重跑同一条调用是合理的工作方式
	//（改代码 → go test → 再改 → 再 go test），所以 R1/R2 的计数从这里重新算。
	if loopGuardWriteTools[event.Call.Name] && !failed {
		h.counts = map[string]int{}
		h.last, h.streak = "", 0
	}
	key := group + loopGuardSeq + loopGuardResultKey(event.Result, failed)
	if key == h.last {
		h.streak++
	} else {
		h.last, h.streak = key, 1
	}
	h.counts[key]++
	total := h.counts[key]
	if event.Call.Name == loopGuardShellTool && failed {
		h.fails[group]++
	}
	rule, n, hintKey := "", 0, ""
	switch {
	case event.Call.Name == loopGuardShellTool && h.fails[group] >= loopGuardRule3Fails:
		// R3 优先：一条反复失败的命令比“结果没变”更值得提醒（失败命令的输出
		// 往往每次都一样，否则 R1 会先抢到这一次）。
		rule, n, hintKey = "R3", h.fails[group], "R3"+loopGuardSeq+group
	default:
		rule, n = loopGuardRule(total, h.streak)
		hintKey = group
	}
	if rule == "" || h.hints[hintKey] >= loopGuardMaxHints {
		return nil
	}
	h.hints[hintKey]++
	hint := loopGuardHint(rule, event.Call.Name, n)
	event.Result.Content = appendLoopGuardHint(event.Result.Content, hint)
	h.trace(ctx, event.Call.Name, rule, n)
	h.show(ctx, event.Call.Name, rule, n)
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
// lines) and the wording of chat's gray loop_guard line. Each rule describes
// what actually happened: R1/R2 know the arguments and the result repeated,
// R3 knows the command failed again.
func loopGuardHint(rule, tool string, n int) string {
	if rule == "R3" {
		return fmt.Sprintf("↻ 这条命令在本回合已失败 %d 次（%s），换一种方法或停下说明卡在哪里。", n, tool)
	}
	return fmt.Sprintf("↻ 这是本回合第 %d 次相同调用且结果没有变化（%s），请换一种方法；若确实没有变化，请停下说明原因。", n, tool)
}

// ensureMaps initializes the counters, so a hook built without a constructor
// still counts instead of panicking on a nil map.
func (h *loopGuardHook) ensureMaps() {
	if h.counts == nil {
		h.counts = map[string]int{}
	}
	if h.hints == nil {
		h.hints = map[string]int{}
	}
	if h.fails == nil {
		h.fails = map[string]int{}
	}
}

// loopGuardResultKey is the result part of a repetition key: whether the call
// failed plus a hash of the result text before any reminder was appended. A
// call whose result changed is not a repeat (a new answer, a different failure),
// which is what keeps a real edit/test/fix cycle out of the guard.
func loopGuardResultKey(res *agentcore.ToolResult, failed bool) string {
	flag := "ok"
	if failed {
		flag = "fail"
	}
	sum := sha256.Sum256([]byte(res.Content))
	return flag + ":" + hex.EncodeToString(sum[:8])
}

// loopGuardFailed reports whether a call failed. IsError is the general signal;
// shell_exec does not set it on a non-zero exit, so its exit code is read the
// way the tool reports it (Metadata exit_code, or the "[exit code: N]" trailer
// native/code_exec.go appends to the output).
func loopGuardFailed(res *agentcore.ToolResult) bool {
	if res.IsError {
		return true
	}
	if code, ok := intArg(res.Metadata["exit_code"]); ok {
		return code != 0
	}
	if m := exitCodePattern.FindAllStringSubmatch(res.Content, -1); len(m) > 0 {
		code, err := strconv.Atoi(m[len(m)-1][1])
		return err == nil && code != 0
	}
	return false
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
func (h *loopGuardHook) show(ctx context.Context, tool, rule string, n int) {
	if h.sink == nil {
		return
	}
	text := fmt.Sprintf("↻ 重复调用 %s ×%d，已提醒模型", tool, n)
	if rule == "R3" {
		text = fmt.Sprintf("↻ %s 已失败 ×%d，已提醒模型", tool, n)
	}
	if err := emit(ctx, h.sink, Event{Kind: "loop_guard", ToolName: tool, SessionID: h.sessionID, TurnID: h.turnID, RunID: h.runID,
		Text: text}); err != nil {
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
