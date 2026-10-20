package chat

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/chat/lineedit"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// gateProv holds every model request until the test releases it and records the
// last user message of each prompt: how a test types into a turn that is running.
type gateProv struct {
	begin   chan struct{}
	release chan struct{}
	once    sync.Once
	// ignoreCancel keeps holding a request even after its context was cancelled,
	// so a test can press keys while the turn is ending.
	ignoreCancel bool

	mu    sync.Mutex
	users []string
}

func newGateProv() *gateProv {
	return &gateProv{begin: make(chan struct{}, 8), release: make(chan struct{})}
}

func (p *gateProv) Name() string { return "gate" }

// started reports one request that began (buffered: later calls do not block).
func (p *gateProv) started() <-chan struct{} { return p.begin }

func (p *gateProv) Generate(ctx context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	last := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			last = m.Content
		}
	}
	p.mu.Lock()
	p.users = append(p.users, last)
	p.mu.Unlock()
	select {
	case p.begin <- struct{}{}:
	default:
	}
	if p.ignoreCancel {
		<-p.release
		return &agentcore.GenerateResult{Content: "回答", FinishReason: agentcore.FinishFinal}, nil
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &agentcore.GenerateResult{Content: "回答", FinishReason: agentcore.FinishFinal}, nil
}

// prompts is the last user message of every request seen so far.
func (p *gateProv) prompts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.users...)
}

// letGo releases the requests, now and later.
func (p *gateProv) letGo() { p.once.Do(func() { close(p.release) }) }

// heldProvider is a provider a test can hold, so it can type into the turn that
// is running (gateProv, and the failing variant built on it).
type heldProvider interface {
	agentcore.Provider
	started() <-chan struct{}
	letGo()
	prompts() []string
}

// steerHarness drives one chatState turn on the ask harness's fake terminal, so a
// test can type into a running turn.
type steerHarness struct {
	*askHarness
	state  *chatState
	cast   heldProvider
	repo   *store.Store
	sessID string
}

func newSteerHarness(t *testing.T, provider heldProvider) *steerHarness {
	t.Helper()
	h := newAskHarness(t)
	h.source.SetSteering(true)
	runner, repo := newCLITestRunner(t, provider)
	model, err := runner.NewSession(context.Background(), "", "steer test")
	assert.Require(t, assert.NoErr(t, err))
	render := newRenderer(rendererOptions{Out: h.out, Err: h.out})
	state := &chatState{
		ctx: context.Background(), runner: runner, source: h.source, render: render,
		stderr: h.out, sessionID: model.ID, stats: &chatStats{},
		idle:     newIdleConsolidation(runner, model.ID, 0, 0, render, nil, nil),
		steering: true,
	}
	t.Cleanup(func() { state.idle.Close() })
	return &steerHarness{askHarness: h, state: state, cast: provider, repo: repo, sessID: model.ID}
}

// turnInputLine is what the input line holds (the editor's live line, or the
// pending one of the coming read).
func (h *steerHarness) turnInputLine(t *testing.T) string {
	t.Helper()
	line, _ := h.source.terminal.TakeLine()
	return line
}

// turnInjected reports the injected flag the runner recorded for a turn. The flag
// is only written when it is true, so a missing one reads false.
func (h *steerHarness) turnInjected(t *testing.T, index int) bool {
	t.Helper()
	events, err := h.repo.ListEvents(context.Background(), store.EventFilter{
		SessionID: h.sessID, Kinds: []string{store.EventTurn},
	})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.True(t, len(events) > index))
	v, _ := events[index].Attrs["injected"].(bool)
	return v
}

// startTurn runs one turn in the background and returns its result channel.
func (h *steerHarness) startTurn() <-chan error {
	done := make(chan error, 1)
	go func() { done <- h.state.runTurn("干活", false, nil) }()
	return done
}

// TestRunTurnSteerBecomesTheNextTurn: a message a person types into a running turn
// goes to the running turn (Runner.Steer); when the turn answers without carrying
// it, it runs at once as the next turn (design D3), in the person's own words and
// as the person's own input (not injected).
func TestRunTurnSteerBecomesTheNextTurn(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	done := h.startTurn()
	<-h.cast.started()

	h.kb.Type("只改 README\r")
	waitUntil(t, "the inserted message is shown", func() bool {
		return strings.Contains(h.out.String(), "[插话] 只改 README（等待送达）")
	})
	h.cast.letGo()

	assert.NoErr(t, <-done)
	assert.Eq(t, []string{"干活", "只改 README"}, h.cast.prompts())
	// The message ran as the next turn, so it is not left in the input line.
	assert.Eq(t, "", h.turnInputLine(t))
	// The person typed it: that turn is not an injected one.
	assert.False(t, h.turnInjected(t, 1))
}

