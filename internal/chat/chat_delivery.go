package chat

import (
	"strings"

	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore"
)

// The receiving side of reliable delivery (design 2026-10-22
// session-message-delivery, phase 1): one turn carries up to deliveryMergeLimit
// queued messages, and the fate of each of them is reported to its sender when the
// turn ends.

// deliveryMergeLimit caps how many queued messages one turn carries (the design's
// "至多 10 条"): the one taken first plus the ones drained behind it.
const deliveryMergeLimit = 10

// takeDelivery turns the delivery just taken off the queue, plus up to
// deliveryMergeLimit-1 messages queued behind it, into one turn's input (design D3).
// Each line keeps its own source label, the pictures ride together and the turn runs
// at the deepest hop depth of the batch.
func takeDelivery(source *chatInputSource, render *Renderer, first chatInputEvent) chatInput {
	batch := []chatInputEvent{first}
	lines := []string{first.line}
	images := append([]agentcore.ImagePart(nil), first.images...)
	hops := first.hops
drain:
	for len(batch) < deliveryMergeLimit {
		select {
		case next := <-source.deliveries:
			batch = append(batch, next)
			lines = append(lines, next.line)
			images = append(images, next.images...)
			if next.hops > hops {
				hops = next.hops
			}
		default:
			break drain
		}
	}
	for _, delivery := range batch {
		// A --wait sender learns now that its message starts a turn; a
		// --wait-processed one waits for the turn's own end instead
		// (settleTurnDeliveries).
		delivery.taken()
		render.External(delivery.line)
	}
	return chatInput{line: strings.Join(lines, "\n"), hasLine: true, injected: first.injected,
		images: images, hops: hops, deliveries: batch}
}

// takeTurnDeliveries hands over the queue deliveries the turn about to run carries
// and clears them: their receipt is sent when that turn ends.
func (s *chatState) takeTurnDeliveries() []chatInputEvent {
	deliveries := s.turnDeliveries
	s.turnDeliveries = nil
	return deliveries
}

// settleTurnDeliveries reports the fate of the messages a turn carried (design D2):
// a completed turn processed every one of them (heldIDs are the messages an earlier
// failure held for this turn, which it just ran as its context); a failed or
// cancelled turn failed them instead, and holds what is still queued for the
// person's next turn - nothing is retried on its own, so a sender always sees the
// failure rather than a silent second attempt.
func (s *chatState) settleTurnDeliveries(deliveries []chatInputEvent, heldIDs []string, completed bool) {
	for _, delivery := range deliveries {
		if completed {
			s.source.recordDeliveryState(delivery.id, personalprocess.StateProcessed)
			delivery.settle(nil)
			continue
		}
		s.source.recordDeliveryState(delivery.id, personalprocess.StateFailed)
		delivery.settle(personalprocess.ErrFailed)
	}
	if completed {
		for _, id := range heldIDs {
			s.source.recordDeliveryState(id, personalprocess.StateProcessed)
		}
		return
	}
	// A turn cut short because chat itself is ending (SIGTERM, the input closed) is
	// no failure barrier: the queued messages stay queued, and the exit path reports
	// them as discarded (design step 5) instead of holding them for a person who is
	// leaving.
	if s.ctx != nil && s.ctx.Err() != nil {
		return
	}
	s.holdQueuedDeliveries()
}

// holdQueuedDeliveries is the failure barrier (design D2): the messages a failed turn
// never took leave the queue for sessionHeld, so the person's next turn carries them
// instead of a retry loop with the sender. It returns how many were moved.
func (s *chatState) holdQueuedDeliveries() int {
	moved := 0
	for {
		select {
		case delivery := <-s.source.deliveries:
			s.source.recordDeliveryState(delivery.id, personalprocess.StateHeld)
			delivery.settle(personalprocess.ErrHeld)
			s.holdSessionMessage(delivery.line)
			s.holdSessionMessageID(delivery.id)
			moved++
		default:
			return moved
		}
	}
}
