package chat

import (
	"fmt"
	"strings"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/process"
)

// The receiving side of session_message (design 2026-10-21 session_message):
// a message another suga session sent arrives through the same session socket as
// `suga send`, so it is the delivery path that decides what a message does.
//
// A message that would be the automatic-reply number tools.session_message.max_hops
// starts no turn: chat keeps it here and hands it to the next turn the person
// starts, so two sessions cannot answer each other without end.

// holdSessionMessage keeps one session message for the next turn the person
// starts. Deliveries arrive from several socket goroutines, so the queue is
// guarded.
func (s *chatState) holdSessionMessage(line string) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	s.sessionHeld = append(s.sessionHeld, line)
}

// holdSessionMessageID records the message ID of a held session message, in the
// same order as holdSessionMessage: the completed turn that runs it reports it
// processed (chat_delivery.go).
func (s *chatState) holdSessionMessageID(id string) {
	if id == "" {
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	s.sessionHeldIDs = append(s.sessionHeldIDs, id)
}

// takeHeldSessionMessageIDs hands over the IDs of the held messages the turn about
// to run carries and clears them.
func (s *chatState) takeHeldSessionMessageIDs() []string {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	ids := s.sessionHeldIDs
	s.sessionHeldIDs = nil
	return ids
}

// takeHeldSessionMessages hands the held session messages to the turn about to
// run, as the context that runs before the person's own line.
func (s *chatState) takeHeldSessionMessages() []string {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	held := s.sessionHeld
	s.sessionHeld = nil
	return held
}

// heldSessionCount is how many session messages wait for the person's next turn
// (the exit summary mentions them).
func (s *chatState) heldSessionCount() int {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	return len(s.sessionHeld)
}

// shouldHoldSessionMessage reports whether a delivery starts no turn: only a
// message another suga session sent can be held, and only once its hop depth
// reaches tools.session_message.max_hops (design session_message D3).
func shouldHoldSessionMessage(from string, hops int) bool {
	if !strings.HasPrefix(from, process.SessionFromPrefix) {
		return false
	}
	return hops >= appconfig.SessionMessageMaxHops()
}

// sessionQueuedHint is what chat shows the person when a session message was held:
// nothing ran, the message waits for their next input.
func sessionQueuedHint(fromName string, hops int) string {
	name := strings.TrimSpace(fromName)
	if name == "" {
		name = "未知会话"
	}
	return fmt.Sprintf("来自会话 %s 的消息已排队（连续自动回复已达上限，第 %d 跳），下次输入时一起处理", safeSender(name), hops)
}
