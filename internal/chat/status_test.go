package chat

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
)

// The status line above the input line (chat.steering): what it says while a turn
// runs, when it goes and comes back, and in which order it goes with the output of
// the turn.

// statusLog is the output and the status line of one renderer in one ordered log,
// so a test can check both what the status line said and that the output of the turn
// was printed before it.
type statusLog struct {
	mu     sync.Mutex
	events []string
}

func (l *statusLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *statusLog) Write(p []byte) (int, error) {
	l.add("out: " + string(p))
	return len(p), nil
}

func (l *statusLog) SetStatus(text string) { l.add("status: " + text) }

func (l *statusLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// frames lists what the status line was asked to show, in order.
func (l *statusLog) frames() []string {
	var out []string
	for _, event := range l.all() {
		if text, ok := strings.CutPrefix(event, "status: "); ok {
			out = append(out, text)
		}
	}
	return out
}

// last is what the status line shows now ("" while there is none).
func (l *statusLog) last() string {
	frames := l.frames()
	if len(frames) == 0 {
		return ""
	}
	return frames[len(frames)-1]
}

// newStatusRenderer returns a renderer whose waiting line is the status line above
// the input line (chat.steering with a colored terminal) and whose output and status
// go to one ordered log, driven by a fake clock.
func newStatusRenderer(t *testing.T, opts rendererOptions) (*Renderer, *statusLog, *spinnerClock) {
	t.Helper()
	log, clock := &statusLog{}, newSpinnerClock()
	opts.Out, opts.Status = log, log
	opts.spinnerNow, opts.spinnerTicks = clock.Now, clock.ticks
	return newRenderer(opts), log, clock
}

// waitStatusText waits until the status line says exactly want; the frame goroutine
// reacts to a tick asynchronously, so a test cannot assume the write happened on
// delivery.
func waitStatusText(t *testing.T, log *statusLog, want string) bool {
	t.Helper()
	return waitStatusContains(t, log, want) && log.last() == want
}

// waitStatusContains waits until the status line says something containing want.
func waitStatusContains(t *testing.T, log *statusLog, want string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(log.last(), want) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// assertFrame asserts that the status line ends in the label and the elapsed seconds,
// led by one of the animation characters of the line.
func assertFrame(t *testing.T, log *statusLog, label, seconds string) {
	t.Helper()
	got := log.last()
	assert.True(t, strings.HasSuffix(got, " "+label+" "+seconds), got)
	assert.True(t, got != "" && strings.ContainsRune(string(spinnerChars), []rune(got)[0]), got)
}

// TestStatusLineFollowsTheTurn: the line appears with the elapsed seconds while the
// model is asked for its next message, names the tool that runs right now (after the
// tool line itself was printed) and goes back to the model's own wording once the
// result is in. It ends when the turn does.
func TestStatusLineFollowsTheTurn(t *testing.T) {
	plainColor(t)
	r, log, clock := newStatusRenderer(t, enabledOptions())
	r.BeginTurn()
	assert.Empty(t, log.frames()) // nothing is shown before the first frame
	assert.Eq(t, spinnerSpeed, clock.frameInterval())

	clock.tick(time.Second)
	assert.Require(t, waitStatusText(t, log, string(spinnerChars[0])+" 思考中… 1s"))

	t.Run("a tool call names the tool, after its own line", func(t *testing.T) {
		before := len(log.all())
		r.Handle(personal.Event{Kind: "tool_call", ToolName: "shell_exec", Text: `{"command":"echo hi"}`})
		// The tool line first, then what the status line says: the output of the turn
		// goes ahead of the row the line editor draws it around.
		assert.Eq(t, `out: ⏺ shell_exec echo hi`+"\n", log.all()[before])
		assert.StrContains(t, log.last(), toolLabel("shell_exec"))

		// The elapsed seconds of the tool count from the tool call.
		clock.tick(2 * time.Second)
		assert.Require(t, waitStatusContains(t, log, toolLabel("shell_exec")+" 2s"))
		assertFrame(t, log, "执行 shell_exec…", "2s")
	})

	t.Run("the result brings the model's own wording back", func(t *testing.T) {
		before := len(log.all())
		r.Handle(personal.Event{Kind: "tool_result", ToolName: "shell_exec", Text: "hi"})
		assert.Eq(t, "out:   ⎿ ok: hi\n", log.all()[before])
		assert.StrContains(t, log.last(), spinnerLabel+" 0s")
	})

	t.Run("a sub-agent keeps the wording of a waiting turn", func(t *testing.T) {
		r.Handle(personal.Event{Kind: "tool_call", ToolName: "agent", CallID: "c1", Text: `{"description":"survey"}`})
		assert.StrContains(t, log.last(), toolLabel("agent"))
		r.Handle(personal.Event{Kind: "agent_progress", CallID: "c1", Agent: "survey", ToolName: "file_grep", Text: `{"pattern":"x"}`})
		assert.StrContains(t, log.last(), "子 agent：1 个运行中")
		assert.StrNotContains(t, log.last(), "file_grep")
	})

	t.Run("the end of the turn takes the line away", func(t *testing.T) {
		r.Status(personal.RunResult{}, time.Second, 0)
		assert.Eq(t, "", log.last())
		assert.False(t, spinnerRunning(r.spin))

		// A frame delivered late changes nothing.
		clock.tick(time.Second)
		time.Sleep(20 * time.Millisecond)
		assert.Eq(t, "", log.last())
	})
}

// TestStatusLineSuspendedForAQuestion: an approval question borrows the input line,
// so the status line above it goes away while the question is up (no frame of the
// running turn is drawn either) and comes back with what the turn waits for.
func TestStatusLineSuspendedForAQuestion(t *testing.T) {
	plainColor(t)
	r, log, clock := newStatusRenderer(t, enabledOptions())
	r.BeginTurn()
	clock.tick(time.Second)
	assert.Require(t, waitStatusText(t, log, string(spinnerChars[0])+" 思考中… 1s"))

	r.suspendStatus()
	assert.Eq(t, "", log.last())
	before := len(log.all())
	clock.tick(time.Second)
	time.Sleep(20 * time.Millisecond)
	assert.Eq(t, before, len(log.all())) // the frames of the turn draw nothing

	r.resumeStatus()
	assert.Require(t, waitStatusContains(t, log, spinnerLabel+" 2s"))
	clock.tick(time.Second)
	assert.Require(t, waitStatusContains(t, log, spinnerLabel+" 3s"))

	// Leaving chat (or any other write) ends the line for good.
	r.endThinking()
	assert.Eq(t, "", log.last())
	assert.False(t, spinnerRunning(r.spin))
}

// TestStatusLineTurnEndOrder: the end of a turn takes the status line down before it
// prints the turn's own lines, so the block the line editor keeps on screen is out of
// the way of the output that follows.
func TestStatusLineTurnEndOrder(t *testing.T) {
	plainColor(t)
	r, log, clock := newStatusRenderer(t, enabledOptions())
	r.BeginTurn()
	clock.tick(time.Second)
	assert.Require(t, waitStatusText(t, log, string(spinnerChars[0])+" 思考中… 1s"))

	before := len(log.all())
	r.Finish()
	r.Status(personal.RunResult{}, time.Second, 0)
	events := log.all()[before:]
	assert.True(t, len(events) > 1)
	assert.Eq(t, "status: ", events[0])
	assert.True(t, strings.HasPrefix(events[1], "out: "), events[1])
	assert.StrContains(t, strings.Join(events, ""), "tokens")
}
