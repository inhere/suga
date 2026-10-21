package chat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore"
)

// The send side of steering (design "send：--steer"): `suga send --steer` asks
// this chat to insert its message into the turn running right now, the way a line
// typed at the keyboard during a turn is inserted. The delivery endpoint of the
// session socket is deliverMessage below; what it inserts is a personal.Steer
// with the source "send:<from>", so the message reaches the model as a user
// message of the running turn and the trace tells it apart from a typed one.
//
// A sender that waits (`--wait`) gets a second response when the message reaches
// a model request or starts its own turn; when the message is handed back instead
// - the turn was cancelled or failed, or chat is leaving - the response is
// "discarded" and the CLI exits 3.

// deliverMessage is this chat's session-socket delivery endpoint
// (personalprocess.DeliverWaitFunc, registered by registerChatProcess). A "steer"
// request goes into the turn running right now when there is one and is queued for
// the next turn otherwise (the chat is idle, the turn is already closing, or
// chat.steering is off); every other mode queues the message, which is what every
// client that predates Request.Mode asks for.
func (s *chatState) deliverMessage(mode, text, from, fromName string, hops int, refs []personalprocess.ImageRef) (personalprocess.Delivery, <-chan error, error) {
	// The pictures of `suga send --image` are checked before anything is queued: a
	// reference whose file is gone or whose bytes changed refuses the whole message
	// (design send --image).
	images, err := verifyDeliveryImages(s.runner.SessionsDir(), s.sessionID, refs)
	if err != nil {
		return personalprocess.Delivery{}, nil, err
	}
	warning := s.deliveryWarning(len(images))
	// A session message that has used up the automatic replies is held for the
	// person's next turn instead of starting one (design session_message D3).
	if shouldHoldSessionMessage(from, hops) {
		s.holdSessionMessage(deliveryLineFor(text, from, fromName))
		s.render.Hint(sessionQueuedHint(fromName, hops))
		return personalprocess.Delivery{Queued: true, Warning: warning}, nil, nil
	}
	// A session message is never inserted into the running turn: that turn keeps
	// its own hop depth, so a steered reply would let two sessions answer each
	// other inside one long turn without the limit above ever seeing it. It waits
	// for a turn of its own instead, which carries the message's hops.
	if mode == personalprocess.ModeSteer && strings.HasPrefix(from, personalprocess.SessionFromPrefix) {
		mode = personalprocess.ModeFollowUp
	}
	if mode == personalprocess.ModeSteer {
		settled := make(chan error, 1)
		accepted, err := s.insertSendSteer(deliveryLineFor(text, from, fromName), from, images, settled)
		if errors.Is(err, personal.ErrSteerQueueFull) {
			// The session socket answers queue_full only for its own ErrQueueFull:
			// a full steering inbox is the same refusal to the sender.
			return personalprocess.Delivery{}, nil, fmt.Errorf("%w: %w", personalprocess.ErrQueueFull, err)
		}
		if err != nil {
			return personalprocess.Delivery{}, nil, err
		}
		if accepted {
			return personalprocess.Delivery{Steered: true, Warning: warning}, settled, nil
		}
	}
	queued, settled, err := s.source.DeliverImages(text, from, fromName, hops, images)
	if err != nil {
		return personalprocess.Delivery{}, nil, err
	}
	if mode == personalprocess.ModeSteer {
		// From the sender's point of view the message was not inserted into the
		// running turn, so it waits for a turn of its own - whether the chat was
		// idle (the message starts that turn at once) or busy (it queues behind the
		// running one). Design "send：--steer" answers that case with "queued".
		return personalprocess.Delivery{Queued: true, Warning: warning}, settled, nil
	}
	return personalprocess.Delivery{Queued: queued, Warning: warning}, settled, nil
}

// deliveryWarning tells a sender what the first-phase rule of image input does to
// the pictures of a model without vision: the message still carries them, and the
// provider layer replaces each with a text note (pkg/agentcore/llm/images.go).
func (s *chatState) deliveryWarning(images int) string {
	if images == 0 || s.canSeeImages() {
		return ""
	}
	return fmt.Sprintf("当前模型 %s 不支持看图，附带的 %d 张图片已转为文字说明", s.modelInfo.Ref(), images)
}