// TestRunTurnAbortEndsThisTurnOnly: a single Ctrl-C (or Esc) at a turn whose input
// line is empty cancels that turn's context; chat goes on, the turn reads 已中断本轮
// and every message inserted into it goes back to the input line instead of running
// (design D4). A Ctrl-C with text on the line is not an abort any more - it drops
// that line and the turn goes on (wll1, TestChatInputSource_CtrlCClearsTurnLine) -
// so the ctrl-c case presses it a second time on the empty line the first left.
func TestRunTurnAbortEndsThisTurnOnly(t *testing.T) {
	// start runs one turn with a message inserted into it and returns the harness.
	start := func(t *testing.T) (*steerHarness, <-chan error) {
		t.Helper()
		h := newSteerHarness(t, newGateProv())
		// A lone Esc needs the platform wait to answer "nothing followed it";
		// the harness has no terminal behind it, so the test supplies it (input_pump.go).
		h.source.inputWait = func(time.Duration) bool { return false }
		done := h.startTurn()
		<-h.cast.started()

		h.kb.Type("先改 README\r")
		waitUntil(t, "the inserted message is shown", func() bool {
			return strings.Contains(h.out.String(), "[插话] 先改 README（等待送达）")
		})
		return h, done
	}

	t.Run("esc ends this turn only and the half line stays", func(t *testing.T) {
		h, done := start(t)
		h.kb.Type("半行") // half typed when the turn ends: it stays in the line
		waitUntil(t, "the half line is echoed", func() bool {
			return strings.Contains(h.out.String(), "半行")
		})
		h.kb.Type("\x1b")

		// The abort ends this turn, not the chat: runTurn reports no error.
		assert.NoErr(t, <-done)
		got := h.out.String()
		assert.StrContains(t, got, "已中断本轮，1 条插话已放回输入行")
		assert.StrNotContains(t, got, "context canceled")
		// The message waits in the input line, in front of the half-typed one.
		assert.Eq(t, "先改 README\n半行", h.turnInputLine(t))
		// Nothing was run: the turn never reached a second model request.
		assert.Eq(t, []string{"干活"}, h.cast.prompts())
	})

	t.Run("ctrl-c ends this turn once the line is empty", func(t *testing.T) {
		h, done := start(t)
		h.kb.Type("半行")
		waitUntil(t, "the half line is echoed", func() bool {
			return strings.Contains(h.out.String(), "半行")
		})
		// A non-empty line is what Ctrl-C drops: it goes away, the turn goes on, and
		// the press is not the first of the two that leave chat (wll1): the second
		// press below has to end this turn only, not the chat.
		h.kb.Type("\x03")
		waitKbIdle(t, h.kb)
		assert.Eq(t, "", h.turnInputLine(t))
		h.kb.Type("\x03")

		assert.NoErr(t, <-done)
		got := h.out.String()
		assert.StrContains(t, got, "已中断本轮，1 条插话已放回输入行")
		assert.StrNotContains(t, got, "context canceled")
		// The half-typed line was dropped by the first press; the inserted message
		// still came back.
		assert.Eq(t, "先改 README", h.turnInputLine(t))
		assert.Eq(t, []string{"干活"}, h.cast.prompts())
	})
}

// TestRunTurnSecondCtrlCLeavesChat: a second Ctrl-C during the turn (the same read
// still open) asks to leave chat: the turn ends and chat is told to stop, while the
// provider ignores the cancellation so both presses land inside the turn.
func TestRunTurnSecondCtrlCLeavesChat(t *testing.T) {
	h := newSteerHarness(t, &gateProv{begin: make(chan struct{}, 8), release: make(chan struct{}), ignoreCancel: true})
	done := h.startTurn()
	<-h.cast.started()

	h.kb.Type("\x03")
	time.Sleep(50 * time.Millisecond)
	h.kb.Type("\x03") // the clock is the input source's: within exitConfirmWindow
	time.Sleep(100 * time.Millisecond)
	h.cast.letGo()

	assert.ErrIs(t, <-done, errExitChat)
}

