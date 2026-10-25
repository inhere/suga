package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
)

// The gates below are the thresholds a comparison verdict is made of. They are
// named constants because the verdict changes when they change, and the
// rationale is part of the number: an eval round with a real provider moves
// every metric on its own, so a gate has to sit above that noise.
const (
	// callsGate is the relative change of the model-call count that counts as a
	// signal. The deferred tool-loading design (2026-10-23) states its effect as
	// "calls +8%, prompt per call -14%": the +8% was the observable number, and
	// the repeats of one binary moved the count by 2-4%, so 5% is the gate.
	callsGate = 0.05
	// tokenGate is the relative change of a prompt-token metric that counts as a
	// signal. Repeats of the same binary with the same model moved the total
	// prompt by 5-15% (3 rounds of longsession-hard-*), so a 5% gate would fire
	// on noise; the smallest effect the tool descriptions aimed at was -14% per
	// call, and the deferred loading design measured -14% per call, so 10%.
	tokenGate = 0.10
	// durationGate is the relative change of wall-clock time. Provider latency
	// and retries move it by 10-20% between identical runs (one longsession
	// repeat took 580s, another 776s), so only 20% is reported as a cost change.
	durationGate = 0.20
	// minRounds is the number of rounds per group a verdict needs. With two
	// rounds a pass rate can only be 0/2, 1/2 or 2/2 and one flaky round flips
	// the verdict, so such a comparison is reported as 样本不足 instead.
	minRounds = 3
	// faultShareGate is the share of the turn time spent on failed provider
	// attempts that makes a comparison confounded when the other side has
	// (almost) none (lowFaultShare): a side that lost 20% of its turns to retries
	// was slower for a reason that has nothing to do with the change under test.
	faultShareGate = 0.20
	// lowFaultShare is the fault share the other side must stay under for that
	// mismatch to count as a confound.
	lowFaultShare = 0.05
	// toolCountGate and toolCountMin mark the per-tool call count differences the
	// tool table highlights: a tool called 0.4 times more often per run is noise,
	// so both a relative (20%) and an absolute (0.5 calls per run) change is
	// required.
	toolCountGate = 0.20
	toolCountMin  = 0.5
	// bootstrapIters and bootstrapSeed make the reported interval reproducible:
	// a rerun of compare prints the same numbers.
	bootstrapIters = 400
	bootstrapSeed  = 20261025
)

// Verdict labels. The report prints them as they are (the README「台账」section
// explains each one in Chinese).
const (
	verdictHelped       = "helped"
	verdictHurt         = "hurt"
	verdictNoEffect     = "no observed effect"
	verdictConfounded   = "confounded"
	verdictInsufficient = "insufficient samples"
)

// Verdict is the classification of one task or of the whole comparison: the
// label plus the numbers it was made of.
type Verdict struct {
	Label   string
	Reasons []string
}

// classInput is everything a verdict is made of: the pass counts of both sides,
// the gated cost metrics, the confounds that invalidate the comparison and the
// mixed-pass flag of an overall verdict.
type classInput struct {
	BaseRounds, BasePassed int
	CandRounds, CandPassed int
	Metrics                []metricDiff
	// Confounds are facts that make the comparison unsound (different model,
	// commit or task set, unequal samples, one side's provider faults): any of
	// them turns the verdict into confounded.
	Confounds []string
	// PassMixed marks a net pass rise that came with a drop on some task. The
	// quality difference is then not a clean win, so the cost metrics decide.
	PassMixed bool
}

// gatedMetric is one cost metric of the compare's main table: how to read it
// from a results.jsonl row and the relative change that counts as a signal.
type gatedMetric struct {
	Key, Label string
	Gate       float64
	Value      func(Result) float64
}

// costMetrics are the metrics a verdict is made of; the pass rate is judged on
// its own. Keys are stable: the ledger records them, so a renamed key breaks
// the trend of an existing ledger.
var costMetrics = []gatedMetric{
	{"calls", "调用数（模型）", callsGate, func(r Result) float64 { return float64(r.Calls) }},
	{"tool_calls", "工具调用数", callsGate, func(r Result) float64 { return float64(r.ToolCalls) }},
	{"prompt_total", "prompt 总量", tokenGate, promptTokens},
	{"prompt_per_call", "每次调用 prompt", tokenGate, promptPerCall},
	{"uncached", "未命中输入", tokenGate, func(r Result) float64 { return float64(r.InputTokens) }},
	{"duration", "耗时（秒）", durationGate, func(r Result) float64 { return secs(r.DurationMs) }},
}

// metricDiff is one metric compared between the two groups: the mean (min-max)
// of each side, the relative change of the means, the bootstrap interval of
// that change and whether the change clears the metric's gate.
type metricDiff struct {
	Key, Label string
	Gate       float64
	Base, Cand Stat
	Delta      float64 // 候选均值 / 基线均值 - 1
	Lo, Hi     float64 // 相对变化的 95% 区间
	HasCI      bool
	Rounds     int // 每侧较少的轮数
	Dir        int // +1 显著上升、-1 显著下降、0 门槛内或样本不足
}

