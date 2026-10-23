package chat

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/color"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// spinnerClock is the test clock of the waiting line: Now reports the fake time,
// ticks hands the spinner the channel the test sends frames on, and tick moves
// the clock forward and delivers one frame.
type spinnerClock struct {
	mu     sync.Mutex
	now    time.Time
	frames chan time.Time
	speed  time.Duration // the frame interval Start asked for (0: never started)
}

func newSpinnerClock() *spinnerClock {
	return &spinnerClock{now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), frames: make(chan time.Time, 16)}
}

func (c *spinnerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *spinnerClock) ticks(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.speed = d
	return c.frames, func() {}
}

// tick moves the fake clock d forward and delivers one frame to the spinner.
func (c *spinnerClock) tick(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	c.frames <- now
}

func (c *spinnerClock) frameInterval() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.speed
}

// newSpinnerRenderer returns a renderer writing to a goroutine-safe buffer whose
// waiting line is driven by a fake clock, plus both of them. The caller sets the
// Spinner and Color options (enabledOptions is the default of a colored
// terminal).
func newSpinnerRenderer(t *testing.T, opts rendererOptions) (*Renderer, *syncOutput, *spinnerClock) {
	t.Helper()
	out, clock := &syncOutput{}, newSpinnerClock()
	opts.Out, opts.spinnerNow, opts.spinnerTicks = out, clock.Now, clock.ticks
	return newRenderer(opts), out, clock
}

// enabledOptions is a colored terminal with the spinner on (the config default
// of chat.spinner).
func enabledOptions() rendererOptions {
	return rendererOptions{Spinner: true, Color: true}
}

// plainColor turns the color library off: an assertion on exact text must not
// depend on the environment's color support (opts.Color paints through
// gookit/color).
func plainColor(t *testing.T) {
	t.Helper()
	enabled := color.Disable()
	t.Cleanup(func() { color.Enable = enabled })
}

