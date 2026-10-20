package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/inhere/suga/internal/chat/lineedit"
	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore"
	"golang.org/x/term"
)

type chatInputEvent struct {
	activity bool
	line     string
	err      error
	injected bool
	// key is what a steer read reports when a key, not a message, was pressed
	// during a turn (turnKey).
	key turnKey
	// gen is the steer read (RequestSteer) an event of a steer read was produced
	// in: the turn consumer owns one generation and drops the keys of an older
	// one (a lone Esc the pump decided just as the previous turn ended).
	gen uint64
	// settled (deliveries only) reports the final fate of a queued delivery: nil once it starts
	// a turn, an error if it is discarded at exit. Buffered(1); nil when nobody waits.
	settled chan error
	// images (deliveries only) are the pictures a `suga send --image` message
	// carries; they ride with the turn this delivery starts.
	images []agentcore.ImagePart
}

// settle reports the delivery's final fate to a waiting sender; never blocks.
func (e chatInputEvent) settle(err error) {
	if e.settled == nil {
		return
	}
	select {
	case e.settled <- err:
	default:
	}
}

// chatInputRequest asks the reader goroutine for one read. The mode of that read is
// the source's readMode (see request): one read at a time, and a read in flight can
// change mode (ask.go) without being restarted.
type chatInputRequest struct {
	prompt string
}

// chatInputSource reads exactly one prompt per request and signals its first byte before
// forwarding it to the line editor, so callers can interrupt a waiting Stop hook first.
type chatInputSource struct {
	ctx    context.Context
	cancel context.CancelFunc
	input  io.Reader
	output io.Writer
	// makeRaw switches the tty to raw mode and returns the restore func; injectable so tests
	// need no real tty. nil when input is not a terminal.
	makeRaw    func() (restore func() error, err error)
	restoreRaw func() error // non-nil only while the tty is raw (guarded by mu)
	// restoreOutput and clearOutput set the terminal's output flags for the raw
	// input currently held (outputProcessingForRawLocked); injectable like makeRaw
	// and nil without a terminal. newChatInputSource wires them to
	// restoreOutputProcessing / clearOutputProcessing, which work on the input's fd.
	restoreOutput func() error
	clearOutput   func() error
	// turnActive is true from SetBusy(true) until the next Request: while set the tty must stay
	// cooked so Ctrl-C raises SIGINT, even if a line read started earlier is still blocked.
	turnActive bool
	// steering is true while chat.steering is on and the input is a terminal: a turn
	// then reads the input line instead of going cooked (SetBusy, design
	// "chat：输入层按模式分流"). Set once, before the main loop.
	steering bool
	// steerGen counts the steer reads (RequestSteer): events of a read carry the
	// generation they were produced in, so a key the pump decided just as a turn
	// ended cannot end the next one.
	steerGen uint64
	// withdraw is what Up on an empty input line asks for a line to fill in while a
	// turn runs (steering.go): startTurnInput installs the running turn's own and
	// turnInput.end clears it. nil keeps Up's usual meaning (history).
	withdraw func() (string, bool)
	// screenWidth is the terminal's column count when it could be read (0: unknown).
	screenWidth int
	// sizeFD is the descriptor the terminal size is read from (-1 without a
	// terminal): the output when it is a terminal, else the input. Windows needs a
	// screen-buffer handle for it and the input handle fails inside a ConPTY.
	sizeFD   int
	closed   bool // set by DiscardQueued/Close: deliveries are refused afterwards (guarded by mu)
	terminal *lineedit.Terminal
	history  *inputHistory
	requests chan chatInputRequest
	// events carries the result of a prompt read (Next); answers the answer of a
	// question (Ask) and turnEvents what a steer read reports (S2b). Each mode has
	// its own channel, so an answer can never be taken as a prompt line or as an
	// inserted message (design N5). All buffered(1).
	events     chan chatInputEvent
	answers    chan chatInputEvent
	turnEvents chan chatInputEvent
	// readMode is the mode of the read the reader is serving (guarded by mu): it
	// decides the activity handshake, the lone-Esc decision and the channel a result
	// is delivered to. request sets it, Ask switches it to modeAsk.
	readMode inputMode
	// ctrlCClears is what the read being served does with a Ctrl-C on a non-empty
	// line: it is handed to the line editor (SetCtrlCClearsLine). The prompt and the
	// read a running turn owns set it - they edit the person's own input line, where
	// a stray key press must not throw a half-typed message away - while the answer
	// of an approval question and the read of a running local command leave it off
	// (Ctrl-C is "cancel" there, as it always was). Guarded by mu; the read applies
	// it when it starts (readLine) and Ask switches it on a read it borrows.
	ctrlCClears bool
	// inputWait reports whether more input is ready within the timeout, without
	// reading it: the wait behind the lone-Esc and the pasted-newline decisions of
	// the byte pump. nil without a terminal.
	inputWait func(timeout time.Duration) bool
	// readErr is the input's end (io.EOF) once it has ended: every later request
	// answers it at once instead of blocking in a read.
	readErr error
	// lastInterrupt and interruptSeen time the Ctrl-C presses that confirm leaving
	// chat (exitConfirmWindow); the prompt read and the steer read share them (m7).
	lastInterrupt time.Time
	interruptSeen bool
	activity      chan struct{}
	deliveries    chan chatInputEvent
	// taskWake signals a background task end (tasks.wake_on_exit); nil when
	// off. taskNotice takes the pending notices as the text of an injected
	// turn ("" when the running turn already took them).
	taskWake   <-chan struct{}
	taskNotice func() string
	done       chan struct{}
	mu         sync.Mutex
	pending    bool
	busy       bool
	reader     *chatActivityReader

	// Approval questions (ask.go). flushInput drops terminal input that was
	// typed but not read yet; injectable, nil when input is not a terminal.
	flushInput func() error
	askMu      sync.Mutex // one question at a time
	// cond (on mu) signals handing changes.
	cond *sync.Cond
	// inputEpoch is bumped by Ask right after flushing: a byte read from the
	// terminal before that is dropped instead of reaching the line editor.
	inputEpoch uint64
	// handing is set while the reader has passed a byte to the line editor
	// that the editor has not processed yet.
	handing bool
	// savedLine is a half-typed line taken out of a pending read for a
	// question; it prefills the next prompt read.
	savedLine string
}