// promptTokens is every prompt token a run sent (uncached + cache read/write).
func promptTokens(r Result) float64 {
	return float64(r.InputTokens + r.CacheRead + r.CacheWrite)
}

// promptPerCall is the prompt size of one model call: the metric the tool
// description trimming was aimed at (fewer schema tokens per call).
func promptPerCall(r Result) float64 {
	if r.Calls == 0 {
		return 0
	}
	return promptTokens(r) / float64(r.Calls)
}

// faultShare is the part of a group's turn time spent on failed provider
// attempts; 0 when no turn time was recorded.
func faultShare(rows []Result) float64 {
	var turn, fault int64
	for _, r := range rows {
		turn += r.TurnMs
		fault += r.ProviderFaultMs
	}
	if turn <= 0 {
		return 0
	}
	return float64(fault) / float64(turn)
}

// sampleOf reads one metric of every row.
func sampleOf(rows []Result, m gatedMetric) []float64 {
	vals := make([]float64, 0, len(rows))
	for _, r := range rows {
		vals = append(vals, m.Value(r))
	}
	return vals
}

// compareMetric compares one gated metric of two groups: means with their
// min-max range, the relative change of the means, its bootstrap interval and
// whether the change clears the gate. A zero baseline mean leaves Delta at 0
// (a relative change from nothing says nothing).
func compareMetric(base, cand []Result, m gatedMetric) metricDiff {
	bv, cv := sampleOf(base, m), sampleOf(cand, m)
	d := metricDiff{Key: m.Key, Label: m.Label, Gate: m.Gate, Base: statOf(bv), Cand: statOf(cv)}
	d.Rounds = min(len(bv), len(cv))
	if d.Base.Mean != 0 {
		d.Delta = d.Cand.Mean/d.Base.Mean - 1
		d.Lo, d.Hi, d.HasCI = bootstrapDelta(bv, cv)
	}
	if d.Rounds >= 2 && math.Abs(d.Delta) >= m.Gate {
		d.Dir = 1
		if d.Delta < 0 {
			d.Dir = -1
		}
	}
	return d
}

// bootstrapDelta resamples both sides with replacement and returns the 2.5% and
// 97.5% percentiles of the relative change of the means. ok is false when a
// side has fewer than two rounds (there is nothing to resample).
func bootstrapDelta(base, cand []float64) (lo, hi float64, ok bool) {
	if len(base) < 2 || len(cand) < 2 {
		return 0, 0, false
	}
	rng := rand.New(rand.NewSource(bootstrapSeed)) //nolint:gosec // a fixed seed keeps the report reproducible
	deltas := make([]float64, 0, bootstrapIters)
	for range bootstrapIters {
		bm, cm := resampleMean(rng, base), resampleMean(rng, cand)
		if bm == 0 {
			continue
		}
		deltas = append(deltas, cm/bm-1)
	}
	if len(deltas) == 0 {
		return 0, 0, false
	}
	sort.Float64s(deltas)
	return percentile(deltas, 0.025), percentile(deltas, 0.975), true
}

// resampleMean is the mean of vals drawn with replacement.
func resampleMean(rng *rand.Rand, vals []float64) float64 {
	var sum float64
	for range vals {
		sum += vals[rng.Intn(len(vals))]
	}
	return sum / float64(len(vals))
}

// percentile returns the p-quantile of the sorted vals.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)-1))
	return sorted[i]
}

// classifyVerdict turns one comparison into a verdict. The order is the one the
// README documents:
//
//  1. a confound (different model/commit/tasks, unequal samples, provider
//     faults on one side) makes any number meaningless -> confounded;
//  2. fewer than minRounds rounds per group -> insufficient samples;
//  3. a pass-rate drop -> hurt;
//  4. a clean pass-rate rise (no task got worse) -> helped, the cost change is
//     a note: a task the agent now solves is worth the extra calls, and the
//     report still shows the cost split;
//  5. pass rate equal (or a net rise with a task dropping) -> the cost metrics
//     decide: any gated rise -> hurt, any gated drop -> helped;
//  6. otherwise there is no observed effect.
func classifyVerdict(in classInput) Verdict {
	if len(in.Confounds) > 0 {
		return Verdict{Label: verdictConfounded, Reasons: in.Confounds}
	}
	if in.BaseRounds < minRounds || in.CandRounds < minRounds {
		return Verdict{Label: verdictInsufficient, Reasons: []string{fmt.Sprintf(
			"样本不足：基线 %d 轮 / 候选 %d 轮，每种至少 %d 轮才给结论", in.BaseRounds, in.CandRounds, minRounds)}}
	}
	passReason := fmt.Sprintf("通过率 %d/%d → %d/%d", in.BasePassed, in.BaseRounds, in.CandPassed, in.CandRounds)
	diff := in.CandPassed - in.BasePassed
	switch {
	case diff < 0:
		return Verdict{Label: verdictHurt, Reasons: append([]string{"通过率下降：" + passReason}, costNotes(in.Metrics)...)}
	case diff > 0 && !in.PassMixed:
		return Verdict{Label: verdictHelped, Reasons: append([]string{"通过率上升：" + passReason}, costNotes(in.Metrics)...)}
	}
	worse, better := gatedDirs(in.Metrics)
	if diff > 0 { // a net rise with a task sliding back: the cost metrics decide
		worse = append([]string{passReason + "（有任务下降）"}, worse...)
	}
	if len(worse) > 0 {
		return Verdict{Label: verdictHurt, Reasons: append(worse, better...)}
	}
	if len(better) > 0 {
		return Verdict{Label: verdictHelped, Reasons: append([]string{"通过率持平：" + passReason}, better...)}
	}
	return Verdict{Label: verdictNoEffect, Reasons: []string{passReason + "，成本差异均在门槛内"}}
}