// TestRunTurnAbortLeavesTheClockForThePrompt: the Ctrl-C that ended a turn counts as
// the first of the two (m7), so the next one at the prompt is the confirmation that
// leaves chat - the two presses do not have to fall inside the turn.
func TestRunTurnAbortLeavesTheClockForThePrompt(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	done := h.startTurn()
	<-h.cast.started()
	h.kb.Type("\x03")
	assert.NoErr(t, <-done) // the turn ended; chat goes on
	h.cast.letGo()

	// Back at the prompt (what Run's loop asks for next).
	assert.True(t, h.source.Request("> "))
	h.kb.Type("\x03")
	select {
	case event := <-h.source.Next():
		assert.True(t, event.activity)
		h.source.AckActivity()
	case <-time.After(3 * time.Second):
		t.Fatal("no activity event")
	}
	var event chatInputEvent
	select {
	case event = <-h.source.Next():
	case <-time.After(3 * time.Second):
		t.Fatal("no prompt event")
	}
	assert.ErrIs(t, event.err, lineedit.ErrInterrupt)
	// What Run's loop checks: a second press within the window leaves chat.
	assert.True(t, h.source.takeInterrupt())
}

// TestTurnInputStaleKeys: the keys of an older steer read (an Esc the pump decided
// just as the previous turn ended) must not end the next turn; a line that late is
// still a message the person typed.
func TestTurnInputStaleKeys(t *testing.T) {
	newInput := func(t *testing.T, gen uint64) (*turnInput, chan struct{}, *steerHarness) {
		t.Helper()
		h := newSteerHarness(t, newGateProv())
		canceled := make(chan struct{})
		turn := &turnInput{state: h.state, gen: gen, cancel: func() { close(canceled) },
			stop: make(chan struct{}), done: make(chan struct{})}
		return turn, canceled, h
	}

	t.Run("a key of an older read is dropped", func(t *testing.T) {
		turn, canceled, _ := newInput(t, 2)
		turn.handle(chatInputEvent{key: keyExit, gen: 1})
		select {
		case <-canceled:
			t.Fatal("a stale key ended the turn")
		default:
		}
		assert.False(t, turn.aborted)
	})

	t.Run("a line of an older read is still inserted", func(t *testing.T) {
		turn, _, h := newInput(t, 2)
		turn.handle(chatInputEvent{line: "晚到的一句", gen: 1})
		waitUntil(t, "the late line is shown", func() bool {
			return strings.Contains(h.out.String(), "[插话] 晚到的一句")
		})
		assert.Eq(t, 1, len(turn.steers))
		assert.False(t, turn.steers[0].accepted) // no turn is running: it waits for the next input
	})

	t.Run("its own key ends the turn", func(t *testing.T) {
		turn, canceled, _ := newInput(t, 2)
		turn.handle(chatInputEvent{key: keyAbort, gen: 2})
		assert.True(t, turn.aborted)
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("the turn was not cancelled")
		}
	})
}

// TestSteerReturns: what a finished turn hands to chat (design D4) - the runner's
// returned messages first, then the ones the turn's input could not insert, each
// with its sender; a message from elsewhere keeps its label in a prompt and makes
// a turn injected only when nothing was typed here (design m1).
func TestSteerReturns(t *testing.T) {
	keyboard := personal.Steer{ID: "k1", Text: "只改 README", Source: personal.SteerSourceKeyboard}
	fromElsewhere := personal.Steer{ID: "k2", Text: "还有一句", Source: "send:gofer"}

	t.Run("nothing to settle", func(t *testing.T) {
		assert.Empty(t, steerReturns(nil, nil))
	})

	t.Run("returned first, then the unaccepted ones", func(t *testing.T) {
		got := steerReturns([]personal.Steer{keyboard}, []personal.Steer{fromElsewhere})
		assert.Eq(t, 2, len(got))
		assert.Eq(t, "只改 README", got[0].text)
		assert.Eq(t, "", got[0].sender)
		assert.Eq(t, "还有一句", got[1].text)
		assert.Eq(t, "gofer", got[1].sender)
	})

	t.Run("a message from elsewhere keeps its label in a prompt", func(t *testing.T) {
		assert.Eq(t, []string{"只改 README", "[来自 gofer] 还有一句"},
			steerLines(steerReturns([]personal.Steer{keyboard, fromElsewhere}, nil)))
	})

	t.Run("injected unless the person typed one", func(t *testing.T) {
		// Nothing typed here: the turn that runs on those is an injected one.
		assert.False(t, typedHere(steerReturns([]personal.Steer{fromElsewhere}, nil)))
		assert.True(t, typedHere(steerReturns([]personal.Steer{keyboard, fromElsewhere}, nil)))
	})

	t.Run("the notice names the count", func(t *testing.T) {
		assert.Eq(t, "", steerBackNotice(0))
		assert.Eq(t, "，2 条插话已放回输入行", steerBackNotice(2))
	})
}

