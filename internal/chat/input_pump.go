package chat

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/inhere/suga/pkg/chatui/lineedit"
	"github.com/inhere/suga/pkg/chatui/lineedit/pump"
)

// The reading side of the chat input: the mode one read is in, the event that read
// produces and the byte pump the line editor reads through. chat_input.go holds the
// source itself (deliveries, requests, terminal modes).
//
// Read contract (design F2): a read happens only while a request is outstanding —
// the reader goroutine is blocked inside the line editor's Read, so input typed
// between two requests is never taken early. The lone-Esc decision does not change
// that: it asks the platform whether a follow-up byte is ready (WaitInput) instead
// of starting a read of its own.

// inputMode is how one read interprets what the person types. Exactly one mode is
// in use at a time: the mode of the read the reader is serving.
type inputMode uint8

const (
	// modePrompt reads a line for the next turn, between turns: the chat loop takes
	// the result from events (activity handshake, Ctrl-C/Ctrl-D rules unchanged).
	modePrompt inputMode = iota
	// modeSteer reads while a turn runs (S2b): Enter submits an inserted message,
	// Ctrl-C and Esc ask to end this turn only, Ctrl-D is ignored. Results go to
	// turnEvents.
	modeSteer
	// modeAsk reads the answer of an approval question (ask.go): the answer goes to
	// answers, so no consumer of another mode can take it as input of its own.
	modeAsk
)

// turnKey is what a steer read reports when a key, not a message, was pressed
// during a turn.
type turnKey uint8

const (
	keyNone  turnKey = iota
	keyAbort         // Esc, or the first Ctrl-C during a turn: end this turn only
	keyExit          // a second Ctrl-C within exitConfirmWindow: leave chat
)

// escWaitTimeout and pasteNewlineTimeout are the waits of the byte pump
// (pkg/chatui/lineedit/pump).
const (
	escWaitTimeout      = pump.EscWaitTimeout
	pasteNewlineTimeout = pump.PasteNewlineTimeout
)

// chatActivityReader is the byte pump the line editor reads through: the pump of
// pkg/chatui/lineedit/pump with the chat input source as its Hooks (the mode of the
// read in progress, the activity handshake, the epoch of flushed input, the sticky
// EOF). The pump is made on the first Read, when the source's terminal exists.
type chatActivityReader struct {
	source *chatInputSource
	input  io.Reader
	once   sync.Once
	pump   *pump.Pump
}

func (r *chatActivityReader) Read(p []byte) (int, error) {
	r.init()
	return r.pump.Read(p)
}

func (r *chatActivityReader) init() {
	r.once.Do(func() {
		r.pump = pump.NewPump(r.input, r.source)
		r.pump.Bind(r.source.terminal)
	})
}

// resetActivity re-arms the activity handshake for a new read (see
// pump.Pump.ResetFirstByte).
func (r *chatActivityReader) resetActivity() {
	r.init()
	r.pump.ResetFirstByte()
}

// PasteNewlineWanted reports whether the pump may take a newline for pasted text:
// only a read on a terminal whose platform wait really reports a ready byte
// (pump.WaitInputSupported). Elsewhere the wait would answer "a byte follows" for
// every newline, so a typed Enter could never submit a line.
func (s *chatInputSource) PasteNewlineWanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal != nil && s.inputWait != nil && pump.WaitInputSupported
}

// AltEnterWanted reports whether the read in progress takes ESC followed by Enter as
// "insert a newline in the input" and has the wait for the byte after the ESC. The
// prompt and question reads do; a steer read owns the lone Esc as "end this turn".
func (s *chatInputSource) AltEnterWanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readMode != modeSteer && s.terminal != nil && s.inputWait != nil && pump.WaitInputSupported
}

// EscKeyWanted reports whether the read in progress takes a lone Esc as a key press
// and has a wait for the follow-up byte: a steer read on a terminal. Every other
// read keeps x/term's behaviour, an Esc byte waiting for its sequence.
func (s *chatInputSource) EscKeyWanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readMode == modeSteer && s.terminal != nil && s.inputWait != nil
}

// WaitInput reports whether more input is ready within timeout, without reading it
// (inputwait_*.go: poll(2) on Unix, WaitForSingleObject on Windows). It is the wait
// behind the lone-Esc decision and the pasted-newline decision, so the timeout
// leaves no read pending.
func (s *chatInputSource) WaitInput(timeout time.Duration) bool {
	s.mu.Lock()
	wait := s.inputWait
	s.mu.Unlock()
	if wait == nil {
		// No wait wired up: leave the Esc byte to the editor as a sequence and the
		// newline as a submit.
		return true
	}
	return wait(timeout)
}

// Win32Records is win32Records under the name of pump.Hooks.
func (s *chatInputSource) Win32Records() bool { return s.win32Records() }

// OnEscPressed is escKeyPressed under the name of pump.Hooks.
func (s *chatInputSource) OnEscPressed() { s.escKeyPressed() }

