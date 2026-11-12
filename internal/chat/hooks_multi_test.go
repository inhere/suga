package chat

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/pkg/agentcore"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// multiHookEvent is the part of the event JSON the fake hook command reads.
type multiHookEvent struct {
	Name string `json:"hook_event_name"`
}

// TestMultiHookProcess is the fake hook command of the multi-command tests,
// started as "<test binary> -test.run=^TestMultiHookProcess$ -- <label> <mode>"
// (the fake process contract of the multi-command hooks plan). It does nothing
// unless CHAT_HOOK_PROCESS is set, so the ordinary test run does not behave like
// a hook.
//
// Every event appends "<label> <event> start" to CHAT_HOOK_LOG when it begins and
// "<label> <event> end" before it exits, so the log tells which entry ran in which
// order and whether it was killed. SessionStart and UserPromptSubmit answer
// additionalContext "<label>-ctx" and the notice "notice-<label>"; the mode
// decides the answer (block, fail, wait, silent, ifail, timeout).
func TestMultiHookProcess(t *testing.T) {
	if os.Getenv("CHAT_HOOK_PROCESS") == "" {
		return
	}
	label, mode := multiHookProcessArgs()
	if label == "" {
		os.Exit(2)
	}
	logPath := os.Getenv("CHAT_HOOK_LOG")
	name := multiHookProcessEventName(os.Stdin)
	multiHookProcessLog(logPath, label+" "+name+" start")
	end := func() { multiHookProcessLog(logPath, label+" "+name+" end") }

	if mode == "timeout" {
		// Killed by the test's own timeout_ms long before this returns.
		time.Sleep(time.Second)
	}
	if mode == "fail" {
		end()
		os.Exit(1)
	}
	if name == "Interrupt" {
		// An interrupt ends the command: it can only refuse the interrupt, which
		// the ifail mode does.
		end()
		if mode == "ifail" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	switch name {
	case "SessionStart", "UserPromptSubmit":
		_, _ = os.Stdout.WriteString(`{"hookSpecificOutput":{"additionalContext":"` + label + `-ctx"}}`)
		_, _ = os.Stderr.WriteString("notice-" + label)
	default:
		switch mode {
		case "block":
			_, _ = os.Stdout.WriteString(`{"decision":"block","reason":"` + label + `-reason"}`)
		case "wait", "ifail":
			// Runs until the hook's bound or the caller's cancel kills it.
			time.Sleep(30 * time.Second)
		}
	}
	end()
	os.Exit(0)
}

// multiHookProcessArgs returns the label and mode that follow the "--" separator
// of the test binary's arguments.
func multiHookProcessArgs() (string, string) {
	args := os.Args
	for index, arg := range args {
		if arg != "--" {
			continue
		}
		rest := args[index+1:]
		if len(rest) < 2 {
			return "", ""
		}
		return rest[0], rest[1]
	}
	return "", ""
}

// multiHookProcessEventName reads the hook event name of the JSON payload on
// stdin; an unreadable payload is reported as "Unknown".
func multiHookProcessEventName(stdin io.Reader) string {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "Unknown"
	}
	var event multiHookEvent
	if err := json.Unmarshal(data, &event); err != nil || event.Name == "" {
		return "Unknown"
	}
	return event.Name
}

// multiHookProcessLog appends one line to the shared log. Every line is a
// separate append, which is what lets the parent watch for a "start" line while
// the command is still running.
func multiHookProcessLog(path, line string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = file.WriteString(line + "\n")
	_ = file.Close()
}

// multiHookSpec is one fake hook command of a chat multi-hook test; timeoutMS is
// the entry's own bound (0 keeps the hooks.timeout_ms default).
type multiHookSpec struct {
	label     string
	mode      string
	timeoutMS int
}

// multiHookCommand is the argv of the fake hook command for one spec.
func multiHookCommand(spec multiHookSpec) []string {
	return []string{os.Args[0], "-test.run=^TestMultiHookProcess$", "--", spec.label, spec.mode}
}

