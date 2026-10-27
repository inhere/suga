package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// forceCompact starts a new segment of the session between two turns by
// running `suga session compact <session> --json` in the task workspace: the forced
// context compaction of a task's compact_after turn.
func forceCompact(ctx context.Context, bin, sid, ws string, env []string) (TurnCompaction, error) {
	out, err := runCmd(ctx, bin, []string{"session", "compact", sid, "--json"}, ws, env)
	if err != nil {
		return TurnCompaction{}, err
	}
	var comp TurnCompaction
	if err := json.Unmarshal(bytes.TrimSpace(out), &comp); err != nil {
		return TurnCompaction{}, fmt.Errorf("compact %s: %w: %s", sid, err, tailString(string(out), 300))
	}
	return comp, nil
}

// turnChecks attributes every check result to the turn it judges: an answer
// check names its own turn, the workspace checks (files, commands) run once at
// the end and belong to the last turn. The key is 1-based.
func turnChecks(r Result) map[int][]CheckResult {
	out := map[int][]CheckResult{}
	last := len(r.Turns)
	if last == 0 {
		last = 1
	}
	for _, c := range r.Checks {
		turn := c.Turn
		if turn <= 0 {
			turn = last
		}
		out[turn] = append(out[turn], c)
	}
	return out
}

// turnStatus is PASS when every check of the turn passed (a turn without a
// check counts as PASS: it was not judged).
func turnStatus(checks []CheckResult) string {
	if allPassed(checks) {
		return "PASS"
	}
	return "FAIL"
}

// longSession reports whether a run is judged as a long session: it has a
// forced compaction or at least three turns (a two-turn task such as
// read-then-edit keeps the plain table output).
func longSession(r Result) bool {
	return hasCompaction(r) || len(r.Turns) >= 3
}

// printTurnDetail writes the long-session report: one block per long session,
// one line per turn with its checks and its compaction (tokens before ->
// after and how long the break cost), then the pass rate over those tasks. A
// run without a long session prints nothing.
func printTurnDetail(w io.Writer, rs []Result, repeated bool) {
	var long []Result
	for _, r := range rs {
		if longSession(r) {
			long = append(long, r)
		}
	}
	if len(long) == 0 {
		return
	}
	byTurn := make(map[string]map[int][]CheckResult, len(long))
	fmt.Fprintln(w, "\nlong sessions (per turn; a check without a turn is judged on the last turn):")
	passed := 0
	for _, r := range long {
		checks := turnChecks(r)
		byTurn[r.Task] = checks
		status := "FAIL"
		if r.Passed {
			status = "PASS"
			passed++
		}
		compactions := 0
		for _, t := range r.Turns {
			if t.Compacted != nil {
				compactions++
			}
		}
		fmt.Fprintf(w, "  %s  %s  (%d turns, %d compactions, %.1fs, %d checks)\n",
			runLabel(r, repeated), status, len(r.Turns), compactions, float64(r.DurationMs)/1000, len(r.Checks))
		for _, t := range r.Turns {
			ts := turnStatus(checks[t.Turn])
			fmt.Fprintf(w, "    t%d %s  %s", t.Turn, ts, checkSummary(checks[t.Turn]))
			if t.Compacted != nil {
				fmt.Fprintf(w, "  [compacted ~%d -> ~%d tokens, segment %d, %.1fs]",
					t.Compacted.TokensBefore, t.Compacted.TokensAfter, t.Compacted.Segment, secs(t.Compacted.DurationMs))
			}
			fmt.Fprintf(w, "  (%.1fs)\n", float64(t.DurationMs)/1000)
		}
		if r.Error != "" {
			fmt.Fprintf(w, "    error: %s\n", firstLine(r.Error))
		}
	}
	fmt.Fprintf(w, "long sessions: passed %d/%d\n", passed, len(long))
}

// hasCompaction reports whether any turn of the run was followed by a forced
// context compaction.
func hasCompaction(r Result) bool {
	for _, t := range r.Turns {
		if t.Compacted != nil {
			return true
		}
	}
	return false
}

// answersOf wraps the single final answer of an agent whose event stream keeps
// no per-turn record (omp, claude) into the answers list the checks take.
func answersOf(answer string) []string {
	if answer == "" {
		return nil
	}
	return []string{answer}
}

// checkSummary lists the checks of one turn as "type:text=PASS" (the text is
// shortened), or "-" when the turn was not judged.
func checkSummary(checks []CheckResult) string {
	if len(checks) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(checks))
	for _, c := range checks {
		label := c.Type
		if c.Path != "" {
			label += ":" + c.Path
		}
		if c.Text != "" {
			label += ":" + truncArgs(singleLine(c.Text), 24)
		}
		if c.Passed {
			label += "=PASS"
		} else {
			label += "=FAIL"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " ")
}

// firstLine returns the first line of s (an error message can carry the tail
// of a command's stderr).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// singleLine folds the line breaks and runs of spaces of s into one space, so a
// check's text (a file fragment can be multi-line) stays on one report line.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
