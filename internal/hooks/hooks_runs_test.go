package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestHookRunsProcess is the fake hook command of the run-record tests: it sleeps
// SUGA_RUNS_SLEEP_MS, prints SUGA_RUNS_STDOUT / SUGA_RUNS_STDERR and exits with
// SUGA_RUNS_EXIT (0 when unset). It does nothing unless SUGA_RUNS_HOOK is set,
// so the ordinary test run does not behave like a hook.
func TestHookRunsProcess(t *testing.T) {
	if os.Getenv("SUGA_RUNS_HOOK") == "" {
		return
	}
	if ms, _ := strconv.Atoi(os.Getenv("SUGA_RUNS_SLEEP_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	_, _ = os.Stdout.WriteString(os.Getenv("SUGA_RUNS_STDOUT"))
	_, _ = os.Stderr.WriteString(os.Getenv("SUGA_RUNS_STDERR"))
	code, _ := strconv.Atoi(os.Getenv("SUGA_RUNS_EXIT"))
	os.Exit(code)
}

// newRunsClient builds the compatible client of TestHookRunsProcess; configure
// runs after the config reset and before New, so it can set the keys New reads.
func newRunsClient(t *testing.T, configure func()) *commandClient {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "")
	t.Setenv("SUGA_RUNS_HOOK", "1")
	t.Setenv("SUGA_RUNS_SLEEP_MS", "")
	t.Setenv("SUGA_RUNS_STDOUT", "")
	t.Setenv("SUGA_RUNS_STDERR", "")
	t.Setenv("SUGA_RUNS_EXIT", "")
	// A -race child otherwise sleeps 1s at exit, which would blur the timeout checks.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	if configure != nil {
		configure()
	}
	assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0], "-test.run=^TestHookRunsProcess$"}))
	client, err := New()
	assert.Require(t, assert.NoErr(t, err))
	compat, ok := client.(*commandClient)
	assert.Require(t, assert.True(t, ok))
	return compat
}

// assertEmptyFailedRun asserts that a failed hook call answered nothing and left
// exactly one run record of the compatible entry, carrying the error.
func assertEmptyFailedRun(t *testing.T, response Response) {
	t.Helper()
	assert.Eq(t, "", response.Decision)
	assert.Eq(t, "", response.Reason)
	assert.Eq(t, "", response.AdditionalContext)
	assert.Eq(t, "", response.Notice)
	assert.Nil(t, response.UpdatedInput)
	assert.Require(t, assert.Eq(t, 1, len(response.Runs)))
	assert.Eq(t, 0, response.Runs[0].Entry)
	assert.True(t, response.Runs[0].Err != nil, "a failed call records the error")
}

// assertEmptyRecordedAnswer asserts that one run recorded no answer at all.
func assertEmptyRecordedAnswer(t *testing.T, run EntryRun) {
	t.Helper()
	assert.Eq(t, "", run.Response.Decision)
	assert.Eq(t, "", run.Response.Reason)
	assert.Eq(t, "", run.Response.AdditionalContext)
	assert.Eq(t, "", run.Response.Notice)
	assert.Nil(t, run.Response.UpdatedInput)
	assert.Eq(t, 0, len(run.Response.Runs))
}

