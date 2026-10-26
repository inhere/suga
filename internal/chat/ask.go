package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/inhere/suga/pkg/agentcore/tools"
)

// errAskCancelled is returned when the person ends an approval question with
// Ctrl-C or Ctrl-D. It wraps context.Canceled so the runner records the turn as
// cancelled; the approval layer wraps it in tools.ErrAborted.
var errAskCancelled = fmt.Errorf("确认被取消: %w", context.Canceled)

// askPollInterval is how often Ask re-checks a pending read that is between
// states (just starting, or its finished line not yet in events).
const askPollInterval = 2 * time.Millisecond

// chatAsker binds the chat terminal to the approval layer (tools.Asker).
type chatAsker struct {
	source   *chatInputSource
	render   *Renderer
	notifier chatNotifier
}

func (a chatAsker) Ask(ctx context.Context, q tools.Question) (string, error) {
	// The question borrows the input line, so the turn's status line above it goes
	// away for as long as the question is up (it would read as if the tool were
	// still working away on its own) and comes back after the answer.
	a.render.suspendStatus()
	defer a.render.resumeStatus()
	a.notifier.notify(a.render.tr().T("approval.notify.waiting", map[string]any{"tool": q.Tool}))
	// A file_edit/file_write is shown as the diff it would make first, so the
	// person answers having seen what the call changes (same cut as any preview).
	return a.source.Ask(ctx, a.render.ApprovalDiff(q.Tool)+a.render.ApprovalDetails(q), a.render.ApprovalPrompt(q))
}

// Ask shows detail and reads one answer line on prompt, in the middle of a
// turn. Nothing typed before the question can become the answer:
//
//  1. Raw mode for the question (turnActive is cleared meanwhile); afterwards
//     turnActive and cooked mode are restored, so Ctrl-C in a tool that runs
//     next raises SIGINT again.
//  2. Unread terminal input is flushed; a byte the reader already holds is
//     dropped (inputEpoch) and one already passed to the editor is waited for.
//     A byte the byte pump holds (the activity handshake, or the lone-Esc wait)
//     is dropped the same way: it was read before the flush, so its epoch no
//     longer matches by the time the pump hands it over.
//  3. A read started before the turn may still be pending: a line that already
//     finished (in events, or on the way) is kept as the next input after the
//     turn and a new read asks the question, while a read still editing has its
//     half-typed line taken out (prefilled into the next prompt read) and
//     answers the question itself.
//  4. activity events are acknowledged; the answer stays out of history;
//     deliveries keep queueing (they are never read here).
//
// The read in progress answers in modeAsk: whatever it reads while the question is
// on screen goes to "answers", Ask's own channel, so no consumer of the prompt or
// turn channel can take an answer as input of its own (N5). A line that finished just
// before the question is taken out of either channel (settlePendingRead), and endAsk
// gives the read its mode back.
//
// Ctrl-C/Ctrl-D returns errAskCancelled. Without a terminal it returns
// tools.ErrCannotAsk.
func (s *chatInputSource) Ask(ctx context.Context, detail, prompt string) (string, error) {
	if s.terminal == nil {
		return "", tools.ErrCannotAsk
	}
	s.askMu.Lock()
	defer s.askMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", tools.ErrCannotAsk
	}
	s.turnActive = false
	// The read in progress is the answer read now: its result goes to answers, so no
	// other consumer can take it as input of its own, and Ctrl-C cancels the question
	// even with text typed (the read of a prompt or of a turn drops such a line and
	// goes on instead). Both are given back by endAsk.
	prevMode, prevClears := s.readMode, s.ctrlCClears
	s.readMode, s.ctrlCClears = modeAsk, false
	if err := s.enterRawLocked(); err != nil {
		s.readMode, s.ctrlCClears = prevMode, prevClears
		s.turnActive = true
		s.mu.Unlock()
		return "", err
	}
	if s.flushInput != nil {
		_ = s.flushInput() // best effort: the epoch below still drops held bytes
	}
	s.inputEpoch++
	// An answer left over from a question that was cancelled must not be read as the
	// answer of this one: drop it (and acknowledge its activity, which the pump may
	// be waiting for).
	select {
	case event := <-s.answers:
		if event.activity {
			s.AckActivity()
		}
	default:
	}
	for s.handing {
		s.cond.Wait()
	}
	pending := s.pending
	s.mu.Unlock()

	var held *chatInputEvent
	answerRead := false // an answer read is outstanding (its event not consumed)
	defer func() { s.endAsk(held, answerRead, prevMode, prevClears) }()

	reuse := false
	if pending {
		var err error
		if held, reuse, err = s.settlePendingRead(ctx); err != nil {
			return "", err
		}
	}

	if _, err := s.Write([]byte(detail)); err != nil {
		return "", err
	}
	answerRead = true
	if reuse {
		// The read a turn started is borrowed (design N5): the question draws its own
		// prompt on it and the answer shows as it is typed (UpdatePrompt repaints
		// even when no line is on the screen).
		if err := s.terminal.UpdatePrompt(prompt); err != nil {
			return "", err
		}
		// The borrowed read is answering the question now: endAsk gives it its mode's
		// Ctrl-C behaviour back (the read is the person's own input line again).
		s.terminal.SetCtrlCClearsLine(false)
	} else {
		// A previous question can have just restored the turn prompt while its read
		// ended. Repaint the new question before asking the reader for a fresh read;
		// readLine's SetPrompt only affects a block that is not already on screen.
		if err := s.terminal.UpdatePrompt(prompt); err != nil {
			return "", err
		}
		s.mu.Lock()
		s.pending = true
		s.mu.Unlock()
		select {
		case s.requests <- chatInputRequest{prompt: prompt}:
		case <-ctx.Done():
			s.CompleteLine() // the request was never taken
			answerRead = false
			return "", ctx.Err()
		case <-s.ctx.Done():
			return "", s.ctx.Err()
		}
	}
	for {
		var event chatInputEvent
		select {
		case event = <-s.answers:
		case event = <-s.events:
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.ctx.Done():
			return "", s.ctx.Err()
		}
		if event.activity {
			s.AckActivity()
			continue
		}
		s.CompleteLine()
		answerRead = false
		if event.err != nil {
			s.terminal.AbortLine()
			return "", errAskCancelled
		}
		return event.line, nil
	}
}

