package personal

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// loopStep is one tool call of a turn, replayed through the hook by hand.
type loopStep struct {
	name string
	args map[string]any
	// content is what the tool produced before the hook saw it.
	content string
	// isError marks the result as a tool error (agentcore.ToolResult.IsError).
	isError bool
}

// runLoopHint is the reminder R1/R2 append for file_read; shellLoopHint is R3's;
// prefixLoopHint is R4's.
func runLoopHint(n int) string { return loopGuardHint("R1", "file_read", n, "") }

func shellLoopHint(n int) string { return loopGuardHint("R3", "shell_exec", n, "") }

func prefixLoopHint(n int, prefix string) string { return loopGuardHint("R4", "file_read", n, prefix) }

// runLoopStepsHook feeds steps (one turn's calls, in order) through the hook
// and returns the result content the events end up carrying.
func runLoopStepsHook(t *testing.T, hook *loopGuardHook, stepList []loopStep) []string {
	t.Helper()
	var got []string
	for _, step := range stepList {
		event := &agentcore.ToolEvent{
			Call:   &agentcore.ToolCall{CallID: "c-h", Name: step.name, Arguments: step.args},
			Result: &agentcore.ToolResult{Name: step.name, Content: step.content, IsError: step.isError},
		}
		assert.NoErr(t, hook.AfterTool(context.Background(), event, agentcore.NewControl()))
		got = append(got, event.Result.Content)
	}
	return got
}

// newLoopGuardHook builds the hook as wired: fresh per-turn state.
func newLoopGuardHook(record func(context.Context, store.Event)) *loopGuardHook {
	return &loopGuardHook{record: record, counts: map[string]int{}, hints: map[string]int{}, fails: map[string]int{}}
}