// terminalSizeFD picks the descriptor the terminal size is read from: the output
// when it is a terminal, else the input, else -1. Windows answers
// GetConsoleScreenBufferInfo only for a screen-buffer (output) handle: asking the
// input handle fails inside a ConPTY (Zed, Windows Terminal) and the line editor
// then keeps its 80-column default - a wide window wrapped a line at 80. Unix
// answers TIOCGWINSZ for either end of the terminal.
func terminalSizeFD(input io.Reader, output io.Writer) int {
	if f, ok := output.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return int(f.Fd())
	}
	if f, ok := input.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return int(f.Fd())
	}
	return -1
}

func newChatInputSource(parent context.Context, input io.Reader, output io.Writer) (*chatInputSource, error) {
	ctx, cancel := context.WithCancel(parent)
	source := &chatInputSource{
		ctx: ctx, cancel: cancel, input: input, output: &lockedWriter{w: output},
		requests: make(chan chatInputRequest), events: make(chan chatInputEvent, 1),
		answers: make(chan chatInputEvent, 1), turnEvents: make(chan chatInputEvent, 1),
		activity: make(chan struct{}), deliveries: make(chan chatInputEvent, 8), done: make(chan struct{}),
	}
	source.cond = sync.NewCond(&source.mu)
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		fd := int(file.Fd())
		// Raw mode is entered per line in readLine, not here: with chat.steering off
		// the terminal must stay cooked while model and tools run (Ctrl-C raises SIGINT,
		// stderr keeps CR translation), and with it on the turn's own read enters raw
		// through readLine just the same. The output flags raw input clears are put
		// back per read, not per raw mode (outputProcessingForRawLocked).
		source.makeRaw = func() (func() error, error) {
			state, err := term.MakeRaw(fd)
			if err != nil {
				return nil, err
			}
			return func() error { return term.Restore(fd, state) }, nil
		}
		source.restoreOutput = func() error { return restoreOutputProcessing(fd) }
		source.clearOutput = func() error { return clearOutputProcessing(fd) }
		source.flushInput = func() error { return flushTerminalInput(fd) }
		// The wait behind the lone-Esc and pasted-newline decisions: "is a byte
		// ready?", asked without reading it (inputwait_*.go).
		source.inputWait = func(timeout time.Duration) bool { return waitTerminalInput(fd, timeout) }
		source.reader = &chatActivityReader{source: source, input: input}
		terminalIO := &chatTerminalIO{reader: source.reader, output: source.output}
		source.terminal = lineedit.NewTerminal(terminalIO, "")
		// A lone Esc during a turn is the Esc key (end this turn). The pump declares
		// it before handing the byte over; prompt and ask reads never declare one.
		source.terminal.EscCallback = source.escKeyPressed
		// Up on an empty input line asks the running turn whether a message it is
		// still waiting to deliver can come back for editing (steering.go).
		source.terminal.WithdrawCallback = source.withdrawSteers
		// The terminal's real size drives the line editor's layout: it wraps a line
		// at termWidth and the highlighted rows are filled to the same width. Without
		// this the editor keeps wrapping at lineedit.DefaultWidth (80) however wide
		// the window is, so a window wider than 80 columns wrapped a line early and
		// the fill width (screenWidth) disagreed with the layout.
		source.sizeFD = terminalSizeFD(input, output)
		source.applyTerminalSize()
		// A window resized while chat runs: follow it, so the editor keeps wrapping
		// at the current width (SIGWINCH on Unix, a poll where there is no signal;
		// resize_unix.go / resize_poll.go).
		go source.watchScreenSize()
	}
	go source.run()
	return source, nil
}

