package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"

	"github.com/inhere/suga/internal/output"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// The --verify delivery check (design docs/design/2026-10-21-run-verify-design.md):
// after a normally finished turn the caller's command decides whether the
// delivery is done; a failure is handed back to the model as an injected turn.
const (
	// DefaultVerifyRounds is how many fix rounds a failing check gets when
	// --verify-rounds is not given (the first check does not count).
	DefaultVerifyRounds = 2
	// MaxVerifyRounds caps --verify-rounds: an unbounded loop would just burn
	// tokens on a command the model cannot fix.
	MaxVerifyRounds = 5
	// DefaultVerifyTimeout bounds one verify command (--verify-timeout).
	DefaultVerifyTimeout = 10 * time.Minute
	// verifyTailLines and verifyTailChars cap the failure text that enters the
	// session (the last 120 lines, at most 8000 characters).
	verifyTailLines = 120
	verifyTailChars = 8000
	// verifyKeepBytes is how much of the command's output the tail is built
	// from: the whole output may be megabytes, the model gets its end.
	verifyKeepBytes = 256 << 10
)

// ErrVerifyFailed ends a run with exit code 5: the --verify command never
// passed within --verify-rounds fix rounds.
var ErrVerifyFailed = errors.New("验证未通过")

// verifySettings is one run's resolved --verify options.
type verifySettings struct {
	// Command is --verify: the command the caller wants to pass.
	Command string
	// Rounds is how many fix rounds follow a failing check.
	Rounds int
	// Timeout bounds one run of the command.
	Timeout time.Duration
	// Workspace is the directory the command runs in (the session workspace),
	// the same one shell_exec uses.
	Workspace string
	// Shell is tools.shell ("" = the OS default): exactly the shell shell_exec
	// would run a command with.
	Shell string
}

// verifySettingsFrom resolves the flags of opts for workspace. A zero rounds or
// timeout (an options struct built without the flags) takes the default.
func verifySettingsFrom(opts personalOptions, workspace string) verifySettings {
	rounds, timeout := opts.VerifyRounds, opts.VerifyTimeout
	if rounds <= 0 {
		rounds = DefaultVerifyRounds
	}
	if timeout <= 0 {
		timeout = DefaultVerifyTimeout
	}
	return verifySettings{
		Command:   strings.TrimSpace(opts.Verify),
		Rounds:    rounds,
		Timeout:   timeout,
		Workspace: workspace,
		Shell:     gkconfig.String("tools.shell"),
	}
}

// needsVerify reports whether a finished turn is checked by --verify: only a
// normally completed turn is (a failed, cancelled or yielded turn keeps its own
// exit semantics: error, 130, input required), and only when the caller gave a
// command.
func (o personalOptions) needsVerify(result personal.RunResult, err error) bool {
	return err == nil && strings.TrimSpace(o.Verify) != "" && result.State == personal.RunCompleted
}

// verifyRun is one execution of the --verify command.
type verifyRun struct {
	// Round counts from 0: the first check.
	Round int
	// ExitCode is the shell's exit code (-1 when killed before exiting).
	ExitCode int
	Duration time.Duration
	Passed   bool
	TimedOut bool
	// Cancelled is true when the run's context ended the command (Ctrl-C).
	Cancelled bool
	// Bytes is everything the command wrote, both streams together.
	Bytes int
	// Tail is the end of the merged output, redacted and cleaned: what the
	// model is shown. Kept for a failure only.
	Tail string
}

// verifyReport is the outcome of a run's --verify loop.
type verifyReport struct {
	// Rounds is how many times the command ran (1 = the check alone).
	Rounds int
	// Passed is the last run's outcome.
	Passed bool
	// Last is the last run, whose failure text mode summarises at the end.
	Last verifyRun
}

// verifyRunner is the slice of *personal.Runner the check needs: a fix round is
// an ordinary injected turn.
type verifyRunner interface {
	Run(ctx context.Context, req personal.RunRequest) (personal.RunResult, error)
	RecordEvent(ctx context.Context, sessionID string, ev personal.HostEvent)
}