// TestRunTurnUpTakesWaitingMessagesBack: Up on the empty input line of a running
// turn takes the messages still waiting to be delivered back, in order, onto the
// input line for editing; a line that already holds text keeps Up's usual meaning
// (history), and nothing waiting means Up does nothing.
func TestRunTurnUpTakesWaitingMessagesBack(t *testing.T) {
	// What a terminal sends for the Up key (lineedit.keyUp).
	const up = "\x1b[A"

	t.Run("an empty line takes them all back, in order", func(t *testing.T) {
		h := newSteerHarness(t, newGateProv())
		done := h.startTurn()
		<-h.cast.started()

		h.kb.Type("只改 README\r")
		waitUntil(t, "the first message is shown as waiting", func() bool {
			return strings.Contains(h.out.String(), "[插话] 只改 README（等待送达）")
		})
		h.kb.Type("再加一句\r")
		waitUntil(t, "the second message is shown as waiting", func() bool {
			return strings.Contains(h.out.String(), "[插话] 再加一句（等待送达）")
		})

		h.kb.Type(up)
		waitUntil(t, "both are shown as withdrawn", func() bool {
			got := h.out.String()
			return strings.Contains(got, "[插话] 只改 README（已撤回）") &&
				strings.Contains(got, "[插话] 再加一句（已撤回）")
		})
		// The lines say so in the order the messages were typed.
		got := h.out.String()
		assert.True(t, strings.Index(got, "[插话] 只改 README（已撤回）") <
			strings.Index(got, "[插话] 再加一句（已撤回）"))
		// Both texts go back onto the input line, one per line, in the same order (a
		// line break of the editor is a CR LF: nothing else writes one here).
		waitUntil(t, "both texts are on the input line", func() bool {
			return strings.Contains(h.out.String(), "\r\n再加一句")
		})
		assert.Eq(t, "只改 README\n再加一句", h.turnInputLine(t))
		// Nothing is left waiting: a request of this turn cannot carry them any more.
		assert.Empty(t, h.state.runner.WithdrawSteers(h.sessID, nil))

		h.cast.letGo()
		assert.NoErr(t, <-done)
		// The turn runs on its own prompt only: the messages were taken back, so
		// they are neither a turn's request nor a turn of their own.
		assert.Eq(t, []string{"干活"}, h.cast.prompts())
		// The status line counts delivered messages only.
		assert.StrNotContains(t, h.out.String(), "插话 1")
	})

	t.Run("the withdrawn text can be edited and submitted again", func(t *testing.T) {
		h := newSteerHarness(t, newGateProv())
		done := h.startTurn()
		<-h.cast.started()

		h.kb.Type("只改 README\r再加一句\r")
		waitUntil(t, "both messages are shown as waiting", func() bool {
			return strings.Contains(h.out.String(), "[插话] 再加一句（等待送达）")
		})
		h.kb.Type(up)
		waitUntil(t, "both are taken back", func() bool {
			return strings.Contains(h.out.String(), "[插话] 只改 README（已撤回）")
		})

		// The cursor is at the end of the line: typing goes on, and Enter submits
		// the whole (edited) text as one message again.
		h.kb.Type("补充\r")
		waitUntil(t, "the edited text is inserted again", func() bool {
			return strings.Contains(h.out.String(), "[插话] 只改 README 再加一句补充（等待送达）")
		})

		h.cast.letGo()
		assert.NoErr(t, <-done)
		// This turn never carried it, so it runs as the next turn (the person typed
		// it, design D3) - with the edit in place.
		assert.Eq(t, []string{"干活", "只改 README\n再加一句补充"}, h.cast.prompts())
	})

	t.Run("a non-empty line keeps Up's history behaviour", func(t *testing.T) {
		h := newSteerHarness(t, newGateProv())
		// One line of history, typed at the prompt before the turn.
		assert.True(t, h.source.Request("> "))
		h.kb.Type("历史行\r")
		h.nextLine(t)

		done := h.startTurn()
		<-h.cast.started()
		h.kb.Type("插话一句\r")
		waitUntil(t, "the message is shown as waiting", func() bool {
			return strings.Contains(h.out.String(), "[插话] 插话一句（等待送达）")
		})
		h.kb.Type("半行")
		waitUntil(t, "the half line is echoed", func() bool {
			return strings.Contains(h.out.String(), "半行")
		})

		// The line holds text, so Up walks the history: the line it submitted into
		// the turn is the most recent entry, the one before it is the prompt line.
		h.kb.Type(up)
		h.kb.Type(up)
		waitUntil(t, "Up walks the history instead of filling the line", func() bool {
			got := h.out.String()
			return strings.LastIndex(got, "历史行") > strings.LastIndex(got, "半行")
		})
		assert.StrNotContains(t, h.out.String(), "已撤回")
		assert.Eq(t, "历史行", h.turnInputLine(t))

		h.cast.letGo()
		assert.NoErr(t, <-done)
		// Nothing was taken back: the completed turn hands the message to the next
		// turn, which is what a message it did not carry does.
		assert.Eq(t, []string{"干活", "插话一句"}, h.cast.prompts())
	})

	t.Run("nothing waiting: Up does nothing", func(t *testing.T) {
		h := newSteerHarness(t, newGateProv())
		done := h.startTurn()
		<-h.cast.started()

		h.kb.Type(up)
		time.Sleep(100 * time.Millisecond)
		assert.StrNotContains(t, h.out.String(), "已撤回")
		// Nothing was filled in either: the line is still empty (with no history to
		// walk, Up leaves it as it is).
		assert.Eq(t, "", h.turnInputLine(t))

		h.cast.letGo()
		assert.NoErr(t, <-done)
		assert.Eq(t, []string{"干活"}, h.cast.prompts())
	})
}

