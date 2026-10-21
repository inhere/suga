package chat

import (
	"errors"
	"io"
	"time"

	"github.com/inhere/suga/internal/chat/lineedit"
)

// The reading side of the chat input: the mode one read is in, the event that read
// produces and the byte pump the line editor reads through. chat_input.go holds the
// source itself (deliveries, requests, terminal modes).
//
// Read contract (design F2): a read happens only while a request is outstanding —
// the reader goroutine is blocked inside the line editor's Read, so input typed
// between two requests is never taken early. The lone-Esc decision does not change
// that: it asks the platform whether a follow-up byte is ready (waitInput) instead
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

// escByte is the Escape byte: on its own it is the Esc key, anything that follows
// makes it the start of an escape sequence (arrow keys, paste markers).
const escByte = 0x1b

// crByte and lfByte are the bytes a terminal hands over for Enter: CR in raw mode,
// LF for a line typed in cooked mode (ICRNL on Unix; the Windows console sends CRLF),
// which the line editor takes as Enter too (lineedit.keyLF).
const (
	crByte = 0x0d
	lfByte = 0x0a
)

// escWaitTimeout is how long a read waits for a byte after 0x1b before taking the
// Esc as a key press of its own (design D2).
const escWaitTimeout = 50 * time.Millisecond

// pasteNewlineTimeout is how long a read waits for a byte after a newline before
// taking it as a submit where the terminal does not bracket pastes. A paste
// arrives as one chunk, so the byte after one of its line breaks is already
// readable within a few milliseconds, while a person's Enter is followed by
// nothing: no key press comes that fast (fastest typing and key repeat are
// around 30-50ms apart). Only platforms whose wait really reports a ready byte
// are asked (waitInputSupported).
const pasteNewlineTimeout = 8 * time.Millisecond

// chatActivityReader is the byte pump the line editor reads through: it hands over
// one byte per Read (the editor keeps partial key sequences itself), signals the
// first byte of a read before that byte reaches the editor, and decides what the
// editor cannot tell by itself: whether a 0x1b byte is the Esc key on its own or
// the start of an escape sequence, and whether a newline is pasted text or a
// submit.
type chatActivityReader struct {
	source       *chatInputSource
	input        io.Reader
	activitySent bool
	// crHanded is true while the byte read last is a CR: the LF directly after it is
	// the LF half of a CRLF pair (a pre-typed line from the Windows console, a CRLF
	// paste), not an Enter of its own — x/term drops the same LF when both bytes
	// arrive in one read. Any other byte clears it.
	crHanded bool
	// held is a byte the pump read one step ahead while deciding whether a newline
	// is pasted text, with the epoch it was read in: it is handed to the editor by
	// the next Read like any other byte.
	held *heldByte
}

// heldByte is one byte of input read ahead of the editor (see chatActivityReader.held).
type heldByte struct {
	b     byte
	epoch uint64
}

func (r *chatActivityReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Asking for more input means the previous byte has been processed.
	r.source.byteConsumed()
	// Records carry whole keys, so the byte-level guesses below are off while they
	// are what the editor reads (win32-input-mode).
	records := r.source.win32Records()
	for {
		// An input that has already ended answers its EOF at once: a later request
		// must not block in a read that will not return.
		if err := r.source.stickyEOF(); err != nil {
			return 0, err
		}
		b, epoch, ok, err := r.nextByte()
		if err != nil {
			return 0, err
		}
		if !ok {
			// The LF of a CRLF pair belongs to the CR before it: it is dropped, so
			// the read goes on with the byte after the pair (see crHanded).
			continue
		}
		if !r.activitySent {
			r.activitySent = true
			if err := r.source.signalActivity(); err != nil {
				return 0, err
			}
		}
		if !records && b == escByte && r.source.altEnterWanted() && r.source.waitInput(escWaitTimeout) {
			// A byte follows the ESC within the Esc window: Alt+Enter (ESC CR, or ESC LF
			// where the terminal turns Alt+Enter into that) asks for a newline inside the
			// input, not a submit. The ESC is dropped and the newline is declared as
			// text; anything else is an escape sequence, and the byte read ahead waits
			// for the next Read. Ctrl+J cannot be used for this: on Unix a pre-typed
			// line ends in a bare LF too (ICRNL), so it is ambiguous.
			next, nepoch, ok, err := r.nextByte()
			if err != nil {
				return 0, err
			}
			if ok && (next == crByte || next == lfByte) {
				r.source.declareNewlineAsText()
				if !r.source.handOver(nepoch) {
					continue
				}
				p[0] = next
				return 1, nil
			}
			if ok {
				r.held = &heldByte{b: next, epoch: nepoch}
			}
		}
		if !records && b == lfByte && ctrlJIsInputNewline {
			// A bare LF is Ctrl+J on Windows (a console hands a cooked line over as
			// CRLF, whose LF the pump drops): a newline inside the input. On Unix ICRNL
			// turns Enter itself into a bare LF, so there it has to submit
			// (ctrlj_windows.go / ctrlj_other.go).
			r.source.declareNewlineAsText()
		} else if !records && (b == crByte || b == lfByte) {
			// The newline may be text of a paste instead of a submit, which a
			// terminal without bracketed paste gives no other sign of. Decide it
			// before handing the byte over, so the editor inserts the newline
			// instead of ending the read on it (lineedit.SetNewlineAsText).
			if r.newlineIsPaste(b) {
				r.source.declareNewlineAsText()
			}
		}
		if !records && b == escByte && r.source.escKeyWanted() && !r.source.waitInput(escWaitTimeout) {
			// Nothing followed the 0x1b within escWaitTimeout: it is the Esc key, not
			// the start of a sequence. Declare it before handing the byte over, so the
			// editor reports the key instead of waiting for the rest of a sequence.
			if !r.source.handOver(epoch) {
				continue
			}
			r.source.declareEscLone()
			p[0] = b
			return 1, nil
		}
		// A byte held here (e.g. during the activity handshake, or the lone-Esc wait)
		// while a question flushed the input was typed before the question: drop it.
		if !r.source.handOver(epoch) {
			continue
		}
		p[0] = b
		return 1, nil
	}
}

