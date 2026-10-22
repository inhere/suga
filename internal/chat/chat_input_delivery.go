package chat

import (
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore"
)

// The message state table of the chat input source (design session-message-delivery
// D1/D4): which IDs this process took, their latest state, the duplicates waiting for
// it, and the exit path that reports what never ran.

// enqueueDelivery is what both delivery entry points run: it labels the message,
// dedupes it by ID and queues it.
func (s *chatInputSource) enqueueDelivery(id string, waitProcessed bool, text, from, fromName string, hops int, images []agentcore.ImagePart) (bool, <-chan error, error) {
	if strings.TrimSpace(text) == "" {
		return false, nil, fmt.Errorf("delivery text is empty")
	}
	settled := make(chan error, 1)
	line := deliveryLine(text, from)
	if strings.HasPrefix(from, process.SessionFromPrefix) {
		// A message of another session names the peer and its full ID, so the model
		// reads it as a request or report of a companion, not as the person's own
		// instruction (design session_message "来源标记").
		line = sessionDeliveryLine(text, from, fromName)
	}
	event := chatInputEvent{line: line, injected: true, settled: settled, images: images, fromName: fromName, hops: hops, id: id, deferSettle: waitProcessed}
	// Hold mu across the closed check and the enqueue so DiscardQueued cannot miss a delivery.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, nil, process.ErrSessionClosed
	}
	if id != "" {
		if s.deliveryStates == nil {
			s.deliveryStates = make(map[string]string)
		}
		if s.deliverySeen == nil {
			s.deliverySeen = make(map[string]struct{})
		}
		if _, seen := s.deliverySeen[id]; seen {
			// A message whose ID this process already took is not queued again (design
			// D2: at most once). The sender reads the ack of the first copy, and a
			// status query names this ID.
			state := s.deliveryStates[id]
			if !waitProcessed {
				// A --wait or plain duplicate reads the first copy's ack only, as before.
				return state == process.StateQueued || state == process.StateSteered, nil, nil
			}
			// The duplicate gets the first copy's fate too: at once when it is
			// final, else when the first copy settles. Without a settled channel a
			// --wait-processed sender would read a closed connection ("not online").
			dup := make(chan error, 1)
			if err, final := deliveryStateErr(state); final {
				dup <- err
			} else {
				if s.deliveryWaiters == nil {
					s.deliveryWaiters = make(map[string][]chan error)
				}
				s.deliveryWaiters[id] = append(s.deliveryWaiters[id], dup)
			}
			return true, dup, nil
		}
		s.deliverySeen[id] = struct{}{}
	}
	queued := s.busy || len(s.deliveries) > 0
	select {
	case s.deliveries <- event:
		s.recordDeliveryStateLocked(id, process.StateQueued)
		return queued, settled, nil
	default:
		// A refused message never entered the queue, so its ID is free again.
		delete(s.deliverySeen, id)
		return true, nil, process.ErrQueueFull
	}
}

// maxDeliveryStates is how many message IDs the state table keeps: a long chat must
// not grow it without bound, and an ID older than that is reported as unknown (the
// same answer a restarted chat gives).
const maxDeliveryStates = 1000

// recordDeliveryState remembers the state of one message ID for a status query
// (`session_message action=status`, design D3).
func (s *chatInputSource) recordDeliveryState(id, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordDeliveryStateLocked(id, state)
}

// recordDeliveryStateLocked is recordDeliveryState with mu held.
func (s *chatInputSource) recordDeliveryStateLocked(id, state string) {
	if id == "" {
		return
	}
	if s.deliveryStates == nil {
		s.deliveryStates = make(map[string]string)
	}
	if s.deliverySeen == nil {
		s.deliverySeen = make(map[string]struct{})
	}
	if _, known := s.deliveryStates[id]; !known {
		s.deliveryOrder = append(s.deliveryOrder, id)
		// The oldest ID leaves the table (and the dedupe set) once the cap is reached.
		for len(s.deliveryOrder) > maxDeliveryStates {
			oldest := s.deliveryOrder[0]
			s.deliveryOrder = s.deliveryOrder[1:]
			delete(s.deliveryStates, oldest)
			delete(s.deliverySeen, oldest)
		}
	}
	s.deliveryStates[id] = state
	// Duplicates of this ID waiting for its fate learn it now.
	if err, final := deliveryStateErr(state); final {
		for _, ch := range s.deliveryWaiters[id] {
			select {
			case ch <- err:
			default:
			}
		}
		delete(s.deliveryWaiters, id)
	}
}

// deliveryStateErr maps a recorded message state to the error a settled channel
// carries for it (nil processed, ErrHeld, ErrFailed, ErrDiscarded); final is
// false while the message has not reached such a state.
func deliveryStateErr(state string) (err error, final bool) {
	switch state {
	case process.StateProcessed:
		return nil, true
	case process.StateHeld:
		return process.ErrHeld, true
	case process.StateFailed:
		return process.ErrFailed, true
	case process.StateDiscarded:
		return process.ErrDiscarded, true
	}
	return nil, false
}

// DeliveryState answers the state of one message ID of this process: ok=false for an
// ID it never saw (the restart case a sender must be told about).
func (s *chatInputSource) DeliveryState(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.deliveryStates[id]
	return state, ok
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
			s.recordDeliveryStateLocked(event.id, process.StateDiscarded)
			event.settle(process.ErrDiscarded)
			n++
		default:
			return n
		}
	}
}
