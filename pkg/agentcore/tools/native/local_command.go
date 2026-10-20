package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/internal/proctree"
	"github.com/inhere/suga/pkg/agentcore/redact"
)

// LocalCommand runs one command a person typed at the chat prompt (a "!cmd" or
// "!!cmd" line, chat's local command). It runs exactly the way shell_exec runs
// one: the same shell (tools.shell, else the OS default), the workspace as the
// working directory, the same environment (suga's own settings removed) and the
// same promise that the whole process tree dies with the run - the person's
// Ctrl-C and the timeout both go through it. The output is streamed to the
// caller as it is produced (the person watches the command run) and the same
// text is handed back, tidied by the tool output minimizer and cut to the
// inline limit, for the next model message.
//
// It is deliberately not a tool: a local command never goes through approval,
// the tool list or a turn. shell_exec's "[stderr]" marker is the one difference
// from a tool call: stdout and stderr share the stream the person watches, so
// the text is their interleaving.
type LocalCommand struct {
	workspace string
	shell     shellSpec
	// timeout bounds one run (tools.shell_timeout_max: a command a person typed
	// themselves gets the generous end of the limits, not the tool default).
	timeout time.Duration
	// rawBytes is how much output is kept for the model (0 = DefaultRawOutputBytes),
	// inlineBytes the largest block handed to the model (0 = DefaultMaxOutputBytes).
	rawBytes    int
	inlineBytes int
	// minimize is the tool result tidier (tools.OutputConfig.Minimize) the model
	// text goes through: the same output rules a shell_exec result gets. nil
	// keeps the raw text.
	minimize func(api.ToolCall, string) (string, bool)
	// extraEnv adds the variables suga exports to a command (its session and
	// turn), read at every run like shell_exec's own (native.Env.ExtraEnv).
	extraEnv func(context.Context) []string
}

// LocalCommandOptions configures a LocalCommand runner; every zero value falls
// back to shell_exec's own default.
type LocalCommandOptions struct {
	// Workspace is the directory the command runs in (the session's workspace).
	Workspace string
	// Shell is tools.shell: a command name or path ("", the OS default).
	Shell string
	// Timeout caps one run (tools.shell_timeout_max: 0 = DefaultShellTimeoutMax).
	Timeout time.Duration
	// RawBytes caps the output kept (tools.max_spill_bytes).
	RawBytes int
	// InlineBytes caps the block handed to the model (tools.max_output_bytes).
	InlineBytes int
	// Minimize is the tool result tidier (wiring's minimizeOutput); nil keeps
	// the output as it is.
	Minimize func(api.ToolCall, string) (string, bool)
	// ExtraEnv adds the variables suga exports to a command (native.Env.ExtraEnv).
	ExtraEnv func(context.Context) []string
}

// NewLocalCommand returns the runner of a front end's local commands.
func NewLocalCommand(opts LocalCommandOptions) *LocalCommand {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultShellTimeoutMax
	}
	return &LocalCommand{
		workspace:   opts.Workspace,
		shell:       resolveShell(opts.Shell),
		timeout:     timeout,
		rawBytes:    opts.RawBytes,
		inlineBytes: opts.InlineBytes,
		minimize:    opts.Minimize,
		extraEnv:    opts.ExtraEnv,
	}
}

// ShellName names the shell this runner uses (for the banner and the usage
// hint); it is the shell shell_exec would use for the same configuration.
func (c *LocalCommand) ShellName() string { return c.shell.name() }

// LocalCommandResult is what one run produced.
type LocalCommandResult struct {
	// ExitCode is the shell's exit code (-1 when it was killed before exiting).
	ExitCode int
	// Duration is how long the command ran.
	Duration time.Duration
	// Bytes is how much output the command wrote.
	Bytes int
	// TimedOut is true when the run hit its timeout (the process tree was killed).
	TimedOut bool
	// Cancelled is true when ctx ended the run (the person's Ctrl-C or Esc, or
	// chat leaving).
	Cancelled bool
	// ModelText is the block the next model message carries for a "!!" command:
	// the command, its exit code and its output (tidied like a shell_exec result
	// and cut to the inline limit). Empty when the command never ran or wrote
	// nothing at all.
	ModelText string
}