// TestHookRuns checks the run record of a single command client: every outcome
// (success, non-zero exit, timeout, cancel, unreadable answer) fills exactly one
// EntryRun with the entry, the program base name, the duration and the error.
func TestHookRuns(t *testing.T) {
	program := filepath.Base(os.Args[0])

	t.Run("success carries the answer of the run", func(t *testing.T) {
		h := newRunsClient(t, nil)
		t.Setenv("SUGA_RUNS_STDOUT", `{"decision":"block","reason":"tighten the test"}`)
		t.Setenv("SUGA_RUNS_STDERR", "notice-here")
		response := h.Call(context.Background(), Event{Name: "Stop"})
		// The merged answer keeps its fields, the run records the same answer once.
		assert.Eq(t, "block", response.Decision)
		assert.Require(t, assert.Eq(t, 1, len(response.Runs)))
		run := response.Runs[0]
		assert.Eq(t, 0, run.Entry)
		assert.Eq(t, program, run.Program)
		assert.NoErr(t, run.Err)
		assert.True(t, run.Duration > 0, "a run has a duration")
		assert.Eq(t, "block", run.Response.Decision)
		assert.Eq(t, "tighten the test", run.Response.Reason)
		assert.Eq(t, "notice-here", run.Response.Notice)
		// The recorded answer never nests runs of its own.
		assert.Eq(t, 0, len(run.Response.Runs))
	})

	t.Run("a non-zero exit records the failure", func(t *testing.T) {
		h := newRunsClient(t, nil)
		t.Setenv("SUGA_RUNS_EXIT", "1")
		t.Setenv("SUGA_RUNS_STDOUT", `{"decision":"block","reason":"ignored"}`)
		t.Setenv("SUGA_RUNS_STDERR", "notice-here")
		response := h.Call(context.Background(), Event{Name: "UserPromptSubmit"})
		assert.Require(t, assert.Eq(t, 1, len(response.Runs)))
		assert.Eq(t, program, response.Runs[0].Program)
		assert.Err(t, response.Runs[0].Err)
		assertEmptyRecordedAnswer(t, response.Runs[0])
	})

	t.Run("a timeout records the timeout error", func(t *testing.T) {
		h := newRunsClient(t, func() {
			assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", 200))
		})
		t.Setenv("SUGA_RUNS_SLEEP_MS", "2000")
		response := h.Call(context.Background(), Event{Name: "UserPromptSubmit"})
		assert.Require(t, assert.Eq(t, 1, len(response.Runs)))
		run := response.Runs[0]
		assert.True(t, errors.Is(run.Err, ErrTimedOut), "a bound reached is a timeout")
		assertEmptyRecordedAnswer(t, run)
	})

	t.Run("a cancelled call records the cancel", func(t *testing.T) {
		h := newRunsClient(t, nil)
		t.Setenv("SUGA_RUNS_SLEEP_MS", "3000")
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(300 * time.Millisecond)
			cancel()
		}()
		defer cancel()
		response := h.Call(ctx, Event{Name: "UserPromptSubmit"})
		assert.Require(t, assert.Eq(t, 1, len(response.Runs)))
		run := response.Runs[0]
		assert.True(t, errors.Is(run.Err, context.Canceled), "the cancel is the cause")
		assertEmptyRecordedAnswer(t, run)
	})

	t.Run("an answer that is not JSON records the failure", func(t *testing.T) {
		h := newRunsClient(t, nil)
		t.Setenv("SUGA_RUNS_STDOUT", "not json")
		response := h.Call(context.Background(), Event{Name: "SessionStart"})
		assert.Require(t, assert.Eq(t, 1, len(response.Runs)))
		assert.Err(t, response.Runs[0].Err)
		assertEmptyRecordedAnswer(t, response.Runs[0])
	})
}

