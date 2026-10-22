package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/inhere/suga/pkg/agentcore/internal/proctree"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/inhere/suga/pkg/agentcore/api"
)

const (
	// DefaultShellTimeout is used when the call gives no timeout.
	DefaultShellTimeout = 120 * time.Second
	// DefaultShellBackgroundAfter is the foreground grace period before a
	// long-running command is handed to the task manager.
	DefaultShellBackgroundAfter = 120 * time.Second
	// DefaultShellTimeoutMax caps the timeout a call may ask for
	// (tools.shell_timeout_max).
	DefaultShellTimeoutMax = 600 * time.Second
	// shellWaitDelay bounds how long Wait keeps reading pipes after the shell
	// exited or was killed: a background child holding stdout must not hang
	// the tool.
	shellWaitDelay = 2 * time.Second
)

// psPrefix makes PowerShell write UTF-8 (both its own output and what it
// pipes to native commands) so Chinese text is not mangled, turns off ANSI
// colors (PowerShell 7 only; they cost tokens and carry no information), and
// opens the script block psSuffix closes.
const psPrefix = "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);$OutputEncoding=[Console]::OutputEncoding;" +
	"if ($PSStyle) { $PSStyle.OutputRendering='PlainText' }\n& {\n"

// psSuffix pipes the command's output to Out-Default before exiting: implicit
// table formatting (Get-ChildItem, Select-Object, ...) waits about 300 ms to
// size its columns, and an exit inside that window dropped the whole table,
// so the model saw an empty result with exit code 0. It also makes the native
// command's exit code the process exit code; a failed cmdlet (with no native
// exit code) exits 1. -Command alone would report only whether the last
// statement succeeded.
const psSuffix = "\n$global:__ok=$? } | Out-Default\nif ($LASTEXITCODE) { exit $LASTEXITCODE }; if (-not $__ok) { exit 1 }; exit 0"

// shellSpec is the resolved shell the tool runs commands with.
type shellSpec struct {
	path       string // executable name or path
	powershell bool   // PowerShell argv, prefix and suffix
}

