package native

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

func TestParseWatch(t *testing.T) {
	t.Run("neither set is no watch", func(t *testing.T) {
		_, ok, err := parseWatch(map[string]any{"command": "x"})
		assert.NoErr(t, err)
		assert.False(t, ok)
	})
	t.Run("every and paths", func(t *testing.T) {
		spec, ok, err := parseWatch(map[string]any{"every": float64(30), "watch_paths": []any{"./internal/**/*.go", " "}})
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, ok)
		assert.Eq(t, 30*time.Second, spec.every)
		assert.Eq(t, []string{"internal/**/*.go"}, spec.paths)
		assert.Eq(t, "every 30s and on changes of internal/**/*.go", spec.describe())
		spec, ok, _ = parseWatch(map[string]any{"watch_paths": "*.go"})
		assert.True(t, ok)
		assert.Eq(t, []string{"*.go"}, spec.paths)
	})
	t.Run("errors", func(t *testing.T) {
		_, _, err := parseWatch(map[string]any{"every": float64(2)})
		assert.ErrMsgContains(t, err, "every must be at least 10 seconds")
		_, _, err = parseWatch(map[string]any{"watch_paths": []any{"../x/*.go"}})
		assert.ErrMsgContains(t, err, "relative to the workspace")
		_, _, err = parseWatch(map[string]any{"watch_paths": []any{"/etc/*"}})
		assert.ErrMsgContains(t, err, "relative to the workspace")
	})
}

func TestNormalizeWatchOutput(t *testing.T) {
	same := func(a, b string) bool {
		return normalizeWatchOutput(strings.Split(a, "\n")) == normalizeWatchOutput(strings.Split(b, "\n"))
	}
	assert.True(t, same("ok  \tpkg\t0.42s\n--- PASS: TestA (0.00s)", "ok  \tpkg\t1.3s\n--- PASS: TestA (12ms)"))
	assert.True(t, same("2026-10-13T08:00:01Z built", "2026-10-13 09:12:44 built"))
	assert.True(t, same("at 08:00:01 done", "at 09:12:44.123 done"))
	assert.False(t, same("ok pkg 0.4s", "FAIL pkg 0.4s"))
	assert.False(t, same("3 tests", "4 tests"))
}

func TestLiteralPrefix(t *testing.T) {
	assert.Eq(t, "", literalPrefix([]string{"*.go"}))
	assert.Eq(t, "internal/cli", literalPrefix([]string{"internal/cli/*.go"}))
	assert.Eq(t, "internal", literalPrefix([]string{"internal/cli/*.go", "internal/**/x.go"}))
	assert.Eq(t, "", literalPrefix([]string{"internal/*.go", "pkg/*.go"}))
	assert.Eq(t, "src", literalPrefix([]string{"src/main.go"}))
}

// watchFixture is a workspace with a shell_exec whose background tasks run in
// a manager; notices returns what the session's notices say so far.
type watchFixture struct {
	ws    string
	m     *tasks.Manager
	shell *ShellExecTool
	ctx   context.Context
}

func newWatchFixture(t *testing.T) *watchFixture {
	t.Helper()
	poll, every := watchPollInterval, minWatchEvery
	watchPollInterval, minWatchEvery = 50*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { watchPollInterval, minWatchEvery = poll, every })
	dir := t.TempDir()
	m := tasks.NewManager(tasks.Config{Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, id+".txt"), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(m.Close)
	ws := t.TempDir()
	shell := NewShellExecTool(ws, 0)
	shell.SetTasks(m)
	return &watchFixture{ws: ws, m: m, shell: shell, ctx: api.WithSessionID(context.Background(), "s1")}
}

func (f *watchFixture) write(t *testing.T, name, content string) {
	t.Helper()
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(f.ws, name), []byte(content), 0o644)))
}

func (f *watchFixture) start(t *testing.T, args map[string]any) string {
	t.Helper()
	args["background"] = true
	res, err := f.shell.Execute(f.ctx, api.ToolCall{CallID: "c", Name: "shell_exec", Arguments: args})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, res.Content, "[watch task t")
	id, _ := res.Metadata["task_id"].(string)
	return id
}

// runs is the watch's run counter.
func (f *watchFixture) runs(id string) int {
	info, _ := f.m.Get("s1", id)
	return info.Counters["runs"]
}

// waitRuns waits until the watch made at least n runs.
func (f *watchFixture) waitRuns(t *testing.T, id string, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if f.runs(id) >= n {
			return
		}
	}
	t.Fatalf("watch %s made %d runs, want %d", id, f.runs(id), n)
}

// notices takes the session's pending notice texts.
func (f *watchFixture) notices() []string {
	var out []string
	for _, info := range f.m.TakeNotices("s1") {
		out = append(out, info.Notice)
	}
	return out
}

// TestWatchLimitsNotice: a watch run that hits the resource limits says so in
// its run header line, so the output and the change comparison both carry it.
func TestWatchLimitsNotice(t *testing.T) {
	f := newWatchFixture(t)
	f.shell.limits = ShellLimits{MemoryBytes: 1 << 30}
	id := f.start(t, map[string]any{"command": helperCommand(t, 4096), "every": 10})
	f.waitRuns(t, id, 1)

	info, err := f.m.Get("s1", id)
	assert.Require(t, assert.NoErr(t, err))
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		data, readErr := os.ReadFile(info.Path)
		assert.Require(t, assert.NoErr(t, readErr))
		if strings.Contains(string(data), "超出 suga 的资源限制") {
			assert.StrContains(t, string(data), "内存 1GiB")
			assert.StrContains(t, string(data), "=== exit 3;")
			return
		}
	}
	t.Fatalf("the watch output of %s does not name the resource limit", id)
}

