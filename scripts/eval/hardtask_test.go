package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// TestFileMatchesChecks covers the regex file checks: file_matches needs the
// pattern to match somewhere in the file, file_not_matches needs it to match
// nowhere; a missing file and a bad pattern both fail.
func TestFileMatchesChecks(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "notes_index.md"), "notes/session_summary.md\n")
	writeFile(t, filepath.Join(ws, "mixed.md"), "notes/Session_Summary.md\n")
	checks := []Check{
		{Type: "file_matches", Path: "notes_index.md", Text: `(?m)^notes/[a-z0-9_]+\.md$`},
		{Type: "file_not_matches", Path: "notes_index.md", Text: `(?m)^notes/.*[A-Z -]`},
		{Type: "file_matches", Path: "mixed.md", Text: `(?m)^notes/[a-z0-9_]+\.md$`},
		{Type: "file_not_matches", Path: "mixed.md", Text: `(?m)^notes/.*[A-Z -]`},
		{Type: "file_matches", Path: "absent.md", Text: `notes`},
	}
	got := runChecks(ws, nil, checks)
	assert.Eq(t, []bool{true, true, false, false, false}, passedOf(got))
	assert.StrContains(t, got[4].Detail, "absent.md")

	t.Run("bad pattern", func(t *testing.T) {
		got := runChecks(ws, nil, []Check{{Type: "file_matches", Path: "notes_index.md", Text: `notes/(`}})
		assert.Eq(t, false, got[0].Passed)
		assert.StrContains(t, got[0].Detail, "error parsing regexp")
	})
	t.Run("loading rejects an empty or invalid pattern", func(t *testing.T) {
		dir := t.TempDir()
		for _, text := range []string{`''`, `'notes/('`} {
			writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompt: hi\nchecks:\n  - {type: file_not_matches, path: a.md, text: "+text+"}\n")
			_, err := loadTask(dir)
			assert.Err(t, err)
		}
	})
}

// TestLongSessionHardTasksLoad checks that the hard long-session tasks keep
// their design: long enough to grow the context (>= 12 turns), at least two
// forced compactions, per-turn checks, a deterministic fake reply and (for the
// naming task) the regex file checks.
func TestLongSessionHardTasksLoad(t *testing.T) {
	names := []string{
		"longsession-hard-naming-rules",
		"longsession-hard-rule-update",
		"longsession-hard-topic-cycles",
		"longsession-hard-distractor-rules",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			task, err := loadTask(filepath.Join("testdata", "tasks", name))
			if err != nil {
				t.Fatal(err)
			}
			assert.Eq(t, true, len(task.Turns()) >= 12)
			assert.Eq(t, true, len(task.CompactAfter) >= 2)
			assert.Eq(t, true, task.FakeReply != "")
			assert.Eq(t, "allow-write", task.Mode)
			assert.Eq(t, true, task.timeout >= 20*time.Minute)
			turnAware, positive, negative := 0, 0, 0
			for _, c := range task.Checks {
				if c.Turn > 0 {
					turnAware++
				}
				if strings.HasPrefix(c.Type, "answer_contains") || strings.HasPrefix(c.Type, "answer_matches") || strings.Contains(c.Type, "file_") {
					positive++
				}
				if strings.Contains(c.Type, "not_") {
					negative++
				}
			}
			assert.Eq(t, true, turnAware >= 3) // judged on specific turns, not only the last
			assert.Eq(t, true, negative >= 1)  // a "must not" rule somewhere
			assert.Eq(t, true, positive >= 3)  // and enough positive evidence
		})
	}

	t.Run("naming rules use the regex file checks", func(t *testing.T) {
		task, err := loadTask(filepath.Join("testdata", "tasks", "longsession-hard-naming-rules"))
		assert.NoErr(t, err)
		kinds := map[string]bool{}
		for _, c := range task.Checks {
			kinds[c.Type] = true
		}
		assert.True(t, kinds["file_matches"])
		assert.True(t, kinds["file_not_matches"])
		assert.True(t, kinds["answer_not_matches"])
	})
}

