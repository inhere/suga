package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// TestStatOf covers the mean/min/max summary of one metric.
func TestStatOf(t *testing.T) {
	t.Run("mean and range", func(t *testing.T) {
		s := statOf([]float64{4, 10, 7})
		assert.Eq(t, 7.0, s.Mean)
		assert.Eq(t, 4.0, s.Min)
		assert.Eq(t, 10.0, s.Max)
	})
	t.Run("single value", func(t *testing.T) {
		s := statOf([]float64{3})
		assert.Eq(t, Stat{Mean: 3, Min: 3, Max: 3}, s)
	})
	t.Run("empty", func(t *testing.T) {
		assert.Eq(t, Stat{}, statOf(nil))
	})
}

// TestStatRounds covers the per-task aggregation of a --repeat run: the pass
// count plus the mean and range of calls, tokens and time, and that the rounds
// of two variants are never mixed.
func TestStatRounds(t *testing.T) {
	rs := []Result{
		{Task: "a", Passed: true, Calls: 4, ToolCalls: 2, InputTokens: 1000, CacheRead: 500, OutputToks: 100, DurationMs: 10000},
		{Task: "a", Passed: false, Calls: 6, ToolCalls: 2, InputTokens: 3000, OutputToks: 300, DurationMs: 14000},
	}
	s := statRounds(rs)
	assert.Eq(t, 2, s.Rounds)
	assert.Eq(t, 1, s.Passed)
	assert.Eq(t, Stat{Mean: 5, Min: 4, Max: 6}, s.Calls)
	assert.Eq(t, Stat{Mean: 2, Min: 2, Max: 2}, s.ToolCalls)
	assert.Eq(t, Stat{Mean: 2250, Min: 1500, Max: 3000}, s.Prompt) // uncached + cache read/write
	assert.Eq(t, Stat{Mean: 2000, Min: 1000, Max: 3000}, s.Uncached)
	assert.Eq(t, Stat{Mean: 200, Min: 100, Max: 300}, s.Out)
	assert.Eq(t, Stat{Mean: 12, Min: 10, Max: 14}, s.Seconds)
	assert.Eq(t, 0, statRounds(nil).Rounds)

	t.Run("averageRounds stays the mean", func(t *testing.T) {
		a := averageRounds(rs)
		assert.Eq(t, 1, a.Passed)
		assert.Eq(t, 5.0, a.Calls)
		assert.Eq(t, 12.0, a.Seconds)
	})
	t.Run("roundGroup keeps variants apart", func(t *testing.T) {
		mixed := []Result{
			{Task: "a", Variant: "v1"}, {Task: "a", Variant: "v1"},
			{Task: "a", Variant: "v2"}, {Task: "a", Variant: "v2"},
		}
		assert.Eq(t, 2, len(roundGroup(mixed, 1)))
		assert.Eq(t, "v1", roundGroup(mixed, 1)[0].Variant)
		assert.Eq(t, 2, len(roundGroup(mixed, 2)))
		assert.Eq(t, "v2", roundGroup(mixed, 2)[0].Variant)
		assert.Eq(t, "a [v2] (avg)", avgLabel(mixed[2]))
		assert.Eq(t, "a (avg)", avgLabel(Result{Task: "a"}))
	})
}

// goldenSingleRunTable is the summary table of one run without --repeat: the
// plain table must not change when the repeat summaries are added.
const goldenSingleRunTable = `TASK              AGENT  MODEL    RESULT  CALLS  TOOLS  SPILL  FIRST  PROMPT  TOKENS(in/out)  HIT%  STEADY%  COST     TIME   WEB  ARGS
fix-failing-test  suga   fake/m1  PASS    5      3      0      1200   4200    3000/800        23.8  -        $0.0123  12.5s  off  -
`