// TestLoopGuardHookRules covers the rule boundaries of the counters. Each step
// of the table carries what that call's result should say afterwards: "" for
// no reminder, or the hint text for one.
func TestLoopGuardHookRules(t *testing.T) {
	readA := loopStep{name: "file_read", args: map[string]any{"path": "a.go"}, content: "r"}
	readB := loopStep{name: "file_read", args: map[string]any{"path": "b.go"}, content: "r"}
	readErr := loopStep{name: "file_read", args: map[string]any{"path": "a.go"}, content: "r", isError: true}
	plan := loopStep{name: "update_plan", args: map[string]any{"plan": []any{}}, content: "ok"}
	edit := loopStep{name: "file_edit", args: map[string]any{"path": "a.go"}, content: "ok: edited a.go"}
	write := loopStep{name: "file_write", args: map[string]any{"path": "a.go"}, content: "ok: wrote a.go"}
	shellArgs := map[string]any{"command": "go test ./..."}
	shellOK := loopStep{name: "shell_exec", args: shellArgs, content: "ok\n[exit code: 0]"}
	shellFail := loopStep{name: "shell_exec", args: shellArgs, content: "FAIL\n[exit code: 1]"}
	steps := func(all ...loopStep) []loopStep { return all }

	for _, tc := range []struct {
		name     string
		stepList []loopStep
		want     []string // one entry per step
		// events is how many loop_guard trace events the turn records.
		events int
		// lastRule/lastCount are the attrs of the last recorded event.
		lastRule  string
		lastCount int
	}{
		{name: "两次连续相同不触发", stepList: steps(readA, readA),
			want: []string{"", ""}, events: 0},
		{name: "第三次连续相同触发 R1",
			stepList: steps(readA, readA, readA),
			want:     []string{"", "", runLoopHint(3)}, events: 1,
			lastRule: "R1", lastCount: 3},
		{name: "参数不同不算重复",
			stepList: steps(readA, readB, readA, readB),
			want:     []string{"", "", "", ""}, events: 0},
		{name: "结果不同不算重复",
			// 同一工具、同一参数，但每次结果都在变（改了代码再跑）：不是死路。
			stepList: steps(loopStep{name: "shell_exec", args: shellArgs, content: "a\n[exit code: 0]"},
				loopStep{name: "shell_exec", args: shellArgs, content: "b\n[exit code: 0]"},
				loopStep{name: "shell_exec", args: shellArgs, content: "c\n[exit code: 0]"},
				loopStep{name: "shell_exec", args: shellArgs, content: "d\n[exit code: 0]"}),
			want: []string{"", "", "", ""}, events: 0},
		{name: "IsError 区分：正文相同也不算同一结果",
			stepList: steps(readA, readErr, readA, readErr),
			want:     []string{"", "", "", ""}, events: 0},
		{name: "非连续累计四次触发 R2",
			stepList: steps(readA, readA, readB, readA, readA),
			want:     []string{"", "", "", "", runLoopHint(4)}, events: 1,
			lastRule: "R2", lastCount: 4},
		{name: "排除的工具不计数",
			stepList: steps(plan, plan, plan, plan),
			want:     []string{"", "", "", ""}, events: 0},
		{name: "同一组每回合最多提示两次",
			stepList: steps(readA, readA, readA, readA, readA, readA),
			want:     []string{"", "", runLoopHint(3), runLoopHint(4), "", ""},
			events:   2, lastRule: "R2", lastCount: 4},
		{name: "编辑后重跑不触发 R1/R2",
			// 读、读、改、读、读：改完工作区变了，同一条读不是重复死路。
			stepList: steps(readA, readA, edit, readA, readA),
			want:     []string{"", "", "", "", ""}, events: 0},
		{name: "写入后重跑命令不触发 R2",
			// 跑、跑、改、跑、跑：旧规则在第 4 次触发 R2，但中间改了代码，重跑合理。
			stepList: steps(shellOK, shellOK, write, shellOK, shellOK),
			want:     []string{"", "", "", "", ""}, events: 0},
		{name: "失败的写入不重置计数",
			// 编辑本身报错不代表工作区变了：同一条读仍算累计。
			stepList: steps(readA, readA, loopStep{name: "file_edit", args: map[string]any{"path": "a.go"}, content: "error: no match", isError: true}, readA, readA),
			want:     []string{"", "", "", "", runLoopHint(4)}, events: 1,
			lastRule: "R2", lastCount: 4},
		{name: "成功的重复命令（结果相同）仍触发 R1",
			stepList: steps(shellOK, shellOK, shellOK),
			want:     []string{"", "", loopGuardHint("R1", "shell_exec", 3, "")}, events: 1,
			lastRule: "R1", lastCount: 3},
		{name: "同一命令失败三次触发 R3",
			stepList: steps(shellFail, edit, shellFail, edit, shellFail),
			// 中间有编辑也计数：改了还失败才是死路。
			want: []string{"", "", "", "", shellLoopHint(3)}, events: 1,
			lastRule: "R3", lastCount: 3},
		{name: "R3 只看失败，不看输出",
			stepList: steps(shellFail,
				loopStep{name: "shell_exec", args: shellArgs, content: "other failure\n[exit code: 2]"},
				loopStep{name: "shell_exec", args: shellArgs, content: "third failure\n[exit code: 3]"}),
			want: []string{"", "", shellLoopHint(3)}, events: 1,
			lastRule: "R3", lastCount: 3},
		{name: "失败计数只累加失败的那几次",
			stepList: steps(shellFail, shellOK, shellFail, shellFail),
			want:     []string{"", "", "", shellLoopHint(3)}, events: 1,
			lastRule: "R3", lastCount: 3},
		{name: "R3 每回合最多提示两次",
			stepList: steps(shellFail, shellFail, shellFail, shellFail, shellFail),
			want:     []string{"", "", shellLoopHint(3), shellLoopHint(4), ""},
			events:   2, lastRule: "R3", lastCount: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []store.Event
			got := runLoopStepsHook(t, newLoopGuardHook(func(_ context.Context, e store.Event) {
				events = append(events, e)
			}), tc.stepList)
			assert.Require(t, assert.Len(t, got, len(tc.want)))
			for i, want := range tc.want {
				if want == "" {
					assert.StrNotContains(t, got[i], "换一种方法")
					continue
				}
				assert.StrContains(t, got[i], want)
			}
			assert.Eq(t, len(events), tc.events, "loop_guard trace events")
			if tc.events > 0 {
				assert.Eq(t, store.EventLoopGuard, events[len(events)-1].Kind)
				assert.Eq(t, events[len(events)-1].Name, tc.stepList[len(tc.stepList)-1].name)
				assert.Eq(t, tc.lastRule, events[len(events)-1].Attrs["rule"])
				assert.Eq(t, tc.lastCount, events[len(events)-1].Attrs["count"])
			}
		})
	}
}

