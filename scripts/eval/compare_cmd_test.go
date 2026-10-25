package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// compareRows builds n rows of one task whose metric fields are taken from vals
// (one value per round), so a test can drive compareMetric with a known mean.
func compareRows(task string, vals []float64, set func(*Result, float64)) []Result {
	rows := make([]Result, 0, len(vals))
	for i, v := range vals {
		r := Result{Task: task, Repeat: i + 1, Agent: "suga", Model: "test/model", Passed: true}
		set(&r, v)
		rows = append(rows, r)
	}
	return rows
}

// callsOf sets the model-call count of a row.
func callsOf(r *Result, v float64) { r.Calls = int(v) }

// promptOf sets the prompt tokens of a row and one model call, so the
// prompt-per-call metric of the same rows is the token count itself.
func promptOf(r *Result, v float64) {
	r.InputTokens = int64(v)
	r.Calls = 1
}

// durationOf sets the wall-clock time of a row (v is in seconds).
func durationOf(r *Result, v float64) { r.DurationMs = int64(v * 1000) }

// metricDiffOf compares one metric of two synthetic row sets.
func metricDiffOf(t *testing.T, key string, base, cand []float64, set func(*Result, float64)) metricDiff {
	t.Helper()
	var m gatedMetric
	for _, c := range costMetrics {
		if c.Key == key {
			m = c
		}
	}
	if m.Key == "" {
		t.Fatalf("unknown metric %q", key)
	}
	return compareMetric(compareRows("t", base, set), compareRows("t", cand, set), m)
}

// TestClassifyVerdict is the table of the classification rules: the label and
// the reason that has to show up in the report.
func TestClassifyVerdict(t *testing.T) {
	const (
		n = 3 // rounds per group
	)
	// flat builds the metrics of a comparison whose cost did not move.
	flat := func(t *testing.T) []metricDiff {
		return []metricDiff{
			metricDiffOf(t, "calls", []float64{40, 41, 42}, []float64{40, 41, 42}, callsOf),
			metricDiffOf(t, "prompt_per_call", []float64{10000, 10000, 10000}, []float64{10000, 10000, 10000}, promptOf),
		}
	}
	cases := []struct {
		name      string
		in        func(t *testing.T) classInput
		want      string
		wantIn    string // 理由里必须出现的片段
		wantNotIn string // 理由里不能出现的片段
	}{
		{
			name: "通过率下降即 hurt",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 3, CandRounds: n, CandPassed: 2, Metrics: flat(t)}
			},
			want:   verdictHurt,
			wantIn: "通过率下降",
		},
		{
			name: "通过率上升且无任务下降即 helped",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 2, CandRounds: n, CandPassed: 3, Metrics: flat(t)}
			},
			want:   verdictHelped,
			wantIn: "通过率上升",
		},
		{
			name: "通过率净上升但有任务下降由成本决定",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 2, CandRounds: n, CandPassed: 3, PassMixed: true,
					Metrics: []metricDiff{metricDiffOf(t, "calls", []float64{40, 40, 40}, []float64{45, 45, 45}, callsOf)}}
			},
			want:   verdictHurt,
			wantIn: "有任务下降",
		},
		{
			name: "通过率持平、调用数 +12.5% 即 hurt",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 3, CandRounds: n, CandPassed: 3,
					Metrics: []metricDiff{metricDiffOf(t, "calls", []float64{40, 40, 40}, []float64{45, 45, 45}, callsOf)}}
			},
			want:   verdictHurt,
			wantIn: "调用数（模型） 上升 12.5%",
		},
		{
			name: "调用数 +2.5% 在门槛内",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 3, CandRounds: n, CandPassed: 3,
					Metrics: []metricDiff{metricDiffOf(t, "calls", []float64{40, 40, 40}, []float64{41, 41, 41}, callsOf)}}
			},
			want: verdictNoEffect,
		},
		{
			name: "通过率持平、每次调用 prompt -14% 即 helped",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 3, CandRounds: n, CandPassed: 3,
					Metrics: []metricDiff{metricDiffOf(t, "prompt_per_call", []float64{14000, 14000, 14000}, []float64{12040, 12040, 12040}, promptOf)}}
			},
			want:   verdictHelped,
			wantIn: "每次调用 prompt 下降 14.0%",
		},
		{
			name: "耗时单涨但调用数没涨按 provider 延迟处理",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 3, CandRounds: n, CandPassed: 3,
					Metrics: []metricDiff{
						metricDiffOf(t, "calls", []float64{40, 40, 40}, []float64{40, 40, 40}, callsOf),
						metricDiffOf(t, "duration", []float64{300, 300, 300}, []float64{420, 420, 420}, durationOf),
					}}
			},
			want: verdictNoEffect,
		},
		{
			name: "耗时上涨且调用数也涨则计入成本",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 3, CandRounds: n, CandPassed: 3,
					Metrics: []metricDiff{
						metricDiffOf(t, "calls", []float64{40, 40, 40}, []float64{44, 44, 44}, callsOf),
						metricDiffOf(t, "duration", []float64{300, 300, 300}, []float64{420, 420, 420}, durationOf),
					}}
			},
			want:   verdictHurt,
			wantIn: "耗时（秒） 上升 40.0%",
		},
		{
			name: "配置/版本不一致即 confounded",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: n, BasePassed: 3, CandRounds: n, CandPassed: 3, Metrics: flat(t),
					Confounds: []string{"二进制提交 不一致：基线 abc / 候选 def"}}
			},
			want:   verdictConfounded,
			wantIn: "二进制提交 不一致",
		},
		{
			name: "样本不足时不给结论",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: 2, BasePassed: 2, CandRounds: 2, CandPassed: 1,
					Metrics: []metricDiff{metricDiffOf(t, "calls", []float64{40, 40}, []float64{50, 50}, callsOf)}}
			},
			want:   verdictInsufficient,
			wantIn: "样本不足",
		},
		{
			name: "样本不足也优先报 confounded",
			in: func(t *testing.T) classInput {
				return classInput{BaseRounds: 1, BasePassed: 1, CandRounds: 1, CandPassed: 0,
					Confounds: []string{"模型 不一致：基线 a / 候选 b"}}
			},
			want: verdictConfounded,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := classifyVerdict(c.in(t))
			assert.Eq(t, c.want, v.Label)
			reasons := strings.Join(v.Reasons, "；")
			if c.wantIn != "" {
				assert.True(t, strings.Contains(reasons, c.wantIn), reasons)
			}
			if c.wantNotIn != "" {
				assert.False(t, strings.Contains(reasons, c.wantNotIn), reasons)
			}
		})
	}
}