func (s *chatInputSource) Deliver(text, from string) (bool, error) {
	queued, _, err := s.DeliverWait(text, from)
	return queued, err
}

// DeliverWait enqueues a message for the chat loop. settled yields nil once the message starts
// a turn, or process.ErrDiscarded if the chat exits while it is still queued.
func (s *chatInputSource) DeliverWait(text, from string) (bool, <-chan error, error) {
	return s.DeliverImages(text, from, nil)
}

// DeliverImages enqueues a message that carries pictures for the chat loop: they
// are attached to the turn the delivery starts (`suga send --image`, design
// send --image D3).
func (s *chatInputSource) DeliverImages(text, from string, images []agentcore.ImagePart) (bool, <-chan error, error) {
	if strings.TrimSpace(text) == "" {
		return false, nil, fmt.Errorf("delivery text is empty")
	}
	settled := make(chan error, 1)
	event := chatInputEvent{line: deliveryLine(text, from), injected: true, settled: settled, images: images}
	// Hold mu across the closed check and the enqueue so DiscardQueued cannot miss a delivery.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, nil, process.ErrSessionClosed
	}
	queued := s.busy || len(s.deliveries) > 0
	select {
	case s.deliveries <- event:
		return queued, settled, nil
	default:
		return true, nil, process.ErrQueueFull
	}
}

// DiscardQueued refuses further deliveries and fails every message still queued (its sender,
// if waiting, learns it was not delivered). It returns how many were dropped.
func (s *chatInputSource) DiscardQueued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	n := 0
	for {
		select {
		case event := <-s.deliveries:
			event.settle(process.ErrDiscarded)
			n++
		default:
			return n
		}
	}
}

// deliveryLine labels a delivered message with its source: "[来自 <from>] text".
// Text that already starts with a "[...] " tag (the sender labeled it itself)
// is kept as is.
func deliveryLine(text, from string) string {
	if hasSourceTag(text) {
		return text
	}
	return "[来自 " + safeSender(from) + "] " + text
}

// hasSourceTag reports whether text starts with a non-empty "[label]" on its first line
// followed by a space.
func hasSourceTag(text string) bool {
	if !strings.HasPrefix(text, "[") {
		return false
	}
	firstLine, _, _ := strings.Cut(text, "\n")
	end := strings.IndexByte(firstLine, ']')
	return end > 1 && end+1 < len(firstLine) && firstLine[end+1] == ' '
}

// safeSender is the source label: the request's From (`suga send --source`),
// "unknown" when empty, with control characters dropped and at most 64 runes.
func safeSender(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return "unknown"
	}
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, from)
	runes := []rune(clean)
	if len(runes) > 64 {
		clean = string(runes[:64])
	}
	return clean
}

// SetSteering turns the turn-time input line on or off (chat.steering): with it
// on, SetBusy(true) keeps the tty raw and the turn reads the input line, with it
// off a turn goes back to cooked mode (Ctrl-C as SIGINT). Without a terminal the
// flag stays off: there is no line to edit and no read to make during a turn.
func (s *chatInputSource) SetSteering(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steering = on && s.terminal != nil
}