// newMultiHookClient configures the fake compatible hooks.command (nil for none)
// and the fake hooks.commands entries, builds the hook client of that config and
// returns it with the path of the shared hook log. prepare runs after the config
// reset and before New, so a case can set the keys New reads.
func newMultiHookClient(t *testing.T, prepare func(), compat *multiHookSpec, entries ...multiHookSpec) (sugahooks.Client, string) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "")
	t.Setenv("CHAT_HOOK_PROCESS", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	logPath := filepath.Join(t.TempDir(), "hook.log")
	t.Setenv("CHAT_HOOK_LOG", logPath)
	if prepare != nil {
		prepare()
	}
	if compat != nil {
		argv := multiHookCommand(*compat)
		if compat.timeoutMS > 0 {
			assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", compat.timeoutMS)))
		}
		assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.command", argv)))
	}
	if len(entries) > 0 {
		list := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			item := map[string]any{"command": multiHookCommand(entry)}
			if entry.timeoutMS > 0 {
				item["timeout_ms"] = entry.timeoutMS
			}
			list = append(list, item)
		}
		assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.commands", list)))
	}
	client, err := sugahooks.New()
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NotNil(t, client))
	return client, logPath
}

// recordInto is recordHooks with a test sink instead of the personal runner, so
// a case can read the hook events of the trace.
func recordInto(inner sugahooks.Client, sink eventRecorder) *recordingHooks {
	return &recordingHooks{Client: inner, runner: sink}
}

// multiHookLogLines returns the lines the fake hook commands wrote so far; a
// missing log means nothing has run yet.
func multiHookLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	assert.Require(t, assert.NoErr(t, err))
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// multiHookLogHas reports whether the log already holds the given line.
func multiHookLogHas(t *testing.T, path, line string) bool {
	t.Helper()
	for _, got := range multiHookLogLines(t, path) {
		if got == line {
			return true
		}
	}
	return false
}

// multiHookStarted returns the labels of the entries whose "<event> start" line
// is in the log, in log order.
func multiHookStarted(t *testing.T, path, eventName string) []string {
	t.Helper()
	suffix := " " + eventName + " start"
	labels := make([]string, 0, len(multiHookLogLines(t, path)))
	for _, line := range multiHookLogLines(t, path) {
		if strings.HasSuffix(line, suffix) {
			labels = append(labels, strings.TrimSuffix(line, suffix))
		}
	}
	return labels
}

// waitMultiHookLine waits (10s) until the log holds the line, the deterministic
// "the entry is running now" point every case uses instead of a fixed sleep.
func waitMultiHookLine(t *testing.T, path, line string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if multiHookLogHas(t, path, line) {
			return
		}
	}
	t.Fatalf("the hook log never got %q, it holds %v", line, multiHookLogLines(t, path))
}

// waitMultiHookEvents waits (10s) until the sink holds at least count events.
func waitMultiHookEvents(t *testing.T, sink *eventSink, count int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if len(sink.all()) >= count {
			return
		}
	}
	t.Fatalf("the trace holds %d hook events, want %d", len(sink.all()), count)
}

// waitRecordedResult waits (10s) until the forwarding goroutine of the recording
// wrapper took the pipeline result, which is what makes Interrupt see a finished
// Stop.
func waitRecordedResult(t *testing.T, w *recordingWait) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if len(w.out) == 1 {
			return
		}
	}
	t.Fatal("the recording wrapper never took the result of the Stop wait")
}