// shellBase is the lower-case executable name without directory or .exe; it
// splits on both separators so a Windows path is handled on any OS.
func shellBase(p string) string {
	return strings.TrimSuffix(strings.ToLower(p[strings.LastIndexAny(p, `/\`)+1:]), ".exe")
}

// name is the shell's display name for descriptions and prompts.
func (s shellSpec) name() string {
	base := shellBase(s.path)
	if s.powershell {
		if base == "pwsh" {
			return "PowerShell 7 (pwsh)"
		}
		return "Windows PowerShell"
	}
	return base
}

// argv builds the full command line for command.
func (s shellSpec) argv(command string) []string {
	if s.powershell {
		return []string{s.path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", psPrefix + command + psSuffix}
	}
	return []string{s.path, "-c", command}
}

// resolveShell picks the shell: an explicit tools.shell wins (a command name
// or path, PowerShell detected by name); otherwise Windows uses pwsh, then
// powershell, and every other OS uses sh. bash is never auto-detected on
// Windows because the one on PATH may be WSL's.
func resolveShell(explicit string) shellSpec {
	isPS := func(p string) bool {
		base := shellBase(p)
		return base == "pwsh" || base == "powershell"
	}
	if explicit != "" {
		return shellSpec{path: explicit, powershell: isPS(explicit)}
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("pwsh"); err == nil {
			return shellSpec{path: p, powershell: true}
		}
		return shellSpec{path: "powershell", powershell: true}
	}
	return shellSpec{path: "sh"}
}

// ShellName reports the shell shell_exec uses for the given tools.shell value
// (empty = OS default), for the system prompt.
func ShellName(explicit string) string { return resolveShell(explicit).name() }

// ShellExecTool runs a shell command inside the workspace directory. A call
// either completes within its foreground timeout or, when tasks are enabled,
// transfers a long-running process tree to the task manager without dropping
// output; the configured maximum remains a hard kill deadline.
type ShellExecTool struct {
	workspace  string
	shell      shellSpec
	timeout    time.Duration // default per call
	maxTimeout time.Duration
	// backgroundAfter hands a foreground command to tasks instead of killing it;
	// zero disables the handoff.
	backgroundAfter time.Duration
	maxOut          int // raw result cap in bytes; 0 = DefaultRawOutputBytes
	// interpreters are the script interpreters detected on PATH
	// (tools.interpreters); non-empty adds the write-a-script advice to the
	// description.
	interpreters []Interpreter
	// tasks runs commands with background: true; nil hides the option.
	tasks *tasks.Manager
	// extraEnv adds the variables suga exports to a command (its session and
	// turn). It is read at every run, so a session switch, a sub-agent's own
	// session and another ACP session of the same process are all seen.
	extraEnv func(context.Context) []string
	// limits caps the memory, process count and CPU time of a run and of every
	// process it starts (tools.shell_limits); the zero value is no limit at all.
	limits ShellLimits
}

// SetTasks enables background: true (the host owns m and its lifetime).
func (t *ShellExecTool) SetTasks(m *tasks.Manager) { t.tasks = m }

// SetExtraEnv adds the environment entries extra returns for a call
// (native.Env.ExtraEnv): the session and turn a command runs for.
func (t *ShellExecTool) SetExtraEnv(extra func(context.Context) []string) { t.extraEnv = extra }

// NewShellExecTool returns a ShellExecTool scoped to workspace using the OS
// default shell. timeout is the default per call (0 = DefaultShellTimeout).
func NewShellExecTool(workspace string, timeout time.Duration) *ShellExecTool {
	if timeout <= 0 {
		timeout = DefaultShellTimeout
	}
	return &ShellExecTool{
		workspace:       workspace,
		shell:           resolveShell(""),
		timeout:         timeout,
		maxTimeout:      DefaultShellTimeoutMax,
		backgroundAfter: DefaultShellBackgroundAfter,
	}
}

func (t *ShellExecTool) Name() string { return "shell_exec" }
func (t *ShellExecTool) Description() string {
	syntax := "POSIX sh syntax"
	if t.shell.powershell {
		syntax = "PowerShell syntax (use ; not &&, $env:NAME for env vars)"
	}
	desc := fmt.Sprintf("Execute a command with %s in the workspace directory; write %s. "+
		"Returns stdout, stderr and the exit code; output gathered before a timeout is kept. "+
		"Default timeout %ds, max %ds. No interactive input: stdin is empty.",
		t.shell.name(), syntax, int(t.timeout.Seconds()), int(t.maxTimeout.Seconds()))
	if t.shell.powershell {
		desc += " " + PowerShellQuoteAdvice
		// With an interpreter at hand a script file beats a long single line of
		// PowerShell quoting (the environment section names the interpreters).
		if len(t.interpreters) > 0 {
			desc += " " + WriteScriptAdvice
		}
	}
	if t.tasks != nil && t.backgroundAfter > 0 {
		desc += fmt.Sprintf(" Foreground commands running longer than %s are transferred to a background task instead of being killed; tools.shell_timeout_max (%s) remains the hard limit.", t.backgroundAfter, t.maxTimeout)
	}
	if !t.limits.Empty() {
		desc += fmt.Sprintf(" Every run is capped (%s, tools.shell_limits): a process over the memory limit fails its allocation, "+
			"one over the process or CPU time limit is ended; the result says so.", t.limits.describe())
	}
	if t.tasks != nil {
		desc += fmt.Sprintf(" With background: true it starts the command in the background and returns a task id at once "+
			"(long builds, full test runs, servers, watchers; ended after %s): keep working, read its output or wait for it "+
			"with the task tool; you are told when it ends.", t.tasks.Limits().MaxDuration)
		desc += fmt.Sprintf(" Adding every (seconds, at least %d) and/or watch_paths (globs) makes it a watch: the command "+
			"runs now and again on each tick or change of the matching files, and you are told only when its exit code or "+
			"output changes (e.g. keep the tests running while you edit).", int(minWatchEvery.Seconds()))
	}
	return desc
}

// ToolMeta marks the tool as an execute: it runs commands.
func (*ShellExecTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryExecute, ReadOnly: false}
}
func (t *ShellExecTool) Schema() api.ToolSchema {
	schema := api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Command to execute",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Timeout in seconds (default %d, max %d)", int(t.timeout.Seconds()), int(t.maxTimeout.Seconds())),
				},
			},
			"required": []any{"command"},
		},
	}
	if t.tasks != nil {
		props := schema.InputSchema["properties"].(map[string]any)
		props["background"] = map[string]any{
			"type":        "boolean",
			"description": "Run in the background and return a task id at once (timeout does not apply, except to each run of a watch)",
		}
		props["every"] = map[string]any{
			"type":        "integer",
			"description": fmt.Sprintf("With background: run the command again every N seconds (at least %d); a watch", int(minWatchEvery.Seconds())),
		}
		props["watch_paths"] = map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "With background: run the command again when files matching these globs (relative to the workspace, ** spans directories, *.go matches at any depth) change; a watch",
		}
	}
	return schema
}

// callTimeout reads the optional foreground timeout argument, clamped to
// maxTimeout. Once a command is handed to tasks, maxTimeout remains its hard
// deadline.
func (t *ShellExecTool) callTimeout(args map[string]any) time.Duration {
	secs, ok := numberArg(args["timeout"])
	if !ok || secs <= 0 {
		return t.timeout
	}
	d := time.Duration(secs * float64(time.Second))
	if d > t.maxTimeout {
		return t.maxTimeout
	}
	return d
}

func (t *ShellExecTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	command, _ := call.Arguments["command"].(string)
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("shell_exec: command is required")
	}
	watch, isWatch, err := parseWatch(call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("shell_exec: %w", err)
	}
	bg, _ := call.Arguments["background"].(bool)
	switch {
	case isWatch && (!bg || t.tasks == nil):
		return nil, fmt.Errorf("shell_exec: every and watch_paths need background: true")
	case isWatch:
		return t.startWatch(ctx, call, command, watch)
	case bg && t.tasks != nil:
		return t.startBackground(ctx, call, command)
	}
	timeout := t.callTimeout(call.Arguments)
	start := time.Now()
	argv := t.shell.argv(command)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = t.workspace
	cmd.Env = shellEnv(ctx, t.extraEnv)
	output := newHandoffOutput()
	cmd.Stdout, cmd.Stderr = output.stdout, output.stderr // stdin stays nil: the null device
	// The tree is created before Start so Unix gets a process group and Windows
	// puts the shell in a kill-on-close job object (with the resource limits on
	// it). Adopt preserves this same owner when the foreground call becomes a
	// task.
	tree := proctree.NewLimited(cmd, t.limits.tree())
	// WaitDelay forces Wait to return when a leftover child keeps a pipe open.
	cmd.WaitDelay = shellWaitDelay
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("shell_exec: start %s: %w", t.shell.name(), err)
	}
	tree.Started()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var handoff <-chan time.Time
	var handoffTimer *time.Timer
	if t.tasks != nil && t.backgroundAfter > 0 && t.backgroundAfter <= timeout {
		handoffTimer = time.NewTimer(t.backgroundAfter)
		handoff = handoffTimer.C
		defer handoffTimer.Stop()
	}
	var hardTimeout = timeout
	if handoff != nil {
		hardTimeout = t.maxTimeout
	}
	hardTimer := time.NewTimer(hardTimeout)
	defer hardTimer.Stop()

	var waitErr error
	var timedOut bool
	select {
	case waitErr = <-done:
		tree.Close()
	case <-ctx.Done():
		_ = tree.Kill()
		<-done
		tree.Close()
		return nil, ctx.Err()
	case <-handoff:
		if ctx.Err() != nil {
			_ = tree.Kill()
			<-done
			tree.Close()
			return nil, ctx.Err()
		}
		partial := foregroundOutput(output, t.maxOut)
		info, err := t.tasks.Adopt(ctx, tasks.Spec{Kind: "shell", Summary: taskSummary(command),
			Clean: cleanTaskLine}, tasks.RunningCommand{
			Cmd: cmd, Tree: tree, Wait: done, Started: start, MaxDuration: t.maxTimeout,
			AttachOutput: func(dst io.Writer) { output.attach(dst) },
		})
		if err != nil {
			// Adoption is the ownership boundary. If it cannot be completed, kill
			// the original tree here rather than leaving an orphan behind.
			_ = tree.Kill()
			<-done
			tree.Close()
			return nil, fmt.Errorf("shell_exec: transfer to background: %w", err)
		}
		return &api.ToolResult{
			CallID: call.CallID,
			Name:   t.Name(),
			Content: partial + fmt.Sprintf("\n[foreground command exceeded %s; transferred to background task %s, completion will notify]\nOutput: %s (read it with task output, or recall with the ref).",
				t.backgroundAfter, info.ID, info.Ref),
			Metadata: map[string]any{"task_id": info.ID, "background": true, "background_after": true},
		}, nil
	case <-hardTimer.C:
		timedOut = true
		_ = tree.Kill()
		waitErr = <-done
		tree.Close()
	}

	content := foregroundOutput(output, t.maxOut)
	exitCode := cmd.ProcessState.ExitCode()
	var status string
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case timedOut:
		status = fmt.Sprintf("[timeout after %s: process tree killed; output above is partial]", hardTimeout)
	case errors.Is(waitErr, exec.ErrWaitDelay):
		status = fmt.Sprintf("[exit code: %d; a background process kept the output open and was killed]", exitCode)
	case exitCode < 0 && waitErr != nil:
		status = fmt.Sprintf("[error: %v]", waitErr)
	default:
		status = fmt.Sprintf("[exit code: %d]", exitCode)
	}
	// A run the limits stopped says so: the exit code alone would read like the
	// program's own failure.
	limitHit := t.limits.limitHit(tree, exitCode, content)
	if limitHit {
		status += "\n" + t.limits.notice()
	}
	if content != "" {
		content += "\n"
	}
	result := &api.ToolResult{
		CallID:   call.CallID,
		Name:     t.Name(),
		Content:  content + status,
		Metadata: map[string]any{"exit_code": exitCode},
	}
	if limitHit {
		result.Metadata["limits_exceeded"] = true
	}
	return result, nil
}

// handoffWriter buffers foreground output until the manager owns the command,
// then forwards every subsequent write to the task output without dropping the
// bytes crossing the ownership boundary.
type handoffWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	target io.Writer
	prefix string
	marked bool
}

func (w *handoffWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.target == nil {
		return w.buf.Write(p)
	}
	w.markPrefix()
	return w.target.Write(p)
}

func (w *handoffWriter) markPrefix() {
	if w.prefix != "" && !w.marked {
		_, _ = io.WriteString(w.target, w.prefix)
		w.marked = true
	}
}

func (w *handoffWriter) attach(target io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.target = target
	if w.buf.Len() > 0 {
		w.markPrefix()
		_, _ = w.target.Write(w.buf.Bytes())
		w.buf.Reset()
	}
}

func (w *handoffWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

type handoffOutput struct {
	stdout *handoffWriter
	stderr *handoffWriter
}

func newHandoffOutput() *handoffOutput {
	return &handoffOutput{stdout: &handoffWriter{}, stderr: &handoffWriter{prefix: "\n[stderr]\n"}}
}

func (o *handoffOutput) attach(dst io.Writer) {
	o.stdout.attach(dst)
	o.stderr.attach(dst)
}

func foregroundOutput(o *handoffOutput, max int) string {
	var sb strings.Builder
	// Colour codes carry nothing for the model (test libraries such as
	// gookit/assert colour their failure reports even without a terminal).
	if out := ansiPattern.ReplaceAllString(decodeOutput(o.stdout.bytes()), ""); out != "" {
		sb.WriteString(strings.TrimRight(out, "\r\n"))
	}
	if errOut := ansiPattern.ReplaceAllString(decodeOutput(o.stderr.bytes()), ""); strings.TrimSpace(errOut) != "" {
		sb.WriteString("\n[stderr]\n")
		sb.WriteString(strings.TrimRight(errOut, "\r\n"))
	}
	return TruncateHeadTail(strings.TrimLeft(sb.String(), "\n"), rawLimit(max))
}

// shellEnv is the environment of a command: suga's own settings removed, the
// variables suga exports to the command on top (extra, read at every call),
// git and pagers kept from waiting for input.
func shellEnv(ctx context.Context, extra func(context.Context) []string) []string {
	env := dropEnv(commandEnv(os.Environ()), exportedEnv)
	if extra != nil {
		env = overrideEnv(env, extra(ctx))
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat")
}

// startBackground starts command as a background task of the session.
func (t *ShellExecTool) startBackground(ctx context.Context, call api.ToolCall, command string) (*api.ToolResult, error) {
	argv := t.shell.argv(command)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = t.workspace, shellEnv(ctx, t.extraEnv)
	info, err := t.tasks.Start(ctx, tasks.Spec{Kind: "shell", Summary: taskSummary(command), Cmd: cmd, Clean: cleanTaskLine,
		Limits: t.limits.tree(), LimitNotice: t.limits.notice()})
	if err != nil {
		return nil, fmt.Errorf("shell_exec: background: %w", err)
	}
	return &api.ToolResult{
		CallID: call.CallID,
		Name:   t.Name(),
		Content: fmt.Sprintf("[background task %s started]\nOutput: %s (read it with task output, or recall with the ref). "+
			"You will be told when it ends; task wait blocks on it, task kill stops it.", info.ID, info.Ref),
		Metadata: map[string]any{"task_id": info.ID, "background": true},
	}, nil
}

// taskSummary is the command on one line, cut to 200 runes.
func taskSummary(command string) string {
	s := strings.Join(strings.Fields(command), " ")
	if utf8.RuneCountInString(s) > 200 {
		s = string([]rune(s)[:199]) + "…"
	}
	return redact.Secrets(s)
}

// cleanTaskLine is one line of a background task's output as text: decoded,
// colour codes dropped, secrets masked.
func cleanTaskLine(line []byte) string {
	return redact.Secrets(ansiPattern.ReplaceAllString(decodeOutput(line), ""))
}

// decodeOutput returns b as text. Non-UTF-8 output (a native Windows program
// writing in the ANSI code page) is decoded as GBK as a best-effort fallback.
func decodeOutput(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(b); err == nil {
		return string(decoded)
	}
	return strings.ToValidUTF8(string(b), "�")
}

// numberArg reads a JSON number argument that models sometimes send as a
// string.
func numberArg(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		var f float64
		if _, err := fmt.Sscan(strings.TrimSpace(n), &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// privateEnv are suga's own settings. Commands run for the user's project do
// not inherit them: a project's code or tests that read SUGA_HOME (suga's
// own tests, for one) would otherwise act on the agent's configuration.
var privateEnv = []string{"SUGA_HOME", "SUGA_CONFIG", "SUGA_MODEL"}

// exportedEnv are the session variables suga sets per command. An inherited
// value (from a nested suga, or from the shell suga was started in) is dropped
// before the session's own entries go in: a session without a short name, or a
// command outside a turn, then has no value rather than a foreign one.
var exportedEnv = []string{"SUGA_SESSION_ID", "SUGA_SESSION_NAME", "SUGA_TURN_ID"}

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

// overrideEnv returns env with every name extra sets replaced by extra's value
// (names compared without case). The inherited entry is dropped rather than
// shadowed: Unix getenv returns the first match, so a parent's SUGA_SESSION_ID
// would otherwise win over the session's own one.
func overrideEnv(env, extra []string) []string {
	if len(extra) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+len(extra))
next:
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		for _, add := range extra {
			if addName, _, ok := strings.Cut(add, "="); ok && strings.EqualFold(name, addName) {
				continue next
			}
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// commandEnv returns env without privateEnv (names compared without case:
// Windows environment names are case-insensitive).
func commandEnv(env []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		for _, p := range privateEnv {
			if strings.EqualFold(name, p) {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}