// SetBusy marks a turn start/end. Starting a turn forces the tty back to cooked mode even when a
// line read begun earlier is still blocked (a `suga send` delivery can start a turn while the
// prompt is waiting); otherwise Ctrl-C would arrive as byte 0x03 and never interrupt the turn.
// With chat.steering on the opposite holds: the turn keeps reading the input line (the steer read
// RequestSteer asks for), so the tty stays raw and Ctrl-C/Esc arrive as bytes.
func (s *chatInputSource) SetBusy(busy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = busy
	if !s.steering {
		if busy {
			s.turnActive = true
			_ = s.leaveRawLocked()
		}
		return
	}
	if busy {
		// turnActive would keep the coming steer read cooked (enterRawLocked) and
		// with it turn a Ctrl-C into SIGINT; leave it clear and go raw at once, so
		// the key is a byte from the turn's first moment on.
		s.turnActive = false
		// A read from before the turn (a delivery started it while the prompt was
		// waiting) is already raw and goes on as the turn's own read: its output
		// flags change here. A fresh raw mode sets them itself (enterRawLocked).
		held := s.restoreRaw != nil
		_ = s.enterRawLocked()
		if held {
			s.outputProcessingForRawLocked()
		}
	}
	// The end of a turn changes nothing here: the turn's read goes on as the next
	// prompt's, and that read sets its own output flags when it is asked for
	// (request, which the chat does before it reads the next line).
}

// SetInputHighlight paints the line the person submits at the prompt
// (chat.highlight_input): the style is written around a repaint of the line, its
// background reaching the end of every terminal row. A zero style turns it off;
// input that is not a terminal has no line to paint.
func (s *chatInputSource) SetInputHighlight(style lineedit.InputStyle) {
	if s.terminal != nil {
		s.terminal.SetInputStyle(style)
	}
}

// highlightWidth is the number of columns chat fills a highlighted line to: the
// terminal's width when it reports one, else the width the line editor lays
// lines out with. 0 when input is not a terminal (no line editing, no filling).
// It is re-read per call, so a resized window is taken into account.
func (s *chatInputSource) highlightWidth() int {
	if s.terminal == nil {
		return 0
	}
	if width := s.currentScreenWidth(); width > 0 {
		return width
	}
	return lineedit.DefaultWidth
}

// currentScreenWidth is the last terminal width chat read (0: unknown).
func (s *chatInputSource) currentScreenWidth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.screenWidth
}

// ctrlCClearsNow reports what the read being served does with a Ctrl-C on a
// non-empty line (nothing off a terminal: there is no line to edit).
func (s *chatInputSource) ctrlCClearsNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctrlCClears
}

// watchScreenSize applies window size changes for as long as chat runs (SIGWINCH on
// Unix, a poll where there is no signal). The line editor wraps at the size it was
// last given, so a resized window has to reach it (SetSize also repaints the line
// being edited; a blocked read releases the editor's lock, so that normally applies
// at once).
func (s *chatInputSource) watchScreenSize() {
	events, stop := resizeNotify()
	defer stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-events:
			s.applyTerminalSize()
		}
	}
}

// applyTerminalSize reads the terminal size (sizeFD) and hands a changed one to the
// line editor. A window that did not change costs one ioctl and nothing else; a
// size that cannot be read leaves the editor with its default width, which decides
// how every line of the chat is laid out, so it is worth a log line.
func (s *chatInputSource) applyTerminalSize() {
	if s.terminal == nil || s.sizeFD < 0 {
		return
	}
	width, height, err := term.GetSize(s.sizeFD)
	if err != nil || width <= 0 {
		slog.Warn("read the terminal size; the chat keeps its default line width",
			"fd", s.sizeFD, "err", err)
		return
	}
	s.mu.Lock()
	changed := width != s.screenWidth
	s.screenWidth = width
	s.mu.Unlock()
	if !changed {
		return
	}
	slog.Info("terminal size", "fd", s.sizeFD, "width", width, "height", height)
	_ = s.terminal.SetSize(width, height)
}

// Request asks for one prompt line (between turns): the chat loop takes the result
// from Next.
func (s *chatInputSource) Request(prompt string) bool { return s.request(prompt, modePrompt, true) }

