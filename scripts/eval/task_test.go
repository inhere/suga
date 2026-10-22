package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Dir(p), 0o755))
	assert.NoErr(t, os.WriteFile(p, []byte(s), 0o644))
}

func TestLoadTaskNewFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "task.yaml"), `name: x
prompt: hi
timeout: 20m
acceptance: acc
checks:
  - {type: command, args: [go, version], timeout: 1m}
`)
	task, err := loadTask(dir)
	assert.NoErr(t, err)
	assert.Eq(t, 20*time.Minute, task.effectiveTimeout(time.Minute))
	assert.Eq(t, "acc", task.Acceptance)
	assert.Eq(t, []string{"go", "version"}, task.Checks[0].Args)

	writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompt: hi\n")
	task, err = loadTask(dir)
	assert.NoErr(t, err)
	assert.Eq(t, time.Minute, task.effectiveTimeout(time.Minute))

	writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompt: hi\ntimeout: soon\n")
	_, err = loadTask(dir)
	assert.Err(t, err)
	writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompt: hi\nchecks:\n  - {type: command}\n")
	_, err = loadTask(dir)
	assert.Err(t, err)
	writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompt: hi\nacceptance: ../out\n")
	_, err = loadTask(dir)
	assert.Err(t, err)
}

func TestLongCodingTaskLoads(t *testing.T) {
	task, err := loadTask(filepath.Join("testdata", "tasks", "long-coding-schedule"))
	assert.NoErr(t, err)
	assert.Eq(t, 25*time.Minute, task.effectiveTimeout(time.Minute))
	_, err = os.Stat(filepath.Join(task.dir, task.Acceptance, "schedule", "zz_acceptance_test.go"))
	assert.NoErr(t, err)
}

func TestAcceptanceInjectionAndCommandCheck(t *testing.T) {
	taskDir, ws := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(taskDir, "acc", "sub", "a.txt"), "from-acceptance")
	writeFile(t, filepath.Join(ws, "sub", "a.txt"), "agent-version")
	writeFile(t, filepath.Join(ws, "keep.txt"), "keep")
	task := &Task{
		dir:        taskDir,
		Acceptance: "acc",
		Checks: []Check{
			{Type: "file_equals", Path: "sub/a.txt", Text: "from-acceptance"},
			{Type: "file_contains", Path: "keep.txt", Text: "keep"},
			{Type: "command", Args: []string{"go", "env", "GOOS"}},
			{Type: "command", Args: []string{"go", "nosuchsubcommand"}},
			{Type: "command", Args: []string{"definitely-not-a-binary-xyz"}},
		},
	}
	var r Result
	task.finish(ws, nil, nil, &r)
	assert.Eq(t, 5, len(r.Checks))
	assert.True(t, r.Checks[0].Passed)
	assert.True(t, r.Checks[1].Passed)

	ok := r.Checks[2]
	assert.True(t, ok.Passed)
	assert.Eq(t, 0, *ok.ExitCode)
	assert.Eq(t, runtime.GOOS, ok.Output)
	assert.Eq(t, "go env GOOS", ok.Text)

	bad := r.Checks[3]
	assert.False(t, bad.Passed)
	assert.True(t, *bad.ExitCode > 0)
	assert.True(t, strings.Contains(bad.Output, "nosuchsubcommand"))

	missing := r.Checks[4]
	assert.False(t, missing.Passed)
	assert.Eq(t, -1, *missing.ExitCode)
	assert.NotEmpty(t, missing.Detail)
	assert.False(t, allPassed(r.Checks))
}

func TestCommandCheckTimeoutAndOutputTail(t *testing.T) {
	ws := t.TempDir()
	r := runCommandCheck(ws, Check{Type: "command", Args: []string{"go", "version"}, Timeout: "1ns"})
	assert.False(t, r.Passed)
	assert.Eq(t, -1, *r.ExitCode)

	s := tailString(strings.Repeat("é", 5000), 100)
	assert.True(t, strings.HasPrefix(s, "..."))
	assert.True(t, len(s) <= 103)
}

func TestAcceptanceMissingDirFails(t *testing.T) {
	task := &Task{dir: t.TempDir(), Acceptance: "nope"}
	var r Result
	task.finish(t.TempDir(), nil, nil, &r)
	assert.False(t, allPassed(r.Checks))
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@l", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	assert.NoErr(t, err)
	return string(out)
}

func TestPrepareWorkspaceRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	gitIn(t, src, "init", "-q")
	writeFile(t, filepath.Join(src, "a.txt"), "v1\n")
	writeFile(t, filepath.Join(src, "sub", "b.go"), "package sub\n")
	gitIn(t, src, "add", "-A")
	gitIn(t, src, "commit", "-q", "-m", "one")
	gitIn(t, src, "tag", "v1")
	writeFile(t, filepath.Join(src, "a.txt"), "v2\n")
	gitIn(t, src, "commit", "-qam", "two")

	ws := filepath.Join(t.TempDir(), "ws")
	ctx := context.Background()
	task := &Task{Repo: &RepoSpec{Ref: "v1"}}
	// archiveRef runs in repoDir; prepareWorkspace uses "." so exercise both.
	assert.NoErr(t, archiveRef(ctx, src, "v1", ws))
	assert.NoErr(t, os.RemoveAll(ws))
	t.Chdir(src)
	assert.NoErr(t, task.prepareWorkspace(ctx, ws))
	b, err := os.ReadFile(filepath.Join(ws, "a.txt"))
	assert.NoErr(t, err)
	// git archive applies the eol settings (core.autocrlf=true on Windows CI).
	assert.Eq(t, "v1\n", strings.ReplaceAll(string(b), "\r\n", "\n"))
	_, err = os.Stat(filepath.Join(ws, "sub", "b.go"))
	assert.NoErr(t, err)
	assert.Eq(t, "", strings.TrimSpace(gitIn(t, ws, "status", "--porcelain")))
	assert.Eq(t, "base", strings.TrimSpace(gitIn(t, ws, "log", "-1", "--format=%s")))
	assert.Eq(t, "1", strings.TrimSpace(gitIn(t, ws, "rev-list", "--count", "HEAD")))
	// The agent can commit without a global identity (suga runs with an isolated HOME).
	assert.Eq(t, "eval", strings.TrimSpace(gitIn(t, ws, "config", "--local", "user.name")))

	assert.Err(t, archiveRef(ctx, src, "nope", filepath.Join(t.TempDir(), "x")))
}

func TestLoadTaskRepo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompt: hi\nrepo: {ref: abc}\n")
	task, err := loadTask(dir)
	assert.NoErr(t, err)
	assert.Eq(t, "abc", task.Repo.Ref)
	writeFile(t, filepath.Join(dir, "fixture", "go.mod"), "module x\n")
	_, err = loadTask(dir)
	assert.Err(t, err)
	writeFile(t, filepath.Join(dir, "task.yaml"), "name: x\nprompt: hi\nrepo: {}\n")
	_, err = loadTask(dir)
	assert.Err(t, err)

	real, err := loadTask(filepath.Join("testdata", "tasks", "repo-t9-schedule"))
	assert.NoErr(t, err)
	assert.Eq(t, "202bfa0", real.Repo.Ref)
	assert.Eq(t, 3, len(real.Checks))
	assert.Eq(t, []string{"go", "test", "./pkg/agentcore/", "-run", "Layering"}, real.Checks[2].Args)
}

func TestSplitArgsAndSugaRunArgs(t *testing.T) {
	assert.Eq(t, []string{"--no-project-rules", "--x", "1"}, splitArgs("  --no-project-rules\t--x   1 "))
	assert.Eq(t, 0, len(splitArgs("")))
	args := sugaRunArgs("s", "p", "", false, splitArgs("--a --b"))
	assert.Eq(t, []string{"--a", "--b"}, args[len(args)-2:])
}

func TestParseOmpToolCounts(t *testing.T) {
	ev := `{"type":"tool_execution_start","toolName":"bash"}
{"type":"tool_execution_start","toolName":"read"}
{"type":"tool_execution_start","toolName":"bash"}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","name":"bash"},{"type":"text","text":"hi"}]}}
`
	var r Result
	parseOmpEvents([]byte(ev), &r)
	assert.Eq(t, map[string]int{"bash": 2, "read": 1}, r.ToolCounts)
	assert.Eq(t, 1, r.ToolCalls)

	var r2 Result // fallback: toolCall blocks only
	parseOmpEvents([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","name":"edit"},{"type":"toolCall","name":"edit"}]}}`+"\n"), &r2)
	assert.Eq(t, map[string]int{"edit": 2}, r2.ToolCounts)
}

func TestParseStatsToolCounts(t *testing.T) {
	var r Result
	parseStats([]byte(`{"models":[],"tools":[{"name":"bash","calls":3},{"name":"read","calls":1},{"name":"idle","calls":0}]}`), &r)
	assert.Eq(t, map[string]int{"bash": 3, "read": 1}, r.ToolCounts)
	assert.Eq(t, 4, r.ToolCalls)
}

func TestAverageRounds(t *testing.T) {
	a := averageRounds([]Result{
		{Passed: true, Calls: 10, ToolCalls: 4, InputTokens: 100, CacheRead: 300, OutputToks: 50, DurationMs: 2000},
		{Passed: false, Calls: 20, ToolCalls: 6, InputTokens: 300, CacheWrite: 100, OutputToks: 150, DurationMs: 4000},
	})
	assert.Eq(t, 1, a.Passed)
	assert.Eq(t, 2, a.Rounds)
	assert.Eq(t, 15.0, a.Calls)
	assert.Eq(t, 5.0, a.ToolCalls)
	assert.Eq(t, 400.0, a.Prompt)
	assert.Eq(t, 200.0, a.Uncached)
	assert.Eq(t, 100.0, a.Out)
	assert.Eq(t, 3.0, a.Seconds)
	assert.Eq(t, 0, averageRounds(nil).Rounds)
}

func TestSetConfigKey(t *testing.T) {
	cfg := map[string]any{"tools": map[string]any{"allow_write": false}}
	assert.NoErr(t, setConfigKey(cfg, "tools.shell=C:/Program Files/Git/usr/bin/bash.exe"))
	assert.NoErr(t, setConfigKey(cfg, "tools.allow_write=true"))
	assert.NoErr(t, setConfigKey(cfg, "context.idle_after=0"))
	tools := cfg["tools"].(map[string]any)
	assert.Eq(t, "C:/Program Files/Git/usr/bin/bash.exe", tools["shell"])
	assert.Eq(t, true, tools["allow_write"])
	assert.NotNil(t, cfg["context"].(map[string]any)["idle_after"])
	assert.Err(t, setConfigKey(cfg, "no-equals"))
}