// TestLoopGuardPrefixRule: a model that keeps failing with the same argument
// prefix (it appends a suffix to the path and retries) is reminded to confirm
// the value instead of guessing (session sg_31nv13n28jb8: 16 such file_read
// calls in one turn, none of them identical, so R1/R2 never fired).
func TestLoopGuardPrefixRule(t *testing.T) {
	step := func(suffix string, failed bool) loopStep {
		return loopStep{
			name:    "file_read",
			args:    map[string]any{"path": "deploy-hub/cmd/dhcli/internal/cmds/login/login_test.go; " + suffix},
			content: "r",
			isError: failed,
		}
	}
	_, prefix := loopGuardPrefixKey("file_read", step("end!", true).args)

	hook := newLoopGuardHook(nil)
	got := runLoopStepsHook(t, hook, []loopStep{step("stop!", true), step("why?", true), step("end!", true)})
	assert.Eq(t, "r", got[0])
	assert.Eq(t, "r", got[1])
	assert.Eq(t, "r\n\n"+prefixLoopHint(3, prefix), got[2])

	// A successful call clears the prefix, so the next failures start over.
	got = runLoopStepsHook(t, hook, []loopStep{step("fixed", false), step("a", true), step("b", true)})
	assert.NotContains(t, got[2], "参数前缀相同")

	// Another tool with the same prefix is a different group.
	other := loopStep{name: "file_symbols", args: step("c", true).args, content: "r", isError: true}
	got = runLoopStepsHook(t, newLoopGuardHook(nil), []loopStep{step("a", true), step("b", true), other})
	assert.NotContains(t, got[2], "参数前缀相同")
}

// TestLoopGuardHintWording: each rule's text says what the rule knows (the
// repeated arguments and an unchanged result, or a command that failed again).
func TestLoopGuardHintWording(t *testing.T) {
	t.Run("R1/R2 说结果没有变化", func(t *testing.T) {
		for _, rule := range []string{"R1", "R2"} {
			hint := loopGuardHint(rule, "file_read", 3, "")
			assert.StrContains(t, hint, "第 3 次相同调用且结果没有变化")
			assert.StrContains(t, hint, "换一种方法")
			assert.StrNotContains(t, hint, "已失败")
		}
	})
	t.Run("R3 说已失败几次", func(t *testing.T) {
		hint := loopGuardHint("R3", "shell_exec", 3, "")
		assert.StrContains(t, hint, "已失败 3 次")
		assert.StrContains(t, hint, "换一种方法")
		assert.StrNotContains(t, hint, "结果没有变化")
	})
}

// TestLoopGuardFailedResult covers the two ways a result says it failed: the
// IsError flag, and shell_exec's non-zero exit code (Metadata or the
// "[exit code: N]" trailer, since the tool leaves IsError unset).
type failedResultCase struct {
	name string
	res  *agentcore.ToolResult
	want bool
}

func TestLoopGuardFailedResult(t *testing.T) {
	for _, tc := range []failedResultCase{
		{name: "IsError", res: &agentcore.ToolResult{IsError: true}, want: true},
		{name: "退出码在 Metadata 里", res: &agentcore.ToolResult{Metadata: map[string]any{"exit_code": 2}}, want: true},
		{name: "退出码 0 不算失败", res: &agentcore.ToolResult{Metadata: map[string]any{"exit_code": 0}}, want: false},
		{name: "退出码在结尾的方括号里", res: &agentcore.ToolResult{Content: "FAIL\n[exit code: 1]"}, want: true},
		{name: "成功输出", res: &agentcore.ToolResult{Content: "ok\n[exit code: 0]"}, want: false},
		{name: "普通输出", res: &agentcore.ToolResult{Content: "r"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, loopGuardFailed(tc.res))
		})
	}
}

// TestLoopGuardHookFreshPerTurn: consecutive runs of the same calls hint again,
// because turnHooks builds a new hook per turn.
func TestLoopGuardHookFreshPerTurn(t *testing.T) {
	readA := loopStep{name: "file_read", args: map[string]any{"path": "a.go"}, content: "r"}
	first := runLoopStepsHook(t, newLoopGuardHook(nil), []loopStep{readA, readA, readA})
	assert.StrContains(t, first[2], "第 3 次", "first turn hints")

	second := runLoopStepsHook(t, newLoopGuardHook(nil), []loopStep{readA, readA, readA})
	assert.StrContains(t, second[2], "第 3 次", "second turn counts from zero again")
}

// loopTurnRunner builds a runner whose provider replays `calls` identical
// file_read steps and then answers, with one file_read tool in the registry.
func loopTurnRunner(t *testing.T, loopGuard bool, calls int) (*Runner, *loopProvider) {
	t.Helper()
	repo := openObserveRepo(t)
	prov := &loopProvider{calls: calls, args: map[string]any{"path": "a.go"}}
	reg := tools.NewRegistry()
	reg.Register(fnTool{name: "file_read", fn: func() (*agentcore.ToolResult, error) {
		return &agentcore.ToolResult{Content: "package a"}, nil
	}})
	runner, err := New(Config{Repository: repo, Provider: prov, Tools: reg, LoopGuard: loopGuard})
	assert.Require(t, assert.NoErr(t, err))
	return runner, prov
}