var _ pump.Hooks = (*chatInputSource)(nil)

// escKeyPressed is the lone Esc of a steer read (lineedit.EscCallback): the person
// asks to end this turn only (design D2). The pump declared the key, so this runs
// inside the read that was not waiting for a sequence; a read that is no longer the
// steer read (the turn ended in between) reports nothing.
func (s *chatInputSource) escKeyPressed() {
	s.mu.Lock()
	steer, gen := s.readMode == modeSteer, s.steerGen
	s.mu.Unlock()
	if !steer {
		return
	}
	select {
	case s.turnEvents <- chatInputEvent{key: keyAbort, gen: gen}:
	case <-s.ctx.Done():
	}
}

// OnFirstByte tells whoever reads the mode in progress that the first byte of
// this read has arrived — a Stop hook waiting at the prompt is cut short before the
// byte reaches the line editor — and returns once it was acknowledged. A steer read
// sends none: during a turn there is no prompt wait to interrupt (design N4).
func (s *chatInputSource) OnFirstByte() error {
	mode := s.currentReadMode()
	if mode == modeSteer {
		return nil
	}
	select {
	case s.readChannel(mode) <- chatInputEvent{activity: true}:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	select {
	case <-s.activity:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	return nil
}

// readChannel is where a read in mode delivers: events for a prompt line (the chat
// loop), turnEvents for a steer read (the turn consumer, S2b) and answers for an ask
// read (Ask's own channel).
func (s *chatInputSource) readChannel(mode inputMode) chan chatInputEvent {
	switch mode {
	case modeSteer:
		return s.turnEvents
	case modeAsk:
		return s.answers
	default:
		return s.events
	}
}

// currentReadMode is the mode of the read the reader is serving.
func (s *chatInputSource) currentReadMode() inputMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readMode
}

// readResult is the event one finished read reports. ok=false is input the mode has
// no event for: a steer read reads on for it, so the input line stays available
// while the turn runs.
func (s *chatInputSource) readResult(mode inputMode, line string, err error) (chatInputEvent, bool) {
	switch {
	case mode != modeSteer:
		return chatInputEvent{line: line, err: err}, true
	case err == nil:
		return chatInputEvent{line: line, gen: s.steerGenNow()}, true
	case errors.Is(err, lineedit.ErrInterrupt):
		// Ctrl-C during a turn: end this turn only. A second press within
		// exitConfirmWindow asks to leave chat; the clock is the source's, shared with
		// the prompt read (m7). The line the read leaves behind (a read that keeps
		// the line on Ctrl-C - a running local command's - may have text on it) is
		// taken out of the editor here and rides on the event: the watcher of a local
		// command puts it back into the input line, so the half-typed line survives
		// however the timing falls out between the read and the next prompt. A read
		// that clears the line itself has nothing here to take.
		taken := ""
		if s.terminal != nil {
			taken = s.terminal.TakeInterruptedLine()
		}
		if s.takeInterrupt() {
			return chatInputEvent{key: keyExit, line: taken, gen: s.steerGenNow()}, true
		}
		return chatInputEvent{key: keyAbort, line: taken, gen: s.steerGenNow()}, true
	case errors.Is(err, io.EOF) && s.StickyEOF() == nil:
		// Ctrl-D on an empty line: a turn ignores it.
		return chatInputEvent{}, false
	default:
		return chatInputEvent{err: err}, true
	}
}

// deliver hands one read result to the channel of the mode it was read in; it
// reports false when the chat is closing.
func (s *chatInputSource) deliver(mode inputMode, event chatInputEvent) bool {
	select {
	case s.readChannel(mode) <- event:
		return true
	case <-s.ctx.Done():
		return false
	}
}

// StickyEOF is the input's end, remembered: once the input is done every later
// request answers EOF at once instead of blocking in a read (design F2).
func (s *chatInputSource) StickyEOF() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readErr
}

// OnEOF remembers an input that ended with EOF.
func (s *chatInputSource) OnEOF(err error) {
	if !errors.Is(err, io.EOF) {
		return
	}
	s.mu.Lock()
	if s.readErr == nil {
		s.readErr = err
	}
	s.mu.Unlock()
}

// takeInterrupt records one Ctrl-C press and reports whether it confirms leaving
// chat: a second press within exitConfirmWindow of the previous one. The clock lives
// on the input source, so the prompt read (chat.go) and the steer read share it: a
// Ctrl-C that ended a turn counts as the first of the two (m7).
func (s *chatInputSource) takeInterrupt() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	confirm := s.interruptSeen && now.Sub(s.lastInterrupt) <= exitConfirmWindow
	s.lastInterrupt, s.interruptSeen = now, true
	return confirm
}

// clearInterrupt forgets the Ctrl-C presses: anything else the person does starts
// the exit confirmation over.
func (s *chatInputSource) clearInterrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interruptSeen = false
}
