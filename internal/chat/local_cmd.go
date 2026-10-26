package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// The local commands of the chat prompt: a line typed here that starts with "!"
// runs straight in the local shell (the same shell, working directory and
// environment as shell_exec), its output goes to the terminal as it is
// produced, and nothing of it enters the session - no model call, no turn
// (用户 2026-10-14). "!!" runs the same and hands what the command produced to
// the model with the next message (localContext below).
//
// Only this keyboard runs them: a line delivered by `suga send`, by a job
// runner or by a Stop hook is an ordinary message whatever it starts with (the
// `case injected:` guard of Run's loop), so delivered text can never run a
// command here.
const (
	localRunPrefix  = "!"
	localSendPrefix = "!!"

	// localCommandHelpKey is the usage line of a bare "!" or "!!".
	localCommandHelpKey = "cmd.local.help"
	// localCommandBusyHint answers a "!…" line typed at the ↳ input line while a
	// turn runs: a command cannot run there, and the line goes back to the input
	// (the way a message a turn did not deliver does). The printed text comes from
	// the catalog (status.local.command_busy, steering.go and followup.go); this is
	// what the tests expect.
	localCommandBusyHint = "回合进行中不能执行本地命令（结束本轮后在提示符处运行）"
	// localSendNoticeKey tells the person that what the command produced travels to
	// the model with the next message.
	localSendNoticeKey = "cmd.local.send_notice"
	// localStoppedNoticeKey answers a command the person ended with Ctrl-C or Esc.
	localStoppedNoticeKey = "cmd.local.stopped"
	// localTimeoutNoticeKey answers a command that hit tools.shell_timeout_max.
	localTimeoutNoticeKey = "cmd.local.timeout"
)

// localCommandKind is what a "!" line asks for.
type localCommandKind uint8

const (
	// localRun ("!cmd"): run the command and show its output.
	localRun localCommandKind = iota + 1
	// localSend ("!!cmd"): run it too, and hand the result to the model with the
	// next message.
	localSend
)

// localCommand is one local command line of the chat prompt.
type localCommand struct {
	kind localCommandKind
	// text is the command as it was typed, without the "!" prefix; only spaces
	// (a bare "!" or "!!") is answered with the usage.
	text string
}

// parseLocalCommand reads a line typed at the prompt as a local command. ok is
// false for every other line, so a line that only looks like one stays an
// ordinary message: the prefix is read from the line as it was typed, and a
// leading space before the "!" (which the chat trims off the message) makes it
// text, not a command.
func parseLocalCommand(line string) (localCommand, bool) {
	switch {
	case strings.HasPrefix(line, localSendPrefix):
		return localCommand{kind: localSend, text: line[len(localSendPrefix):]}, true
	case strings.HasPrefix(line, localRunPrefix):
		return localCommand{kind: localRun, text: line[len(localRunPrefix):]}, true
	}
	return localCommand{}, false
}

