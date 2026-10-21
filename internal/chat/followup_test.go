package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	sugahooks "github.com/inhere/suga/internal/hooks"
)

// TestQueueCommandAtThePrompt: :queue is an ordinary local command at the prompt
// too (the turn is where it is most useful, see TestQueueCommandDuringTurn).
func TestQueueCommandAtThePrompt(t *testing.T) {
	runner, _ := newCLITestRunner(t, &hookTestProvider{})
	var stdout, stderr strings.Builder
	input := strings.NewReader(":queue\n:queue clear\n:queue bogus\n:quit\n")
	assert.NoErr(t, Run(context.Background(), runner, Options{}, input, &stdout, &stderr, nil))
	assert.StrContains(t, stdout.String(), chatQueueEmpty)
	assert.StrContains(t, stdout.String(), queueUsageHint)
}

// TestFollowUpQueue: the queue a turn fills keeps messages in order and can be
// read and cleared from either side.
func TestFollowUpQueue(t *testing.T) {
	t.Run("take keeps the order", func(t *testing.T) {
		var q followUpQueue
		assert.Eq(t, 1, q.push("第一句"))
		assert.Eq(t, 2, q.push("第二句"))
		line, ok := q.take()
		assert.True(t, ok)
		assert.Eq(t, "第一句", line)
		assert.Eq(t, []string{"第二句"}, q.pending())
		line, ok = q.take()
		assert.True(t, ok)
		assert.Eq(t, "第二句", line)
		_, ok = q.take()
		assert.False(t, ok)
	})

	t.Run("clear drops everything", func(t *testing.T) {
		var q followUpQueue
		q.push("a")
		q.push("b")
		assert.Eq(t, 2, q.clear())
		assert.Eq(t, 0, len(q.pending()))
	})
}

// TestTakeFollowUpCutsTheStopWait: a queued line starts the turn after this one,
// so a Stop hook still waiting when the turn ended is cut short the way typing at
// the prompt cuts it: its reply joins that turn as context.
func TestTakeFollowUpCutsTheStopWait(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	interrupted := false
	h.state.waiting = &fakeStopWait{done: make(chan sugahooks.Result, 1), lost: "网页回复",
		interrupt: func() { interrupted = true }}
	h.state.followUps.push("下一句")

	line, ok := h.state.takeFollowUp()
	assert.True(t, ok)
	assert.Eq(t, "下一句", line)
	assert.True(t, interrupted)
	// The wait is gone: the next prompt does not wait for it again.
	assert.Nil(t, h.state.waiting)
	assert.StrContains(t, h.state.lostReply, "网页回复")
}

