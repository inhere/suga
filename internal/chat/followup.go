package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// The follow-up key (issue suga-8pug; the Q2 of the steering design that was left
// out): while a turn runs, Enter inserts the line into that turn and Tab queues it
// instead. The running turn never sees a queued line; it runs as the turn right
// after this one, one message per turn and in the order they were queued - the
// same meaning `suga send` has without --steer. The queue belongs to this chat
// process (queued messages are not persisted, a non-goal of the steering design):
// the end of a turn drains it (chat_turn.go), and :queue lists or clears what is
// still waiting.

// What chat says about a queued line: it waits for the running turn to end, or
// that turn runs it.
const (
	followUpQueuedSuffix = "（本轮结束后发送）"
	followUpSentSuffix   = "（已发送）"
)

const (
	// queueCmd names the command that shows and changes the queue.
	queueCmd = ":queue"
	// chatQueueEmptyKey answers :queue (and :queue clear) with nothing queued.
	chatQueueEmptyKey = "cmd.queue.empty"
)

// isQueueCommand recognizes :queue and its arguments.
func isQueueCommand(line string) bool {
	return line == queueCmd || strings.HasPrefix(line, queueCmd+" ")
}

// followUpQueue is the messages a person queued with Tab while a turn ran. The
// turn's input line (a goroutine of its own) appends, the chat loop takes them
// after the turn, and :queue reads or clears them from either side.
type followUpQueue struct {
	mu    sync.Mutex
	lines []string
}

// push appends one message and reports how many are queued after it.
func (q *followUpQueue) push(text string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.lines = append(q.lines, text)
	return len(q.lines)
}

// take removes the oldest queued message.
func (q *followUpQueue) take() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.lines) == 0 {
		return "", false
	}
	line := q.lines[0]
	q.lines = append([]string(nil), q.lines[1:]...)
	return line, true
}

// pending lists what is queued, oldest first.
func (q *followUpQueue) pending() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.lines...)
}

// remove drops the queued message at index (0-based) and returns it; false means
// there is no such message.
func (q *followUpQueue) remove(index int) (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if index < 0 || index >= len(q.lines) {
		return "", false
	}
	line := q.lines[index]
	// The full slice expression caps the capacity at index, so append copies the
	// tail into a new array and the queue keeps no overlapping view of it.
	q.lines = append(q.lines[:index:index], q.lines[index+1:]...)
	return line, true
}

// clear drops every queued message and reports how many went.
func (q *followUpQueue) clear() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.lines)
	q.lines = nil
	return n
}

// queueFollowUp queues one line typed at the Tab key during a turn, and reports
// whether it was taken: then the line editor drops it from the input line. A line
// a local command starts is not a message and cannot run during a turn, so it
// stays in the input line for the next prompt (the refusal Enter gets, steering.go).
func (s *chatState) queueFollowUp(text string) bool {
	if _, isLocal := parseLocalCommand(text); isLocal {
		s.render.Hint(s.tr().T("status.local.command_busy"))
		return false
	}
	s.followUps.push(text)
	s.render.FollowUpQueued(text)
	return true
}

// takeFollowUp is the queued message a finished turn leaves to run right away: it
// is the turn that follows this one, so a Stop hook wait is cut short the way
// typing at the prompt cuts it (its reply joins that turn).
func (s *chatState) takeFollowUp() (string, bool) {
	line, ok := s.followUps.take()
	if !ok {
		return "", false
	}
	if s.waiting != nil {
		s.lostReply = appendContext(s.lostReply, s.waiting.Interrupt(context.Background()))
		s.waiting = nil
	}
	s.render.FollowUpSent(line)
	return line, true
}

// runQueueCommand answers :queue (list what waits), :queue -d <N> (drop the Nth
// message) and :queue clear (drop them all). It is the one local command a
// running turn runs itself (steering.go), because the turn is what fills the
// queue.
func (s *chatState) runQueueCommand(line string) {
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, queueCmd)))
	if len(args) == 0 {
		s.printQueue()
		return
	}
	switch args[0] {
	case "clear":
		if len(args) > 1 {
			fmt.Fprintln(s.source, commandUsageText(s.tr(), queueCmd))
			return
		}
		if n := s.followUps.clear(); n > 0 {
			fmt.Fprintln(s.source, s.tr().T("cmd.queue.cleared", map[string]any{"count": n}))
			return
		}
		fmt.Fprintln(s.source, s.tr().T(chatQueueEmptyKey))
	case "-d":
		s.dropQueued(args[1:])
	default:
		fmt.Fprintln(s.source, commandUsageText(s.tr(), queueCmd))
	}
}

// printQueue lists what waits, oldest first.
func (s *chatState) printQueue() {
	pending := s.followUps.pending()
	if len(pending) == 0 {
		fmt.Fprintln(s.source, s.tr().T(chatQueueEmptyKey))
		return
	}
	fmt.Fprintln(s.source, s.tr().T("cmd.queue.header", map[string]any{"count": len(pending)}))
	for i, text := range pending {
		fmt.Fprintf(s.source, "  %d. %s\n", i+1, truncateLine(text, resultSummaryLimit))
	}
}

// dropQueued removes the message :queue -d names, counted as :queue lists them.
func (s *chatState) dropQueued(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(s.source, commandUsageText(s.tr(), queueCmd))
		return
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 {
		fmt.Fprintln(s.source, commandUsageText(s.tr(), queueCmd))
		return
	}
	text, ok := s.followUps.remove(n - 1)
	if !ok {
		fmt.Fprintln(s.source, s.tr().T("cmd.queue.missing", map[string]any{"index": n}))
		return
	}
	fmt.Fprintln(s.source, s.tr().T("cmd.queue.dropped", map[string]any{"index": n, "text": truncateLine(text, resultSummaryLimit)}))
}

// setFollowUp installs (or clears, with nil) what Tab on a non-empty line does
// while a read is in progress: the turn queues that line for after it.
func (s *chatInputSource) setFollowUp(followUp func(text string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.followUp = followUp
}

// tabFollowUp serves the line editor's Tab key (installCompletion): only the read
// of a running turn queues a message. Every other read - the prompt between turns,
// an approval question - returns false and leaves Tab to the editor, so a command
// line still completes and an empty line does nothing.
func (s *chatInputSource) tabFollowUp(line string) bool {
	s.mu.Lock()
	mode, followUp := s.readMode, s.followUp
	s.mu.Unlock()
	if mode != modeSteer || followUp == nil || !queuesFollowUp(line) {
		return false
	}
	return followUp(strings.TrimSpace(line))
}

// queuesFollowUp reports whether Tab queues a line typed during a turn: a message
// does, a blank line does nothing and a ":..." line keeps Tab's command completion.
func queuesFollowUp(line string) bool {
	text := strings.TrimSpace(line)
	return text != "" && !strings.HasPrefix(text, ":")
}
