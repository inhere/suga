package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/inhere/suga/internal/personal"
)

// Steering and ending one turn only (design "chat：输入层按模式分流" and "chat：
// 退回插话的处理"): while a turn runs the input line stays available in steer
// mode. Enter inserts a message into the running turn (the model sees it before
// its next request), Esc or a single Ctrl-C end this turn only (a second Ctrl-C
// within exitConfirmWindow leaves chat). The consumer here runs next to
// runner.Run; the messages a turn never delivered come back through
// RunResult.ReturnedSteers and are settled below: a completed turn's ones become
// a turn of their own right away, a cancelled or failed turn's ones go back to
// the input line and are not run.

// steerPrompt is the prompt of the read that stays open during a turn (design
// D2/Q1).
const steerPrompt = "↳ "

// steerFullHint is what chat says when the running turn's inbox is full (design
// D11): the keyboard read and the session socket both report it here.
const steerFullHint = "插话过多，请等本步结束"

// errExitChat ends the chat REPL: a second Ctrl-C during a turn asked to leave.
// Run's loop turns it into the same clean exit as the second Ctrl-C at the
// prompt.
var errExitChat = errors.New("leave chat")

// What chat says about a message inserted into the running turn: it waits for
// the turn's next model request, a request carried it, the person took it back
// for editing, or the turn did not take it (it was already ending, or its inbox
// was full).
const (
	steerWaitingSuffix   = "（等待送达）"
	steerTakenSuffix     = "（已送达）"
	steerWithdrawnSuffix = "（已撤回）"
	steerLateSuffix      = "（本轮未接收，稍后处理）"
)

// turnInput is the input of one running turn: the steer read the source serves
// and the goroutine acting on what it reads. runTurn starts it right before
// runner.Run and ends it when the turn is over.
type turnInput struct {
	state  *chatState
	gen    uint64
	cancel context.CancelFunc
	stop   chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	steers  []turnSteer
	aborted bool
	exited  bool
}

// turnSteer is one message a person inserted into the running turn.
type turnSteer struct {
	msg personal.Steer
	// accepted is false when the turn took nothing (it was already ending, or
	// its inbox was full): such a message is the person's next input.
	accepted bool
	// delivered is set when the turn reported the message as carried by one of
	// its model requests ("steer_delivered").
	delivered bool
}

// turnEnd is what a stopped turn input leaves behind.
type turnEnd struct {
	steers  []turnSteer
	aborted bool
	exited  bool
}

// unaccepted lists the messages the running turn never took: they were typed
// into a turn that was already ending, so they have to become the person's next
// input instead.
func (e turnEnd) unaccepted() []personal.Steer {
	var out []personal.Steer
	for _, s := range e.steers {
		if !s.accepted {
			out = append(out, s.msg)
		}
	}
	return out
}

// deliveredCount is how many messages a model request of the turn carried: what
// the end-of-turn status line counts ("插话 N"). A message the person took back
// with Up never reached a request, and one the turn never took runs as a later
// turn, so neither is counted - the lines in the output area are what says what
// became of them.
func (e turnEnd) deliveredCount() int {
	n := 0
	for _, s := range e.steers {
		if s.delivered {
			n++
		}
	}
	return n
}

