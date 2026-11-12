package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// hookProcessEvent is the part of the event JSON the fake hook command reads.
type hookProcessEvent struct {
	Name string `json:"hook_event_name"`
}

// TestPipelineHookProcess is the fake hook command of the pipeline tests, started
// as "<test binary> -test.run=^TestPipelineHookProcess$ -- <label> <mode>". It
// does nothing unless SUGA_HOOK_PROCESS is set, so the ordinary test run does not
// behave like a hook.
//
// Every event appends "<label> <event> start" to SUGA_HOOK_LOG when it begins and
// "<label> <event> end" before it exits, so the log tells which entry ran in which
// order and whether it was killed. SessionStart and UserPromptSubmit answer
// additionalContext "<label>-ctx" and the notice "notice-<label>"; the mode
// decides the Stop answer (block, fail, wait, silent, ifail, timeout).
func TestPipelineHookProcess(t *testing.T) {
	if os.Getenv("SUGA_HOOK_PROCESS") == "" {
		return
	}
	label, mode := hookProcessArgs()
	if label == "" {
		os.Exit(2)
	}
	logPath := os.Getenv("SUGA_HOOK_LOG")
	name := hookProcessEventName(os.Stdin)
	hookProcessLog(logPath, label+" "+name+" start")
	end := func() { hookProcessLog(logPath, label+" "+name+" end") }

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
		case "wait":
			// Runs until the hook's bound kills it (Stop waits are the only ones
			// that can get here).
			time.Sleep(30 * time.Second)
		case "ifail":
			time.Sleep(30 * time.Second)
		}
	}
	end()
	os.Exit(0)
}

// hookProcessArgs returns the label and mode that follow the "--" separator of
// the test binary's arguments.
func hookProcessArgs() (string, string) {
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

// hookProcessEventName reads the hook event name of the JSON payload on stdin;
// an unreadable payload is reported as "Unknown".
func hookProcessEventName(stdin io.Reader) string {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "Unknown"
	}
	var event hookProcessEvent
	if err := json.Unmarshal(data, &event); err != nil || event.Name == "" {
		return "Unknown"
	}
	return event.Name
}

// hookProcessLog appends one line to the shared log. Every line is a separate
// append, which is what lets the parent watch for a "start" line while the
// command is still running.
func hookProcessLog(path, line string) {
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

// hookSpec is one fake hook command of a pipeline test.
type hookSpec struct {
	label   string
	mode    string
	timeout int
}

// hookProcessCommand is the argv of the fake hook command for one spec.
func hookProcessCommand(spec hookSpec) []string {
	return []string{os.Args[0], "-test.run=^TestPipelineHookProcess$", "--", spec.label, spec.mode}
}

// newPipelineTestClient builds the pipeline of a fake compatible hooks.command
// (nil for none) and the fake hooks.commands entries, and returns it with the
// path of the shared hook log. prepare runs after the config reset and before
// New, so a case can set the keys New reads.
func newPipelineTestClient(t *testing.T, prepare func(), compat *hookSpec, entries ...hookSpec) (*pipeline, string) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "")
	t.Setenv("SUGA_HOOK_PROCESS", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	logPath := filepath.Join(t.TempDir(), "hook.log")
	t.Setenv("SUGA_HOOK_LOG", logPath)
	if prepare != nil {
		prepare()
	}
	if compat != nil {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.command", hookProcessCommand(*compat))))
	}
	if len(entries) > 0 {
		list := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			item := map[string]any{"command": hookProcessCommand(entry)}
			if entry.timeout > 0 {
				item["timeout_ms"] = entry.timeout
			}
			list = append(list, item)
		}
		assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.commands", list)))
	}
	client, err := New()
	assert.Require(t, assert.NoErr(t, err))
	p, ok := client.(*pipeline)
	assert.Require(t, assert.True(t, ok, "a hooks.commands setup builds a pipeline"))
	return p, logPath
}