// gatedDirs splits the metrics into the ones that rose (worse) and the ones
// that dropped (better) beyond their gate, each as a readable statement. A
// duration rise with an unchanged call count is not a cost of the change (the
// same calls simply ran longer, i.e. provider latency), so it stays in the table
// but does not decide the verdict.
func gatedDirs(metrics []metricDiff) (worse, better []string) {
	calls := metricDir(metrics, "calls")
	for _, m := range metrics {
		switch m.Dir {
		case 1:
			if m.Key == "duration" && calls != 1 {
				continue
			}
			worse = append(worse, fmt.Sprintf("%s 上升 %s（门槛 ±%s）", m.Label, formatAbs(m.Delta), formatGate(m.Gate)))
		case -1:
			better = append(better, fmt.Sprintf("%s 下降 %s（门槛 ±%s）", m.Label, formatAbs(m.Delta), formatGate(m.Gate)))
		}
	}
	return worse, better
}

// metricDir is the direction of one metric (0 when it is absent or within its
// gate).
func metricDir(metrics []metricDiff, key string) int {
	for _, m := range metrics {
		if m.Key == key {
			return m.Dir
		}
	}
	return 0
}

// costNotes lists every metric whose change clears its gate, in both
// directions, for a verdict that is decided by the pass rate alone.
func costNotes(metrics []metricDiff) []string {
	var notes []string
	for _, m := range metrics {
		if m.Dir != 0 {
			notes = append(notes, fmt.Sprintf("成本：%s %s %s（门槛 ±%s）", m.Label, directionWord(m.Dir), formatAbs(m.Delta), formatGate(m.Gate)))
		}
	}
	return notes
}

// directionWord is "上升"/"下降" for a metric direction.
func directionWord(dir int) string {
	if dir > 0 {
		return "上升"
	}
	return "下降"
}

// formatRel renders a relative change as "+8.3%" / "-14.0%".
func formatRel(d float64) string {
	return fmt.Sprintf("%+.1f%%", d*100)
}

// formatAbs renders the magnitude of a relative change ("8.3%"), for the
// statements that already carry a direction word.
func formatAbs(d float64) string { return fmt.Sprintf("%.1f%%", math.Abs(d)*100) }

// formatGate renders a gate as "5%" / "10%".
func formatGate(g float64) string { return fmt.Sprintf("%.0f%%", g*100) }

// toolDiff is one tool's call count per run on both sides.
type toolDiff struct {
	Tool        string
	Base, Cand  float64 // 每轮均值
	Significant bool
}

// toolDiffs compares the per-run tool call counts of two groups, one entry per
// tool either side called, sorted by tool name.
func toolDiffs(base, cand []Result) []toolDiff {
	touched := map[string]bool{}
	for _, set := range [][]Result{base, cand} {
		for _, r := range set {
			for name := range r.ToolCounts {
				touched[name] = true
			}
		}
	}
	out := make([]toolDiff, 0, len(touched))
	for name := range touched {
		d := toolDiff{Tool: name, Base: meanToolCount(base, name), Cand: meanToolCount(cand, name)}
		d.Significant = math.Abs(d.Cand-d.Base) >= toolCountMin &&
			math.Abs(d.Cand-d.Base) >= toolCountGate*math.Max(d.Base, d.Cand)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

// meanToolCount is the mean number of calls of one tool per run.
func meanToolCount(rows []Result, tool string) float64 {
	if len(rows) == 0 {
		return 0
	}
	var sum int
	for _, r := range rows {
		sum += r.ToolCounts[tool]
	}
	return float64(sum) / float64(len(rows))
}

// toolLine renders one tool diff as a table row of the given group column count.
func toolLine(d toolDiff) string {
	mark := ""
	if d.Significant {
		mark = "**"
	}
	return fmt.Sprintf("| %s%s%s | %.1f | %.1f | %+.1f |\n", mark, d.Tool, mark, d.Base, d.Cand, d.Cand-d.Base)
}

// joinDirs lists the directories of a group for the report and the ledger.
func joinDirs(dirs []string) string {
	if len(dirs) == 0 {
		return "-"
	}
	return strings.Join(dirs, ", ")
}