// RequestSteer asks for a read during a turn (S2b): the input line stays available
// while the model works, Enter submits an inserted message and Ctrl-C/Esc ask to end
// the turn. Results arrive on TurnEvents, tagged with the generation this read
// returns: the turn consumer acts on its own generation only. ok=false means the
// source is closing (the read could not be asked for).
func (s *chatInputSource) RequestSteer(prompt string) (gen uint64, ok bool) {
	return s.steer(prompt, true)
}

// RequestCommandKeys asks for the read a running local command owns (local_cmd.go's
// watchLocalCommand): the same steer read, but Ctrl-C ends the command even with
// text on the line. The line there belongs to the command - it goes back to the
// input once the command is over - so dropping it instead of ending the command
// would leave the person with a command they cannot stop.
func (s *chatInputSource) RequestCommandKeys() (gen uint64, ok bool) {
	return s.steer("", false)
}

// steer asks for a steer read with the given Ctrl-C behaviour (ctrlCClears) and
// returns its generation.
func (s *chatInputSource) steer(prompt string, ctrlCClears bool) (gen uint64, ok bool) {
	s.mu.Lock()
	s.steerGen++
	gen = s.steerGen
	s.mu.Unlock()
	if !s.request(prompt, modeSteer, ctrlCClears) {
		return 0, false
	}
	return gen, true
}

// steerGenNow is the generation of the steer read in progress.
func (s *chatInputSource) steerGenNow() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.steerGen
}

// lineOnScreen reports whether the line editor's line is on screen right now:
// chat.steering is on and a read is pending (a turn keeps reading for its whole
// length, and the prompt reads between turns). What is written while it is up
// has to go through the input source: lineedit.Terminal.Write prints it above
// the line and repaints the line afterwards, while a direct write would land on
// top of the line, which the editor cannot know about (design N4).
func (s *chatInputSource) lineOnScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.steering && s.pending
}

// request asks the reader goroutine for one read in mode, with ctrlCClears deciding
// what a Ctrl-C on a non-empty line does in it (the line editor's
// SetCtrlCClearsLine). The mode is set here, under the same lock as pending: a read
// already waiting for input continues as this read (only its prompt, its mode and
// its Ctrl-C behaviour change), a finished one starts a new request - and between
// the two nothing reads, so input typed meanwhile is never taken early.
func (s *chatInputSource) request(prompt string, mode inputMode, ctrlCClears bool) bool {
	s.mu.Lock()
	s.readMode, s.ctrlCClears = mode, ctrlCClears
	// Back at a prompt: raw mode is allowed again. If a read from before the turn is still
	// blocked it continues as this prompt's read, so switch to raw for it right here.
	s.turnActive = false
	if s.pending {
		err := s.enterRawLocked()
		// The pending read goes on as a read of this mode now, and its output flags
		// change with it: the turn's read looks the same (OPOST|ONLCR back on), a
		// prompt read keeps them as raw input left them - the way v0.24.0 read a
		// prompt - and the repaint of its prompt below already writes that way.
		s.outputProcessingForRawLocked()
		s.mu.Unlock()
		_ = err // best effort: the pending read still works in cooked mode
		// The read keeps its line; only the prompt and the Ctrl-C behaviour change
		// (e.g. a Stop hook that returned without waiting for an external reply).
		if s.terminal != nil {
			s.terminal.SetCtrlCClearsLine(ctrlCClears)
			_ = s.terminal.UpdatePrompt(prompt)
		}
		return true
	}
	s.pending = true
	s.mu.Unlock()
	select {
	case s.requests <- chatInputRequest{prompt: prompt}:
		return true
	case <-s.ctx.Done():
		s.mu.Lock()
		s.pending = false
		s.mu.Unlock()
		return false
	}
}

func (s *chatInputSource) Next() <-chan chatInputEvent { return s.events }

// TurnEvents is where a steer read reports what the person asked for during a turn:
// a line for an inserted message, a key for abort/exit. The turn consumer reads it
// (S2b); while chat.steering is off nothing is written to it.
func (s *chatInputSource) TurnEvents() <-chan chatInputEvent { return s.turnEvents }

func (s *chatInputSource) AckActivity() {
	select {
	case s.activity <- struct{}{}:
	case <-s.ctx.Done():
	}
}