// TestMultiHookTrace covers HF14: every hook entry run is a hook event of its own,
// named with the entry and program that ran, and an Interrupt leaves no event.
func TestMultiHookTrace(t *testing.T) {
	t.Run("a compatible command names entry 0 and its program, a failure still shows ok", func(t *testing.T) {
		client, _ := newMultiHookClient(t, nil, &multiHookSpec{label: "c0", mode: "fail"})
		sink := &eventSink{}
		h := recordInto(client, sink)

		// Call swallows the command error (as before), so a failing SessionStart
		// stays an ok event that still names its run.
		h.Call(testCtx(t), sugahooks.Event{Name: "SessionStart", SessionID: "s1"})
		h.Call(testCtx(t), sugahooks.Event{Name: "UserPromptSubmit", SessionID: "s1"})

		events := sink.all()
		assert.Require(t, assert.Len(t, events, 2))
		assert.Eq(t, "SessionStart", events[0].Name)
		assert.Eq(t, "UserPromptSubmit", events[1].Name)
		for _, event := range events {
			assert.Eq(t, "ok", event.Status)
			assert.Eq(t, 0, event.Attrs["entry"])
			assert.Eq(t, filepath.Base(os.Args[0]), event.Attrs["program"])
		}
	})

	t.Run("a failing PreToolUse entry names entry 0 and stays an error", func(t *testing.T) {
		client, _ := newMultiHookClient(t, func() {
			assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.pre_tool_use.tools", []string{"shell_*"})))
		}, &multiHookSpec{label: "c0", mode: "fail"})
		sink := &eventSink{}
		gate := newToolGate(sugahooks.GateOf(client), sink, nil)

		ctx := agentcoreapi.WithSessionID(testCtx(t), "s1")
		_, err := gate.Rewrite(ctx, agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": "ls"}}, nil)
		assert.Err(t, err)

		events := sink.all()
		assert.Require(t, assert.Len(t, events, 1))
		assert.Eq(t, "PreToolUse", events[0].Name)
		assert.Eq(t, "error", events[0].Status)
		assert.Eq(t, 0, events[0].Attrs["entry"])
		assert.Eq(t, filepath.Base(os.Args[0]), events[0].Attrs["program"])
	})

	t.Run("two entries record two Stop events", func(t *testing.T) {
		client, logPath := newMultiHookClient(t, nil, nil,
			multiHookSpec{label: "A", mode: "silent"}, multiHookSpec{label: "B", mode: "silent"})
		sink := &eventSink{}
		h := recordInto(client, sink)

		result := <-h.StartStop(testCtx(t), sugahooks.Event{Name: "Stop", SessionID: "s1"}).Done()
		assert.Eq(t, "", result.Response.Decision)
		assert.Eq(t, []string{"A", "B"}, multiHookStarted(t, logPath, "Stop"))

		events := sink.all()
		assert.Require(t, assert.Len(t, events, 2))
		assert.Eq(t, "Stop", events[0].Name)
		assert.Eq(t, 1, events[0].Attrs["entry"])
		assert.Eq(t, 2, events[1].Attrs["entry"])
		assert.Eq(t, filepath.Base(os.Args[0]), events[0].Attrs["program"])
	})

	t.Run("an interrupt records no hook event of its own", func(t *testing.T) {
		client, logPath := newMultiHookClient(t, nil, nil,
			multiHookSpec{label: "W", mode: "wait", timeoutMS: 30000})
		sink := &eventSink{}
		h := recordInto(client, sink)

		w := h.StartStop(testCtx(t), sugahooks.Event{Name: "Stop", SessionID: "s1"})
		waitMultiHookLine(t, logPath, "W Stop start")
		w.Interrupt(testCtx(t))

		waitMultiHookEvents(t, sink, 1)
		events := sink.all()
		assert.Require(t, assert.Len(t, events, 1))
		assert.Eq(t, "Stop", events[0].Name) // the Interrupt event reached no trace
		assert.Eq(t, 1, events[0].Attrs["entry"])
	})
}

// TestMultiHookInterrupt covers HPF1 through the recording wrapper: an interrupt
// reaches exactly the entry that has to stop and the reply of a finished pipeline
// reaches its caller once.
func TestMultiHookInterrupt(t *testing.T) {
	t.Run("running: the interrupt reaches the waiting entry", func(t *testing.T) {
		client, logPath := newMultiHookClient(t, nil, nil,
			multiHookSpec{label: "A", mode: "block"},
			multiHookSpec{label: "B", mode: "wait", timeoutMS: 30000})
		h := recordInto(client, &eventSink{})

		w := h.StartStop(testCtx(t), sugahooks.Event{Name: "Stop", SessionID: "s1"})
		waitMultiHookLine(t, logPath, "B Stop start")

		start := time.Now()
		reason := w.Interrupt(testCtx(t))
		// A's block was already received, so it is handed back (the interrupt never
		// drops a reply); B was killed before it answered.
		assert.Eq(t, "A-reason", reason)
		assert.True(t, time.Since(start) < 5*time.Second, "the interrupt returns without hanging")
		assert.True(t, multiHookLogHas(t, logPath, "B Interrupt start"), "the waiting entry got the interrupt")
		assert.False(t, multiHookLogHas(t, logPath, "A Interrupt start"), "the entry that already ran got none")
	})

	t.Run("finished: the reply of a finished pipeline comes back once", func(t *testing.T) {
		client, logPath := newMultiHookClient(t, nil, nil,
			multiHookSpec{label: "A", mode: "block"}, multiHookSpec{label: "B", mode: "silent"})
		sink := &eventSink{}
		h := recordInto(client, sink)

		w := h.StartStop(testCtx(t), sugahooks.Event{Name: "Stop", SessionID: "s1"})
		recorded, ok := w.(*recordingWait)
		assert.Require(t, assert.True(t, ok))
		waitRecordedResult(t, recorded)

		reason := recorded.Interrupt(testCtx(t))
		assert.Eq(t, "A-reason", reason)
		assert.Eq(t, 0, len(recorded.out), "the reply is not handed back a second time")
		assert.True(t, multiHookLogHas(t, logPath, "B Interrupt start"), "the last entry that ran got the interrupt")
		assert.False(t, multiHookLogHas(t, logPath, "A Interrupt start"))
		select {
		case _, open := <-recorded.Done():
			assert.False(t, open, "Done is closed after the result was taken")
		case <-time.After(5 * time.Second):
			t.Fatal("the forwarding goroutine did not close Done")
		}
		// Both runs are in the trace, the interrupt itself is not.
		assert.Eq(t, 2, len(sink.all()))
	})
}

// newMultiHookBackgroundStop builds a background Stop over the fake pipeline and
// a real task manager with the given limits.
func newMultiHookBackgroundStop(t *testing.T, client sugahooks.Client, limits tasks.Limits) (*backgroundStop, *tasks.Manager) {
	t.Helper()
	dir := t.TempDir()
	m := tasks.NewManager(tasks.Config{Limits: limits, Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, id+".txt"), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(m.Close)
	return &backgroundStop{hooks: client, tasks: m, sessionID: "s1"}, m
}

// TestMultiHookBackgroundStop covers a Stop run as a background task: a block of
// an entry becomes a notice, and a Stop ended by a newer Stop (HF13), by
// tasks.max_duration or by Close (HF21) leaves none.
func TestMultiHookBackgroundStop(t *testing.T) {
	t.Run("a block of an entry is a notice of the Stop task", func(t *testing.T) {
		client, logPath := newMultiHookClient(t, nil, nil,
			multiHookSpec{label: "A", mode: "block", timeoutMS: 30000},
			multiHookSpec{label: "B", mode: "silent"})
		b, m := newMultiHookBackgroundStop(t, recordInto(client, &eventSink{}), tasks.Limits{})

		assert.Require(t, assert.NoErr(t, b.start(testCtx(t), sugahooks.Event{Name: "Stop", SessionID: "s1"})))
		info := waitStatus(t, m, b.current, tasks.Exited)
		assert.Eq(t, []string{"A", "B"}, multiHookStarted(t, logPath, "Stop"))

		notices := m.TakeNotices("s1")
		assert.Require(t, assert.Len(t, notices, 1))
		assert.Eq(t, info.ID, notices[0].ID)
		assert.Eq(t, "Stop hook asks to continue: A-reason", notices[0].Notice)
		assert.True(t, b.takeBlocked())
		assert.False(t, b.takeBlocked())
	})

	t.Run("HF13: a newer Stop leaves no notice of the killed one", func(t *testing.T) {
		// A block, B waits: the first Stop ends only by the kill. The waiting
		// compatible entry is phase two, so it never starts either.
		client, logPath := newMultiHookClient(t, func() {
			assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.stop_background", true)))
		}, &multiHookSpec{label: "C", mode: "wait", timeoutMS: 30000},
			multiHookSpec{label: "A", mode: "block", timeoutMS: 30000},
			multiHookSpec{label: "B", mode: "wait", timeoutMS: 30000})
		b, m := newMultiHookBackgroundStop(t, recordInto(client, &eventSink{}), tasks.Limits{})

		assert.Require(t, assert.NoErr(t, b.start(testCtx(t), sugahooks.Event{Name: "Stop", SessionID: "s1"})))
		first := b.current
		waitMultiHookLine(t, logPath, "B Stop start")
		assert.Require(t, assert.NoErr(t, b.start(testCtx(t), sugahooks.Event{Name: "Stop", StopHookActive: true, SessionID: "s1"})))

		// Kill waited for the old task before start returned, so its end is final.
		info, err := m.Get("s1", first)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, tasks.Killed, info.Status)
		assert.Eq(t, -1, info.ExitCode)
		assert.NotEq(t, first, b.current)
		firstNotices := make([]tasks.Info, 0, 1)
		for _, notice := range m.TakeNotices("s1") {
			if notice.ID == first {
				firstNotices = append(firstNotices, notice)
			}
		}
		assert.Empty(t, firstNotices)
		assert.False(t, b.takeBlocked())
		assert.False(t, multiHookLogHas(t, logPath, "C Stop start"), "the waiting compatible entry never ran")
	})

	t.Run("HF21: a Stop ended by tasks.max_duration leaves no notice", func(t *testing.T) {
		client, _ := newMultiHookClient(t, func() {
			assert.Require(t, assert.NoErr(t, gkconfig.Set("tasks.max_duration", "200ms")))
		}, nil,
			multiHookSpec{label: "A", mode: "block", timeoutMS: 30000},
			multiHookSpec{label: "B", mode: "wait", timeoutMS: 30000})
		settings, err := appconfig.ReadTaskSettings()
		assert.Require(t, assert.NoErr(t, err))
		b, m := newMultiHookBackgroundStop(t, recordInto(client, &eventSink{}), settings.Limits)

		assert.Require(t, assert.NoErr(t, b.start(testCtx(t), sugahooks.Event{Name: "Stop", SessionID: "s1"})))
		waitStatus(t, m, b.current, tasks.TimedOut)
		assert.Empty(t, m.TakeNotices("s1"))
		assert.False(t, b.takeBlocked())
	})

	t.Run("HF21: closing the manager leaves no notice", func(t *testing.T) {
		client, logPath := newMultiHookClient(t, nil, nil,
			multiHookSpec{label: "A", mode: "block", timeoutMS: 30000},
			multiHookSpec{label: "B", mode: "wait", timeoutMS: 30000})
		b, m := newMultiHookBackgroundStop(t, recordInto(client, &eventSink{}), tasks.Limits{})

		assert.Require(t, assert.NoErr(t, b.start(testCtx(t), sugahooks.Event{Name: "Stop", SessionID: "s1"})))
		waitMultiHookLine(t, logPath, "B Stop start")
		m.Close()
		assert.Empty(t, m.TakeNotices("s1"))
		assert.False(t, b.takeBlocked())
	})
}