// TestPrintTableSingleRun is the byte-for-byte regression of the table: a run
// without --repeat prints the same rows and header as before the repeat summary
// was added (no avg row).
func TestPrintTableSingleRun(t *testing.T) {
	first := int64(1200)
	r := Result{Task: "fix-failing-test", Repeat: 1, Agent: "suga", Model: "fake/m1", Passed: true,
		Calls: 5, ToolCalls: 3, FirstUncached: &first, InputTokens: 3000, CacheRead: 1000, CacheWrite: 200,
		OutputToks: 800, CacheHit: 1000.0 / 4200.0, CostUSD: 0.0123, DurationMs: 12500, Web: webOff}
	var buf bytes.Buffer
	printTable(&buf, []Result{r}, false)
	assert.Eq(t, goldenSingleRunTable, buf.String())
	assert.StrNotContains(t, buf.String(), "(avg)")
}

// TestPrintTableRepeat covers the per-task summary row of a --repeat run: the
// pass count and the mean with its range for calls, tokens and time.
func TestPrintTableRepeat(t *testing.T) {
	rs := []Result{
		{Task: "a", Repeat: 1, Agent: "suga", Model: "m", Passed: true, Calls: 4, ToolCalls: 2, InputTokens: 1000, OutputToks: 100, DurationMs: 10000},
		{Task: "a", Repeat: 2, Agent: "suga", Model: "m", Passed: true, Calls: 5, ToolCalls: 2, InputTokens: 2000, OutputToks: 100, DurationMs: 12000},
		{Task: "a", Repeat: 3, Agent: "suga", Model: "m", Passed: false, Calls: 6, ToolCalls: 2, InputTokens: 3000, OutputToks: 100, DurationMs: 14000},
	}
	var buf bytes.Buffer
	printTable(&buf, rs, true)
	out := buf.String()
	for _, want := range []string{
		"a-r1", "a-r2", "a-r3",
		"a (avg)", "2/3", // pass count over the rounds
		"5.0 (4-6)",          // calls mean and range
		"2000 (1000-3000)",   // uncached input mean and range
		"12.0s (10.0-14.0s)", // time mean and range
	} {
		assert.StrContains(t, out, want)
	}
	assert.Eq(t, 1, bytes.Count([]byte(out), []byte("a (avg)")))
}

// TestFillBreakdown covers the wall-clock split of a run: turns plus forced
// compactions plus the runner's overhead add up to duration_ms, and an agent
// with no per-turn record reports its whole run as turn time.
func TestFillBreakdown(t *testing.T) {
	t.Run("turns and compaction", func(t *testing.T) {
		r := Result{DurationMs: 10000, Turns: []TurnRun{
			{Turn: 1, DurationMs: 3000},
			{Turn: 2, DurationMs: 4000, Compacted: &TurnCompaction{DurationMs: 1500}},
		}}
		fillBreakdown(&r)
		assert.Eq(t, int64(7000), r.TurnMs)
		assert.Eq(t, int64(1500), r.CompactionMs)
		assert.Eq(t, int64(1500), r.OverheadMs)
		assert.Eq(t, r.DurationMs, r.TurnMs+r.CompactionMs+r.OverheadMs)
	})
	t.Run("no per-turn record", func(t *testing.T) {
		r := Result{DurationMs: 5000}
		fillBreakdown(&r)
		assert.Eq(t, int64(5000), r.TurnMs)
		assert.Eq(t, int64(0), r.CompactionMs)
		assert.Eq(t, int64(0), r.OverheadMs)
	})
}

// TestPrintTiming covers the timing section: it names the split of every run and
// prints nothing without results.
func TestPrintTiming(t *testing.T) {
	var buf bytes.Buffer
	printTiming(&buf, []Result{{Task: "a", Repeat: 2, DurationMs: 10000, TurnMs: 7000, CompactionMs: 1500, ProviderFaultMs: 900, OverheadMs: 1500}}, true)
	out := buf.String()
	assert.StrContains(t, out, "timing (turn + compaction + overhead = duration")
	assert.StrContains(t, out, "a-r2  turn 7.0s  compaction 1.5s  fault 0.9s  overhead 1.5s  = 10.0s")

	t.Run("variant rows are distinguishable", func(t *testing.T) {
		var b bytes.Buffer
		printTiming(&b, []Result{{Task: "a", Variant: "v1", DurationMs: 2000, TurnMs: 2000}}, false)
		assert.StrContains(t, b.String(), "v1/a  turn 2.0s")
	})

	t.Run("no results", func(t *testing.T) {
		var quiet bytes.Buffer
		printTiming(&quiet, nil, false)
		assert.Eq(t, "", quiet.String())
	})
}