// settlePendingRead finds out what the read started before the turn is doing.
// It returns the finished line to keep (held), or reuse=true when the read is
// still editing and will answer the question (its half line is saved).
func (s *chatInputSource) settlePendingRead(ctx context.Context) (held *chatInputEvent, reuse bool, err error) {
	// take handles one event: activity is acknowledged (its held byte is
	// dropped by the epoch); anything else is the finished line.
	take := func(event chatInputEvent) *chatInputEvent {
		if event.activity {
			s.AckActivity()
			return nil
		}
		return &event
	}
	for {
		// The finished line may wait on either channel: a prompt read delivers to
		// events, a read that already answered a question to answers.
		select {
		case event := <-s.answers:
			if held = take(event); held != nil {
				return held, false, nil
			}
			continue
		default:
		}
		select {
		case event := <-s.events:
			if held = take(event); held != nil {
				return held, false, nil
			}
			continue
		default:
		}
		line, reading := s.terminal.TakeLine()
		s.addSavedLine(line)
		if reading {
			return nil, true, nil
		}
		// Not editing: the read is just starting, or its line is on the way.
		select {
		case event := <-s.answers:
			if held = take(event); held != nil {
				return held, false, nil
			}
		case event := <-s.events:
			if held = take(event); held != nil {
				return held, false, nil
			}
		case <-time.After(askPollInterval):
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-s.ctx.Done():
			return nil, false, s.ctx.Err()
		}
	}
}

// endAsk restores the turn state: cooked mode and turnActive, the mode and the
// Ctrl-C behaviour the question replaced, and a line that finished before the
// question goes back to events as the next input (unless an answer read is still
// outstanding, which only happens when the turn is being cancelled).
func (s *chatInputSource) endAsk(held *chatInputEvent, answerRead bool, mode inputMode, ctrlCClears bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnActive = true
	s.readMode, s.ctrlCClears = mode, ctrlCClears
	if s.terminal != nil {
		s.terminal.SetCtrlCClearsLine(ctrlCClears)
	}
	if s.steering {
		// The turn keeps reading the keyboard after the question (chat.steering):
		// back to raw input, not cooked.
		_ = s.enterRawLocked()
	} else {
		_ = s.leaveRawLocked()
	}
	if mode == modeSteer && s.terminal != nil {
		// Back at the turn's own input line: the shared read echoes again (a question
		// that hid it gives it back) and shows the turn's prompt instead of the
		// question's, so the person can go on typing.
		s.terminal.RestoreEcho()
		_ = s.terminal.UpdatePrompt(steerPrompt)
	}
	if held != nil && !answerRead {
		s.pending = true
		select {
		case s.events <- *held:
		default:
		}
	}
}

func (s *chatInputSource) addSavedLine(line string) {
	if line == "" {
		return
	}
	s.mu.Lock()
	s.savedLine += line
	s.mu.Unlock()
}

func (s *chatInputSource) takeSavedLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := s.savedLine
	s.savedLine = ""
	return saved
}

func (s *chatInputSource) currentEpoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputEpoch
}

// handOver marks a byte as passed to the line editor, unless the input was
// flushed after it was read (then it must be dropped and false is returned).
func (s *chatInputSource) handOver(epoch uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEpoch != epoch {
		return false
	}
	s.handing = true
	return true
}

// byteConsumed records that the line editor processed the last byte it got.
func (s *chatInputSource) byteConsumed() {
	s.mu.Lock()
	if s.handing {
		s.handing = false
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}