func (s *chatInputSource) CompleteLine() {
	s.mu.Lock()
	s.pending = false
	s.mu.Unlock()
}

func (s *chatInputSource) Write(p []byte) (int, error) {
	if s.terminal != nil {
		return s.terminal.Write(p)
	}
	return s.output.Write(p)
}

// SetStatus shows the status line above the input line: what a running turn does
// right now (the model thinking, a tool running). It is part of the renderer's
// waiting line (spinner.go); input that is not a terminal has no line to put it
// above, so nothing is shown.
func (s *chatInputSource) SetStatus(text string) {
	if s.terminal != nil {
		s.terminal.SetStatus(text)
	}
}

// putBackLine writes text into the prompt's input line, in front of whatever was
// already typed, without submitting it: what chat uses to hand messages that a
// cancelled or failed turn could not deliver back to the person (design D4).
// Input that is not a terminal has no line to put them in.
func (s *chatInputSource) putBackLine(text string) {
	if s.terminal != nil {
		s.terminal.PrependLine(text)
	}
}

func (s *chatInputSource) Close() error {
	s.cancel()
	// The reader goroutine may still be blocked inside a raw-mode line edit; restore for it and
	// keep it cooked from now on.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnActive = true
	s.closed = true
	return s.leaveRawLocked()
}

// enterRaw switches the tty to raw mode for line editing (no-op if already raw, or while a turn
// is running, when the terminal must stay cooked).
func (s *chatInputSource) enterRaw() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enterRawLocked()
}

func (s *chatInputSource) enterRawLocked() error {
	if s.restoreRaw != nil || s.makeRaw == nil {
		return nil
	}
	if s.turnActive && !s.steering {
		// A turn without chat.steering must stay cooked so Ctrl-C becomes SIGINT;
		// with steering the turn reads raw input on purpose (SetBusy).
		return nil
	}
	restore, err := s.makeRaw()
	if err != nil {
		return fmt.Errorf("enable terminal input editing: %w", err)
	}
	s.restoreRaw = restore
	// A fresh raw mode: give the terminal the output flags the read this raw mode
	// is entered for needs (the reads of a turn are entered one by one, and each
	// of them sets its own state).
	s.outputProcessingForRawLocked()
	// Ask the terminal to bracket pastes for as long as a line is being edited
	// (SetBracketedPasteMode), so a pasted multi-line text reaches the editor as
	// one chunk with its newlines and does not submit at the first of them. A
	// terminal that does not support it is unaffected: the byte pump recognises
	// such a paste by the bytes that follow (input_pump.go).
	s.setBracketedPasteLocked(true)
	return nil
}

// outputProcessingForRawLocked sets the terminal's output flags for the raw input
// being held; it does nothing while the terminal is cooked, whose flags are the
// ones it started with. Only the read of a running turn (chat.steering) puts
// OPOST|ONLCR back: what is written to the terminal straight while that read holds
// raw mode - a hook or a tool holding the tty, the process log - is a "\n" line
// that has to start a line, and raw input cleared the flags (design N4). A read
// between turns (a prompt, the answer of a question) keeps them as raw input set
// them, the way v0.24.0 read a prompt: the line editor writes "\r\n" itself, and
// output processing would add another CR to it. Best effort - a terminal that
// cannot be adjusted still edits fine (termios_*.go).
//
// It is called wherever the raw mode or the read holding it changes: entering raw
// (enterRawLocked), a read being handed to another mode (request) and a turn
// starting on a read that is already raw (SetBusy).
func (s *chatInputSource) outputProcessingForRawLocked() {
	if s.restoreRaw == nil {
		return
	}
	if s.steering && s.busy {
		if s.restoreOutput != nil {
			_ = s.restoreOutput()
		}
		return
	}
	if s.clearOutput != nil {
		_ = s.clearOutput()
	}
}

// leaveRaw restores the original (cooked) tty state; idempotent and safe from any goroutine.
func (s *chatInputSource) leaveRaw() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leaveRawLocked()
}

func (s *chatInputSource) leaveRawLocked() error {
	if s.restoreRaw == nil {
		return nil
	}
	// The line is done (or the terminal goes back to cooked mode for a turn): the
	// terminal must not keep bracketing pastes, whose markers would otherwise
	// reach a cooked-mode line read as bytes (SetBracketedPasteMode).
	s.setBracketedPasteLocked(false)
	err := s.restoreRaw()
	s.restoreRaw = nil
	return err
}

