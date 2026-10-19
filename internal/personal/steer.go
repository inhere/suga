package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// Steering: a person types into a turn that is already running (design
// "personal：会话收件箱"). A steer enters the session's inbox, the turn's next
// model request takes it before the request goes out (takeSteers), and whatever
// the turn did not deliver is handed back to the caller when it ends (finish).
// Nothing a person typed is dropped; a steer no request has taken yet can also be
// taken back before that (WithdrawSteers), so chat can hand it to the person for
// editing again.

// steerCapacity is how many steers one session's inbox holds (design D11).
const steerCapacity = 8

// ErrSteerQueueFull reports an inbox that already holds steerCapacity steers:
// the caller tells the person to wait for the step to end instead of queueing
// without bound.
var ErrSteerQueueFull = errors.New("steering queue is full")

// SteerSourceKeyboard is the source of a steer a person typed into the chat (a
// steer delivered by `suga send --steer` carries "send:<from>" instead).
const SteerSourceKeyboard = "keyboard"

// SteerSender returns the sender of a steer source: the <from> of "send:<from>",
// or "" for a steer a person typed at this keyboard (SteerSourceKeyboard, an
// unknown source). The chat labels a message from elsewhere with it ("[来自 x]").
func SteerSender(source string) string {
	from, ok := strings.CutPrefix(source, "send:")
	if !ok {
		return ""
	}
	return from
}

// metaSteer marks a user entry a person typed into a running turn (steering).
// Such an entry is not the turn's request (see injectedItemKeys) and is replayed
// in place by later turns.
const metaSteer = "suga.steer"

// metaSteerSource and metaSteerID carry where a steer came from ("keyboard",
// "send:<from>") and its id, so a trace and the chat can tell steers apart.
const (
	metaSteerSource = "suga.source"
	metaSteerID     = "suga.steer_id"
)

// Steer is one message a caller inserts into the running turn of a session
// (keyboard input, `suga send --steer`).
type Steer struct {
	// ID identifies the steer across the turn (the chat marks the one it sent
	// as delivered, the trace records it); empty is allowed.
	ID string
	// Text is the message as typed. An empty text is never accepted.
	Text string
	// Source names where the steer came from: "keyboard", "send:<from>".
	Source string
}

// steerInbox is the pending steers of one session plus the ones the running
// turn already delivered. It exists only while a turn of the session runs in
// this process: Run opens it right before calling the runtime and its defer
// drops it (finish closes it on the normal path).
type steerInbox struct {
	mu     sync.Mutex
	closed bool
	// turnID is the turn the inbox was opened for: a withdrawal is recorded in
	// the trace as an event of it, next to the deliveries and returns (turnID is
	// written by openSteerInbox before the inbox is published, so it is never
	// read while it changes).
	turnID string
	// pending are the steers no model request has taken yet, taken the ones a
	// request of this turn already carried in its context.
	pending []Steer
	taken   []Steer
}

// push enqueues msg. It reports accepted=false when the inbox is (or already
// was) closing, when msg carries no text, and ErrSteerQueueFull when it is
// full.
func (in *steerInbox) push(msg Steer) (bool, error) {
	if in == nil {
		return false, nil
	}
	// An empty steer would only add an empty user message to the context.
	if strings.TrimSpace(msg.Text) == "" {
		return false, nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return false, nil
	}
	if len(in.pending) >= steerCapacity {
		return false, ErrSteerQueueFull
	}
	in.pending = append(in.pending, msg)
	return true, nil
}

// take moves every pending steer to taken and returns them; the turn's coming
// model request carries them. Nothing is taken once the inbox is closed (the
// turn's outcome is being decided then).
func (in *steerInbox) take() []Steer {
	if in == nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed || len(in.pending) == 0 {
		return nil
	}
	out := in.pending
	in.pending = nil
	in.taken = append(in.taken, out...)
	return out
}

