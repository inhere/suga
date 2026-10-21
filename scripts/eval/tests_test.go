package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Stat(p)
	return err == nil
}

// TestIsTestFile: the sweep has to recognise the test-file naming of the
// languages eval tasks use, and nothing else.
func TestIsTestFile(t *testing.T) {
	cases := map[string]bool{
		"stats_test.go":            true,
		"test_stats.py":            true,
		"stats_test.py":            true,
		"StatsTest.java":           true,
		"StatsTests.kt":            true,
		"stats_test.php":           true,
		"stats_test.rs":            true,
		"stats_spec.rb":            true,
		"stats.test.ts":            true,
		"stats.spec.js":            true,
		"StatsTest.cs":             true,
		"stats.go":                 false,
		"stats.py":                 false,
		"contest.java":             false,
		"stats_test.go.bak":        false,
		"README.md":                false,
		"TestingStrategy.md":       false,
		"testdata/input.txt":       false,
		"stats_benchmark_test.go2": false,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Eq(t, want, isTestFile(name))
		})
	}
}

// TestRemoveForeignTestsKeepsTaskAndInjectedFiles: the sweep deletes only the
// test files the agent added (any depth, .git aside) and keeps the task's own
// files, the acceptance injection and the agent's implementation files.
func TestRemoveForeignTestsKeepsTaskAndInjectedFiles(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "stats.go"), "package stats\n")
	writeFile(t, filepath.Join(ws, "stats_test.go"), "package stats\n")
	writeFile(t, filepath.Join(ws, ".git", "objects", "ab_test.go"), "not a test file of the run\n")
	before := snapshotWorkspace(ws)

	writeFile(t, filepath.Join(ws, "zz_local_verify_test.go"), "package stats\n")
	writeFile(t, filepath.Join(ws, "helper.go"), "package stats\n")
	writeFile(t, filepath.Join(ws, "sub", "more_test.go"), "package sub\n")
	writeFile(t, filepath.Join(ws, "cases_test.go"), "package stats\n") // injected acceptance

	removed := removeForeignTests(ws, before, map[string]bool{"cases_test.go": true})
	assert.Eq(t, []string{"sub/more_test.go", "zz_local_verify_test.go"}, removed)
	for _, keep := range []string{"stats.go", "stats_test.go", "helper.go", "cases_test.go", filepath.Join(".git", "objects", "ab_test.go")} {
		assert.True(t, exists(t, filepath.Join(ws, keep)), keep)
	}
	for _, gone := range removed {
		assert.False(t, exists(t, filepath.Join(ws, filepath.FromSlash(gone))), gone)
	}
}

// TestFinishScoresOnlyAcceptanceTests is the end-to-end of the rule: the agent
// left a self-written test that does not compile, the injected acceptance test
// passes, so the command check passes.
func TestFinishScoresOnlyAcceptanceTests(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "go.mod"), "module evalfixture/score\n\ngo 1.21\n")
	writeFile(t, filepath.Join(ws, "x.go"), "package score\n\n// X is the implementation the acceptance test checks.\nfunc X() int { return 1 }\n")
	before := snapshotWorkspace(ws)
	writeFile(t, filepath.Join(ws, "zz_local_verify_test.go"), "package score\n\nfunc TestLocal(t *testing.T) { X(1) }\n")

	taskDir := t.TempDir()
	writeFile(t, filepath.Join(taskDir, "acc", "zz_acc_test.go"), `package score

import "testing"

func TestAccept(t *testing.T) {
	if X() != 1 {
		t.Fatalf("X() = %d", X())
	}
}
`)
	task := &Task{dir: taskDir, Acceptance: "acc", Checks: []Check{
		{Type: "command", Args: []string{"go", "test", "-count=1", "./..."}, Timeout: "2m"},
	}}
	var r Result
	task.finish(ws, before, "", &r)
	assert.Eq(t, []string{"zz_local_verify_test.go"}, r.RemovedTests)
	assert.Eq(t, 1, len(r.Checks))
	assert.True(t, r.Checks[0].Passed, r.Checks[0].Output)
}

// TestFinishKeepTests: a task whose checks judge the test file the agent wrote
// sets keep_tests, and then nothing is removed.
func TestFinishKeepTests(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "x.go"), "package x\n")
	before := snapshotWorkspace(ws)
	writeFile(t, filepath.Join(ws, "config_test.go"), "package x\n")
	task := &Task{dir: t.TempDir(), KeepTests: true, Checks: []Check{
		{Type: "file_contains", Path: "config_test.go", Text: "package x"},
	}}
	var r Result
	task.finish(ws, before, "", &r)
	assert.Eq(t, 0, len(r.RemovedTests))
	assert.True(t, allPassed(r.Checks))
}

// TestSnapshotWorkspace: the snapshot lists slash-separated relative paths, so
// it compares with the paths the sweep records on every OS.
func TestSnapshotWorkspace(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.txt"), "a")
	writeFile(t, filepath.Join(ws, "sub", "b.go"), "package sub\n")
	assert.Eq(t, map[string]bool{"a.txt": true, "sub/b.go": true}, snapshotWorkspace(ws))
}

// TestRunTaskRemovesAgentTests drives one whole suga task: a stub binary plays
// the agent (it writes a self-written test that does not compile), then the
// runner injects the acceptance test, sweeps the agent's test and judges with a
// real `go test`.
func TestRunTaskRemovesAgentTests(t *testing.T) {
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

// The agent under test: it leaves a self-written test behind and answers.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		os.WriteFile("zz_local_verify_test.go", []byte("package score\n\nimport \"testing\"\n\nfunc TestLocal(t *testing.T) { X(1) }\n"), 0o644)
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
	writeFile(t, filepath.Join(taskDir, "fixture", "go.mod"), "module evalfixture/score\n\ngo 1.21\n")
	writeFile(t, filepath.Join(taskDir, "fixture", "x.go"), "package score\n\n// X is what the acceptance test checks.\nfunc X() int { return 1 }\n")
	writeFile(t, filepath.Join(taskDir, "acceptance", "zz_acc_test.go"), `package score

import "testing"

func TestAccept(t *testing.T) {
	if X() != 1 {
		t.Fatalf("X() = %d", X())
	}
}
`)
	task := &Task{Name: "score", Mode: "allow-write", Prompt: "fix it", Acceptance: "acceptance", dir: taskDir, Checks: []Check{
		{Type: "command", Args: []string{"go", "test", "-count=1", "./..."}, Timeout: "2m"},
	}}
	configFrom := t.TempDir()
	writeFile(t, filepath.Join(configFrom, "config.yaml"), "model: fake/m1\n")
	o := &options{agent: "suga", out: t.TempDir(), model: "fake/m1", configFrom: configFrom, timeout: 2 * time.Minute}

	r, err := runTask(o, stub, "testrun", task, 1, nil)
	assert.NoErr(t, err)
	assert.Eq(t, []string{"zz_local_verify_test.go"}, r.RemovedTests)
	assert.Eq(t, webOff, r.Web)
	assert.Eq(t, "done", r.Answer)
	assert.True(t, r.Passed, r.Checks)
	// The web policy landed in the SUGA_HOME config suga would read: the general
	// tools.disabled setting, so the tools are not registered at all.
	raw, err := os.ReadFile(filepath.Join(o.out, "score", "home", "config.yaml"))
	assert.NoErr(t, err)
	assert.StrContains(t, string(raw), "web_fetch")
	assert.StrContains(t, string(raw), "web_search")
	assert.StrContains(t, string(raw), "disabled")
}
