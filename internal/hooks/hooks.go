// Package hooks runs the user-configured hook command for chat events.
//
// Each event runs hooks.command once with the event as one JSON line on stdin.
// Events follow the Claude Code hook format (SessionStart, UserPromptSubmit,
// PostToolUse, Stop, SessionEnd) plus the suga-specific Interrupt, sent when the
// person types while a Stop call is still waiting. The command answers on stdout
// with optional JSON ({"decision","reason","hookSpecificOutput":{"additionalContext"}});
// a Stop answer with decision "block" and a reason starts a new turn with that
// reason as input. Anything on stderr is shown to the person as a notice.
// Failures are logged and never stop the chat.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	gkconfig "github.com/gookit/config/v2"
)

// Event is one hook invocation payload.
type Event struct {
	Name           string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Source         string `json:"source,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	// Injected marks a UserPromptSubmit whose input did not come from terminal typing
	// (Stop-block reply/notification or a local-socket delivery). Omitted when false.
	Injected       bool   `json:"injected,omitempty"`
	LastAssistant  string `json:"last_assistant_message,omitempty"`
	StopHookActive bool   `json:"stop_hook_active"`
	ToolName       string `json:"tool_name,omitempty"`
	ToolOutput     string `json:"tool_output,omitempty"`
}

// Response is the decoded hook answer.
type Response struct {
	Decision          string
	Reason            string
	AdditionalContext string
	// Notice is a one-line message the hook command wants the person at this
	// terminal to see (e.g. the session was taken over elsewhere). The command
	// prints it on its stderr, so the client captures that stream.
	Notice string
}

// Result is the outcome of a Stop wait.
type Result struct {
	Response Response
	Err      error
}

// Wait is a pending Stop hook call.
type Wait interface {
	Done() <-chan Result
	// Interrupt ends the wait. It returns a reply that had already been received
	// (Stop block) but not yet consumed, so the caller can still deliver it.
	Interrupt(context.Context) string
}

// Client calls the hook command.
type Client interface {
	Call(context.Context, Event) Response
	StartStop(context.Context, Event) Wait
	// PostToolName reports whether a PostToolUse event is sent for the tool,
	// and the tool name to put in it.
	PostToolName(tool string) (string, bool)
}

const defaultTimeoutMS = 3000

// backgroundStopTimeout is the hook's own bound on a background Stop: longer
// than any tasks.max_duration, which ends it first.
const backgroundStopTimeout = 7 * 24 * time.Hour

// StopBackground reports hooks.stop_background: chat runs the Stop hook as a
// background task that typing does not interrupt; a block reply reaches the
// model as a task notice (and wakes an idle chat).
func StopBackground() bool { return gkconfig.Bool("hooks.stop_background", false) }

// commandClient runs the configured argv once per event, the event JSON on stdin.
type commandClient struct {
	argv     []string
	stopArgs []string
	timeout  time.Duration
	// stopTimeout bounds the local wait for a Stop call.
	stopTimeout time.Duration
	postTool    PostToolFilter
	// extraEnv adds the variables suga exports to the hook command (the session
	// and turn of the call); nil leaves the inherited environment alone.
	extraEnv func(context.Context) []string
}

// Option configures the client New returns.
type Option func(*commandClient)

// WithExtraEnv adds the variables suga exports to the hook command (the
// session, its short name and the turn, see wiring's sessionEnvProvider). They
// are read at every call, so a session switch is followed.
func WithExtraEnv(fn func(context.Context) []string) Option {
	return func(c *commandClient) { c.extraEnv = fn }
}

// PostToolFilter selects the tools that send PostToolUse and the name they are sent as.
type PostToolFilter struct {
	// Tools are tool name globs (path.Match); empty sends no PostToolUse at all.
	Tools []string
	// Name replaces the tool name in the event; empty keeps the original.
	Name string
}

// Match reports whether tool sends PostToolUse and the name to send.
func (f PostToolFilter) Match(tool string) (string, bool) {
	for _, pattern := range f.Tools {
		if ok, _ := path.Match(pattern, tool); ok {
			if f.Name != "" {
				return f.Name, true
			}
			return tool, true
		}
	}
	return "", false
}

// New returns the hook client for the current configuration, or nil when hooks
// are disabled: hooks.command is unset or empty, or SUGA_HOOKS=off (tests use it
// so a local `go test` never runs a developer's configured hook command).
// Configuration keys:
//   - hooks.command: argv list, executed directly (never through a shell)
//   - hooks.timeout_ms: timeout of every non-Stop event (default 3000)
//   - hooks.stop_args: argv appended only to the Stop event
//   - hooks.stop_wait_sec: Stop waits up to stop_wait_sec+2 seconds; 0 uses timeout_ms
//     (with stop_background: no limit of its own, tasks.max_duration applies)
//   - hooks.stop_background: run Stop as a background task (see StopBackground)
//   - hooks.post_tool_use.tools / .name: PostToolUse tool globs and the name sent
//
// opts add what the caller knows (WithExtraEnv: the session variables of the
// command's environment).
func New(opts ...Option) (Client, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SUGA_HOOKS")), "off") {
		return nil, nil
	}
	argv, err := stringList("hooks.command")
	if err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(argv[0]) == "" {
		return nil, fmt.Errorf("hooks.command: the program name is empty")
	}
	stopArgs, err := stringList("hooks.stop_args")
	if err != nil {
		return nil, err
	}
	tools, err := stringList("hooks.post_tool_use.tools")
	if err != nil {
		return nil, err
	}
	for _, pattern := range tools {
		if strings.TrimSpace(pattern) == "" {
			return nil, fmt.Errorf("hooks.post_tool_use.tools contains an empty glob")
		}
		if _, err := path.Match(pattern, "sample"); err != nil {
			return nil, fmt.Errorf("hooks.post_tool_use.tools glob %q: %w", pattern, err)
		}
	}
	timeoutMS := gkconfig.Int("hooks.timeout_ms", defaultTimeoutMS)
	if timeoutMS <= 0 {
		timeoutMS = defaultTimeoutMS
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	stopTimeout := timeout
	// The extra 2s lets a command that waits stop_wait_sec answer before it is killed.
	if waitSec := gkconfig.Int("hooks.stop_wait_sec", 0); waitSec > 0 {
		stopTimeout = time.Duration(waitSec+2) * time.Second
	} else if StopBackground() {
		// A background Stop (a full test run, say) is bounded by the task
		// manager's tasks.max_duration instead.
		stopTimeout = backgroundStopTimeout
	}
	client := &commandClient{
		argv: argv, stopArgs: stopArgs, timeout: timeout, stopTimeout: stopTimeout,
		postTool: PostToolFilter{Tools: tools, Name: strings.TrimSpace(gkconfig.String("hooks.post_tool_use.name"))},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

// stringList reads an optional list of strings from the config.
func stringList(key string) ([]string, error) {
	raw := gkconfig.Get(key)
	if raw == nil {
		return nil, nil
	}
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...), nil
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a list of strings", key)
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be a list of strings", key)
	}
}

func (h *commandClient) PostToolName(tool string) (string, bool) { return h.postTool.Match(tool) }

func (h *commandClient) Call(ctx context.Context, event Event) Response {
	response, err := h.call(ctx, event, false)
	if err != nil {
		slog.Warn("hook command failed", "event", event.Name, "err", err)
	}
	return response
}

func (h *commandClient) StartStop(ctx context.Context, event Event) Wait {
	waitCtx, cancel := context.WithCancel(ctx)
	done := make(chan Result, 1)
	wait := &stopWait{call: h.Call, event: event, cancel: cancel, done: done}
	go func() {
		response, err := h.call(waitCtx, event, true)
		switch {
		case err == nil:
		case waitCtx.Err() != nil:
			// Cancelled on purpose (the user typed, or chat is exiting): the
			// kill error is expected and, on Windows, often "Access is denied"
			// for a child that already exited. Not worth a warning on the prompt.
			slog.Debug("stop hook cancelled", "event", event.Name, "err", err)
		default:
			slog.Warn("hook command failed", "event", event.Name, "err", err)
		}
		done <- Result{Response: response, Err: err}
		close(done)
	}()
	return wait
}

func (h *commandClient) call(parent context.Context, event Event, stop bool) (Response, error) {
	timeout := h.timeout
	args := append([]string(nil), h.argv[1:]...)
	if stop {
		timeout = h.stopTimeout
		args = append(args, h.stopArgs...)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	input, err := json.Marshal(event)
	if err != nil {
		return Response{}, err
	}
	input = append(input, '\n')
	cmd := exec.CommandContext(ctx, h.argv[0], args...)
	cmd.Env = hookEnv(ctx, event.SessionID, h.extraEnv)
	cmd.Stdin = bytes.NewReader(input)
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return Response{}, err
	}
	var wire struct {
		Decision           string `json:"decision"`
		Reason             string `json:"reason"`
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if len(bytes.TrimSpace(output)) != 0 {
		if err := json.Unmarshal(output, &wire); err != nil {
			return Response{}, err
		}
	}
	return Response{Decision: wire.Decision, Reason: wire.Reason, AdditionalContext: wire.HookSpecificOutput.AdditionalContext, Notice: strings.TrimSpace(stderr.String())}, nil
}

type stopWait struct {
	call   func(context.Context, Event) Response
	event  Event
	cancel context.CancelFunc
	done   chan Result
}

func (w *stopWait) Done() <-chan Result { return w.done }

func (w *stopWait) Interrupt(parent context.Context) string {
	interrupt := w.event
	interrupt.Name = "Interrupt"
	interrupt.LastAssistant = ""
	interrupt.StopHookActive = false
	ctx, cancel := context.WithTimeout(parent, time.Second)
	w.call(ctx, interrupt)
	cancel()
	w.cancel()
	// The reply may have landed in done before cancellation; hand it back instead of dropping it.
	result := <-w.done
	if result.Err == nil && result.Response.Decision == "block" {
		return result.Response.Reason
	}
	return ""
}

// hookEnv is the environment of the hook command: the inherited one with the
// session variables of the call on top (sessionID fills in SUGA_SESSION_ID when
// the ctx carries no session, e.g. chat's own SessionStart). The inherited entry
// of a name being set is dropped, not shadowed: Unix getenv returns the first
// match, so the parent's value would otherwise win.
func hookEnv(ctx context.Context, sessionID string, extra func(context.Context) []string) []string {
	var pairs []string
	if extra != nil {
		pairs = extra(ctx)
	}
	if sessionID != "" && !envHas(pairs, envSessionID) {
		pairs = append(pairs, envSessionID+"="+sessionID)
	}
	if len(pairs) == 0 {
		return dropEnv(os.Environ(), exportedEnv)
	}
	env := dropEnv(os.Environ(), exportedEnv)
	out := make([]string, 0, len(env)+len(pairs))
next:
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		for _, add := range pairs {
			if addName, _, ok := strings.Cut(add, "="); ok && strings.EqualFold(name, addName) {
				continue next
			}
		}
		out = append(out, kv)
	}
	return append(out, pairs...)
}

// The session variables hookEnv adds; SUGA_SESSION_ID is also what the event's
// session_id says. GOFER_SESSION_ID is not touched: it is gofer's own name.
const (
	envSessionID   = "SUGA_SESSION_ID"
	envSessionName = "SUGA_SESSION_NAME"
	envTurnID      = "SUGA_TURN_ID"
)

// exportedEnv are the names hookEnv sets: an inherited value is dropped before
// them, so a session without a short name (or outside a turn) has no value, not
// the one of a parent process.
var exportedEnv = []string{envSessionID, envSessionName, envTurnID}

// dropEnv returns env without the entries named in names (without case).
func dropEnv(env []string, names []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		for _, drop := range names {
			if strings.EqualFold(name, drop) {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

// envHas reports whether pairs sets name (without case).
func envHas(pairs []string, name string) bool {
	for _, kv := range pairs {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// limitedBuffer keeps at most noticeLimit bytes of a hook's stderr.
type limitedBuffer struct{ bytes.Buffer }

const noticeLimit = 1024

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := noticeLimit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return TruncateOutput(b.Buffer.String(), noticeLimit) }

// AdditionalContext is the trimmed additional context of a hook response.
func AdditionalContext(response Response) string {
	return strings.TrimSpace(response.AdditionalContext)
}

// TruncateOutput cuts value to at most maxBytes without splitting a UTF-8 rune.
func TruncateOutput(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return value
}