// runLocalCommand runs one "!"/"!!" line typed at the prompt. The command runs
// in the local shell with the terminal as its output, Ctrl-C and Esc end it
// alone, and the chat stays where it is: the next prompt follows. A "!!"
// command's result is kept for the model (localContext) instead of being
// dropped with the output on screen.
func (s *chatState) runLocalCommand(cmd localCommand) {
	command := strings.TrimSpace(cmd.text)
	if command == "" {
		s.render.Hint(s.tr().T(localCommandHelpKey))
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	// The command's own read: Ctrl-C and Esc arrive as bytes instead of a SIGINT
	// that would end chat, and end this command only (nil without a terminal).
	watch := s.watchLocalCommand(cancel)
	// A child process reads plain key bytes: win32-input-mode's key records (DECSET
	// 9001, Ctrl+Enter) would mean nothing to it, so the terminal goes back to bytes
	// for as long as it runs and takes the mode up again afterwards.
	s.source.PauseWin32Input()
	res, err := s.local.Run(ctx, command, s.source)
	s.source.ResumeWin32Input()
	watch.stop()
	// A Ctrl-C that ended a command is not the first of the two presses that
	// leave chat: the next one at the prompt starts the confirmation over.
	s.source.clearInterrupt()
	s.recordLocalCommand(command, cmd, res, err)

	if err != nil {
		if s.ctx.Err() == nil {
			s.render.Error(fmt.Errorf("%s", s.tr().T("cmd.local.error", map[string]any{"error": err.Error()})))
		}
		return
	}
	switch {
	case res.Cancelled:
		// The person ended it (Esc, Ctrl-C), or chat is leaving: say so only in
		// the first case.
		if s.ctx.Err() == nil {
			s.render.Notice(s.tr().T(localStoppedNoticeKey))
		}
	case res.TimedOut:
		s.render.Notice(s.tr().T(localTimeoutNoticeKey))
	case res.ExitCode != 0:
		s.render.Notice(s.tr().T("cmd.local.exit_code", map[string]any{
			"code": res.ExitCode, "elapsed": formatElapsed(res.Duration),
		}))
	}
	if cmd.kind != localSend {
		return
	}
	// The result joins the next message as additional context, before the
	// person's own text (localContext is folded into the next turn's
	// SystemContext and dropped when chat ends).
	if res.ModelText != "" {
		s.localContext = appendContext(s.localContext, res.ModelText)
	}
	s.render.Hint(s.tr().T(localSendNoticeKey))
}

// localKeyWatch is the keyboard read a running local command owns: it carries
// the keys as bytes (so Ctrl-C and Esc end the command instead of the chat) and
// hands a line typed meanwhile back to the input.
type localKeyWatch struct {
	source *chatInputSource
	// gen is the generation of the steer read this watcher works on: a key an
	// older read decided belongs to a turn that is over, not to this command.
	gen  uint64
	quit chan struct{}
	done chan struct{}
}

// watchLocalCommand opens the read a running local command owns and acts on
// what the person presses while it runs: Ctrl-C and Esc cancel the run, a line
// typed meanwhile goes back to the input line (the command is not a message, so
// the next prompt carries it). The read is a steer read, but Ctrl-C keeps its
// usual meaning in it (RequestCommandKeys): the line belongs to the command, so
// clearing it instead of ending the command would leave no way to stop it.
// It reports nil when the input is not a terminal (a chat fed by a pipe has no
// keys to watch; the command runs to its end).
func (s *chatState) watchLocalCommand(cancel context.CancelFunc) *localKeyWatch {
	if s.source.terminal == nil {
		return nil
	}
	gen, ok := s.source.RequestCommandKeys()
	if !ok {
		return nil
	}
	watch := &localKeyWatch{source: s.source, gen: gen, quit: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(watch.done)
		for {
			select {
			case <-watch.quit:
				return
			case event := <-s.source.TurnEvents():
				watch.handle(event, cancel)
			}
		}
	}()
	return watch
}

// handle acts on one event of the command's read.
func (w *localKeyWatch) handle(event chatInputEvent, cancel context.CancelFunc) {
	if event.key != keyNone {
		if event.gen != w.gen {
			return // a key of another read (a turn's): not this command's business
		}
		// The key that ends the command (Ctrl-C, Esc) may carry the line it ends it
		// on (the read reports it: a local command's read keeps the line on Ctrl-C).
		// It belongs to the command exactly like a line event, so it goes back to
		// the input now - the editor was cleared on the way here, and the next
		// prompt must not be left without what was on the line.
		if strings.TrimSpace(event.line) != "" {
			w.source.putBackLine(event.line)
		}
		cancel()
		return
	}
	if event.err == nil && strings.TrimSpace(event.line) != "" {
		w.source.putBackLine(event.line)
	}
}

// stop ends the read and waits for the watcher to be gone, so the next prompt
// read owns the keyboard again; what the read reported meanwhile is taken here
// (the consumer is gone, it would sit in the channel otherwise).
func (w *localKeyWatch) stop() {
	if w == nil {
		return
	}
	close(w.quit)
	<-w.done
	for {
		select {
		case event := <-w.source.TurnEvents():
			// A key the watcher never acted on (its quit turned ready first): since
			// the command is over either way, only what a key carried - the line it
			// was typed on - belongs here; the wait loop's handle puts it back the
			// same way.
			if event.key != keyNone && strings.TrimSpace(event.line) == "" {
				continue
			}
			if strings.TrimSpace(event.line) != "" {
				w.source.putBackLine(event.line)
			}
		default:
			return
		}
	}
}

// recordLocalCommand writes the local_command event of one run: what was typed
// (redacted), how it ended, how much it wrote and whether the result goes to the
// model with the next message.
func (s *chatState) recordLocalCommand(command string, cmd localCommand, res native.LocalCommandResult, err error) {
	if s.runner == nil || s.sessionID == "" {
		return
	}
	attrs := map[string]any{
		"command": truncateLine(redact.Secrets(command), 200),
		"bytes":   res.Bytes,
	}
	if err == nil {
		attrs["exit_code"] = res.ExitCode
	}
	if cmd.kind == localSend {
		attrs["sent"] = true
	}
	if res.Cancelled {
		attrs["cancelled"] = true
	}
	if res.TimedOut {
		attrs["timeout"] = true
	}
	event := personal.HostEvent{
		Kind: store.EventLocalCommand, Name: "run", Status: "ok", Duration: res.Duration, Attrs: attrs,
	}
	if err != nil {
		event.Status, event.Error = "error", err.Error()
	}
	s.runner.RecordEvent(s.ctx, s.sessionID, event)
}
