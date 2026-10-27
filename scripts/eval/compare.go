package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeCompare writes <out>/compare.md and prints it to stdout: per-variant
// totals first, then one line per task with every variant's outcome side by
// side; tasks whose variants disagree are marked.
func writeCompare(out string, variants []*Variant, rs []Result) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# 评测对比（variants）\n\n生成：`--variant` 对比，%d 个 variant，共 %d 条结果。状态含义:\nPASS/FAIL=通过/失败判定；TIMEOUT=运行器侧超时（error 含 deadline）；p/n=多轮部分通过。\n\n",
		len(variants), len(rs))

	// Per-variant totals. The hit rates are pooled over the variant's runs:
	// 缓存命中% counts every call, steady 命中% only the steady bucket (an unchanged
	// context inside one segment, the 95% target), so a low overall rate can be
	// told apart from a low in-context one.
	fmt.Fprintf(&b, "## variant 总览\n\n")
	fmt.Fprintln(&b, "| variant | 通过 | 平均 CALLS | 平均 prompt(in+缓存) | 平均未缓存输入 | 总 prompt | 缓存命中% | steady 命中% | steady 复用% | 平均耗时 | 平均 turn/compact/fault | WEB |")
	fmt.Fprintln(&b, "|---|---|---|---|---|---|---|---|---|---|---|---|")
	for _, v := range variants {
		sub := variantResults(rs, v.Name)
		if len(sub) == 0 {
			continue
		}
		a := averageRounds(sub)
		total, hit := totals(sub)
		turn, compact, fault := meanTiming(sub)
		fmt.Fprintf(&b, "| %s | %d/%d | %.1f | %.0f | %.0f | %.0f | %.1f | %s | %s | %.1fs | %.1f/%.1f/%.1fs | %s |\n",
			v.Name, a.Passed, a.Rounds, a.Calls, a.Prompt, a.Uncached, total, hit*100, steadyCell(sub),
			reuseCell(poolBuckets(sub), "steady"), a.Seconds, turn, compact, fault, webLabel(sub))
	}
	fmt.Fprintln(&b)
	writeCompareBuckets(&b, variants, rs)

	// One line per task, variants side by side.
	fmt.Fprintf(&b, "## 逐任务对比\n\n")

	b.WriteString("| task |")
	for _, v := range variants {
		fmt.Fprintf(&b, " %s |", v.Name)
	}
	b.WriteString(" 结果不一致 |\n|---|")
	for range variants {
		b.WriteString("---|")
	}
	b.WriteString("---|\n")

	tasks := taskOrder(rs)
	for _, t := range tasks {
		cells := make([]string, 0, len(variants))
		mark := false
		firstStatus := ""
		for _, v := range variants {
			s, cell := taskCell(rs, v.Name, t)
			if s == "" { // no result row: keep the table aligned
				cell = "-"
			} else if firstStatus == "" {
				firstStatus = s
			} else if s != firstStatus {
				mark = true
			}
			cells = append(cells, cell)
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", t, strings.Join(cells, " | "), yesNo(mark))
	}

	path := filepath.Join(out, "compare.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("\n%s\n", b.String())
	fmt.Printf("compare: %s\n", path)
	return nil
}

// steadyCell is the pooled hit rate of the steady bucket of rs ("-" when no
// row carries bucket data or the bucket reported no cache tokens).
func steadyCell(rs []Result) string {
	for _, b := range poolBuckets(rs) {
		if b.Bucket == "steady" && b.CacheReported && b.prompt() > 0 {
			return fmt.Sprintf("%.1f", b.Hit*100)
		}
	}
	return "-"
}

// writeCompareBuckets appends the per-bucket hit rate of every variant, pooled
// over its runs, one column per variant: the steady bucket beside the overall
// sum shows whether a variant lost its cache inside a segment or at the breaks.
func writeCompareBuckets(b *strings.Builder, variants []*Variant, rs []Result) {
	buckets := poolBuckets(rs)
	if len(buckets) == 0 {
		return
	}
	fmt.Fprintf(b, "## 按桶命中率（steady = 段内未变上下文，目标 95%%）\n\n")
	b.WriteString("| bucket |")
	for _, v := range variants {
		fmt.Fprintf(b, " %s |", v.Name)
	}
	b.WriteString("\n|---|")
	for range variants {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, name := range cacheBucketNames() {
		present := false
		cells := make([]string, 0, len(variants))
		for _, v := range variants {
			cell := bucketCellFor(variantResults(rs, v.Name), name)
			present = present || cell != "-"
			cells = append(cells, cell)
		}
		if present {
			fmt.Fprintf(b, "| %s | %s |\n", name, strings.Join(cells, " | "))
		}
	}
	// The overall row over every call, so the steady bucket has its reference.
	cells := make([]string, 0, len(variants))
	for _, v := range variants {
		cells = append(cells, bucketCellFor(variantResults(rs, v.Name), "overall"))
	}
	fmt.Fprintf(b, "| overall | %s |\n\n", strings.Join(cells, " | "))
	writeCompareReuse(b, variants, rs)
}

// writeCompareReuse appends the prefix reuse rate per bucket of every variant:
// cache read over the cacheable prefix of the previous requests, which does not
// depend on how much a call added. A steady bucket far below 100% is a real
// cache miss, wherever the hit rate of a single run lands.
func writeCompareReuse(b *strings.Builder, variants []*Variant, rs []Result) {
	if len(poolBuckets(rs)) == 0 {
		return
	}
	fmt.Fprintf(b, "## 按桶前缀复用率（复用 = cache read / 上次可缓存前缀，目标 steady 95%%+）\n\n")
	b.WriteString("| bucket |")
	for _, v := range variants {
		fmt.Fprintf(b, " %s |", v.Name)
	}
	b.WriteString("\n|---|")
	for range variants {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, name := range cacheBucketNames() {
		present := false
		cells := make([]string, 0, len(variants))
		for _, v := range variants {
			cell := reuseCell(poolBuckets(variantResults(rs, v.Name)), name)
			present = present || cell != "-"
			cells = append(cells, cell)
		}
		if present {
			fmt.Fprintf(b, "| %s | %s |\n", name, strings.Join(cells, " | "))
		}
	}
	cells := make([]string, 0, len(variants))
	for _, v := range variants {
		cells = append(cells, reuseCell(poolBuckets(variantResults(rs, v.Name)), "overall"))
	}
	fmt.Fprintf(b, "| overall | %s |\n\n", strings.Join(cells, " | "))
}

// bucketCellFor renders one pooled bucket of rs: the hit rate as a percentage,
// "-" when the rows carry no such bucket or it reported no cache tokens.
func bucketCellFor(rs []Result, name string) string {
	for _, b := range poolBuckets(rs) {
		if b.Bucket != name {
			continue
		}
		if !b.CacheReported || b.prompt() == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1f", b.Hit*100)
	}
	return "-"
}

// webLabel summarises the Result.Web values of one variant's rows ("off",
// "on", "unsupported", joined with +) so compare.md records whether the run
// had web access; "-" when nothing is recorded.
func webLabel(rs []Result) string {
	var parts []string
	for _, value := range []string{webOff, webOn, webUnsupported} {
		for _, r := range rs {
			if r.Web == value {
				parts = append(parts, value)
				break
			}
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "+")
}

// variantResults keeps the result rows of one variant, in run order.
func variantResults(rs []Result, name string) []Result {
	var sub []Result
	for _, r := range rs {
		if r.Variant == name {
			sub = append(sub, r)
		}
	}
	return sub
}

// totals sums all prompt tokens (uncached + cache read/write) and averages
// the per-run cache hit rate across the given rows.
func totals(rs []Result) (totalPrompt float64, avgHit float64) {
	for _, r := range rs {
		totalPrompt += float64(r.InputTokens + r.CacheRead + r.CacheWrite)
		avgHit += r.CacheHit
	}
	if n := float64(len(rs)); n > 0 {
		avgHit /= n
	}
	return totalPrompt, avgHit
}

// taskOrder lists the distinct tasks of rs in first-seen order.
func taskOrder(rs []Result) []string {
	var order []string
	seen := map[string]bool{}
	for _, r := range rs {
		if !seen[r.Task] {
			seen[r.Task] = true
			order = append(order, r.Task)
		}
	}
	return order
}

// resultStatus classifies one row: pass, fail or timeout.
func resultStatus(r Result) string {
	if r.Passed {
		return "pass"
	}
	if strings.Contains(r.Error, "deadline") {
		return "timeout"
	}
	return "fail"
}

// taskCell renders one (variant, task) cell: status (+ pass counts when
// repeated), calls, prompt tokens and time; a repeated task shows the mean and
// the min-max of its rounds (the variant overview above only states the mean
// over all its rows, tasks and rounds mixed).
func taskCell(rs []Result, variant, task string) (status, cell string) {
	var sub []Result
	for _, r := range rs {
		if r.Variant == variant && r.Task == task {
			sub = append(sub, r)
		}
	}
	if len(sub) == 0 {
		return "", ""
	}
	status = resultStatus(sub[0])
	label := status
	if len(sub) > 1 {
		pass := 0
		for _, r := range sub {
			if r.Passed {
				pass++
			}
		}
		label = fmt.Sprintf("%d/%d %s", pass, len(sub), status)
	}
	// A repeated task shows mean and range, a single run its plain value.
	s := statRounds(sub)
	total, _ := totals(sub)
	return status, fmt.Sprintf("%s c=%s p=%.0f %s", label, countCell(len(sub), s.Calls), total, secondsCell(len(sub), s.Seconds))
}

func yesNo(b bool) string {
	if b {
		return "**是**"
	}
	return "-"
}
