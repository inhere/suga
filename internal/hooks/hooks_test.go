package hooks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestFakeHookProcess is the fake hook command: tests run the test binary itself
// with -test.run=^TestFakeHookProcess$ so no real hook program is needed. It
// appends {"args","event"} to SUGA_FAKE_HOOK_LOG, sleeps SUGA_FAKE_HOOK_SLEEP_MS,
// then prints SUGA_FAKE_HOOK_STDOUT / SUGA_FAKE_HOOK_STDERR.
func TestFakeHookProcess(t *testing.T) {
	logPath := os.Getenv("SUGA_FAKE_HOOK_LOG")
	if logPath == "" {
		return
	}
	var event map[string]any
	_ = json.NewDecoder(bufio.NewReader(os.Stdin)).Decode(&event)
	// os.Args[1] is the -test.run flag; the rest are the configured hook args.
	line, _ := json.Marshal(map[string]any{"args": os.Args[2:], "event": event["hook_event_name"]})
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(3)
	}
	_, _ = file.Write(append(line, '\n'))
	_ = file.Close()
	if ms, _ := strconv.Atoi(os.Getenv("SUGA_FAKE_HOOK_SLEEP_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	fmt.Fprint(os.Stdout, os.Getenv("SUGA_FAKE_HOOK_STDOUT"))
	fmt.Fprint(os.Stderr, os.Getenv("SUGA_FAKE_HOOK_STDERR"))
	// Exit before the testing framework prints PASS on stdout.
	os.Exit(0)
}

type fakeHookCall struct {
	Args  []string `json:"args"`
	Event string   `json:"event"`
}

// useFakeHook configures hooks.command to run TestFakeHookProcess with baseArgs
// and returns a reader for the recorded calls.
func useFakeHook(t *testing.T, baseArgs ...string) func() []fakeHookCall {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "hook.log")
	t.Setenv("SUGA_HOOKS", "")
	t.Setenv("SUGA_FAKE_HOOK_LOG", logPath)
	t.Setenv("SUGA_FAKE_HOOK_SLEEP_MS", "")
	t.Setenv("SUGA_FAKE_HOOK_STDOUT", "")
	t.Setenv("SUGA_FAKE_HOOK_STDERR", "")
	// A -race child otherwise sleeps 1s at exit, which would blur the timeout checks.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	assert.NoErr(t, gkconfig.Set("hooks.command", append([]string{os.Args[0], "-test.run=^TestFakeHookProcess$"}, baseArgs...)))
	return func() []fakeHookCall {
		data, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return nil
		}
		assert.Require(t, assert.NoErr(t, err))
		var calls []fakeHookCall
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var call fakeHookCall
			assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(line), &call)))
			calls = append(calls, call)
		}
		return calls
	}
}

func mustNew(t *testing.T) Client {
	t.Helper()
	client, err := New()
	assert.Require(t, assert.NoErr(t, err))
	return client
}

func TestNewIsOffUnlessConfigured(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "") // exercise the config, not the TestMain override

	t.Run("no hooks.command", func(t *testing.T) {
		// Other hooks keys alone never enable hooks.
		assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", 100))
		assert.NoErr(t, gkconfig.Set("hooks.stop_args", []string{"--wait", "5"}))
		assert.NoErr(t, gkconfig.Set("hooks.post_tool_use.tools", []string{"shell_*"}))
		assert.Nil(t, mustNew(t))
	})
	t.Run("empty hooks.command", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("hooks.command", []string{}))
		assert.Nil(t, mustNew(t))
	})
	t.Run("legacy gofer keys are ignored", func(t *testing.T) {
		gkconfig.Reset()
		t.Setenv("GOFER_JOB_ID", "")
		assert.NoErr(t, gkconfig.Set("gofer.hooks", "on"))
		assert.NoErr(t, gkconfig.Set("gofer.command", os.Args[0]))
		assert.Nil(t, mustNew(t))
	})
	t.Run("SUGA_HOOKS=off wins over hooks.command", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0]}))
		t.Setenv("SUGA_HOOKS", "OFF")
		assert.Nil(t, mustNew(t))
		t.Setenv("SUGA_HOOKS", "")
		assert.NotNil(t, mustNew(t))
	})
	t.Run("a job id no longer disables hooks", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0]}))
		t.Setenv("GOFER_JOB_ID", "job-1")
		assert.NotNil(t, mustNew(t))
	})
}