// TestNewHookEntries covers what New builds from hooks.command and
// hooks.commands: the compatible single client, the pipeline, the waiting flag
// of the compatible Stop and the environment every entry receives.
func TestNewHookEntries(t *testing.T) {
	// resetConfig gives a case a clean config and leaves hooks enabled.
	resetConfig := func(t *testing.T) {
		t.Helper()
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		t.Setenv("SUGA_HOOKS", "")
	}
	// entries returns one hooks.commands entry running the fake env process.
	entries := func() []map[string]any {
		return []map[string]any{{"command": []string{os.Args[0], "-test.run=^TestFakeHookEnvProcess$"}}}
	}

	t.Run("stop_background leaves the Stop bound to the task manager", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0]}))
		assert.NoErr(t, gkconfig.Set("hooks.stop_args", []string{"--wait", "5"}))
		assert.NoErr(t, gkconfig.Set("hooks.stop_background", true))
		compat, ok := mustNew(t).(*commandClient)
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, 3*time.Second, compat.timeout)
		assert.Eq(t, backgroundStopTimeout, compat.stopTimeout)
	})

	t.Run("stop_args without stop_wait_sec keeps the timeout_ms bound", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0]}))
		assert.NoErr(t, gkconfig.Set("hooks.stop_args", []string{"--wait", "5"}))
		compat, ok := mustNew(t).(*commandClient)
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, compat.timeout, compat.stopTimeout)
	})

	t.Run("only hooks.commands returns a pipeline without tool events", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("hooks.commands", entries()))
		client := mustNew(t)
		p, ok := client.(*pipeline)
		assert.Require(t, assert.True(t, ok))
		assert.Nil(t, p.compat)
		assert.False(t, p.compatWaiting)
		assert.Require(t, assert.Eq(t, 1, len(p.entries)))
		entry := p.entries[0]
		assert.Eq(t, 1, entry.entry)
		assert.Eq(t, 3*time.Second, entry.timeout)
		assert.Eq(t, entry.timeout, entry.stopTimeout)
		assert.Eq(t, 0, len(entry.stopArgs))
		_, send := p.PostToolName("shell_exec")
		assert.False(t, send)
		_, send = p.PreToolName("shell_exec")
		assert.False(t, send)
		response, err := p.PreTool(context.Background(), Event{Name: "PreToolUse", ToolName: "shell_exec"})
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(response.Runs))
	})

	t.Run("an entry timeout_ms replaces the default", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("hooks.commands", []map[string]any{
			{"command": []string{os.Args[0]}, "timeout_ms": 250},
			{"command": []string{os.Args[0]}},
		}))
		p, ok := mustNew(t).(*pipeline)
		assert.Require(t, assert.True(t, ok))
		assert.Require(t, assert.Eq(t, 2, len(p.entries)))
		assert.Eq(t, 250*time.Millisecond, p.entries[0].timeout)
		assert.Eq(t, p.entries[0].timeout, p.entries[0].stopTimeout)
		assert.Eq(t, 2, p.entries[1].entry)
		assert.Eq(t, 3*time.Second, p.entries[1].timeout)
	})

	t.Run("a malformed stop_args does not stop a hooks.commands setup", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("hooks.stop_args", "--wait 5"))
		assert.NoErr(t, gkconfig.Set("hooks.commands", entries()))
		client, err := New()
		assert.NoErr(t, err)
		assert.NotNil(t, client)
	})

	t.Run("no hook command or entry returns nil", func(t *testing.T) {
		resetConfig(t)
		assert.Nil(t, mustNew(t))
	})

	t.Run("compatWaiting follows the wait keys of the compatible Stop", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0]}))
		assert.NoErr(t, gkconfig.Set("hooks.commands", entries()))
		p, ok := mustNew(t).(*pipeline)
		assert.Require(t, assert.True(t, ok))
		assert.False(t, p.compatWaiting)
		assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 1))
		p, ok = mustNew(t).(*pipeline)
		assert.Require(t, assert.True(t, ok))
		assert.True(t, p.compatWaiting)
		assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 0))
		assert.NoErr(t, gkconfig.Set("hooks.stop_background", true))
		p, ok = mustNew(t).(*pipeline)
		assert.Require(t, assert.True(t, ok))
		assert.True(t, p.compatWaiting)
	})

	t.Run("an entry receives the session variables of the call", func(t *testing.T) {
		resetConfig(t)
		logPath := filepath.Join(t.TempDir(), "hook-env.txt")
		t.Setenv("SUGA_FAKE_HOOK_ENV_LOG", logPath)
		t.Setenv("GOFER_SESSION_ID", "gofer-9")
		assert.NoErr(t, gkconfig.Set("hooks.commands", entries()))
		client, err := New(WithExtraEnv(func(context.Context) []string {
			return []string{"SUGA_SESSION_NAME=ws-k7z", "SUGA_TURN_ID=turn-1"}
		}))
		assert.Require(t, assert.NoErr(t, err))
		p, ok := client.(*pipeline)
		assert.Require(t, assert.True(t, ok))
		// The skeleton leaves Call to the compatible entry, so the entry is called
		// directly here: the environment is what this case checks.
		p.entries[0].Call(context.Background(), Event{Name: "SessionStart", SessionID: "sess-hook"})
		assert.Eq(t, "sess-hook|ws-k7z|turn-1|gofer-9", readHookEnv(t, logPath))
	})
}