// nextByte returns the next byte to hand to the editor together with the epoch it
// was read in (the input may have been flushed since, and such a byte must not
// reach the editor). ok is false for a byte that belongs to the byte before it —
// the LF of a CRLF pair — the caller reading on instead of handing it over.
func (r *chatActivityReader) nextByte() (b byte, epoch uint64, ok bool, err error) {
	if h := r.held; h != nil {
		r.held = nil
		return h.b, h.epoch, true, nil
	}
	var one [1]byte
	n, err := r.input.Read(one[:])
	if n == 0 {
		r.source.recordEOF(err)
		return 0, 0, false, err
	}
	pairLF := one[0] == lfByte && r.crHanded
	r.crHanded = one[0] == crByte
	if pairLF {
		return 0, 0, false, nil
	}
	return one[0], r.source.currentEpoch(), true, nil
}

// newlineIsPaste reports whether the newline byte b is text of a paste rather
// than a submit: a paste goes on with the next byte within pasteNewlineTimeout,
// while a person's Enter is followed by nothing (see pasteNewlineTimeout).
//
// A CR followed by the LF of a CRLF pair is the one case that needs a second
// look: such a pair is a typed Enter on a Windows console, so it is a submit
// unless something follows the pair as well (then the pair is a line break of a
// pasted CRLF text). The byte looked at is held for the next Read.
//
// The decision reads the byte the wait promised, so a platform wait that reports a
// byte which is not there blocks this read — the same hazard the lone-Esc wait has
// (inputwait_*.go).
func (r *chatActivityReader) newlineIsPaste(b byte) bool {
	if !r.source.pasteNewlineWanted() || !r.source.waitInput(pasteNewlineTimeout) {
		return false
	}
	if b != crByte {
		return true
	}
	next, epoch, ok, err := r.nextByte()
	if err != nil {
		// Nothing left to read: the input ended right after the newline, which is
		// what a submit looks like (the error is reported by the next Read).
		return false
	}
	if ok {
		r.held = &heldByte{b: next, epoch: epoch}
		return true
	}
	// The pair's LF stays dropped; what follows it decides.
	return r.source.waitInput(pasteNewlineTimeout)
}

// pasteNewlineWanted reports whether the pump may take a newline for pasted text:
// only a read on a terminal whose platform wait really reports a ready byte
// (waitInputSupported). Elsewhere the wait would answer "a byte follows" for
// every newline, so a typed Enter could never submit a line.
func (s *chatInputSource) pasteNewlineWanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal != nil && s.inputWait != nil && waitInputSupported
}

// altEnterWanted reports whether the read in progress takes ESC followed by Enter as
// "insert a newline in the input" and has the wait for the byte after the ESC. The
// prompt and question reads do; a steer read owns the lone Esc as "end this turn".
func (s *chatInputSource) altEnterWanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readMode != modeSteer && s.terminal != nil && s.inputWait != nil && waitInputSupported
}

// declareNewlineAsText marks the newline byte the pump is about to hand over as
// text of a paste (lineedit.SetNewlineAsText); only the pump declares one.
func (s *chatInputSource) declareNewlineAsText() {
	s.mu.Lock()
	terminal := s.terminal
	s.mu.Unlock()
	if terminal != nil {
		terminal.SetNewlineAsText()
	}
}

// escKeyWanted reports whether the read in progress takes a lone Esc as a key press
// and has a wait for the follow-up byte: a steer read on a terminal. Every other
// read keeps x/term's behaviour, an Esc byte waiting for its sequence.
func (s *chatInputSource) escKeyWanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readMode == modeSteer && s.terminal != nil && s.inputWait != nil
}

// waitInput reports whether more input is ready within timeout, without reading it
// (inputwait_*.go: poll(2) on Unix, WaitForSingleObject on Windows). It is the wait
// behind the lone-Esc decision and the pasted-newline decision, so the timeout
// leaves no read pending.
func (s *chatInputSource) waitInput(timeout time.Duration) bool {
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

// declareEscLone marks the Esc byte the pump is about to hand over as the Esc key
// on its own (lineedit.SetEscAlone); only a steer read does this.
func (s *chatInputSource) declareEscLone() {
	s.mu.Lock()
	terminal := s.terminal
	s.mu.Unlock()
	if terminal != nil {
		terminal.SetEscAlone()
	}
}

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

// signalActivity tells whoever reads the mode in progress that the first byte of
// this read has arrived — a Stop hook waiting at the prompt is cut short before the
// byte reaches the line editor — and returns once it was acknowledged. A steer read
// sends none: during a turn there is no prompt wait to interrupt (design N4).
func (s *chatInputSource) signalActivity() error {
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
	case errors.Is(err, io.EOF) && s.stickyEOF() == nil:
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

// stickyEOF is the input's end, remembered: once the input is done every later
// request answers EOF at once instead of blocking in a read (design F2).
func (s *chatInputSource) stickyEOF() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readErr
}

// recordEOF remembers an input that ended with EOF.
func (s *chatInputSource) recordEOF(err error) {
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