// TestHardTaskFixturesMatchTheirFileChecks covers the hand-copied fixtures: the
// file checks that must hold before the agent touches anything (file_equals /
// file_contains) pass on the untouched fixture, so a task.yaml cannot drift from
// the file it pins.
func TestHardTaskFixturesMatchTheirFileChecks(t *testing.T) {
	for _, name := range []string{
		"longsession-hard-naming-rules",
		"longsession-hard-rule-update",
		"longsession-hard-topic-cycles",
		"longsession-hard-distractor-rules",
	} {
		t.Run(name, func(t *testing.T) {
			task, err := loadTask(filepath.Join("testdata", "tasks", name))
			if err != nil {
				t.Fatal(err)
			}
			ws := t.TempDir()
			assert.NoErr(t, copyDir(filepath.Join("testdata", "tasks", name, "fixture"), ws))
			var checks []Check
			for _, c := range task.Checks {
				if c.Type == "file_equals" || c.Type == "file_contains" {
					checks = append(checks, c)
				}
			}
			assert.True(t, len(checks) > 0, "the task pins no fixture file")
			for _, r := range runChecks(ws, nil, checks) {
				assert.True(t, r.Passed, r.Type+" "+r.Path+" "+r.Detail)
			}
		})
	}
}

// TestRunTaskRecordsResolvedModel drives a whole run without --model: the stub
// agent answers `suga config get model`, and the result plus the table name the
// model the config resolved to instead of leaving the field empty (suga-h5vn).
func TestRunTaskRecordsResolvedModel(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	stubDir := t.TempDir()
	stubSrc := filepath.Join(stubDir, "stub.go")
	writeFile(t, stubSrc, `package main

import (
	"fmt"
	"os"
)

// The agent under test: it answers the run, the config lookup and the stats
// commands of the runner.
func main() {
	if len(os.Args) > 2 && os.Args[1] == "config" && os.Args[2] == "get" {
		fmt.Println("model = gateway/deepseek-v4.1-flash")
		fmt.Println("source = config: <home>/config.yaml")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "run" {
		fmt.Println("{\"type\":\"result\",\"result\":\"done\"}")
		return
	}
	fmt.Println("{}")
}
`)
	stub := filepath.Join(stubDir, "stub")
	if runtime.GOOS == "windows" {
		stub += ".exe"
	}
	build := exec.Command("go", "build", "-o", stub, stubSrc)
	build.Dir = stubDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v: %s", err, out)
	}

	taskDir := t.TempDir()
	writeFile(t, filepath.Join(taskDir, "fixture", "a.txt"), "x\n")
	task := &Task{Name: "model", Prompt: "do it", dir: taskDir, Checks: []Check{
		{Type: "answer_contains", Text: "done"},
	}}
	configFrom := t.TempDir()
	writeFile(t, filepath.Join(configFrom, "config.yaml"), "model: gateway/deepseek-v4.1-flash\n")
	o := &options{agent: "suga", out: t.TempDir(), configFrom: configFrom, timeout: 2 * time.Minute}

	r, err := runTask(o, stub, "testrun", task, 1, nil)
	assert.NoErr(t, err)
	assert.Eq(t, "gateway/deepseek-v4.1-flash", r.Model)
	assert.True(t, r.Passed, r.Checks)

	var buf bytes.Buffer
	printTable(&buf, []Result{r}, false)
	assert.True(t, strings.Contains(buf.String(), "MODEL") && strings.Index(buf.String(), "AGENT") < strings.Index(buf.String(), "MODEL"), buf.String())
	assert.StrContains(t, buf.String(), "gateway/deepseek-v4.1-flash")

	t.Run("--model wins", func(t *testing.T) {
		o2 := &options{agent: "suga", out: t.TempDir(), model: "explicit/m2", configFrom: configFrom, timeout: 2 * time.Minute}
		r2, err := runTask(o2, stub, "testrun", task, 1, nil)
		assert.NoErr(t, err)
		assert.Eq(t, "explicit/m2", r2.Model)
	})

	t.Run("unreadable config leaves it empty", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "no-such-suga")
		assert.Eq(t, "", resolvedModel(context.Background(), missing, t.TempDir(), nil))
	})
}