// TestPutSteersBackKeepsTheHalfLine: messages that could not be delivered go back
// into the input line, one per line and in front of what is half typed; nothing is
// submitted.
func TestPutSteersBackKeepsTheHalfLine(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	// A prompt read is what the put-back goes into (the chat is between turns).
	assert.True(t, h.source.Request("> "))
	h.typeAtPrompt(t, "半行")

	h.state.putSteersBack(steerReturns(
		[]personal.Steer{{ID: "k1", Text: "键盘说的", Source: personal.SteerSourceKeyboard},
			{ID: "k2", Text: "别处说的", Source: "send:cli"}}, nil))
	assert.Eq(t, "键盘说的\n[来自 cli] 别处说的\n半行", h.turnInputLine(t))
}

// TestRendererSteerNotices: the three states a message inserted into a turn is
// shown in, and the delivered one carrying the person's own input-line background
// when that style is on.
func TestRendererSteerNotices(t *testing.T) {
	newR := func(highlight bool) (*Renderer, *syncOutput) {
		out := &syncOutput{}
		// Color is what highlightInput needs (a colored terminal); it stays off in
		// the plain subtests.
		return newRenderer(rendererOptions{Out: out, Color: highlight, HighlightInput: highlight,
			HighlightWidth: func() int { return lineedit.DefaultWidth }}), out
	}

	t.Run("waiting, late, withdrawn and delivered", func(t *testing.T) {
		r, out := newR(false)
		r.SteerWaiting("只改 README")
		r.SteerLate("不算了")
		r.SteerWithdrawn("只改 README")
		r.SteerDelivered("只改 README")
		assert.Eq(t, "[插话] 只改 README（等待送达）\n"+
			"[插话] 不算了（本轮未接收，稍后处理）\n"+
			"[插话] 只改 README（已撤回）\n"+
			"[插话] 只改 README（已送达）\n", out.String())
	})

	t.Run("delivered carries the input line background", func(t *testing.T) {
		r, out := newR(true)
		r.SteerDelivered("只改 README")
		line := strings.TrimSuffix(out.String(), "\n")
		assert.True(t, strings.HasPrefix(line, inputHighlightPrefix), "line = %q", line)
		assert.True(t, strings.HasSuffix(line, highlightSuffix), "line = %q", line)
		assert.StrContains(t, line, "[插话] 只改 README（已送达）")
	})

	t.Run("a multi-line message is shown as one line", func(t *testing.T) {
		r, out := newR(false)
		r.SteerWaiting("第一行\n第二行")
		assert.Eq(t, "[插话] 第一行 第二行（等待送达）\n", out.String())
	})
}