// TestTabQueuesFollowUp: Tab on a non-empty line during a turn queues that line
// for after the turn (issue suga-8pug): the running turn never sees it, it runs as
// the next turn in the order it was queued, and the input line lets go of it.
func TestTabQueuesFollowUp(t *testing.T) {
	start := func(t *testing.T) (*steerHarness, <-chan error) {
		t.Helper()
		h := newSteerHarness(t, newGateProv())
		// The Tab binding of a real terminal: the line editor's completion callback.
		installCompletion(h.source, nil)
		done := h.startTurn()
		<-h.cast.started()
		return h, done
	}

	t.Run("the queued line runs as the turn after this one", func(t *testing.T) {
		h, done := start(t)
		h.kb.Type("下一句")
		waitKbIdle(t, h.kb)
		h.kb.Type("\t")
		waitUntil(t, "the queued line is shown", func() bool {
			return strings.Contains(h.out.String(), "[排队] 下一句（本轮结束后发送）")
		})
		// Tab took the line: nothing of it waits in the input line, and the running
		// turn was never asked about it.
		assert.Eq(t, "", h.turnInputLine(t))
		assert.Eq(t, []string{"干活"}, h.cast.prompts())

		h.cast.letGo()
		assert.NoErr(t, <-done)
		assert.Eq(t, []string{"干活", "下一句"}, h.cast.prompts())
		assert.StrContains(t, h.out.String(), "[排队] 下一句（已发送）")
		// The person typed it here: that turn is not an injected one.
		assert.False(t, h.turnInjected(t, 1))
	})

	t.Run("queued lines keep their order", func(t *testing.T) {
		h, done := start(t)
		for _, line := range []string{"第一句", "第二句"} {
			h.kb.Type(line)
			waitKbIdle(t, h.kb)
			h.kb.Type("\t")
			waitUntil(t, "the queued line is shown", func() bool {
				return strings.Contains(h.out.String(), "[排队] "+line)
			})
		}
		h.cast.letGo()
		assert.NoErr(t, <-done)
		assert.Eq(t, []string{"干活", "第一句", "第二句"}, h.cast.prompts())
	})

	t.Run("a cancelled turn keeps the queue", func(t *testing.T) {
		h, done := start(t)
		// A lone Esc needs the platform wait to answer "nothing followed it"; the
		// harness has no terminal behind it, so the test supplies it.
		h.source.inputWait = func(time.Duration) bool { return false }
		h.kb.Type("排队的一句")
		waitKbIdle(t, h.kb)
		h.kb.Type("\t")
		waitUntil(t, "the queued line is shown", func() bool {
			return strings.Contains(h.out.String(), "[排队] 排队的一句")
		})
		// Ending the turn does not drop what waits for the turn after it.
		h.kb.Type("\x03")
		waitKbIdle(t, h.kb)
		h.cast.letGo()

		assert.NoErr(t, <-done)
		assert.StrContains(t, h.out.String(), "已中断本轮")
		assert.Eq(t, []string{"干活", "排队的一句"}, h.cast.prompts())
	})

	t.Run("the queue is not touched by a blank or a command line", func(t *testing.T) {
		h, done := start(t)
		// Tab on an empty line does nothing.
		h.kb.Type("\t")
		waitKbIdle(t, h.kb)
		assert.Eq(t, 0, len(h.state.followUps.pending()))
		// Tab on a ":..." line still completes the command.
		h.kb.Type(":he\t")
		waitKbIdle(t, h.kb)
		assert.Eq(t, ":help", h.turnInputLine(t))
		assert.Eq(t, 0, len(h.state.followUps.pending()))
		assert.StrNotContains(t, h.out.String(), "[排队]")

		h.cast.letGo()
		assert.NoErr(t, <-done)
		assert.Eq(t, []string{"干活"}, h.cast.prompts())
	})
}

// TestTabOutsideTurnKeepsTheLine: no turn runs, so Tab keeps its usual meaning -
// a message line stays as typed and nothing is queued.
func TestTabOutsideTurnKeepsTheLine(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	installCompletion(h.source, nil)
	assert.True(t, h.source.Request("> "))
	waitKbIdle(t, h.kb)

	h.kb.Type("abc\t")
	// A prompt read signals its first byte and waits for the acknowledgement before
	// the line editor sees it (the activity handshake).
	select {
	case event := <-h.source.Next():
		assert.True(t, event.activity)
		h.source.AckActivity()
	case <-time.After(3 * time.Second):
		t.Fatal("no activity event")
	}
	waitKbIdle(t, h.kb)

	assert.Eq(t, "abc", h.turnInputLine(t))
	assert.Eq(t, 0, len(h.state.followUps.pending()))
	assert.StrNotContains(t, h.out.String(), "[排队]")
}

// TestQueueCommandDuringTurn: :queue lists what waits and :queue clear drops it -
// both run inside the running turn, which is what fills the queue.
func TestQueueCommandDuringTurn(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	installCompletion(h.source, nil)
	done := h.startTurn()
	<-h.cast.started()

	for _, line := range []string{"第一句", "第二句"} {
		h.kb.Type(line)
		waitKbIdle(t, h.kb)
		h.kb.Type("\t")
		waitUntil(t, "the queued line is shown", func() bool {
			return strings.Contains(h.out.String(), "[排队] "+line)
		})
	}
	h.kb.Type(":queue\r")
	waitUntil(t, "the queue listing", func() bool {
		return strings.Contains(h.out.String(), "排队的消息（2 条")
	})
	assert.StrContains(t, h.out.String(), "1. 第一句")
	assert.StrContains(t, h.out.String(), "2. 第二句")

	h.kb.Type(":queue clear\r")
	waitUntil(t, "the queue cleared", func() bool {
		return strings.Contains(h.out.String(), "已清空 2 条排队的消息")
	})
	assert.Eq(t, 0, len(h.state.followUps.pending()))

	h.cast.letGo()
	assert.NoErr(t, <-done)
	// Nothing was queued any more: the turn after it never ran.
	assert.Eq(t, []string{"干活"}, h.cast.prompts())
}
