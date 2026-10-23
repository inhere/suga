package main

import (
	"fmt"
	"io"
	"math"
)

// Stat is the mean and the range (min-max) of one metric over the rounds of a
// task in a --repeat run.
type Stat struct {
	Mean, Min, Max float64
}

// statOf summarises vals; an empty slice gives a zero Stat.
func statOf(vals []float64) Stat {
	if len(vals) == 0 {
		return Stat{}
	}
	s := Stat{Min: vals[0], Max: vals[0]}
	var sum float64
	for _, v := range vals {
		sum += v
		s.Min, s.Max = math.Min(s.Min, v), math.Max(s.Max, v)
	}
	s.Mean = sum / float64(len(vals))
	return s
}

// formatCount renders a count statistic as mean with its range: "4.0 (3-5)".
func formatCount(s Stat) string { return fmt.Sprintf("%.1f (%.0f-%.0f)", s.Mean, s.Min, s.Max) }

// formatTokens renders a token statistic as mean with its range: "8200 (7000-9400)".
func formatTokens(s Stat) string { return fmt.Sprintf("%.0f (%.0f-%.0f)", s.Mean, s.Min, s.Max) }

// formatSeconds renders a duration statistic as mean with its range: "12.3s (10.0-14.5s)".
func formatSeconds(s Stat) string { return fmt.Sprintf("%.1fs (%.1f-%.1fs)", s.Mean, s.Min, s.Max) }

// countCell renders a count for n results: the mean alone for a single run, the
// mean with its range for a repeated task.
func countCell(n int, s Stat) string {
	if n > 1 {
		return formatCount(s)
	}
	return fmt.Sprintf("%.0f", s.Mean)
}

// tokensCell is countCell for token statistics (no decimals).
func tokensCell(n int, s Stat) string {
	if n > 1 {
		return formatTokens(s)
	}
	return fmt.Sprintf("%.0f", s.Mean)
}

// secondsCell is countCell for a duration in seconds.
func secondsCell(n int, s Stat) string {
	if n > 1 {
		return formatSeconds(s)
	}
	return fmt.Sprintf("%.1fs", s.Mean)
}

// roundStats aggregates the rounds of one task: how often it passed plus the
// mean and range of every metric the summary reports.
type roundStats struct {
	Rounds, Passed                                   int
	Calls, ToolCalls, Prompt, Uncached, Out, Seconds Stat
}

// statRounds aggregates the rounds of one task (one results.jsonl line each).
func statRounds(rs []Result) roundStats {
	st := roundStats{Rounds: len(rs)}
	calls := make([]float64, 0, len(rs))
	tools := make([]float64, 0, len(rs))
	prompt := make([]float64, 0, len(rs))
	uncached := make([]float64, 0, len(rs))
	out := make([]float64, 0, len(rs))
	seconds := make([]float64, 0, len(rs))
	for _, r := range rs {
		if r.Passed {
			st.Passed++
		}
		calls = append(calls, float64(r.Calls))
		tools = append(tools, float64(r.ToolCalls))
		prompt = append(prompt, float64(r.InputTokens+r.CacheRead+r.CacheWrite))
		uncached = append(uncached, float64(r.InputTokens))
		out = append(out, float64(r.OutputToks))
		seconds = append(seconds, float64(r.DurationMs)/1000)
	}
	st.Calls, st.ToolCalls = statOf(calls), statOf(tools)
	st.Prompt, st.Uncached = statOf(prompt), statOf(uncached)
	st.Out, st.Seconds = statOf(out), statOf(seconds)
	return st
}

// resultLabel is the TASK cell of one result row: the task name, suffixed with
// -r<i> for a repeated run (the round is then a column of its own).
func resultLabel(r Result, repeated bool) string {
	if repeated {
		return fmt.Sprintf("%s-r%d", r.Task, r.Repeat)
	}
	return r.Task
}

// fillBreakdown records the wall-clock split of a run's DurationMs: TurnMs sums
// the turn durations (suga's per-turn `run` calls), CompactionMs the forced
// segment breaks (`suga compact`) and OverheadMs what the runner spent between
// them. Preparation and judging happen outside DurationMs, so they are not in
// the split. An agent that keeps no per-turn record (omp, claude) reports its
// whole run as turn time.
func fillBreakdown(r *Result) {
	var turnMs, compMs int64
	for _, t := range r.Turns {
		turnMs += t.DurationMs
		if t.Compacted != nil {
			compMs += t.Compacted.DurationMs
		}
	}
	if len(r.Turns) == 0 {
		turnMs = r.DurationMs
	}
	r.TurnMs, r.CompactionMs = turnMs, compMs
	if over := r.DurationMs - turnMs - compMs; over > 0 {
		r.OverheadMs = over
	}
}

// avgLabel is the TASK cell of a task's --repeat summary row.
func avgLabel(r Result) string {
	if r.Variant != "" {
		return fmt.Sprintf("%s [%s] (avg)", r.Task, r.Variant)
	}
	return r.Task + " (avg)"
}

// roundGroup returns the rows one avg row summarises: the same task and variant
// around i (the rounds of a --repeat run are consecutive).
func roundGroup(rs []Result, i int) []Result {
	same := func(r Result) bool { return r.Task == rs[i].Task && r.Variant == rs[i].Variant }
	start, end := i, i
	for start > 0 && same(rs[start-1]) {
		start--
	}
	for end+1 < len(rs) && same(rs[end+1]) {
		end++
	}
	return rs[start : end+1]
}

// meanTiming returns the mean turn, compaction and provider-fault time (in
// seconds) of rs, for the compare report.
func meanTiming(rs []Result) (turn, compact, fault float64) {
	if len(rs) == 0 {
		return 0, 0, 0
	}
	var t, c, f int64
	for _, r := range rs {
		t, c, f = t+r.TurnMs, c+r.CompactionMs, f+r.ProviderFaultMs
	}
	n := float64(len(rs))
	return float64(t) / n / 1000, float64(c) / n / 1000, float64(f) / n / 1000
}

// secs is a duration in milliseconds as seconds for the reports.
func secs(ms int64) float64 { return float64(ms) / 1000 }

// printTiming writes the timing split of every run: turn time plus the forced
// compactions plus the runner's overhead add up to duration_ms, while the
// provider faults are a part of the turn time (a retry or a switch to another
// provider happens inside a turn, so it is counted, not added).
func printTiming(w io.Writer, rs []Result, repeated bool) {
	if len(rs) == 0 {
		return
	}
	fmt.Fprintln(w, "\ntiming (turn + compaction + overhead = duration; provider faults are inside turns):")
	for _, r := range rs {
		fmt.Fprintf(w, "  %s  turn %.1fs  compaction %.1fs  fault %.1fs  overhead %.1fs  = %.1fs\n",
			resultLabel(r, repeated), secs(r.TurnMs), secs(r.CompactionMs), secs(r.ProviderFaultMs), secs(r.OverheadMs), secs(r.DurationMs))
	}
}