// writeResults writes a results.jsonl file into dir (created) and returns dir.
func writeResults(t *testing.T, dir string, rows []Result) string {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(dir, 0o755))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		assert.NoErr(t, enc.Encode(r))
	}
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "results.jsonl"), buf.Bytes(), 0o644))
	return dir
}

// compareRunRows are the rows of one side of the end-to-end test: two tasks,
// three rounds each, with the calls and pass rate the test drives.
func compareRunRows(task string, pass bool, calls int) []Result {
	var rows []Result
	for i := range 3 {
		rows = append(rows, Result{
			Task: task, Repeat: i + 1, Agent: "suga", Model: "test/model", Passed: pass,
			Calls: calls, InputTokens: int64(1000 * calls), CacheRead: int64(2000 * calls), DurationMs: 60000,
			TurnMs: 50000, ToolCounts: map[string]int{"file_read": calls / 2},
		})
	}
	return rows
}

// TestRunCompareEndToEnd runs the subcommand on two prepared result dirs: the
// report, the exit path and the appended ledger line are all checked.
func TestRunCompareEndToEnd(t *testing.T) {
	root := t.TempDir()
	base := writeResults(t, filepath.Join(root, "base"), append(
		compareRunRows("task-a", false, 40), compareRunRows("task-b", true, 20)...))
	cand := writeResults(t, filepath.Join(root, "cand"), append(
		compareRunRows("task-a", true, 44), compareRunRows("task-b", true, 20)...))
	ledger := filepath.Join(root, "ledger", "ledger.jsonl")

	var out bytes.Buffer
	err := runCompare([]string{base, cand, "--ledger", ledger}, &out)
	assert.NoErr(t, err)
	md := out.String()
	assert.True(t, strings.Contains(md, "## 结论：helped"), md)
	assert.True(t, strings.Contains(md, "| task-a |"), md)
	assert.True(t, strings.Contains(md, "task-b"), md)

	raw, err := os.ReadFile(ledger)
	assert.NoErr(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	assert.Eq(t, 1, len(lines))
	var entry ledgerEntry
	assert.NoErr(t, json.Unmarshal([]byte(lines[0]), &entry))
	assert.Eq(t, verdictHelped, entry.Verdict)
	assert.Eq(t, []string{base}, entry.Base)
	assert.Eq(t, []string{cand}, entry.Cand)
	assert.Eq(t, 2, len(entry.Tasks))
	assert.Eq(t, "task-a", entry.Tasks[0].Task)
	assert.Eq(t, verdictHelped, entry.Tasks[0].Verdict)
	assert.True(t, entry.Metrics["calls"] > 0, entry.Metrics)

	// A second run appends a second line instead of rewriting the ledger.
	out.Reset()
	assert.NoErr(t, runCompare([]string{base, cand, "--ledger", ledger}, &out))
	raw, err = os.ReadFile(ledger)
	assert.NoErr(t, err)
	assert.Eq(t, 2, len(strings.Split(strings.TrimSpace(string(raw)), "\n")))
}

// TestCompareGroupGlobSkipsEmptyDirs loads a side given as a glob: the dirs with
// rows are kept, an aborted run (empty results.jsonl) is only reported.
func TestCompareGroupGlobSkipsEmptyDirs(t *testing.T) {
	root := t.TempDir()
	writeResults(t, filepath.Join(root, "off-a"), compareRunRows("task-a", true, 10))
	writeResults(t, filepath.Join(root, "off-b"), compareRunRows("task-b", true, 10))
	writeResults(t, filepath.Join(root, "off-empty"), nil)

	g, err := loadCompareGroup(filepath.Join(root, "off-*"))
	assert.NoErr(t, err)
	assert.Eq(t, 2, len(g.Dirs))
	assert.Eq(t, 1, len(g.Skipped))
	assert.Eq(t, 6, len(g.Rows))

	_, err = loadCompareGroup(filepath.Join(root, "missing-*"))
	assert.Err(t, err)

	empty := writeResults(t, filepath.Join(root, "only-empty"), nil)
	_, err = loadCompareGroup(empty)
	assert.Err(t, err)
}

// TestCompareReportOneSidedTask checks the alignment: a task only one side ran
// makes the overall verdict confounded, the shared task stays comparable.
func TestCompareReportOneSidedTask(t *testing.T) {
	root := t.TempDir()
	base := writeResults(t, filepath.Join(root, "base"), append(
		compareRunRows("task-a", true, 10), compareRunRows("task-only-base", true, 10)...))
	cand := writeResults(t, filepath.Join(root, "cand"), compareRunRows("task-a", true, 10))

	b, err := loadCompareGroup(base)
	assert.NoErr(t, err)
	c, err := loadCompareGroup(cand)
	assert.NoErr(t, err)
	md, entry := compareReport(b, c, time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))
	assert.Eq(t, verdictConfounded, entry.Verdict)
	assert.True(t, strings.Contains(strings.Join(entry.Reasons, "；"), "任务集不同"), entry.Reasons)
	for _, task := range entry.Tasks {
		if task.Task == "task-only-base" {
			assert.Eq(t, verdictConfounded, task.Verdict)
		}
		if task.Task == "task-a" {
			assert.Eq(t, verdictNoEffect, task.Verdict)
		}
	}
	assert.True(t, strings.Contains(md, "只有一侧有该任务的结果"), md)
}