// insertSendSteer hands one `suga send --steer` message to the turn running right
// now. accepted=false means there is no turn to insert into; the caller queues the
// message for the next turn instead, and the turn must not see it at all (it would
// run it a second time). ErrSteerQueueFull is the full inbox (design D11): the
// message is neither inserted nor queued.
func (s *chatState) insertSendSteer(line, from string, images []agentcore.ImagePart, settled chan error) (bool, error) {
	turn := s.currentTurn()
	if turn == nil {
		return false, nil
	}
	// The message carries the label every delivery gets ("[来自 x] "), and the
	// model sees exactly that text (design "send：--steer"); the source names the
	// sender, so the chat shows it back with the same label. The pictures of the
	// delivery ride with it.
	msg := personal.Steer{ID: s.nextSendSteerID(), Text: line,
		Source: "send:" + safeSender(from), Images: images}
	accepted, err := turn.insertFromElsewhere(msg)
	if err != nil {
		// The inbox is full (design D11): the person sees why nothing appeared, and
		// the sender is answered queue_full instead of the message being queued.
		s.render.Hint(steerFullHint)
		return false, err
	}
	if !accepted {
		// The turn is already ending (its inbox closed): the message waits for the
		// next turn like any other delivery.
		return false, nil
	}
	s.rememberSendSteer(msg.ID, settled)
	return true, nil
}

// currentTurn is the turn of this chat whose read is open right now (nil: between
// turns, or chat.steering off, when nothing is read during a turn).
func (s *chatState) currentTurn() *turnInput {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return s.sendTurn
}

// setCurrentTurn publishes the turn whose read is open (startTurnInput).
func (s *chatState) setCurrentTurn(turn *turnInput) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.sendTurn = turn
}

// dropCurrentTurn forgets a turn whose read ended: nothing can be inserted into it
// any more (turnInput.end).
func (s *chatState) dropCurrentTurn(turn *turnInput) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.sendTurn == turn {
		s.sendTurn = nil
	}
}

// nextSendSteerID names one message inserted by `suga send --steer`, so the trace
// tells it apart from the ones typed here (nextSteerID). The session socket answers
// one request per goroutine, so two of them may ask at once.
func (s *chatState) nextSendSteerID() string {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.sendSeq++
	return fmt.Sprintf("s%d", s.sendSeq)
}

// rememberSendSteer keeps the channel of a waiting sender until the message's fate
// is known; without a waiting sender nothing reads it (buffered, never blocking).
func (s *chatState) rememberSendSteer(id string, settled chan error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sendSettled == nil {
		s.sendSettled = make(map[string]chan error)
	}
	s.sendSettled[id] = settled
}

// settleSendSteer reports the fate of one inserted message to the sender still
// waiting for it, and forgets the message. err is nil once it reached the model or
// runs as a turn of its own; a message whose fate was already reported is dropped
// (a turn cancelled after its delivery only reaches the trace, the known limit of
// design "--wait" 完成点).
func (s *chatState) settleSendSteer(id string, err error) {
	s.sendMu.Lock()
	settled, ok := s.sendSettled[id]
	delete(s.sendSettled, id)
	s.sendMu.Unlock()
	if !ok {
		return
	}
	select {
	case settled <- err:
	default:
	}
}

// settleSendSteers reports the fate of the messages a finished turn handed back,
// by id: nil when they now run as a turn of their own (the turn completed and did
// not deliver them), ErrDiscarded when the turn was cancelled or failed and they
// go back to the input line unrun (design D4). Ids of messages this chat did not
// insert (a typed one), or already reported, are ignored.
func (s *chatState) settleSendSteers(ids []string, err error) {
	for _, id := range ids {
		s.settleSendSteer(id, err)
	}
}

// deliverSendSteer reports that a model request of the turn carried an inserted
// message (its "steer_delivered" event, design "--wait" 完成点 "送达模型"): a
// waiting `suga send --steer` sender is told the message reached the model. How
// the message is shown is the turn's own business (turnInput.deliver shows every
// message it recorded, the typed ones and the ones sent here alike).
func (s *chatState) deliverSendSteer(event personal.Event) {
	s.sendMu.Lock()
	settled, ok := s.sendSettled[event.SteerID]
	delete(s.sendSettled, event.SteerID)
	s.sendMu.Unlock()
	if !ok {
		return
	}
	select {
	case settled <- nil:
	default:
	}
}

// discardPendingSendSteers fails every message still waiting for an outcome: chat
// is leaving, so the messages inserted into the last turn will not run. Their
// senders learn it here rather than from a dropped connection (design m1).
func (s *chatState) discardPendingSendSteers() {
	s.sendMu.Lock()
	pending := make([]string, 0, len(s.sendSettled))
	for id := range s.sendSettled {
		pending = append(pending, id)
	}
	s.sendMu.Unlock()
	s.settleSendSteers(pending, personalprocess.ErrDiscarded)
}