// setBracketedPasteLocked asks the terminal to bracket pastes (on) or to stop
// (off); without a terminal there is nothing to ask.
func (s *chatInputSource) setBracketedPasteLocked(on bool) {
	if s.terminal != nil {
		s.terminal.SetBracketedPasteMode(on)
	}
}

// run serves one read request after another, so no read is started before one was
// asked for (see request).
func (s *chatInputSource) run() {
	defer close(s.done)
	for {
		var request chatInputRequest
		select {
		case <-s.ctx.Done():
			return
		case request = <-s.requests:
		}
		s.serve(request)
	}
}

// serve reads for one request (a prompt line, or a steer read while a turn runs; an
// ask read is requested by Ask) and delivers what it read to the channel of the mode
// the read is in. A steer read also reads on for input a turn does not act on
// (Ctrl-D on an empty line), so the input line stays available for the whole turn;
// the turn consumer ends it by asking for the next prompt read. Every other read ends
// with the one event it produced.
func (s *chatInputSource) serve(request chatInputRequest) {
	for {
		line, err := s.readLine(request)
		s.byteConsumed() // the line editor is done with every byte it got
		if s.ctx.Err() != nil {
			return
		}
		mode := s.currentReadMode()
		event, ok := s.readResult(mode, line, err)
		if !ok {
			continue
		}
		if !s.deliver(mode, event) {
			return
		}
		if mode != modeSteer {
			return
		}
	}
}

func (s *chatInputSource) readLine(request chatInputRequest) (string, error) {
	prompt := request.prompt
	// An input that already ended answers its EOF at once (also for input that is not
	// a terminal, whose read would return it again anyway).
	if err := s.stickyEOF(); err != nil {
		return "", err
	}
	if s.terminal != nil {
		// Raw mode only while editing a line. Ctrl-C here arrives as byte 0x03 (no SIGINT) and the
		// line editor returns io.EOF, so the prompt exits chat like SIGINT did with cooked line reads.
		if err := s.enterRaw(); err != nil {
			return "", err
		}
		defer s.leaveRaw()
		s.terminal.SetPrompt(prompt)
		// What the read does with Ctrl-C on a non-empty line: the prompt and the read
		// of a running turn drop the line (the person's own input line, so a stray key
		// press does not throw a half-typed message away), the answer of an approval
		// question and the read of a local command do not (ask.go, local_cmd.go).
		s.terminal.SetCtrlCClearsLine(s.ctrlCClearsNow())
		s.reader.activitySent = false
		var line string
		var err error
		if s.currentReadMode() == modeAsk {
			// The answer of a question stays out of history.
			line, err = s.terminal.ReadLineNoHistory()
		} else {
			// A steer read keeps the person's input line too (chat.steering): the turn
			// and the prompt read the same line, so a message typed during a turn is
			// either inserted or kept for the next prompt.
			// Give back a half-typed line that a question took out of an earlier read.
			if saved := s.takeSavedLine(); saved != "" {
				s.terminal.SetPendingLine(saved)
			}
			line, err = s.terminal.ReadLine()
			if err == nil && strings.TrimSpace(line) != "" {
				// Persist what the person typed here (a prompt line, a message
				// inserted into a running turn, a "!" command) for the next chat
				// (chat.history). Only this keyboard read reaches here: delivered
				// messages and answers to questions never do. A write failure
				// must not lose the line, so it only shows up in the log.
				if herr := s.history.add(line); herr != nil {
					slog.Warn("chat history: append failed", "err", herr)
				}
			}
		}
		return line, err
	}
	if _, err := fmt.Fprint(s.output, prompt); err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(&chatActivityReader{source: s, input: s.input})
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			s.recordEOF(err)
			return "", err
		}
		s.recordEOF(io.EOF)
		return "", io.EOF
	}
	return scanner.Text(), nil
}

type chatTerminalIO struct {
	reader *chatActivityReader
	output io.Writer
}

func (rw *chatTerminalIO) Read(p []byte) (int, error) {
	return rw.reader.Read(p)
}

func (rw *chatTerminalIO) Write(p []byte) (int, error) {
	return rw.output.Write(p)
}

// lockedWriter serializes writes from the input goroutine (prompts) and the chat loop.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