// loopProvider replays `calls` identical file_read steps and then answers.
type loopProvider struct {
	calls int
	args  map[string]any

	mu   sync.Mutex
	seq  int
	reqs []*agentcore.GenerateRequest
}

func (p *loopProvider) Name() string { return "loop" }

func (p *loopProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	i := p.seq
	p.seq++
	if i%(p.calls+1) < p.calls { // cycles: every turn repeats the same calls
		return callStep("c-loop", "file_read", p.args), nil
	}
	return &agentcore.GenerateResult{Content: "done", FinishReason: agentcore.FinishFinal}, nil
}

// TestLoopGuardTurnAppendsHint exercises the real path: the runner's hook chain
// runs in the runtime, so the reminder reaches the stored tool message and the
// trace, and chat sees its own gray loop_guard event.
func TestLoopGuardTurnAppendsHint(t *testing.T) {
	runner, prov := loopTurnRunner(t, true, 4)
	var shown []Event
	result, err := runner.Run(context.Background(), RunRequest{Prompt: "read it", Emit: func(_ context.Context, e Event) error {
		if e.Kind == "loop_guard" {
			shown = append(shown, e)
		}
		return nil
	}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, prov.seq, 5, "nothing intercepted: all 5 requests as scripted")

	messages, err := runner.cfg.Repository.AgentStorage().LoadMessages(context.Background(), result.TurnID)
	assert.Require(t, assert.NoErr(t, err))
	hints := 0
	for _, m := range messages {
		hints += strings.Count(m.Content, "换一种方法")
	}
	assert.True(t, hints >= 2, "R1 and R2 each left one reminder, got %d", hints)

	events := eventsOf(t, runner.cfg.Repository, result.SessionID, store.EventLoopGuard)
	assert.Require(t, assert.Len(t, events, 2))
	assert.Eq(t, "file_read", events[0].Name)
	assert.Eq(t, "R1", events[0].Attrs["rule"])
	assert.Eq(t, float64(3), events[0].Attrs["count"])
	assert.Eq(t, "R2", events[1].Attrs["rule"])
	assert.Eq(t, float64(4), events[1].Attrs["count"])

	assert.Require(t, assert.Len(t, shown, 2))
	assert.StrContains(t, shown[0].Text, "↻ 重复调用 file_read ×3，已提醒模型")
	assert.StrContains(t, shown[1].Text, "↻ 重复调用 file_read ×4，已提醒模型")
}

// TestLoopGuardCountsResetEveryTurn keeps calling in a fresh turn: the hook
// state starts over, so the same repeats get reminded again.
func TestLoopGuardCountsResetEveryTurn(t *testing.T) {
	runner, _ := loopTurnRunner(t, true, 4)
	first, err := runner.Run(context.Background(), RunRequest{Prompt: "read it"})
	assert.Require(t, assert.NoErr(t, err))
	second, err := runner.Run(context.Background(), RunRequest{SessionID: first.SessionID, Prompt: "read again"})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, second.TurnID != "" && second.TurnID != first.TurnID, "a separate turn")
	all := eventsOf(t, runner.cfg.Repository, first.SessionID, store.EventLoopGuard)
	assert.Require(t, assert.Len(t, all, 4), "two reminders in each turn")
	for _, turn := range []int{0, 1} { // each turn starts over: R1 at 3, R2 at 4
		turnEvents := all[turn*2 : turn*2+2]
		assert.Require(t, assert.Len(t, turnEvents, 2))
		assert.Eq(t, "R1", turnEvents[0].Attrs["rule"])
		assert.Eq(t, "R2", turnEvents[1].Attrs["rule"])
	}
}

// TestLoopGuardTurnOff: tools.loop_guard off leaves the results untouched and
// records nothing, while the turn itself runs as scripted.
func TestLoopGuardTurnOff(t *testing.T) {
	runner, prov := loopTurnRunner(t, false, 4)
	result, err := runner.Run(context.Background(), RunRequest{Prompt: "read it"})
	assert.Require(t, assert.NoErr(t, err))
	messages, err := runner.cfg.Repository.AgentStorage().LoadMessages(context.Background(), result.TurnID)
	assert.Require(t, assert.NoErr(t, err))
	for _, m := range messages {
		if m.Role == "tool" {
			assert.Eq(t, "package a", m.Content)
		}
	}
	events := eventsOf(t, runner.cfg.Repository, result.SessionID, store.EventLoopGuard)
	assert.Eq(t, 0, len(events))
	assert.Eq(t, prov.seq, 5)
}