// verifyAfterTurn runs the --verify command on a finished turn and, while it
// fails, hands the failure back as an injected turn - the same way
// finishBackgroundTasks gives a turn one more chance. It returns the last
// turn's result (a fix round replaces it) and the report; the caller turns a
// failing report into exit code 5. An error is returned only when the command
// could not be started or the context ended.
func verifyAfterTurn(ctx context.Context, vr verifyRunner, set verifySettings, sessionID string,
	result personal.RunResult, emit personal.EmitFunc, live *output.LiveText,
	encoder *json.Encoder, stderr io.Writer) (personal.RunResult, *verifyReport, error) {
	report := &verifyReport{}
	round := 0
	for {
		run, err := runVerifyCommand(ctx, set, round)
		if err != nil {
			return result, report, err
		}
		report.Rounds, report.Passed, report.Last = round+1, run.Passed, run
		recordVerifyRun(ctx, vr, sessionID, run)
		if err := emitVerifyRun(encoder, stderr, set.Command, run); err != nil {
			return result, report, err
		}
		if run.Passed {
			return result, report, nil
		}
		if run.Cancelled {
			// Ctrl-C (or a parent that went away) while checking: the command was
			// killed with its process tree and the run ends cancelled.
			return result, report, fmt.Errorf("%w: %v", context.Canceled, ctx.Err())
		}
		// A round that ended on a limit (budget, max_steps) has no room left for a
		// fix: the check stops here and the run keeps its own exit semantics.
		if round >= set.Rounds || result.WrapUp != "" {
			return result, report, nil
		}
		if live != nil {
			live.Finish() // end the streamed answer line before the fix round starts
		}
		next, err := vr.Run(ctx, personal.RunRequest{
			SessionID: sessionID, Prompt: verifyFailurePrompt(set.Command, run), Injected: true, Emit: emit,
		})
		result = next
		if err != nil {
			return result, report, err
		}
		// Only a normally finished turn is checked again: a yielded or failed fix
		// round keeps its own exit semantics (input required / error).
		if result.State != personal.RunCompleted || result.Status == agentcore.RunYielded {
			return result, report, nil
		}
		round++
	}
}

// runVerifyCommand runs the verify command once, the way shell_exec would run
// it: the same shell (tools.shell), the session workspace as the working
// directory, stdin empty, and the whole process tree killed when ctx ends or the
// timeout is reached.
func runVerifyCommand(ctx context.Context, set verifySettings, round int) (verifyRun, error) {
	run := verifyRun{Round: round}
	sink := &verifyTailBuffer{limit: verifyKeepBytes}
	runner := native.NewLocalCommand(native.LocalCommandOptions{
		Workspace: set.Workspace,
		Shell:     set.Shell,
		Timeout:   set.Timeout,
	})
	// The command's output is only collected here (stream nil): the model sees
	// the tail of it, never the terminal.
	res, err := runner.Run(ctx, set.Command, sink)
	if err != nil {
		return run, fmt.Errorf("verify %q: %w", redact.Secrets(set.Command), err)
	}
	run.ExitCode, run.Duration = res.ExitCode, res.Duration
	run.TimedOut, run.Cancelled, run.Bytes = res.TimedOut, res.Cancelled, res.Bytes
	run.Passed = res.ExitCode == 0 && !res.TimedOut && !res.Cancelled
	if !run.Passed {
		run.Tail = verifyTail(sink.String())
	}
	return run, nil
}

// verifyTail is the failure text handed to the model: secrets masked and
// terminal control sequences dropped first, then cut to the last 120 lines and
// at most 8000 characters, so a cut can never leave a partially masked
// credential behind.
func verifyTail(written string) string {
	text := strings.TrimRight(termtext.Clean(redact.Secrets(written)), "\n")
	if lines := strings.Split(text, "\n"); len(lines) > verifyTailLines {
		text = strings.Join(lines[len(lines)-verifyTailLines:], "\n")
	}
	if r := []rune(text); len(r) > verifyTailChars {
		text = string(r[len(r)-verifyTailChars:])
	}
	return text
}