// readHookLog returns the lines the fake hook commands wrote so far; a missing log
// means nothing has run yet.
func readHookLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	assert.Require(t, assert.NoErr(t, err))
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// startedLabels returns the labels of the events whose "start" line is in the
// log, in log order.
func startedLabels(t *testing.T, path, eventName string) []string {
	t.Helper()
	suffix := " " + eventName + " start"
	labels := make([]string, 0, len(readHookLog(t, path)))
	for _, line := range readHookLog(t, path) {
		if strings.HasSuffix(line, suffix) {
			labels = append(labels, strings.TrimSuffix(line, suffix))
		}
	}
	return labels
}

// TestPipelineEvents covers the ordinary events of the pipeline: which entries
// run, in which order, how their answers merge and which events never reach the
// entries.
func TestPipelineEvents(t *testing.T) {
	t.Run("ordinary events run the compatible entry and then the entries", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, &hookSpec{label: "l0", mode: "silent"},
			hookSpec{label: "l1", mode: "silent"}, hookSpec{label: "l2", mode: "silent"})
		response := p.Call(context.Background(), Event{Name: "SessionStart"})

		assert.Eq(t, []string{"l0", "l1", "l2"}, startedLabels(t, logPath, "SessionStart"))
		// Every command ran to its end: the last one logged its "end" before the
		// call returned.
		lines := readHookLog(t, logPath)
		assert.Require(t, assert.True(t, len(lines) > 0))
		assert.Eq(t, "l2 SessionStart end", lines[len(lines)-1])
		assert.Eq(t, "l0-ctx\n\nl1-ctx\n\nl2-ctx", response.AdditionalContext)
		assert.Eq(t, "notice-l0\nnotice-l1\nnotice-l2", response.Notice)
		assert.Eq(t, "", response.Decision)
		assert.Eq(t, "", response.Reason)
		assert.Nil(t, response.UpdatedInput)
		entries := make([]int, 0, len(response.Runs))
		for _, run := range response.Runs {
			assert.NoErr(t, run.Err)
			assert.Eq(t, filepath.Base(os.Args[0]), run.Program)
			entries = append(entries, run.Entry)
		}
		assert.Eq(t, []int{0, 1, 2}, entries)
	})

	t.Run("a timed out entry is cut off and the next entry still runs", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, &hookSpec{label: "l0", mode: "silent"},
			hookSpec{label: "l1", mode: "timeout", timeout: 200}, hookSpec{label: "l2", mode: "silent"})
		response := p.Call(context.Background(), Event{Name: "SessionStart"})

		assert.Eq(t, []string{"l0", "l1", "l2"}, startedLabels(t, logPath, "SessionStart"))
		// The killed command never reached its "end" line, the next one did.
		assert.False(t, hookLogHasLine(t, logPath, "l1 SessionStart end"), "the timed out entry was killed")
		assert.True(t, hookLogHasLine(t, logPath, "l2 SessionStart end"), "the next entry ran to its end")
		assert.Eq(t, "l0-ctx\n\nl2-ctx", response.AdditionalContext)
		assert.Eq(t, "notice-l0\nnotice-l2", response.Notice)
		assert.Require(t, assert.Eq(t, 3, len(response.Runs)))
		assert.True(t, errors.Is(response.Runs[1].Err, ErrTimedOut), "the entry hit its own bound")
		assert.NoErr(t, response.Runs[2].Err)
	})

	t.Run("a failing entry does not keep the next one from running", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, &hookSpec{label: "l0", mode: "silent"},
			hookSpec{label: "l1", mode: "fail"}, hookSpec{label: "l2", mode: "silent"})
		response := p.Call(context.Background(), Event{Name: "UserPromptSubmit"})

		assert.Eq(t, []string{"l0", "l1", "l2"}, startedLabels(t, logPath, "UserPromptSubmit"))
		assert.True(t, hookLogHasLine(t, logPath, "l1 UserPromptSubmit end"), "the failing entry ran")
		assert.True(t, hookLogHasLine(t, logPath, "l2 UserPromptSubmit end"), "the next entry ran")
		assert.Eq(t, "l0-ctx\n\nl2-ctx", response.AdditionalContext)
		assert.Eq(t, "notice-l0\nnotice-l2", response.Notice)
		assert.Require(t, assert.Eq(t, 3, len(response.Runs)))
		assert.Err(t, response.Runs[1].Err)
		assert.Eq(t, "", response.Runs[1].Response.AdditionalContext)
		assert.NoErr(t, response.Runs[2].Err)
	})

	t.Run("an unknown event name reaches the entries too", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, &hookSpec{label: "l0", mode: "silent"},
			hookSpec{label: "l1", mode: "silent"})
		response := p.Call(context.Background(), Event{Name: "SomeFutureEvent"})

		assert.Eq(t, []string{"l0", "l1"}, startedLabels(t, logPath, "SomeFutureEvent"))
		assert.Require(t, assert.Eq(t, 2, len(response.Runs)))
	})

	t.Run("PostToolUse only reaches the compatible entry", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, &hookSpec{label: "l0", mode: "silent"},
			hookSpec{label: "l1", mode: "silent"}, hookSpec{label: "l2", mode: "silent"})
		response := p.Call(context.Background(), Event{Name: "PostToolUse", ToolName: "shell_exec"})

		assert.Eq(t, []string{"l0"}, startedLabels(t, logPath, "PostToolUse"))
		assert.Eq(t, []string{"l0"}, allStartedLabels(t, logPath), "the entries got no tool event")
		assert.Require(t, assert.Eq(t, 1, len(response.Runs)))
		assert.Eq(t, 0, response.Runs[0].Entry)
	})

	t.Run("PostToolUse without a compatible entry starts no command", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, nil, hookSpec{label: "l1", mode: "silent"})
		_, send := p.PostToolName("shell_exec")
		assert.False(t, send)

		response := p.Call(context.Background(), Event{Name: "PostToolUse", ToolName: "shell_exec"})
		assert.Eq(t, Response{}, response)
		assert.Nil(t, readHookLog(t, logPath))
	})

	t.Run("the entries receive no PreToolUse", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, nil, hookSpec{label: "l1", mode: "silent"})
		_, send := p.PreToolName("shell_exec")
		assert.False(t, send)

		response, err := p.PreTool(context.Background(), Event{Name: "PreToolUse", ToolName: "shell_exec"})
		assert.NoErr(t, err)
		assert.Eq(t, Response{}, response)
		assert.Nil(t, readHookLog(t, logPath))
	})

	t.Run("the tool events stay with the compatible entry when both are configured", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, func() {
			assert.NoErr(t, gkconfig.Set("hooks.post_tool_use.tools", []string{"shell_*"}))
			assert.NoErr(t, gkconfig.Set("hooks.pre_tool_use.tools", []string{"shell_*"}))
		}, &hookSpec{label: "l0", mode: "silent"}, hookSpec{label: "l1", mode: "silent"})

		name, send := p.PostToolName("shell_exec")
		assert.True(t, send)
		assert.Eq(t, "shell_exec", name)
		name, send = p.PreToolName("shell_exec")
		assert.True(t, send)
		assert.Eq(t, "shell_exec", name)
		p.Call(context.Background(), Event{Name: "PostToolUse", ToolName: "shell_exec"})
		assert.Eq(t, []string{"l0"}, allStartedLabels(t, logPath))
	})
}

// hookLogHasLine reports whether the log already holds the given line.
func hookLogHasLine(t *testing.T, path, line string) bool {
	t.Helper()
	for _, got := range readHookLog(t, path) {
		if got == line {
			return true
		}
	}
	return false
}

// allStartedLabels returns the labels of every started event in log order.
func allStartedLabels(t *testing.T, path string) []string {
	t.Helper()
	labels := make([]string, 0, len(readHookLog(t, path)))
	for _, line := range readHookLog(t, path) {
		label, ok := strings.CutSuffix(line, " start")
		if ok {
			labels = append(labels, strings.Fields(label)[0])
		}
	}
	return labels
}