// withdraw takes every pending steer accept matches out of the inbox and returns
// them in the order they arrived, leaving the others waiting. It is the
// counterpart of take, and the inbox lock is what makes the two mutually
// exclusive: a steer a model request already took is in taken and can no longer
// be withdrawn, and a withdrawn steer is gone before any later request could take
// it. Nothing is withdrawn once the inbox is closed (the turn's outcome is being
// decided and returned hands the pending steers to the caller itself), and a nil
// accept matches every pending steer.
func (in *steerInbox) withdraw(accept func(Steer) bool) []Steer {
	if in == nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed || len(in.pending) == 0 {
		return nil
	}
	var out, waiting []Steer
	for _, s := range in.pending {
		if accept == nil || accept(s) {
			out = append(out, s)
			continue
		}
		waiting = append(waiting, s)
	}
	if len(out) == 0 {
		return nil
	}
	in.pending = waiting
	return out
}

// close stops accepting steers (the turn is ending).
func (in *steerInbox) close() {
	if in == nil {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	in.closed = true
}

// returned hands back the steers the caller has to take care of and empties the
// inbox. A completed turn (its messages are stored and replay) returns only the
// steers no request took; a cancelled or failed turn returns all of them, the
// taken ones first, because the replay of such a turn skips steering entries
// (design D4; see replay.go and design D5 of
// docs/design/2026-10-15-interrupted-turn-replay-design.md).
func (in *steerInbox) returned(completed bool) []Steer {
	if in == nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	in.closed = true
	var out []Steer
	if !completed {
		out = append(out, in.taken...)
	}
	out = append(out, in.pending...)
	in.pending, in.taken = nil, nil
	return out
}

// openSteerInbox opens the session's inbox for the turn about to run, replacing
// any left over from an earlier turn. Run calls it after every early return,
// right before calling the runtime: a Steer arriving earlier is not accepted
// (design N7) and becomes the caller's next turn. turnID is the turn the inbox
// belongs to; a withdrawal is recorded as an event of it.
func (r *Runner) openSteerInbox(sessionID, turnID string) *steerInbox {
	in := &steerInbox{turnID: turnID}
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steers == nil {
		r.steers = make(map[string]*steerInbox)
	}
	r.steers[sessionID] = in
	return in
}

// dropSteerInbox forgets the session's inbox when the turn is over. finish
// closes the inbox on the normal path; this is the safety net that keeps a
// stray inbox (a panic) from accepting steers after the turn.
func (r *Runner) dropSteerInbox(sessionID string, in *steerInbox) {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steers[sessionID] == in {
		delete(r.steers, sessionID)
	}
}

// Steer hands msg to the running turn of sessionID (design D6, D11). It returns
// accepted=false when no turn of the session runs in this process, when that
// turn is already closing (its outcome is being decided) or when msg is empty;
// ErrSteerQueueFull when the inbox holds steerCapacity steers already. An
// accepted steer is either taken by the turn's next model request (a
// "steer_delivered" event and a user message in that request) or handed back
// through RunResult.ReturnedSteers when the turn ends.
func (r *Runner) Steer(sessionID string, msg Steer) (bool, error) {
	r.steerMu.Lock()
	in := r.steers[sessionID]
	r.steerMu.Unlock()
	return in.push(msg)
}

// isKeyboardSteer reports whether a steer was typed at this keyboard rather than
// sent from elsewhere (`suga send --steer`): the ones a withdrawal takes back
// (WithdrawSteers, chat's Up key on an empty input line).
func isKeyboardSteer(s Steer) bool { return s.Source == SteerSourceKeyboard }

// WithdrawSteers takes the session's pending steers accept matches back out of the
// running turn's inbox and returns them in the order they were typed; the ones a
// model request of that turn already took stay in the turn's history and are not
// withdrawn. accept=nil takes back the ones the person typed at this keyboard
// (SteerSourceKeyboard): a message a `suga send --steer` caller sent is not
// withdrawn, its sender is waiting for its own fate. Nothing is withdrawn when no
// turn of the session runs in this process, when nothing matches, or when the turn
// is already closing (its outcome is being decided and it hands the pending steers
// back itself).
//
// Each withdrawn steer is recorded in the trace as a "withdrawn" steer event of
// the turn, so `suga inspect trace` shows what a turn did not receive after all. Chat puts
// the returned text back on the input line for editing (design "chat：退回插话的
// 处理").
func (r *Runner) WithdrawSteers(sessionID string, accept func(Steer) bool) []Steer {
	if accept == nil {
		accept = isKeyboardSteer
	}
	r.steerMu.Lock()
	in := r.steers[sessionID]
	r.steerMu.Unlock()
	steers := in.withdraw(accept)
	// The record is written outside any turn context; the inbox carries the turn
	// it belongs to, so the event lands in that turn's timeline like a delivery or
	// a return does.
	for _, s := range steers {
		r.rec.record(context.Background(), steerEvent(sessionID, in.turnID, "withdrawn", s,
			map[string]any{"count": len(steers)}))
	}
	return steers
}

// steerInboxKey carries the turn's open inbox to finish (which closes it and
// computes the returned steers), like turnInput and turnDenials carry theirs.
type steerInboxKey struct{}

func withSteerInbox(ctx context.Context, in *steerInbox) context.Context {
	return context.WithValue(ctx, steerInboxKey{}, in)
}

func steerInboxOf(ctx context.Context) *steerInbox {
	in, _ := ctx.Value(steerInboxKey{}).(*steerInbox)
	return in
}

// SteerMessage is the user message of a steer: the text as typed, its source
// and id in the metadata (isSteerItem tells it apart from the turn's own
// messages, see injectedItemKeys).
func SteerMessage(s Steer) agentcore.Message {
	return agentcore.Message{Role: "user", Content: s.Text, Metadata: map[string]any{
		metaSteer: true, metaSteerSource: s.Source, metaSteerID: s.ID,
	}}
}

// isSteerItem reports whether m is a message a person typed into a running turn.
func isSteerItem(m agentcore.Message) bool {
	v, _ := m.Metadata[metaSteer].(bool)
	return v
}

// steerSourceOf returns the recorded source of a steering message ("" when it
// carries none).
func steerSourceOf(m agentcore.Message) string {
	s, _ := m.Metadata[metaSteerSource].(string)
	return s
}

// steerLabel is how a steering message is labeled where its text is rendered
// for the model (the summary input of a segment break): "[插话]" without a
// known source, "[插话 keyboard]" with one.
func steerLabel(m agentcore.Message) string {
	if src := steerSourceOf(m); src != "" {
		return "[插话 " + src + "]"
	}
	return "[插话]"
}

// takeSteers moves the session's pending steers into this turn, before the
// coming model request: each becomes a user message of the turn - at its end -
// stored as an entry (so later turns replay it in place) with its source and id
// in the metadata. It emits "steer_delivered" for each and records it in the
// trace. Nothing is taken while the step is a wrap-up one (design D5): that
// step only asks for a final answer, and finish hands the steers back instead.
func (b *contextBuilder) takeSteers(ctx context.Context, state *agentcore.RunState) {
	if b.steers == nil || state == nil || state.WrappingUp != "" {
		return
	}
	steers := b.steers.take()
	if len(steers) == 0 {
		return
	}
	step := state.Step
	for _, s := range steers {
		item := turnInjected{at: len(state.Messages), msg: SteerMessage(s)}
		raw, err := json.Marshal(item.msg)
		if err == nil {
			var entries []store.Entry
			entries, err = b.r.cfg.Repository.AppendEntries(context.WithoutCancel(ctx), b.sessionID,
				store.NewEntry{TurnID: b.turnID, Type: store.EntryUser, JSON: raw})
			if err == nil && len(entries) > 0 {
				item.seq = entries[0].Seq
			}
		}
		if err != nil {
			slog.Warn("store steer", "session_id", b.sessionID, "steer_id", s.ID, "err", err)
		}
		b.injected = append(b.injected, item)
		b.r.rec.record(ctx, steerEvent(b.sessionID, b.turnID, "delivered", s,
			map[string]any{"step": step}))
		if err := emit(ctx, b.emit, Event{Kind: "steer_delivered", SessionID: b.sessionID,
			TurnID: b.turnID, RunID: b.turnID, Text: s.Text, SteerID: s.ID, SteerSource: s.Source}); err != nil {
			slog.Warn("emit steer_delivered", "session_id", b.sessionID, "steer_id", s.ID, "err", err)
		}
	}
	// The turn's stored entries changed: the cached view of items() is stale.
	b.turnItemsCache = nil
}

// steerReturnReasons maps how a turn ended to the reason and the disposition a
// returned steer is recorded with (design "观测"): the untaken steers of a
// completed turn become the caller's next turn, the steers of a cancelled or
// failed turn go back to the input line.
func steerReturnReasons(turnStatus string) (reason, disposition string) {
	switch turnStatus {
	case store.TurnCancelled:
		return "cancelled", "input_line"
	case store.TurnFailed:
		return "failed", "input_line"
	}
	return "completed_untaken", "next_turn"
}

// steerEvent is one trace record of a steering message: kind steer, name is
// "delivered", "returned" (which adds reason and disposition) or "withdrawn"
// (which adds count, the size of the withdrawal it belongs to).
func steerEvent(sessionID, turnID, name string, s Steer, extra map[string]any) store.Event {
	attrs := map[string]any{"steer_id": s.ID, "source": s.Source, "chars": len(s.Text)}
	for k, v := range extra {
		attrs[k] = v
	}
	return store.Event{Kind: store.EventSteer, Name: name, SessionID: sessionID, TurnID: turnID,
		Status: eventOK, Attrs: attrs}
}

// returnSteers empties the turn's inbox and hands back the steers the caller
// has to take care of (design D4), recording each in the trace and telling the
// caller through a "steer_returned" event. turnStatus decides what comes back:
// a completed turn (its messages are stored and replay) keeps its delivered
// steers and returns only the untaken ones, a cancelled or failed turn returns
// all of them (its replay skips steering entries, see replayablePrefix).
func (r *Runner) returnSteers(ctx context.Context, inbox *steerInbox, result RunResult, turnStatus string, sink EmitFunc) []Steer {
	steers := inbox.returned(turnStatus == store.TurnCompleted)
	if len(steers) == 0 {
		return nil
	}
	reason, disposition := steerReturnReasons(turnStatus)
	for _, s := range steers {
		r.rec.record(ctx, steerEvent(result.SessionID, result.TurnID, "returned", s,
			map[string]any{"reason": reason, "disposition": disposition}))
		if err := emit(ctx, sink, Event{Kind: "steer_returned", SessionID: result.SessionID,
			TurnID: result.TurnID, RunID: result.RunID, Text: s.Text, SteerID: s.ID, SteerSource: s.Source}); err != nil {
			slog.Warn("emit steer_returned", "session_id", result.SessionID, "steer_id", s.ID, "err", err)
		}
	}
	return steers
}

// turnSteers lists the text and source of a turn's steering messages in the
// order they were delivered (the handoff package carries them; a steer of an
// earlier turn is ordinary history by the time a segment breaks).
func (r *Runner) turnSteers(ctx context.Context, sessionID, turnID string) ([]string, error) {
	if turnID == "" {
		return nil, nil
	}
	entries, err := r.cfg.Repository.ListEntries(ctx, sessionID, store.EntryFilter{
		TurnID: turnID, Types: []string{store.EntryUser},
	})
	if err != nil {
		return nil, fmt.Errorf("list turn steers: %w", err)
	}
	var out []string
	for _, e := range entries {
		var m agentcore.Message
		if err := json.Unmarshal(e.JSON, &m); err != nil || !isSteerItem(m) {
			continue
		}
		text := truncateMiddle(strings.TrimSpace(m.Content), requestTextLimit)
		if src := steerSourceOf(m); src != "" {
			text += "（来源：" + src + "）"
		}
		out = append(out, text)
	}
	return out, nil
}