// verifyFailurePrompt is the injected turn that hands a failing check back to
// the model: what failed, with the end of the output, and the one thing it may
// not do about it.
func verifyFailurePrompt(command string, run verifyRun) string {
	var b strings.Builder
	b.WriteString("[verify failed] 验证命令没有通过，先把代码改到它通过，再给出最终答复。\n\n")
	fmt.Fprintf(&b, "命令: %s\n", oneLineText(redact.Secrets(command)))
	fmt.Fprintf(&b, "退出码: %d\n", run.ExitCode)
	if run.TimedOut {
		fmt.Fprintf(&b, "超时: 命令被终止（输出只是其中一部分）\n")
	}
	fmt.Fprintf(&b, "耗时: %s\n", formatVerifyDuration(run.Duration))
	if run.Tail != "" {
		fmt.Fprintf(&b, "\n输出尾部（最后 %d 行，最多 %d 字符）:\n%s\n", verifyTailLines, verifyTailChars, run.Tail)
	} else {
		b.WriteString("\n（命令没有输出）\n")
	}
	b.WriteString("\n请修复问题后重新给出最终答复；不要修改验证命令或删除测试来让它通过。")
	return b.String()
}

// emitVerifyRun prints one check: text mode writes its one stderr line, and
// stream-json writes the "verify" line the caller reads.
func emitVerifyRun(encoder *json.Encoder, stderr io.Writer, command string, run verifyRun) error {
	if encoder == nil {
		fmt.Fprintln(stderr, verifyTextLine(command, run))
		return nil
	}
	return encoder.Encode(output.VerifyLine(run.Round, redact.Secrets(command), run.ExitCode, run.Passed, run.Duration))
}

// verifyTextLine renders text mode's one line: verify #2: go test ./... → exit 1 (12.3s)
func verifyTextLine(command string, run verifyRun) string {
	line := fmt.Sprintf("verify #%d: %s → exit %d (%s)", run.Round+1,
		oneLineText(termtext.Clean(command)), run.ExitCode, formatVerifyDuration(run.Duration))
	if run.TimedOut {
		line += " 超时"
	}
	return line
}

// verifySummary is text mode's closing note of a check that never passed: what
// ran, how it ended, and the tail of its last output. stdout stays the answer.
func verifySummary(command string, report *verifyReport) string {
	run := report.Last
	var b strings.Builder
	fmt.Fprintf(&b, "验证未通过：%s → exit %d（共验证 %d 次，含 %d 轮回灌）",
		oneLineText(termtext.Clean(command)), run.ExitCode, report.Rounds, max(report.Rounds-1, 0))
	if run.TimedOut {
		b.WriteString(" · 最后一次超时")
	}
	if run.Tail != "" {
		b.WriteString("\n最后一次输出尾部:\n" + run.Tail)
	}
	return b.String()
}

// recordVerifyRun writes the "verify" trace event, so `suga inspect trace`
// shows the delivery check next to the turn (attrs: round, exit, duration,
// output_len, passed).
func recordVerifyRun(ctx context.Context, vr verifyRunner, sessionID string, run verifyRun) {
	attrs := map[string]any{
		"round": run.Round, "exit": run.ExitCode, "duration_ms": run.Duration.Milliseconds(),
		"output_len": run.Bytes, "passed": run.Passed,
	}
	if run.TimedOut {
		attrs["timeout"] = true
	}
	ev := personal.HostEvent{Kind: store.EventVerify, Name: "run", Status: "ok", Duration: run.Duration, Attrs: attrs}
	if !run.Passed {
		ev.Status = "error"
	}
	vr.RecordEvent(ctx, sessionID, ev)
}

// verifyTailBuffer keeps the end of what the verify command wrote (both streams
// together), so a command printing megabytes cannot grow the run's memory.
type verifyTailBuffer struct {
	limit int
	buf   []byte
	bytes int
}

func (b *verifyTailBuffer) Write(p []byte) (int, error) {
	b.bytes += len(p)
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = append(b.buf[:0], b.buf[len(b.buf)-b.limit:]...)
	}
	return len(p), nil
}

// String is the kept tail as text.
func (b *verifyTailBuffer) String() string { return string(b.buf) }

// oneLineText collapses a command or output line to one line, so a multi-line
// command cannot look like several messages.
func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

// formatVerifyDuration renders a check's length in seconds as text mode prints
// it (12.3s), or in milliseconds when it was faster.
func formatVerifyDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
