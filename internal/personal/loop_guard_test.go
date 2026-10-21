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
}

// freshLoopGuard builds the hook with the table's expected hint text.
func hintFor(n int) string { return loopGuardHint("file_read", n) }

// runLoopStepsHook feeds steps (one turn's calls, in order) through the hook
// and returns the result content the events end up carrying.
func runLoopStepsHook(t *testing.T, hook *loopGuardHook, stepList []loopStep) []string {
	t.Helper()
	var got []string
	for _, step := range stepList {
		event := &agentcore.ToolEvent{
			Call:   &agentcore.ToolCall{CallID: "c-h", Name: step.name, Arguments: step.args},
			Result: &agentcore.ToolResult{Name: step.name, Content: step.content},
		}
		assert.NoErr(t, hook.AfterTool(context.Background(), event, agentcore.NewControl()))
		got = append(got, event.Result.Content)
	}
	return got
}

// newLoopGuardHook builds the hook as wired: fresh per-turn state.
func newLoopGuardHook(record func(context.Context, store.Event)) *loopGuardHook {
	return &loopGuardHook{record: record, counts: map[string]int{}, hints: map[string]int{}}
}

// TestLoopGuardHookRules covers the rule boundaries of the counters. Each step
// of the table carries what that call's result should say afterwards: "" for
// no reminder, or "第 N 次" for one.
func TestLoopGuardHookRules(t *testing.T) {
	readA := loopStep{name: "file_read", args: map[string]any{"path": "a.go"}, content: "r"}
	readB := loopStep{name: "file_read", args: map[string]any{"path": "b.go"}, content: "r"}
	plan := loopStep{name: "update_plan", args: map[string]any{"plan": []any{}}, content: "ok"}
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
			want:     []string{"", "", hintFor(3)}, events: 1,
			lastRule: "R1", lastCount: 3},
		{name: "参数不同不算重复",
			stepList: steps(readA, readB, readA, readB),
			want:     []string{"", "", "", ""}, events: 0},
		{name: "非连续累计四次触发 R2",
			stepList: steps(readA, readA, readB, readA, readA),
			want:     []string{"", "", "", "", hintFor(4)}, events: 1,
			lastRule: "R2", lastCount: 4},
		{name: "排除的工具不计数",
			stepList: steps(plan, plan, plan, plan),
			want:     []string{"", "", "", ""}, events: 0},
		{name: "同一组每回合最多提示两次",
			stepList: steps(readA, readA, readA, readA, readA, readA),
			want:     []string{"", "", hintFor(3), hintFor(4), "", ""},
			events:   2, lastRule: "R2", lastCount: 4},
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