func TestNewRejectsMalformedConfig(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		value     any
		want      string
	}{
		{name: "command is a string", key: "hooks.command", value: "gofer hook generic", want: "hooks.command must be a list of strings"},
		{name: "command item not a string", key: "hooks.command", value: []any{"hook", 3}, want: "hooks.command must be a list of strings"},
		{name: "empty program", key: "hooks.command", value: []string{" ", "x"}, want: "program name is empty"},
		{name: "stop_args not a list", key: "hooks.stop_args", value: "--wait 5", want: "hooks.stop_args must be a list"},
		{name: "bad post_tool_use glob", key: "hooks.post_tool_use.tools", value: []string{"[x"}, want: "hooks.post_tool_use.tools glob"},
		{name: "empty post_tool_use glob", key: "hooks.post_tool_use.tools", value: []string{""}, want: "empty glob"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gkconfig.Reset()
			t.Cleanup(gkconfig.Reset)
			t.Setenv("SUGA_HOOKS", "")
			if tc.key != "hooks.command" {
				assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0]}))
			}
			assert.NoErr(t, gkconfig.Set(tc.key, tc.value))
			client, err := New()
			assert.Nil(t, client)
			assert.True(t, err != nil && strings.Contains(err.Error(), tc.want))
		})
	}
}

func TestHookFailuresAreBestEffort(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "")
	assert.NoErr(t, gkconfig.Set("hooks.command", []string{filepath.Join(t.TempDir(), "missing-hook-command-7cb7")}))
	h := mustNew(t)
	// A configured but failing hook process yields an empty response, never a panic.
	assert.Eq(t, Response{}, h.Call(context.Background(), Event{Name: "SessionStart"}))
	result := <-h.StartStop(context.Background(), Event{Name: "Stop"}).Done()
	assert.Err(t, result.Err)
}

func TestHookCommandArgvAndStopArgs(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	calls := useFakeHook(t, "hook", "generic", "--agent", "suga")
	assert.NoErr(t, gkconfig.Set("hooks.stop_args", []string{"--wait", "7"}))
	h := mustNew(t)
	for _, name := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse"} {
		h.Call(context.Background(), Event{Name: name})
	}
	result := <-h.StartStop(context.Background(), Event{Name: "Stop"}).Done()
	assert.NoErr(t, result.Err)
	h.Call(context.Background(), Event{Name: "SessionEnd"})

	base := []string{"hook", "generic", "--agent", "suga"}
	got := calls()
	assert.Eq(t, 5, len(got))
	for _, call := range got {
		if call.Event == "Stop" {
			assert.Eq(t, append(append([]string(nil), base...), "--wait", "7"), call.Args)
			continue
		}
		assert.Eq(t, base, call.Args)
	}
	assert.Eq(t, []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"}, eventNames(got))
}

func TestStopInterruptSendsInterruptWithoutStopArgs(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	calls := useFakeHook(t, "base")
	assert.NoErr(t, gkconfig.Set("hooks.stop_args", []string{"--wait", "7"}))
	assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 7))
	h := mustNew(t)
	t.Setenv("SUGA_FAKE_HOOK_SLEEP_MS", "300")
	wait := h.StartStop(context.Background(), Event{Name: "Stop"})
	assert.Eq(t, "", wait.Interrupt(context.Background()))
	var interrupt *fakeHookCall
	for _, call := range calls() {
		if call.Event == "Interrupt" {
			interrupt = &call
		}
	}
	assert.Require(t, assert.NotNil(t, interrupt))
	assert.Eq(t, []string{"base"}, interrupt.Args)
}

func TestCancelledStopIsNotReportedAsAFailure(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	useFakeHook(t, "base")
	assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 7))
	h := mustNew(t)
	t.Setenv("SUGA_FAKE_HOOK_SLEEP_MS", "2000")

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	// The user typing interrupts the waiting Stop hook: the killed process is
	// expected (on Windows the kill often reports "Access is denied").
	wait := h.StartStop(context.Background(), Event{Name: "Stop"})
	time.Sleep(100 * time.Millisecond)
	wait.Interrupt(context.Background())
	assert.StrNotContains(t, logs.String(), "event=Stop")
}

func eventNames(calls []fakeHookCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.Event)
	}
	return names
}