// TestCompareReportConfoundsVersion checks the confounded path of a real report:
// two runs of different commits are flagged, the numbers are still printed.
func TestCompareReportConfoundsVersion(t *testing.T) {
	root := t.TempDir()
	baseRows := compareRunRows("task-a", true, 10)
	for i := range baseRows {
		baseRows[i].Commit = "aaaa1111"
	}
	candRows := compareRunRows("task-a", true, 10)
	for i := range candRows {
		candRows[i].Commit = "bbbb2222"
	}
	b, err := loadCompareGroup(writeResults(t, filepath.Join(root, "base"), baseRows))
	assert.NoErr(t, err)
	c, err := loadCompareGroup(writeResults(t, filepath.Join(root, "cand"), candRows))
	assert.NoErr(t, err)
	md, entry := compareReport(b, c, time.Now())
	assert.Eq(t, verdictConfounded, entry.Verdict)
	assert.True(t, strings.Contains(strings.Join(entry.Reasons, "；"), "二进制提交 不一致"), entry.Reasons)
	assert.True(t, strings.Contains(md, "| 调用数（模型） |"), md)
}

// TestCompareMetricGates pins the gate arithmetic: the direction is relative to
// the metric's own gate and the bootstrap interval is reproducible.
func TestCompareMetricGates(t *testing.T) {
	t.Run("调用数 +8% 超出门槛", func(t *testing.T) {
		d := metricDiffOf(t, "calls", []float64{25, 25, 25}, []float64{27, 27, 27}, callsOf)
		assert.Eq(t, 1, d.Dir)
		assert.True(t, math.Abs(d.Delta-0.08) < 1e-9, d.Delta)
	})
	t.Run("prompt 总量 -6% 在 10% 门槛内", func(t *testing.T) {
		d := metricDiffOf(t, "prompt_total", []float64{100, 100, 100}, []float64{94, 94, 94}, promptOf)
		assert.Eq(t, 0, d.Dir)
	})
	t.Run("单轮没有区间", func(t *testing.T) {
		d := metricDiffOf(t, "calls", []float64{25}, []float64{30}, callsOf)
		assert.False(t, d.HasCI)
		assert.Eq(t, 0, d.Dir) // 1 轮不给方向
	})
	t.Run("区间重跑一致", func(t *testing.T) {
		a := metricDiffOf(t, "calls", []float64{20, 30, 25}, []float64{30, 22, 28}, callsOf)
		b := metricDiffOf(t, "calls", []float64{20, 30, 25}, []float64{30, 22, 28}, callsOf)
		assert.True(t, a.HasCI)
		assert.Eq(t, a.Lo, b.Lo)
		assert.Eq(t, a.Hi, b.Hi)
	})
}

// TestToolDiffs checks the per-tool table: a tool called noticeably more often
// is marked, a small move is not.
func TestToolDiffs(t *testing.T) {
	base := []Result{{ToolCounts: map[string]int{"file_read": 4, "file_grep": 2}}}
	cand := []Result{{ToolCounts: map[string]int{"file_read": 8, "file_grep": 2}}}
	diffs := toolDiffs(base, cand)
	assert.Eq(t, 2, len(diffs))
	assert.Eq(t, "file_grep", diffs[0].Tool)
	assert.False(t, diffs[0].Significant)
	assert.Eq(t, "file_read", diffs[1].Tool)
	assert.True(t, diffs[1].Significant)
}
