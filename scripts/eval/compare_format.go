package main

import (
	"fmt"
	"strings"
)

// sideName labels a group in the report's header lines.
func sideName(g, base *compareGroup) string {
	if g == base {
		return "基线"
	}
	return "候选"
}

// orUnknown keeps "unknown" where an empty field would read as zero.
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// listOrNone renders a distinct-value list for a confound message.
func listOrNone(vals []string) string {
	if len(vals) == 0 {
		return "-"
	}
	return strings.Join(vals, ",")
}

// statValue renders one mean: a small value keeps a decimal, a large one is
// rounded (a prompt total of 398490 does not need a decimal).
func statValue(v float64) string {
	if v < 100 {
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

// statText renders a mean with its min-max range.
func statText(s Stat, isSeconds bool) string {
	suffix := ""
	if isSeconds {
		suffix = "s"
	}
	return fmt.Sprintf("%s%s (%s-%s%s)", statValue(s.Mean), suffix, statValue(s.Min), statValue(s.Max), suffix)
}

// intervalText renders the bootstrap interval of the relative change; a metric
// with fewer than two rounds per side has none ("样本不足"), and a zero baseline
// mean has no relative change to bracket ("-").
func intervalText(m metricDiff) string {
	if m.HasCI {
		return formatRel(m.Lo) + "…" + formatRel(m.Hi)
	}
	if m.Rounds < 2 {
		return "样本不足"
	}
	return "-"
}

// metricWord is the 判定 cell of one metric row; an ungated metric (gate 0) has
// no verdict of its own.
func metricWord(m metricDiff) string {
	if m.Gate == 0 {
		return "-"
	}
	switch m.Dir {
	case 1:
		return "**上升**"
	case -1:
		return "**下降**"
	}
	return "噪声内"
}

// gateCell is the 门槛 cell of one metric row ("-" for an ungated metric).
func gateCell(m metricDiff) string {
	if m.Gate == 0 {
		return "-"
	}
	return "±" + formatGate(m.Gate)
}

// splitCell renders one column of the per-task timing table.
func splitCell(base, cand float64) string {
	return fmt.Sprintf("%s → %s (%s)", secsText(base), secsText(cand), relText(base, cand))
}

// faultCell renders the provider-fault change of one task as an absolute
// difference (a zero baseline makes a relative change meaningless).
func faultCell(base, cand float64) string {
	if base == 0 && cand == 0 {
		return "-"
	}
	return fmt.Sprintf("%s → %s (%s)", secsText(base), secsText(cand), faultText(base, cand))
}

// metricArrow renders one metric of a task row as "基线 → 候选 (差异)".
func metricArrow(metrics []metricDiff, key string) string {
	for _, m := range metrics {
		if m.Key == key {
			return fmt.Sprintf("%s → %s (%s)", statValue(m.Base.Mean), statValue(m.Cand.Mean), formatRel(m.Delta))
		}
	}
	return "-"
}

// passText renders a pass count as "13/15 (86.7%)"; a group without rows is "-".
func passText(passed, rounds int) string {
	if rounds == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d (%.1f%%)", passed, rounds, float64(passed)/float64(rounds)*100)
}

// passDeltaText renders the pass-rate change of the overall table.
func passDeltaText(bPass, bRounds, cPass, cRounds int) string {
	if bRounds == 0 || cRounds == 0 {
		return "-"
	}
	diff := cPass - bPass
	word := "0 轮"
	if diff != 0 {
		word = fmt.Sprintf("%+d 轮", diff)
	}
	pp := (float64(cPass)/float64(cRounds) - float64(bPass)/float64(bRounds)) * 100
	return fmt.Sprintf("%s（%+.1fpp）", word, pp)
}

// passWord is the 判定 cell of the pass-rate row of the overall table: a net
// rise with a task sliding back is marked, because the cost metrics then decide.
func passWord(base, cand []Result, mixed bool) string {
	diff := passedCount(cand) - passedCount(base)
	switch {
	case diff > 0 && mixed:
		return "**上升**（有任务下降）"
	case diff > 0:
		return "**上升**"
	case diff < 0:
		return "**下降**"
	}
	return "持平"
}

// costSplit is the one-line explanation of which cost metric moved which way:
// the "hurt on calls, helped on prompt" case is the one a reader needs to see
// together with the verdict.
func costSplit(verdict Verdict, overall []metricDiff) string {
	var better, worse []string
	for _, m := range overall {
		switch m.Dir {
		case -1:
			better = append(better, fmt.Sprintf("%s %s", m.Label, formatRel(m.Delta)))
		case 1:
			worse = append(worse, fmt.Sprintf("%s %s", m.Label, formatRel(m.Delta)))
		}
	}
	var parts []string
	if len(better) > 0 {
		parts = append(parts, "有利："+strings.Join(better, "、"))
	}
	if len(worse) > 0 {
		parts = append(parts, "不利："+strings.Join(worse, "、"))
	}
	if len(parts) == 0 {
		return "所有带门槛的成本指标都在门槛内（" + strings.Join(metricKeys(overall), "、") + "）"
	}
	return strings.Join(parts, "；") + " → 综合判定 " + verdict.Label
}

// metricKeys lists the metric labels of the main table (for the "everything in
// the noise" sentence).
func metricKeys(ms []metricDiff) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Label)
	}
	return out
}

// timingRow is one line of the report's timing split table.
type timingRow struct{ label, base, cand, delta string }

// timingRows compares the wall-clock split of both groups: the total, the turn
// time, the forced compactions and the provider faults (a part of the turns).
func timingRows(base, cand []Result) []timingRow {
	bt, bc, bf := meanTiming(base)
	ct, cc, cf := meanTiming(cand)
	bd, cd := meanSeconds(base), meanSeconds(cand)
	return []timingRow{
		{"总耗时", secsText(bd), secsText(cd), relText(bd, cd)},
		{"turn", secsText(bt), secsText(ct), relText(bt, ct)},
		{"compaction", secsText(bc), secsText(cc), relText(bc, cc)},
		{"provider fault（turn 内）", secsText(bf), secsText(cf), faultText(bf, cf)},
	}
}

// meanSeconds is the mean duration of rows in seconds.
func meanSeconds(rows []Result) float64 {
	if len(rows) == 0 {
		return 0
	}
	var sum int64
	for _, r := range rows {
		sum += r.DurationMs
	}
	return float64(sum) / float64(len(rows)) / 1000
}

// secsText renders a duration with one decimal.
func secsText(v float64) string { return fmt.Sprintf("%.1f", v) }

// relText renders the relative change of two means ("-" when the baseline is 0).
func relText(base, cand float64) string {
	if base == 0 {
		return "-"
	}
	return formatRel(cand/base - 1)
}

// faultText renders the fault-time change as an absolute difference: a fault
// time of 0s on the baseline makes a relative change meaningless, and what
// matters here is how many seconds a side lost to retries.
func faultText(base, cand float64) string {
	if base == 0 && cand == 0 {
		return "-"
	}
	return fmt.Sprintf("%+.1fs", cand-base)
}