// TestHookTimeouts checks that timeout_ms bounds ordinary events and the Stop
// wait without stop_wait_sec, and that stop_wait_sec (+2s) applies to Stop only.
func TestHookTimeouts(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	useFakeHook(t)
	t.Setenv("SUGA_FAKE_HOOK_STDOUT", `{"decision":"block","reason":"late"}`)

	t.Run("limits from config", func(t *testing.T) {
		h := mustNew(t).(*commandClient)
		assert.Eq(t, 3*time.Second, h.timeout)
		assert.Eq(t, 3*time.Second, h.stopTimeout)
		assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", 250))
		h = mustNew(t).(*commandClient)
		assert.Eq(t, 250*time.Millisecond, h.stopTimeout)
		assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 60))
		h = mustNew(t).(*commandClient)
		assert.Eq(t, 250*time.Millisecond, h.timeout)
		assert.Eq(t, 62*time.Second, h.stopTimeout)
	})
	t.Run("stop without stop_wait_sec uses timeout_ms", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", 200))
		assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 0))
		t.Setenv("SUGA_FAKE_HOOK_SLEEP_MS", "1500")
		start := time.Now()
		result := <-mustNew(t).StartStop(context.Background(), Event{Name: "Stop"}).Done()
		assert.Err(t, result.Err)
		// Reaching the bound is a timeout, not a failure: the trace and the log show
		// it apart from a hook command that broke (issue my-tools-4x8a).
		assert.True(t, errors.Is(result.Err, ErrTimedOut), result.Err.Error())
		assert.True(t, time.Since(start) < 1200*time.Millisecond)
	})
	t.Run("a failing command is not a timeout", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", 2000))
		result := <-mustNew(t).StartStop(context.Background(), Event{Name: "Stop"}).Done()
		if result.Err != nil {
			assert.False(t, errors.Is(result.Err, ErrTimedOut), "a non-zero exit is a failure, not a timeout")
		}
	})
	t.Run("stop_wait_sec outlasts timeout_ms for Stop only", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", 200))
		// Stop may take up to 3+2s; the 800ms command finishes well inside that.
		assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 3))
		t.Setenv("SUGA_FAKE_HOOK_SLEEP_MS", "800")
		h := mustNew(t)
		result := <-h.StartStop(context.Background(), Event{Name: "Stop"}).Done()
		assert.NoErr(t, result.Err)
		assert.Eq(t, "late", result.Response.Reason)
		// The same slow command on an ordinary event is cut at timeout_ms.
		assert.Eq(t, Response{}, h.Call(context.Background(), Event{Name: "UserPromptSubmit"}))
	})
}

func TestPostToolFilter(t *testing.T) {
	t.Run("empty sends nothing", func(t *testing.T) {
		_, ok := PostToolFilter{Name: "shell"}.Match("shell_exec")
		assert.False(t, ok)
	})
	t.Run("glob and rename", func(t *testing.T) {
		f := PostToolFilter{Tools: []string{"shell_*", "exec"}, Name: "shell"}
		for _, tool := range []string{"shell_exec", "shell_run", "exec"} {
			name, ok := f.Match(tool)
			assert.True(t, ok)
			assert.Eq(t, "shell", name)
		}
		for _, tool := range []string{"file_read", "exec_x", "myshell_exec"} {
			_, ok := f.Match(tool)
			assert.False(t, ok)
		}
	})
	t.Run("empty name keeps the tool name", func(t *testing.T) {
		name, ok := PostToolFilter{Tools: []string{"shell_*"}}.Match("shell_exec")
		assert.True(t, ok)
		assert.Eq(t, "shell_exec", name)
	})
	t.Run("from config", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		t.Setenv("SUGA_HOOKS", "")
		assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0]}))
		_, ok := mustNew(t).PostToolName("shell_exec")
		assert.False(t, ok)
		assert.NoErr(t, gkconfig.Set("hooks.post_tool_use.tools", []string{"shell_*"}))
		assert.NoErr(t, gkconfig.Set("hooks.post_tool_use.name", "shell"))
		name, ok := mustNew(t).PostToolName("shell_exec")
		assert.True(t, ok)
		assert.Eq(t, "shell", name)
	})
}

func TestHookPayloadCapsToolOutputAtUTF8Boundary(t *testing.T) {
	value := strings.Repeat("工", 8*1024)
	got := TruncateOutput(value, 8*1024)
	if len(got) > 8*1024 || !utf8.ValidString(got) {
		t.Fatalf("hook output is not valid bounded UTF-8: bytes=%d", len(got))
	}
}

func TestStopInterruptIsSentBeforeStopProcessCancellation(t *testing.T) {
	var sequence []string
	done := make(chan Result, 1)
	done <- Result{}
	wait := &stopWait{
		call: func(_ context.Context, event Event) Response {
			if event.Name == "Interrupt" {
				sequence = append(sequence, "interrupt")
			}
			return Response{}
		},
		event:  Event{Name: "Stop", SessionID: "s1"},
		cancel: func() { sequence = append(sequence, "cancel") },
		done:   done,
	}
	wait.Interrupt(context.Background())
	if len(sequence) != 2 || sequence[0] != "interrupt" || sequence[1] != "cancel" {
		t.Fatalf("Stop process cancelled before Interrupt completed: %#v", sequence)
	}
}

func TestStopInterruptReturnsAlreadyReceivedReply(t *testing.T) {
	done := make(chan Result, 1)
	done <- Result{Response: Response{Decision: "block", Reason: "[web 回复] hi"}}
	wait := &stopWait{
		call:   func(context.Context, Event) Response { return Response{} },
		cancel: func() {},
		done:   done,
	}
	assert.Eq(t, "[web 回复] hi", wait.Interrupt(context.Background()))
}

func TestHooksCaptureStderrAsNotice(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	useFakeHook(t)
	t.Setenv("SUGA_FAKE_HOOK_STDERR", "taken over on web\n")
	resp := mustNew(t).Call(context.Background(), Event{Name: "UserPromptSubmit"})
	assert.Eq(t, "taken over on web", resp.Notice)
}