// TestRepeatEndToEnd runs the real loop with --repeat 2 against a stub agent:
// every round gets its own workspace and home, and results.jsonl carries the
// repeat number and the timing split (a run without per-turn records reports
// its whole time as turn time).
func TestRepeatEndToEnd(t *testing.T) {
	stub := evalStub(t)
	base := t.TempDir()
	taskDir := filepath.Join(base, "tasks", "rep")
	assert.NoErr(t, os.MkdirAll(filepath.Join(taskDir, "fixture"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(taskDir, "fixture", "go.mod"), []byte("module evalfixture/rep\n"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(taskDir, "task.yaml"),
		[]byte("name: rep\nprompt: hi\nchecks:\n  - {type: answer_contains, text: \"run ok\"}\n"), 0o644))
	tasks, err := loadTasks(filepath.Join(base, "tasks"), "*")
	assert.NoErr(t, err)

	out := filepath.Join(base, "out")
	assert.NoErr(t, os.MkdirAll(out, 0o755))
	jf, err := os.Create(filepath.Join(out, "results.jsonl"))
	assert.NoErr(t, err)
	defer jf.Close()
	o := &options{bin: stub, agent: "suga", out: out, timeout: time.Minute, tasksDir: filepath.Join(base, "tasks"), repeat: 2}
	assert.NoErr(t, runTaskSet(o, stub, "20260101-000000", tasks, nil, jf))

	raw, err := os.ReadFile(filepath.Join(out, "results.jsonl"))
	assert.NoErr(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	assert.Eq(t, 2, len(lines))
	for i, line := range lines {
		var r Result
		assert.NoErr(t, json.Unmarshal([]byte(line), &r))
		assert.Eq(t, i+1, r.Repeat)
		assert.True(t, r.Passed, r.Checks)
		// The stub keeps no per-turn record, so the whole run is turn time. A fast
		// machine runs the stub in under a millisecond, so the duration may be 0;
		// what matters is that the split adds up.
		assert.True(t, r.DurationMs >= 0, r.DurationMs)
		assert.Eq(t, r.DurationMs, r.TurnMs+r.CompactionMs+r.OverheadMs)
		if _, serr := os.Stat(filepath.Join(out, fmt.Sprintf("rep-r%d", i+1), "ws")); serr != nil {
			t.Fatalf("round %d has its own workspace: %v", i+1, serr)
		}
	}
}

// TestTaskCellRepeat covers the compare report's per-task cell for a repeated
// task: pass count, mean and range of calls and time.
func TestTaskCellRepeat(t *testing.T) {
	rs := []Result{
		{Task: "a", Variant: "v1", Repeat: 1, Passed: true, Calls: 4, InputTokens: 1000, DurationMs: 10000},
		{Task: "a", Variant: "v1", Repeat: 2, Passed: true, Calls: 5, InputTokens: 1000, DurationMs: 12000},
		{Task: "a", Variant: "v1", Repeat: 3, Passed: true, Calls: 6, InputTokens: 1000, DurationMs: 14000},
	}
	_, cell := taskCell(rs, "v1", "a")
	assert.StrContains(t, cell, "3/3 pass")
	assert.StrContains(t, cell, "c=5.0 (4-6)")
	assert.StrContains(t, cell, "12.0s (10.0-14.0s)")

	t.Run("single run stays plain", func(t *testing.T) {
		_, cell := taskCell(rs[:1], "v1", "a")
		assert.StrContains(t, cell, "c=4 p=1000 10.0s")
	})
	t.Run("meanTiming", func(t *testing.T) {
		turn, compact, fault := meanTiming([]Result{
			{TurnMs: 2000, CompactionMs: 1000, ProviderFaultMs: 500},
			{TurnMs: 4000, CompactionMs: 0, ProviderFaultMs: 500},
		})
		assert.Eq(t, 3.0, turn)
		assert.Eq(t, 0.5, compact)
		assert.Eq(t, 0.5, fault)
		turn, compact, fault = meanTiming(nil)
		assert.Eq(t, [3]float64{0, 0, 0}, [3]float64{turn, compact, fault})
	})
}