// watchCommands: print the file and fail with 3 when it says "bad"; append
// to a watched file (the run's own write must not trigger a run).
func watchCommands() (check, selfWrite string) {
	if runtime.GOOS == "windows" {
		return `Get-Content a.txt; if (Select-String -Quiet -Pattern bad -Path a.txt) { exit 3 }`,
			`Add-Content -Path log.txt -Value x`
	}
	return `cat a.txt; if grep -q bad a.txt; then exit 3; fi`, `echo x >> log.txt`
}

func TestWatch(t *testing.T) {
	check, selfWrite := watchCommands()

	t.Run("a file change runs it; only a changed result is a notice", func(t *testing.T) {
		f := newWatchFixture(t)
		f.write(t, "a.txt", "one\n")
		id := f.start(t, map[string]any{"command": check, "watch_paths": []any{"*.txt"}})
		f.waitRuns(t, id, 1)
		assert.Empty(t, f.notices()) // a passing baseline says nothing

		f.write(t, "a.txt", "two\n")
		f.waitRuns(t, id, 2)
		got := f.notices()
		assert.Require(t, assert.Len(t, got, 1))
		assert.StrContains(t, got[0], "run 2 (files: a.txt): output changed (exit 0)")
		assert.StrContains(t, got[0], "last lines:\ntwo")

		time.Sleep(20 * time.Millisecond) // a new mtime, the same content
		f.write(t, "a.txt", "two\n")
		f.waitRuns(t, id, 3)
		assert.Empty(t, f.notices())

		f.write(t, "a.txt", "bad\n")
		f.waitRuns(t, id, 4)
		got = f.notices()
		assert.Require(t, assert.Len(t, got, 1))
		assert.StrContains(t, got[0], "exit 0 -> 3")

		info, err := f.m.Kill("s1", id)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, tasks.Killed, info.Status)
		assert.Eq(t, 4, info.Counters["runs"])
		assert.Eq(t, 2, info.Counters["changes"])
		assert.StrContains(t, tasks.Describe(info), "run 4, last exit 3, 2 changes")
		lines, _, _, err := tasks.ReadLines(info.Path, 1, 1000)
		assert.Require(t, assert.NoErr(t, err))
		text := strings.Join(lines, "\n")
		assert.StrContains(t, text, "=== run 1 (start)")
		assert.StrContains(t, text, "=== run 4 (files: a.txt)")
		assert.StrContains(t, text, "=== exit 3 ===")
	})
	t.Run("what a run writes itself is no change", func(t *testing.T) {
		f := newWatchFixture(t)
		id := f.start(t, map[string]any{"command": selfWrite, "watch_paths": []any{"*.txt"}})
		f.waitRuns(t, id, 1)
		time.Sleep(10 * watchPollInterval)
		assert.Eq(t, 1, f.runs(id))
	})
	t.Run("a failing baseline is a notice", func(t *testing.T) {
		f := newWatchFixture(t)
		f.write(t, "a.txt", "bad\n")
		id := f.start(t, map[string]any{"command": check, "watch_paths": []any{"a.txt"}})
		f.waitRuns(t, id, 1)
		got := f.notices()
		assert.Require(t, assert.Len(t, got, 1))
		assert.StrContains(t, got[0], "run 1 (start) failed: exit 3")
	})
	t.Run("every runs it on a timer", func(t *testing.T) {
		f := newWatchFixture(t)
		f.write(t, "a.txt", "one\n")
		id := f.start(t, map[string]any{"command": check, "every": 0.2})
		f.waitRuns(t, id, 3)
		assert.Empty(t, f.notices())
	})
	t.Run("task wait returns after the next run, with its report", func(t *testing.T) {
		f := newWatchFixture(t)
		f.write(t, "a.txt", "one\n")
		id := f.start(t, map[string]any{"command": check, "watch_paths": []any{"*.txt"}})
		f.waitRuns(t, id, 1)
		task := NewTaskTool(f.m, 0)
		f.write(t, "a.txt", "bad\n")
		start := time.Now()
		res, err := task.Execute(f.ctx, api.ToolCall{CallID: "w", Name: "task", Arguments: map[string]any{"action": "wait", "id": id, "timeout": float64(60)}})
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, time.Since(start) < 10*time.Second)
		assert.StrContains(t, res.Content, "[reported while running: run 2 (files: a.txt): exit 0 -> 3")
		assert.StrContains(t, res.Content, "run 2, last exit 3, 1 changes")
		assert.Empty(t, f.notices()) // the wait showed it
	})
	t.Run("watch arguments need background", func(t *testing.T) {
		f := newWatchFixture(t)
		_, err := f.shell.Execute(f.ctx, api.ToolCall{CallID: "c", Name: "shell_exec",
			Arguments: map[string]any{"command": check, "watch_paths": []any{"*.txt"}}})
		assert.ErrMsgContains(t, err, "need background: true")
	})
}