// waitOutput waits until out holds want; the frame goroutine reacts to a tick
// asynchronously, so a test cannot assume the write happened on delivery.
func waitOutput(t *testing.T, out *syncOutput, want string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// spinnerRunning reports whether the frame goroutine is alive: a stopped spinner
// has closed its done channel, one that never started has none.
func spinnerRunning(s *thinkingSpinner) bool {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// TestSpinnerWaitingLine: the line appears with the elapsed seconds once a frame
// is due, and every further frame rewrites it in place.
func TestSpinnerWaitingLine(t *testing.T) {
	plainColor(t)
	r, out, clock := newSpinnerRenderer(t, enabledOptions())
	r.BeginTurn()
	assert.Eq(t, "", out.String()) // nothing is printed before the first frame
	assert.Eq(t, spinnerSpeed, clock.frameInterval())

	clock.tick(3 * time.Second)
	assert.Require(t, waitOutput(t, out, spinnerLabel))
	assert.Eq(t, spinnerClear+string(spinnerChars[0])+" 思考中… 3s", out.String())

	clock.tick(time.Second)
	assert.Require(t, waitOutput(t, out, "思考中… 4s"))
	assert.Eq(t, 2, strings.Count(out.String(), spinnerLabel))
	assert.Eq(t, 2, strings.Count(out.String(), spinnerClear))

	r.Finish()
	assert.False(t, spinnerRunning(r.spin))
}

// TestSpinnerStopsOnTextDelta: the model's first text delta erases the line, so
// the answer starts the line over and no hint is left on screen.
func TestSpinnerStopsOnTextDelta(t *testing.T) {
	plainColor(t)
	r, out, clock := newSpinnerRenderer(t, enabledOptions())
	r.BeginTurn()
	clock.tick(3 * time.Second)
	assert.Require(t, waitOutput(t, out, spinnerLabel))

	r.Handle(personal.Event{Kind: "text_delta", Text: "hi"})
	got := out.String()
	answer := strings.Index(got, "▣ ")
	assert.Require(t, answer > 0)
	assert.True(t, strings.HasSuffix(got[:answer], spinnerClear))
	assert.StrNotContains(t, got[answer:], spinnerLabel)
	assert.StrContains(t, got[answer:], "▣ hi")
	assert.False(t, spinnerRunning(r.spin))

	t.Run("a late frame is dropped", func(t *testing.T) {
		clock.tick(time.Second)
		time.Sleep(20 * time.Millisecond)
		assert.Eq(t, got, out.String())
	})
}

// TestSpinnerRestartsAfterToolResult: the tool line replaces the waiting line
// (the model waits for the tool, it does not think) and the result starts it
// again.
func TestSpinnerRestartsAfterToolResult(t *testing.T) {
	plainColor(t)
	r, out, clock := newSpinnerRenderer(t, enabledOptions())
	r.BeginTurn()
	clock.tick(3 * time.Second)
	assert.Require(t, waitOutput(t, out, "思考中… 3s"))

	r.Handle(personal.Event{Kind: "tool_call", ToolName: "shell_exec", Text: `{"command":"echo hi"}`})
	assert.False(t, spinnerRunning(r.spin))
	r.Handle(personal.Event{Kind: "tool_result", ToolName: "shell_exec", Text: "hi"})
	assert.True(t, spinnerRunning(r.spin))

	clock.tick(2 * time.Second)
	assert.Require(t, waitOutput(t, out, "思考中… 2s"))

	got := out.String()
	result := strings.Index(got, "⎿ ok: hi")
	assert.Require(t, result > 0)
	assert.Eq(t, 2, strings.Count(got, spinnerLabel))
	assert.StrContains(t, got[result:], "思考中… 2s")
}

// TestSpinnerAgentProgress: a sub-agent's progress line ends the waiting line
// and the model is waited for again below it.
func TestSpinnerAgentProgress(t *testing.T) {
	plainColor(t)
	r, out, clock := newSpinnerRenderer(t, enabledOptions())
	r.opts.AgentProgress = "full"
	r.BeginTurn()
	clock.tick(3 * time.Second)
	assert.Require(t, waitOutput(t, out, spinnerLabel))

	r.Handle(personal.Event{Kind: "tool_call", ToolName: "agent", CallID: "c1", Text: `{"description":"survey"}`})
	r.Handle(personal.Event{Kind: "agent_progress", CallID: "c1", Agent: "survey", ToolName: "file_grep", Text: `{"pattern":"x"}`})
	assert.True(t, spinnerRunning(r.spin))

	clock.tick(2 * time.Second)
	assert.Require(t, waitOutput(t, out, "思考中… 2s"))
	assert.StrContains(t, out.String(), "  · file_grep")
}

// TestSpinnerPlanAndRetry: a plan block and a retry notice come out complete,
// and the model is waited for again after each of them.
func TestSpinnerPlanAndRetry(t *testing.T) {
	t.Run("plan", func(t *testing.T) {
		plainColor(t)
		r, out, clock := newSpinnerRenderer(t, enabledOptions())
		r.BeginTurn()
		clock.tick(time.Second)
		assert.Require(t, waitOutput(t, out, spinnerLabel))

		r.Handle(personal.Event{Kind: "plan", Plan: &native.Plan{
			Steps: []native.PlanStep{{Step: "read", Status: "in_progress"}},
		}})
		assert.StrContains(t, out.String(), "⏺ plan")
		assert.True(t, spinnerRunning(r.spin))
	})
	t.Run("retry", func(t *testing.T) {
		plainColor(t)
		r, out, clock := newSpinnerRenderer(t, enabledOptions())
		r.BeginTurn()
		clock.tick(time.Second)
		assert.Require(t, waitOutput(t, out, spinnerLabel))

		r.Handle(personal.Event{Kind: string(agentcore.StreamEventRetry), Text: "HTTP 503"})
		assert.StrContains(t, out.String(), "↻ 模型请求失败（HTTP 503）")
		assert.True(t, spinnerRunning(r.spin))
	})
	t.Run("other events leave the line alone", func(t *testing.T) {
		// run_started, usage and friends are emitted while the model answers:
		// they must not erase the line.
		plainColor(t)
		r, out, clock := newSpinnerRenderer(t, enabledOptions())
		r.BeginTurn()
		clock.tick(2 * time.Second)
		assert.Require(t, waitOutput(t, out, "思考中… 2s"))

		r.Handle(personal.Event{Kind: "run_started"})
		r.Handle(personal.Event{Kind: "usage"})
		assert.True(t, spinnerRunning(r.spin))
		assert.StrContains(t, out.String(), "思考中… 2s")
	})
}

// TestSpinnerTurnEnd: the status line of a finished turn prints on a clean line
// and nothing of the spinner lives on.
func TestSpinnerTurnEnd(t *testing.T) {
	plainColor(t)
	r, out, clock := newSpinnerRenderer(t, enabledOptions())
	r.BeginTurn()
	clock.tick(2 * time.Second)
	assert.Require(t, waitOutput(t, out, spinnerLabel))

	r.Finish()
	assert.False(t, spinnerRunning(r.spin))
	assert.True(t, strings.HasSuffix(out.String(), spinnerClear))

	r.Status(personal.RunResult{}, time.Second, 0)
	clock.tick(time.Second) // a frame delivered late changes nothing
	time.Sleep(20 * time.Millisecond)
	assert.False(t, spinnerRunning(r.spin))
	assert.Eq(t, 1, strings.Count(out.String(), spinnerLabel))
	assert.StrContains(t, out.String(), "in 0 / out 0 tokens")
}

// TestSpinnerOff: with chat.spinner: false, or without a colored terminal (a pipe,
// a redirected output, NO_COLOR), no frame is ever written - neither on a line of
// its own nor as the status line above the input line (chat.steering).
func TestSpinnerOff(t *testing.T) {
	cases := []struct {
		name string
		opts rendererOptions
	}{
		{"chat.spinner off", rendererOptions{Spinner: false, Color: true}},
		{"no colored terminal", rendererOptions{Spinner: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plainColor(t)
			// The same options chat uses while it keeps an input line on screen: the
			// line would go to the status setter, and neither of them may be used.
			tc.opts.InputLineUp = true
			tc.opts.Status = &statusLog{}
			r, out, clock := newSpinnerRenderer(t, tc.opts)
			status := tc.opts.Status.(*statusLog)
			r.BeginTurn()
			r.Handle(personal.Event{Kind: "tool_result", ToolName: "shell_exec", Text: "hi"})
			r.Finish()
			clock.tick(time.Second)
			time.Sleep(20 * time.Millisecond)

			assert.Eq(t, "  ⎿ ok: hi\n", out.String())
			assert.Empty(t, status.frames())
			assert.False(t, spinnerRunning(r.spin))
			// The frame timer was never asked for.
			assert.Eq(t, time.Duration(0), clock.frameInterval())
		})
	}
}

// TestSpinnerRealClock: with no injected clock (the real time.Ticker behind
// chat) the line still appears within its frame interval and is erased on the
// first text delta.
func TestSpinnerRealClock(t *testing.T) {
	plainColor(t)
	out := &syncOutput{}
	r := newRenderer(rendererOptions{Out: out, Spinner: true, Color: true})
	r.BeginTurn()
	assert.Require(t, waitOutput(t, out, spinnerLabel))
	// The frame ends with the elapsed seconds ("… 0s", or more on a slow machine).
	hint := out.String()[strings.Index(out.String(), spinnerLabel)+len(spinnerLabel):]
	assert.True(t, len(hint) > 1 && hint[0] == ' ' && hint[1] >= '0' && hint[1] <= '9')

	r.Handle(personal.Event{Kind: "text_delta", Text: "hi"})
	assert.False(t, spinnerRunning(r.spin))
	got := out.String()
	answer := strings.Index(got, "▣ ")
	assert.Require(t, answer > 0)
	assert.True(t, strings.HasSuffix(got[:answer], spinnerClear))
	r.Finish()
	assert.False(t, spinnerRunning(r.spin))
}

// TestSpinnerPaintsTheLine: on a colored terminal the frame is dimmed (gray,
// like the tool lines) as one piece.
func TestSpinnerPaintsTheLine(t *testing.T) {
	color.ForceColor()
	t.Cleanup(color.RevertColorLevel)

	out, clock := &syncOutput{}, newSpinnerClock()
	s := newThinkingSpinner(spinnerConfig{
		enabled: true, out: out,
		paint: func(line string) string { return color.Gray.Text(line) },
		now:   clock.Now, ticks: clock.ticks,
	})
	s.Start()
	clock.tick(time.Second)
	assert.Require(t, waitOutput(t, out, spinnerLabel))
	s.Stop()

	assert.StrContains(t, out.String(), "\x1b[90m"+string(spinnerChars[0])+" 思考中… 1s\x1b[0m")
	assert.True(t, strings.HasSuffix(out.String(), spinnerClear))
}