// startTurnInput opens the turn's input line: the source reads in steer mode and
// the returned consumer turns what it reads into an inserted message, "end this
// turn only" or "leave chat". It returns nil when steering is off (chat.steering
// off, or an input that is not a terminal): the turn then keeps the plain
// behaviour and nothing is read during it.
func (s *chatState) startTurnInput(cancel context.CancelFunc) *turnInput {
	if !s.steering {
		return nil
	}
	gen, ok := s.source.RequestSteer(steerPrompt)
	if !ok {
		return nil
	}
	turn := &turnInput{state: s, gen: gen, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
	// The turn whose read is open: the session socket inserts `suga send --steer`
	// messages into it while it runs (steer_send.go), and Up on the empty input
	// line asks it for the messages it is still waiting to deliver.
	s.setCurrentTurn(turn)
	s.source.setWithdraw(turn.withdraw)
	// Tab on a non-empty line queues it for the next turn instead of inserting it
	// into this one (followup.go, issue suga-8pug).
	s.source.setFollowUp(s.queueFollowUp)
	go turn.consume()
	return turn
}

// setWithdraw installs (or clears, with nil) what Up on an empty input line asks
// for a line while a read is in progress: the turn whose read is open.
func (s *chatInputSource) setWithdraw(withdraw func() (string, bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.withdraw = withdraw
}

// withdrawSteers serves the line editor's Up key (lineedit.WithdrawCallback): only
// the read of a running turn has something to take back, so every other read (the
// prompt between turns, an approval question) leaves Up to the editor (history).
func (s *chatInputSource) withdrawSteers() (string, bool) {
	s.mu.Lock()
	mode, withdraw := s.readMode, s.withdraw
	s.mu.Unlock()
	if mode != modeSteer || withdraw == nil {
		return "", false
	}
	return withdraw()
}

// consume acts on the events of the turn's read until the turn is over.
func (t *turnInput) consume() {
	defer close(t.done)
	for {
		select {
		case <-t.stop:
			return
		case event := <-t.state.source.TurnEvents():
			t.handle(event)
		}
	}
}

// handle turns one event of the turn's read into what it means for the running
// turn: Esc and a single Ctrl-C end this turn only, a second Ctrl-C leaves chat,
// a submitted line is a message to insert. The keys of an older steer read (an
// Esc the pump decided just as the previous turn ended) belong to a turn that is
// over and are dropped; their lines are not, a message the person typed is
// theirs whichever turn reads it.
func (t *turnInput) handle(event chatInputEvent) {
	if event.key != keyNone {
		if event.gen != t.gen {
			return
		}
		t.endTurn(event.key == keyExit)
		return
	}
	if event.err != nil {
		return
	}
	t.insert(event.line)
}

// endTurn records that the person asked to end this turn only, or to leave chat,
// and cancels the turn's context: the model request or tool call in flight ends
// with it. The turn itself stays the runner's business (it is stored as
// cancelled), so chat keeps going.
func (t *turnInput) endTurn(exit bool) {
	t.mu.Lock()
	t.aborted = true
	t.exited = t.exited || exit
	t.mu.Unlock()
	t.cancel()
}

// insert hands one message a person typed at the keyboard to the running turn.
func (t *turnInput) insert(line string) {
	text := strings.TrimSpace(line)
	if text == "" {
		return
	}
	// :undo and :checkpoints are never steering either: they read and change the
	// session's files, so the line goes back to the input line and runs at the
	// next prompt, after the turn. External delivery bypasses insert and stays
	// ordinary model input.
	if isUndoCommand(text) || isCheckpointCommand(text) {
		t.state.render.Hint(undoBusyHint)
		t.state.source.putBackLine(line)
		return
	}
	// :queue reads and clears this chat's own follow-up queue, which a turn is
	// what fills: it changes no session state, so it runs right here instead of
	// going back to the input line (followup.go).
	if isQueueCommand(text) {
		t.state.runQueueCommand(text)
		return
	}
	// A local command cannot run while the turn does (the prompt belongs to the
	// turn): the line is not inserted as a message either, it goes back to the
	// input line for the next prompt (the way a message the turn refused does).
	if _, isLocal := parseLocalCommand(line); isLocal {
		t.state.render.Hint(localCommandBusyHint)
		t.state.source.putBackLine(line)
		return
	}
	state := t.state
	msg := personal.Steer{ID: state.nextSteerID(), Text: text, Source: personal.SteerSourceKeyboard}
	accepted, err := state.runner.Steer(state.sessionID, msg)
	t.record(msg, accepted)
	if err != nil {
		// The inbox holds steerCapacity messages already (design D11): say so, the
		// message becomes the next input rather than growing the queue further.
		state.render.Hint(steerFullHint)
		return
	}
	if accepted {
		state.render.SteerWaiting(text)
		return
	}
	state.render.SteerLate(text)
}

// withdraw takes the messages still waiting in the running turn's inbox back out of
// it and reports their text, so the input line can carry it for editing (Up on an
// empty line, the counter-key of the Enter that inserted them). Only the messages
// typed at this keyboard come back: a `suga send --steer` message has a sender
// waiting for its own fate (Runner.WithdrawSteers). Each one is shown where it was
// shown as waiting, as withdrawn. The second return value is false when nothing was
// waiting, in which case Up keeps its usual meaning (history).
func (t *turnInput) withdraw() (string, bool) {
	if t == nil || t.state == nil {
		return "", false
	}
	steers := t.state.runner.WithdrawSteers(t.state.sessionID, nil)
	if len(steers) == 0 {
		return "", false
	}
	texts := make([]string, 0, len(steers))
	for _, s := range steers {
		texts = append(texts, s.Text)
		t.state.render.SteerWithdrawn(s.Text)
	}
	// One line per message, in the order they were typed: the joined text is put
	// back on the input line and submitted again as one message (steering.go puts
	// the messages of a finished turn back the same way).
	return strings.Join(texts, "\n"), true
}

// insertFromElsewhere hands a message that arrived through the session socket
// (`suga send --steer`) to the running turn. Unlike a typed one it is not kept when
// the turn refuses it: there is no read of this chat it belongs to, and the caller
// queues it for the next turn instead (design "send：--steer"), so the turn must
// not run it as well. ErrSteerQueueFull is the full inbox.
func (t *turnInput) insertFromElsewhere(msg personal.Steer) (bool, error) {
	accepted, err := t.state.runner.Steer(t.state.sessionID, msg)
	if err != nil || !accepted {
		return false, err
	}
	t.record(msg, true)
	t.state.render.SteerWaiting(msg.Text)
	return true, nil
}

// record notes what the running turn did with one inserted message: its keyboard
// consumer and the session socket both report here, so the turn's end (unaccepted
// messages, the count in the status line) sees every one of them.
func (t *turnInput) record(msg personal.Steer, accepted bool) {
	t.mu.Lock()
	t.steers = append(t.steers, turnSteer{msg: msg, accepted: accepted})
	t.mu.Unlock()
}

// deliver marks the message a "steer_delivered" event names as carried by a model
// request of this turn and shows it as delivered. It runs on the goroutine that
// reads the turn's events (the Emit callback of runner.Run).
func (t *turnInput) deliver(event personal.Event) {
	if t == nil {
		return
	}
	t.mu.Lock()
	found := false
	for i := range t.steers {
		if t.steers[i].msg.ID == event.SteerID && !t.steers[i].delivered {
			t.steers[i].delivered, found = true, true
			break
		}
	}
	t.mu.Unlock()
	if found {
		t.state.render.SteerDelivered(event.Text)
	}
}

// end stops the consumer and reports what it did. What the read reported while the
// turn was ending is taken here (drain): the consumer is gone, so it would sit in
// the channel otherwise.
func (t *turnInput) end() turnEnd {
	if t == nil {
		return turnEnd{}
	}
	// The read is over: nothing can be inserted into this turn any more, and Up no
	// longer has a waiting message to take back.
	t.state.dropCurrentTurn(t)
	t.state.source.setWithdraw(nil)
	t.state.source.setFollowUp(nil)
	close(t.stop)
	<-t.done
	t.drain()
	t.mu.Lock()
	defer t.mu.Unlock()
	return turnEnd{steers: t.steers, aborted: t.aborted, exited: t.exited}
}

// drain takes the events of the turn's read that were delivered after the
// consumer stopped: a key still counts (a second Ctrl-C asks to leave chat even
// though the turn is over) and a line is a message that waits for the person's
// next input. A key of another read is dropped by its generation.
func (t *turnInput) drain() {
	for {
		select {
		case event := <-t.state.source.TurnEvents():
			t.handle(event)
		default:
			return
		}
	}
}

// steerOut is where chat writes what may land while the input line is on screen
// (chat.steering): hook notices, errors and the process log. A direct write to
// stderr would sit on top of the line the person is editing - the editor repaints
// only what it wrote itself - so while the line is up those writes go through the
// input source (lineedit prints them above the line and redraws it), and
// otherwise to the stream they always used (design N4).
type steerOut struct {
	source   *chatInputSource
	fallback io.Writer
}

func (w steerOut) Write(p []byte) (int, error) {
	if w.source.lineOnScreen() {
		return w.source.Write(p)
	}
	return w.fallback.Write(p)
}

// steerReturn is one message inserted into a finished turn that no model request
// carried, as chat has to handle it.
type steerReturn struct {
	// id is the steer's own id: it names the message for a sender waiting on it
	// (steer_send.go) and is ignored for the messages a person typed here.
	id   string
	text string
	// sender is who sent it: "" for the person at this keyboard, otherwise the
	// sender of a message that came from elsewhere (SteerSender).
	sender string
}

// steerReturns lists what a finished turn leaves to chat (design D4): the
// messages the runner handed back - the ones a completed turn never delivered,
// or all of them for a cancelled or failed turn - followed by the ones this
// turn's input could not insert at all.
func steerReturns(returned, unaccepted []personal.Steer) []steerReturn {
	if len(returned)+len(unaccepted) == 0 {
		return nil
	}
	out := make([]steerReturn, 0, len(returned)+len(unaccepted))
	for _, s := range append(append([]personal.Steer(nil), returned...), unaccepted...) {
		out = append(out, steerReturn{id: s.ID, text: s.Text, sender: personal.SteerSender(s.Source)})
	}
	return out
}

// steerIDs lists the ids of the messages of a finished turn, so a waiting sender
// can be told what became of the one it sent.
func steerIDs(backs []steerReturn) []string {
	out := make([]string, 0, len(backs))
	for _, b := range backs {
		out = append(out, b.id)
	}
	return out
}

// typedHere reports whether the person at this keyboard typed any of the
// messages: a turn that runs on them is the person's own input, not an injected
// one (the transcript and the UserPromptSubmit hook record that flag).
func typedHere(backs []steerReturn) bool {
	for _, b := range backs {
		if b.sender == "" {
			return true
		}
	}
	return false
}

// steerLines renders inserted messages the way a prompt carries them: one line
// each, a message from elsewhere labeled with its sender ("[来自 x] ...") and a
// message typed here as it was typed.
func steerLines(backs []steerReturn) []string {
	lines := make([]string, 0, len(backs))
	for _, b := range backs {
		if b.sender != "" {
			lines = append(lines, deliveryLine(b.text, b.sender))
			continue
		}
		lines = append(lines, b.text)
	}
	return lines
}

// putSteersBack writes messages a cancelled or failed turn could not deliver back
// into the input line, one line each, in front of whatever is half typed; they
// are not run: ending the turn is meant to stop the agent (design D4).
func (s *chatState) putSteersBack(backs []steerReturn) {
	s.source.putBackLine(strings.Join(steerLines(backs), "\n"))
}

// steerBackNotice is what chat appends to a turn's end notice when N inserted
// messages went back to the input line ("" when there were none).
func steerBackNotice(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("，%d 条插话已放回输入行", n)
}

// nextSteerID names one message inserted into a turn of this chat process, so the
// display and the trace tell them apart (the turn reports each delivery with its
// id). Only the turn's consumer goroutine asks for one: two consumers never run
// at the same time (turnInput.end waits for the previous one).
func (s *chatState) nextSteerID() string {
	s.steerSeq++
	return fmt.Sprintf("k%d", s.steerSeq)
}