// Run runs command, streaming its output to stream (nil: the output is only
// collected) and killing the whole process tree when ctx ends or the timeout is
// reached. An error is returned only when the command could not be started (or
// the shell itself failed): a cancellation, a timeout and a non-zero exit code
// are reported on the result.
func (c *LocalCommand) Run(ctx context.Context, command string, stream io.Writer) (LocalCommandResult, error) {
	var res LocalCommandResult
	runCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	argv := c.shell.argv(command)
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = c.workspace
	cmd.Env = shellEnv(ctx, c.extraEnv)
	out := &commandOutput{stream: stream, budget: rawLimit(c.rawBytes)}
	// One writer for both streams: exec then copies them with a single
	// goroutine, so a write never lands inside another one.
	cmd.Stdout, cmd.Stderr = out, out
	// tree is the OS process group / job object; Cancel kills all of it, and
	// WaitDelay forces Wait to return when a leftover child keeps a pipe open.
	tree := proctree.New(cmd)
	cmd.Cancel = tree.Kill
	cmd.WaitDelay = shellWaitDelay

	started := time.Now()
	status := ""
	if err := cmd.Start(); err != nil {
		// The shell could not start. A run the person (or chat) ended before it
		// was up - a Ctrl-C pressed in the moment the command starts - is a
		// cancellation, not a failure.
		if runCtx.Err() == nil {
			return res, fmt.Errorf("start %s: %w", c.shell.name(), err)
		}
		res.ExitCode = -1
	} else {
		tree.Started()
		waitErr := cmd.Wait()
		tree.Close() // also ends background children left behind by the command
		res.ExitCode = cmd.ProcessState.ExitCode()
		switch {
		case errors.Is(waitErr, exec.ErrWaitDelay):
			status = fmt.Sprintf("[exit code: %d; a background process kept the output open and was killed]", res.ExitCode)
		case waitErr != nil && res.ExitCode < 0 && runCtx.Err() == nil:
			// A shell killed by a signal nobody here sent. A tree killed for the
			// person's Ctrl-C or the timeout also ends by a signal on Unix
			// ("signal: killed", exit code -1): that is the cancellation or the
			// timeout reported below, not a failure.
			return res, fmt.Errorf("run %s: %w", c.shell.name(), waitErr)
		}
	}
	res.Duration = time.Since(started)
	res.Bytes = out.bytes
	// The deadline is the run's own; a cancellation is chat's context (the
	// person's Ctrl-C, or chat leaving).
	res.TimedOut = runCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
	res.Cancelled = !res.TimedOut && ctx.Err() != nil
	switch {
	case res.TimedOut:
		status = fmt.Sprintf("[timeout after %s: process tree killed; output above is partial]", c.timeout)
	case res.Cancelled && status == "":
		status = "[cancelled: process tree killed; output above is partial]"
	}
	if status == "" {
		status = fmt.Sprintf("[exit code: %d]", res.ExitCode)
	}
	res.ModelText = c.modelText(command, res, out.text(), status)
	return res, nil
}

// modelText is the block the model receives for a "!!" command: the command, its
// exit code and duration, then the output - colour codes dropped and the output
// minimizer's tidy text applied, exactly like the content of a shell_exec
// result - cut to the inline limit.
func (c *LocalCommand) modelText(command string, res LocalCommandResult, output, status string) string {
	content := status
	if output != "" {
		content = output + "\n" + status
	}
	// The command line often carries a token (--token <x>): it is masked like
	// every other text that enters the session.
	header := fmt.Sprintf("[本地命令] %s · 退出码 %d · 耗时 %s",
		oneLine(redact.Secrets(command)), res.ExitCode, formatCommandDuration(res.Duration))
	if res.TimedOut {
		header += " · 超时已终止"
	} else if res.Cancelled {
		header += " · 已终止"
	}
	if c.minimize != nil {
		call := api.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": command}}
		if tidy, ok := c.minimize(call, content); ok {
			return TruncateHeadTail(header+"\n"+tidy+
				fmt.Sprintf("\n[输出已整理：原文 %d 字节]", len(content)), c.inlineBytes)
		}
	}
	return TruncateHeadTail(header+"\n"+content, c.inlineBytes)
}

// oneLine collapses a command to one line for the model-facing header, so a
// multi-line command cannot look like several messages.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// formatCommandDuration renders a run's length the way chat's notices do.
func formatCommandDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(time.Millisecond).String()
}

// commandOutput streams what a command writes and keeps a bounded copy of it:
// the head and the tail up to budget bytes, with a marker naming what was
// dropped in between. A command that never stops printing (a runaway loop)
// therefore cannot grow the chat's memory; the person still sees every byte.
type commandOutput struct {
	stream io.Writer
	budget int
	bytes  int // everything the command wrote
	head   []byte
	tail   []byte
	// dropped counts the bytes neither head nor tail kept.
	dropped int
}

func (o *commandOutput) Write(p []byte) (int, error) {
	o.bytes += len(p)
	o.keep(p)
	if o.stream != nil {
		if _, err := o.stream.Write(p); err != nil {
			// The person's terminal is gone: keep collecting, the command
			// itself must not fail on that.
			o.stream = nil
		}
	}
	return len(p), nil
}

// keep records p in the bounded copy: the head fills up first, everything after
// it is the tail (which keeps the last half of the budget).
func (o *commandOutput) keep(p []byte) {
	half := o.budget / 2
	if len(o.head) < half {
		room := half - len(o.head)
		o.head = append(o.head, p[:min(room, len(p))]...)
		p = p[min(room, len(p)):]
	}
	if len(p) == 0 {
		return
	}
	o.tail = append(o.tail, p...)
	if len(o.tail) > half {
		drop := len(o.tail) - half
		o.tail = append(o.tail[:0], o.tail[drop:]...)
		o.dropped += drop
	}
}

// text is the kept output as text: decoded, colour codes dropped, with an
// omission marker when the budget cut something away.
func (o *commandOutput) text() string {
	head := ansiPattern.ReplaceAllString(decodeOutput(o.head), "")
	tail := ansiPattern.ReplaceAllString(decodeOutput(o.tail), "")
	if o.dropped == 0 {
		return strings.TrimLeft(head+tail, "\n")
	}
	return strings.TrimLeft(head, "\n") + fmt.Sprintf("\n…[省略 %d 字节，输出上限 %d 字节]…\n", o.dropped, o.budget) + strings.TrimLeft(tail, "\n")
}
