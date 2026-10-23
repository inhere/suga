package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestLoadTaskLongSessionFields covers the long-session task fields: per-turn
// checks, the forced compaction points and the fake model's reply.
func TestLoadTaskLongSessionFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "task.yaml"), `name: long
fake_reply: All good, staying in English.
prompts:
  - first
  - second
  - third
compact_after: [2]
checks:
  - {type: answer_contains, text: English, turn: 1}
  - {type: answer_not_matches, text: '[\p{Han}]'}
  - {type: file_equals, path: notes/plan.md, text: "x"}
`)
	task, err := loadTask(dir)
	assert.NoErr(t, err)
	assert.Eq(t, 3, len(task.Turns()))
	assert.Eq(t, []int{2}, task.CompactAfter)
	assert.Eq(t, "All good, staying in English.", task.FakeReply)
	assert.Eq(t, 1, task.Checks[0].Turn)
	assert.Eq(t, 0, task.Checks[1].Turn)
	assert.Eq(t, true, task.compactsAfter(2))
	assert.Eq(t, false, task.compactsAfter(1))
	assert.Eq(t, false, task.compactsAfter(3))

	t.Run("invalid regex", func(t *testing.T) {
		writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompt: hi\nchecks:\n  - {type: answer_matches, text: 'a('}\n")
		_, err := loadTask(dir)
		assert.Err(t, err)
	})
	t.Run("check turn outside the prompts", func(t *testing.T) {
		writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompts: [a, b]\nchecks:\n  - {type: answer_contains, text: a, turn: 3}\n")
		_, err := loadTask(dir)
		assert.Err(t, err)
	})
	t.Run("compact_after outside the prompts", func(t *testing.T) {
		writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompts: [a, b]\ncompact_after: [3]\n")
		_, err := loadTask(dir)
		assert.Err(t, err)
	})
	t.Run("compact_after repeats a turn", func(t *testing.T) {
		writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompts: [a, b]\ncompact_after: [1, 1]\n")
		_, err := loadTask(dir)
		assert.Err(t, err)
	})
}

// TestRunChecksTurnScoped covers the answer checks of a multi-turn task: a
// check names its turn, and one without a turn judges the final answer.
func TestRunChecksTurnScoped(t *testing.T) {
	answers := []string{"Turn one is done", "完成", "final answer in English"}
	checks := []Check{
		{Type: "answer_contains", Text: "one", Turn: 1},
		{Type: "answer_contains", Text: "final", Turn: 3},
		{Type: "answer_contains", Text: "final"},                 // turn 0 -> last answer
		{Type: "answer_not_matches", Text: `[\p{Han}]`, Turn: 3}, // constraint still held
		{Type: "answer_not_matches", Text: `[\p{Han}]`, Turn: 2}, // Chinese in turn 2
		{Type: "answer_matches", Text: `(?i)^turn`, Turn: 1},
		{Type: "answer_not_contains", Text: "one", Turn: 3},
		{Type: "answer_contains", Text: "missing", Turn: 4}, // no such turn: fails
	}
	got := runChecks(t.TempDir(), answers, checks)
	assert.Eq(t, len(checks), len(got))
	assert.Eq(t, []bool{true, true, true, true, false, true, true, false}, passedOf(got))
	assert.Eq(t, 1, got[0].Turn)
	assert.Eq(t, 3, got[2].Turn) // the final answer's turn
	assert.Eq(t, 4, got[7].Turn)

	t.Run("no answers at all", func(t *testing.T) {
		got := runChecks(t.TempDir(), nil, []Check{{Type: "answer_contains", Text: "x"}})
		assert.Eq(t, false, got[0].Passed)
		assert.StrContains(t, got[0].Detail, "no answer")
	})
}

// passedOf collects the Passed flags of check results.
func passedOf(rs []CheckResult) []bool {
	out := make([]bool, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Passed)
	}
	return out
}

// TestTurnChecksAndReport covers the per-turn report: an answer check belongs to
// its own turn, a file or command check to the last one, and the report shows
// each turn with its compaction plus the long-session pass rate.
func TestTurnChecksAndReport(t *testing.T) {
	r := Result{
		Task: "constraint", Passed: true, DurationMs: 4000,
		Turns: []TurnRun{
			{Turn: 1, Answer: "ok", DurationMs: 1000},
			{Turn: 2, Answer: "ok", DurationMs: 1000, Compacted: &TurnCompaction{TokensBefore: 1800, TokensAfter: 300, Segment: 2, DurationMs: 3200}},
			{Turn: 3, Answer: "ok", DurationMs: 1000},
		},
		Checks: []CheckResult{
			{Type: "answer_not_matches", Text: `[\p{Han}]`, Turn: 1, Passed: true},
			{Type: "answer_not_matches", Text: `[\p{Han}]`, Turn: 3, Passed: false},
			{Type: "file_equals", Path: "notes/plan.md", Passed: true},
		},
	}
	byTurn := turnChecks(r)
	assert.Eq(t, 2, len(byTurn)) // turns 1 and 3 hold checks; the compaction-only turn 2 has none
	assert.Eq(t, 1, len(byTurn[1]))
	assert.Eq(t, 0, len(byTurn[2]))
	assert.Eq(t, 2, len(byTurn[3])) // the failing answer check and the file check
	assert.Eq(t, "PASS", turnStatus(byTurn[2]))
	assert.Eq(t, "FAIL", turnStatus(byTurn[3]))

	var buf bytes.Buffer
	twoTurns := Result{Task: "two-turns", Passed: true, Turns: []TurnRun{{Turn: 1}, {Turn: 2}}}
	printTurnDetail(&buf, []Result{r, {Task: "single-turn", Passed: true}, twoTurns}, false)
	out := buf.String()
	assert.StrContains(t, out, "long sessions (per turn")
	assert.StrContains(t, out, "t1 PASS")
	assert.StrContains(t, out, "t2 PASS  -  [compacted ~1800 -> ~300 tokens, segment 2, 3.2s]")
	assert.StrContains(t, out, "t3 FAIL")
	assert.StrContains(t, out, "file_equals:notes/plan.md=PASS")
	assert.StrContains(t, out, "long sessions: passed 1/1")
	assert.StrNotContains(t, out, "single-turn")
	assert.StrNotContains(t, out, "two-turns") // two turns without a compaction keep the plain table

	t.Run("no long session prints nothing", func(t *testing.T) {
		var quiet bytes.Buffer
		printTurnDetail(&quiet, []Result{{Task: "single-turn", Passed: true}}, false)
		assert.Eq(t, "", quiet.String())
	})
}

// TestLongSessionTasksLoad checks that the shipped long-session tasks parse and
// each force a compaction, so the directory cannot silently lose its design.
func TestLongSessionTasksLoad(t *testing.T) {
	names := []string{
		"longsession-constraint-compact",
		"longsession-topic-switch-back",
		"longsession-quoted-rules",
		"longsession-fact-recall",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			task, err := loadTask(filepath.Join("testdata", "tasks", name))
			if err != nil {
				t.Fatal(err)
			}
			assert.Eq(t, true, len(task.Turns()) >= 3)
			assert.Eq(t, true, len(task.CompactAfter) >= 1)
			assert.Eq(t, true, task.FakeReply != "")
			turnAware := false
			for _, c := range task.Checks {
				turnAware = turnAware || c.Turn > 0
			}
			assert.Eq(t, true, turnAware)
		})
	}
}