// loopScriptRunner builds a runner whose provider replays steps (in order) and
// then answers, over a registry holding the tools the steps call.
func loopScriptRunner(t *testing.T, steps []*agentcore.GenerateResult, reg *tools.Registry) *Runner {
	t.Helper()
	runner, err := New(Config{Repository: openObserveRepo(t), Provider: &reqProv{steps: steps}, Tools: reg, LoopGuard: true})
	assert.Require(t, assert.NoErr(t, err))
	return runner
}

// failingShellTool answers like shell_exec does for a failing command: the code
// in Metadata and in the "[exit code: N]" trailer, IsError left unset.
func failingShellTool() fnTool {
	return fnTool{name: "shell_exec", fn: func() (*agentcore.ToolResult, error) {
		return &agentcore.ToolResult{Content: "FAIL\n[exit code: 1]", Metadata: map[string]any{"exit_code": 1}}, nil
	}}
}

// TestLoopGuardFailedCommandTurn exercises R3 on the real turn path: the same
// failing command three times (an edit in between) is reminded once, and its
// exit code reaches the trace.
func TestLoopGuardFailedCommandTurn(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(failingShellTool())
	reg.Register(fnTool{name: "file_edit", fn: func() (*agentcore.ToolResult, error) {
		return &agentcore.ToolResult{Content: "ok: edited a.go"}, nil
	}})
	cmd := map[string]any{"command": "go test ./..."}
	editArgs := map[string]any{"path": "a.go", "old_string": "a", "new_string": "b"}
	runner := loopScriptRunner(t, []*agentcore.GenerateResult{
		callStep("c1", "shell_exec", cmd), callStep("c2", "file_edit", editArgs),
		callStep("c3", "shell_exec", cmd), callStep("c4", "file_edit", editArgs),
		callStep("c5", "shell_exec", cmd),
	}, reg)
	var shown []Event
	result, err := runner.Run(context.Background(), RunRequest{Prompt: "fix it", Emit: func(_ context.Context, e Event) error {
		if e.Kind == "loop_guard" {
			shown = append(shown, e)
		}
		return nil
	}})
	assert.Require(t, assert.NoErr(t, err))

	events := eventsOf(t, runner.cfg.Repository, result.SessionID, store.EventLoopGuard)
	assert.Require(t, assert.Len(t, events, 1), "one R3 reminder for three failures")
	assert.Eq(t, "shell_exec", events[0].Name)
	assert.Eq(t, "R3", events[0].Attrs["rule"])
	assert.Eq(t, float64(3), events[0].Attrs["count"])
	assert.Require(t, assert.Len(t, shown, 1))
	assert.StrContains(t, shown[0].Text, "shell_exec 已失败 ×3")

	messages, err := runner.cfg.Repository.AgentStorage().LoadMessages(context.Background(), result.TurnID)
	assert.Require(t, assert.NoErr(t, err))
	hints := 0
	for _, m := range messages {
		hints += strings.Count(m.Content, "已失败 3 次")
	}
	assert.Eq(t, 1, hints, "the third failure's result carries the R3 hint")
}

// TestLoopGuardEditResetsRepeatTurn: the edit/test/edit/test cycle (same command,
// same output, a successful write in between) stays quiet on the real path.
func TestLoopGuardEditResetsRepeatTurn(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(fnTool{name: "shell_exec", fn: func() (*agentcore.ToolResult, error) {
		return &agentcore.ToolResult{Content: "ok\n[exit code: 0]", Metadata: map[string]any{"exit_code": 0}}, nil
	}})
	reg.Register(fnTool{name: "file_edit", fn: func() (*agentcore.ToolResult, error) {
		return &agentcore.ToolResult{Content: "ok: edited a.go"}, nil
	}})
	cmd := map[string]any{"command": "go test ./..."}
	editArgs := map[string]any{"path": "a.go", "old_string": "a", "new_string": "b"}
	runner := loopScriptRunner(t, []*agentcore.GenerateResult{
		callStep("c1", "shell_exec", cmd), callStep("c2", "shell_exec", cmd),
		callStep("c3", "file_edit", editArgs),
		callStep("c4", "shell_exec", cmd), callStep("c5", "shell_exec", cmd),
	}, reg)
	result, err := runner.Run(context.Background(), RunRequest{Prompt: "fix it"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 0, len(eventsOf(t, runner.cfg.Repository, result.SessionID, store.EventLoopGuard)))
}