// TestTurnInputEndStopsTheConsumer: the turn's consumer stops with the turn and
// reports what it did; a message it could not insert is an unaccepted one, and the
// message itself waits in the input line.
func TestTurnInputEndStopsTheConsumer(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	canceled := make(chan struct{})
	turn := &turnInput{state: h.state, gen: 1, cancel: func() { close(canceled) },
		stop: make(chan struct{}), done: make(chan struct{})}
	go turn.consume()
	turn.insert("插一句")
	waitUntil(t, "the message is shown", func() bool {
		return strings.Contains(h.out.String(), "[插话] 插一句")
	})
	end := turn.end()
	assert.Eq(t, 1, len(end.steers))
	assert.Eq(t, 1, len(end.unaccepted()))
	assert.False(t, end.aborted)
	assert.False(t, end.exited)
	select {
	case <-canceled:
		t.Fatal("ending the turn cancelled its context")
	default:
	}
}

// TestTurnInputStopsReadingWithTheTurn: after the turn input ended, the events of
// its read stay for the next turn's consumer (its keys are dropped there by their
// generation) instead of blocking the reader.
func TestTurnInputStopsReadingWithTheTurn(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	_, ok := h.source.RequestSteer(steerPrompt)
	assert.True(t, ok)
	turn := &turnInput{state: h.state, gen: h.source.steerGenNow(), cancel: func() {},
		stop: make(chan struct{}), done: make(chan struct{})}
	go turn.consume()
	turn.end()

	// A message typed now is read by the source and has to wait in the channel for
	// the next consumer, not wedge the reader.
	h.kb.Type("下一回合的话\r")
	waitUntil(t, "the reader is not blocked", func() bool { return len(h.source.TurnEvents()) == 1 })
}

// TestRunTurnPutsBackMessagesOfAFailedTurn: a turn that fails without being
// cancelled hands its inserted messages back to the input line too.
func TestRunTurnPutsBackMessagesOfAFailedTurn(t *testing.T) {
	h := newSteerHarness(t, &gateErrorProv{gateProv: newGateProv()})
	done := h.startTurn()
	<-h.cast.started()

	h.kb.Type("别改了\r")
	waitUntil(t, "the inserted message is shown", func() bool {
		return strings.Contains(h.out.String(), "[插话] 别改了（等待送达）")
	})
	h.cast.letGo()

	assert.NoErr(t, <-done)
	assert.StrContains(t, h.out.String(), "1 条插话已放回输入行")
	assert.Eq(t, "别改了", h.turnInputLine(t))
}

// TestRunTurnCancellationIsRecognised: a cancelled turn is reported as 已中断本轮
// even when the runner's error only hides the cancellation (the ctx itself was
// cancelled), and never with a raw error.
func TestRunTurnCancellationIsRecognised(t *testing.T) {
	provider := &oddCancelProv{began: make(chan struct{}, 1)}
	h := newSteerHarness(t, provider)
	done := h.startTurn()
	<-provider.began
	h.kb.Type("\x03")

	assert.NoErr(t, <-done)
	assert.StrContains(t, h.out.String(), "已中断本轮")
	assert.StrNotContains(t, h.out.String(), "context canceled")
}

// TestTurnInputNil: a chat without steering (no terminal, or chat.steering off)
// has no turn input at all, and every use of it is a no-op.
func TestTurnInputNil(t *testing.T) {
	var turn *turnInput
	turn.deliver(personal.Event{Kind: "steer_delivered", SteerID: "k1"})
	end := turn.end()
	assert.Empty(t, end.steers)
	assert.Empty(t, end.unaccepted())
	assert.False(t, end.aborted || end.exited)
}

// oddCancelProv answers a cancelled turn with an error that is not the
// cancellation itself (the turn's context is what was cancelled), the shape the
// chat has to recognise from the turn's outcome.
type oddCancelProv struct {
	began chan struct{}
}

func (p *oddCancelProv) Name() string             { return "odd" }
func (p *oddCancelProv) started() <-chan struct{} { return p.began }
func (p *oddCancelProv) letGo()                   {}
func (p *oddCancelProv) prompts() []string        { return nil }

func (p *oddCancelProv) Generate(ctx context.Context, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	select {
	case p.began <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, io.ErrUnexpectedEOF
}

// gateErrorProv is gateProv whose every request ends in a plain failure, so a test
// can insert a message into a turn that ends badly.
type gateErrorProv struct{ *gateProv }

func (p *gateErrorProv) Generate(ctx context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	if _, err := p.gateProv.Generate(ctx, req); err != nil {
		return nil, err
	}
	return nil, errors.New("模型挂了")
}
