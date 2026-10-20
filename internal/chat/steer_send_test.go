package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Tests for the send side of steering (`suga send --steer`, design "send：
// --steer"): the message goes into the turn running right now when there is one,
// it is queued for the next turn otherwise, and a waiting sender is told what
// became of it.

// sendSteerCapacity is the inbox capacity the design fixes (D11): the ninth
// message of one turn is refused instead of queueing without bound.
const sendSteerCapacity = 8

// toolGateProv answers its first request with a call of the "work" stub after the
// test released it, and its later requests with a final answer. A test inserts a
// message while the first request is held, so the turn's next request carries it
// (design "--wait" 完成点 "送达模型").
type toolGateProv struct {
	begin   chan struct{}
	release chan struct{}
	once    sync.Once

	mu    sync.Mutex
	users []string
	calls int
}

func newToolGateProv() *toolGateProv {
	return &toolGateProv{begin: make(chan struct{}, 8), release: make(chan struct{})}
}

func (p *toolGateProv) Name() string { return "tool-gate" }

func (p *toolGateProv) started() <-chan struct{} { return p.begin }

func (p *toolGateProv) letGo() { p.once.Do(func() { close(p.release) }) }

func (p *toolGateProv) prompts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.users...)
}

func (p *toolGateProv) Generate(ctx context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	last := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			last = m.Content
		}
	}
	p.mu.Lock()
	p.users = append(p.users, last)
	p.calls++
	n := p.calls
	p.mu.Unlock()
	select {
	case p.begin <- struct{}{}:
	default:
	}
	if n == 1 {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// One tool call, so the turn asks the model again: that request is where an
		// inserted message is delivered.
		return &agentcore.GenerateResult{
			ToolCalls:    []agentcore.ToolCall{{CallID: "c1", Name: workToolName}},
			FinishReason: agentcore.FinishToolCalls,
		}, nil
	}
	return &agentcore.GenerateResult{Content: "回答", FinishReason: agentcore.FinishFinal}, nil
}

// workToolName is the tool the gated provider calls; the stub below serves it.
const workToolName = "work"

// workStub is the tool of the gated provider: it returns at once, so the turn
// reaches its next model request.
type workStub struct{}

func (workStub) Name() string        { return workToolName }
func (workStub) Description() string { return "work stub" }
func (workStub) Kind() tools.Kind    { return tools.KindNative }
func (workStub) Schema() agentcoreapi.ToolSchema {
	return agentcoreapi.ToolSchema{Name: workToolName}
}

func (workStub) Execute(_ context.Context, call agentcoreapi.ToolCall) (*agentcoreapi.ToolResult, error) {
	return &agentcoreapi.ToolResult{CallID: call.CallID, Name: call.Name, Content: "worked"}, nil
}

// newSendSteerHarness is newSteerHarness with a runner whose registry holds the
// "work" tool the gated provider calls; the state's own delivery endpoint
// (deliverMessage) is what `suga send` reaches.
func newSendSteerHarness(t *testing.T, provider heldProvider) *steerHarness {
	t.Helper()
	h := newAskHarness(t)
	h.source.SetSteering(true)
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	registry := tools.NewRegistry()
	registry.Register(workStub{})
	runner, err := personal.New(personal.Config{Repository: repo, Provider: provider, Tools: registry})
	assert.Require(t, assert.NoErr(t, err))
	model, err := runner.NewSession(context.Background(), "", "send steer test")
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

// turnCount is how many turns the session recorded.
func (h *steerHarness) turnCount(t *testing.T) int {
	t.Helper()
	events, err := h.repo.ListEvents(context.Background(), store.EventFilter{
		SessionID: h.sessID, Kinds: []string{store.EventTurn},
	})
	assert.Require(t, assert.NoErr(t, err))
	return len(events)
}

// settledOrFail reads the fate of a waited-for message.
func settledOrFail(t *testing.T, settled <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-settled:
		return err
	case <-time.After(3 * time.Second):
		t.Fatalf("the waiting sender was not told %s", what)
		return nil
	}
}

// TestDeliverMessageKeepsTheFollowUpDefault: a request that does not ask for the
// steer mode - the field missing entirely, the follow-up mode, or a value from a
// newer client - is delivered the way it always was: the message waits for its own
// turn, and the ack of an idle chat says it starts it at once.
func TestDeliverMessageKeepsTheFollowUpDefault(t *testing.T) {
	for _, mode := range []string{"", personalprocess.ModeFollowUp, "something_else"} {
		t.Run("mode "+strconv.Quote(mode), func(t *testing.T) {
			h := newSendSteerHarness(t, newToolGateProv())
			delivery, _, err := h.state.deliverMessage(mode, "你好", "cli", nil)
			assert.NoErr(t, err)
			assert.False(t, delivery.Steered)
			assert.False(t, delivery.Queued)
			assert.Eq(t, 1, len(h.source.deliveries))
		})
	}
}

// TestSendSteerReachesTheRunningTurn: a `suga send --steer` message goes into the
// turn running right now - the same turn's next request carries it - and a sender
// waiting for it is settled once that request went out (design "send：--steer" and
// the "--wait" 完成点 "送达模型").
func TestSendSteerReachesTheRunningTurn(t *testing.T) {
	prov := newToolGateProv()
	h := newSendSteerHarness(t, prov)
	done := h.startTurn()
	<-prov.started() // the turn's first request is in flight

	delivery, settled, err := h.state.deliverMessage(personalprocess.ModeSteer, "只改 README", "gofer", nil)
	assert.NoErr(t, err)
	assert.True(t, delivery.Steered)
	assert.False(t, delivery.Queued)
	select {
	case <-settled:
		t.Fatal("the sender was settled before a model request carried the message")
	default:
	}
	waitUntil(t, "the message is shown as waiting", func() bool {
		return strings.Contains(h.out.String(), "[插话] [来自 gofer] 只改 README（等待送达）")
	})
	// It went into the turn, not into the queue for the next turn.
	assert.Eq(t, 0, len(h.source.deliveries))

	prov.letGo()
	assert.NoErr(t, settledOrFail(t, settled, "that the message reached the model"))
	assert.NoErr(t, <-done)
	assert.Eq(t, []string{"干活", "[来自 gofer] 只改 README"}, prov.prompts())
	// Shown as delivered and counted in the turn's status line, and the message did
	// not start a turn of its own.
	assert.StrContains(t, h.out.String(), "[插话] [来自 gofer] 只改 README（已送达）")
	assert.StrContains(t, h.out.String(), "插话 1")
	assert.Eq(t, 1, h.turnCount(t))
}

// TestSendSteerWithoutATurnIsQueued: with nothing to insert into - an idle chat, a
// turn that does not read the input line (chat.steering off) - the message waits
// for the next turn like any delivery, and the sender is answered "queued".
func TestSendSteerWithoutATurnIsQueued(t *testing.T) {
	t.Run("idle chat", func(t *testing.T) {
		h := newSendSteerHarness(t, newToolGateProv())
		delivery, settled, err := h.state.deliverMessage(personalprocess.ModeSteer, "你好", "gofer", nil)
		assert.NoErr(t, err)
		assert.True(t, delivery.Queued)
		assert.False(t, delivery.Steered)
		assert.Eq(t, 1, len(h.source.deliveries))
		assert.Eq(t, "[来自 gofer] 你好", (<-h.source.deliveries).line)
		// Nothing was inserted, so nothing settles the sender here.
		select {
		case err := <-settled:
			t.Fatalf("an idle chat settled the sender: %v", err)
		default:
		}
	})

	t.Run("chat.steering off", func(t *testing.T) {
		prov := newToolGateProv()
		h := newSendSteerHarness(t, prov)
		h.state.steering = false
		done := h.startTurn()
		<-prov.started()

		delivery, _, err := h.state.deliverMessage(personalprocess.ModeSteer, "你好", "gofer", nil)
		assert.NoErr(t, err)
		assert.True(t, delivery.Queued)
		assert.Eq(t, 1, len(h.source.deliveries))
		prov.letGo()
		assert.NoErr(t, <-done)
	})

	t.Run("a turn whose inbox is not open yet", func(t *testing.T) {
		h := newSendSteerHarness(t, newToolGateProv())
		// startTurnInput publishes the turn before runner.Run opens its inbox; a
		// message arriving in that window is not inserted (Runner.Steer refuses it)
		// and waits for the next turn instead (design m8).
		h.state.setCurrentTurn(&turnInput{state: h.state})
		delivery, _, err := h.state.deliverMessage(personalprocess.ModeSteer, "你好", "gofer", nil)
		assert.NoErr(t, err)
		assert.True(t, delivery.Queued)
		assert.Eq(t, 0, len(h.state.sendSettled))
	})
}

// TestSendSteerFullInboxIsRefused: a full inbox (design D11, capacity 8) is
// answered queue_full instead of queueing without bound.
func TestSendSteerFullInboxIsRefused(t *testing.T) {
	prov := newToolGateProv()
	h := newSendSteerHarness(t, prov)
	done := h.startTurn()
	<-prov.started()

	for i := 0; i < sendSteerCapacity; i++ {
		delivery, _, err := h.state.deliverMessage(personalprocess.ModeSteer, fmt.Sprintf("插话 %d", i), "gofer", nil)
		assert.NoErr(t, err)
		assert.True(t, delivery.Steered)
	}
	_, settled, err := h.state.deliverMessage(personalprocess.ModeSteer, "太多了", "gofer", nil)
	assert.True(t, err != nil && settled == nil)
	assert.ErrIs(t, err, personal.ErrSteerQueueFull)
	// The session socket maps only its own ErrQueueFull to the queue_full answer.
	assert.ErrIs(t, err, personalprocess.ErrQueueFull)
	// Refused, not queued: the message is nowhere to be found afterwards.
	assert.Eq(t, 0, len(h.source.deliveries))
	assert.Eq(t, sendSteerCapacity, len(h.state.sendSettled))

	prov.letGo()
	assert.NoErr(t, <-done)
	assert.StrContains(t, h.out.String(), "插话过多")
}

// TestSendSteerReturnedByCancelledTurnIsDiscarded: a turn that is cancelled (Esc,
// Ctrl-C) hands every inserted message back - they go to the input line and are not
// run - so a waiting sender is told its message is discarded (design D4 and the
// "--wait" 完成点, exit 3 for the CLI).
func TestSendSteerReturnedByCancelledTurnIsDiscarded(t *testing.T) {
	prov := newToolGateProv()
	h := newSendSteerHarness(t, prov)
	done := h.startTurn()
	<-prov.started()

	delivery, settled, err := h.state.deliverMessage(personalprocess.ModeSteer, "别改了", "gofer", nil)
	assert.NoErr(t, err)
	assert.True(t, delivery.Steered)

	h.kb.Type("\x03") // a single Ctrl-C ends this turn only
	assert.ErrIs(t, settledOrFail(t, settled, "that the message was discarded"), personalprocess.ErrDiscarded)
	assert.NoErr(t, <-done)
	// The message is back in the input line, labeled with its sender, and nothing
	// ran it.
	assert.Eq(t, "[来自 gofer] 别改了", h.turnInputLine(t))
	assert.Eq(t, []string{"干活"}, prov.prompts())
}

// TestSendSteerBecomesTheNextTurn: a turn that answers without carrying the
// message hands it back, it runs at once as the next turn, and the sender is told
// then (design D3 and the "--wait" 完成点 "作为下一回合运行").
func TestSendSteerBecomesTheNextTurn(t *testing.T) {
	// The provider answers its held request with a final reply, so the turn ends
	// without a second request to carry the message.
	prov := newGateProv()
	h := newSendSteerHarness(t, prov)
	done := h.startTurn()
	<-prov.started()

	delivery, settled, err := h.state.deliverMessage(personalprocess.ModeSteer, "改成 README", "gofer", nil)
	assert.NoErr(t, err)
	assert.True(t, delivery.Steered)
	prov.letGo()
	assert.NoErr(t, <-done)

	assert.Eq(t, []string{"干活", "[来自 gofer] 改成 README"}, prov.prompts())
	assert.NoErr(t, settledOrFail(t, settled, "that the message is running"))
	assert.Eq(t, 2, h.turnCount(t))
}

// TestChatExitDiscardsPendingSendSteers: a message inserted into the last turn
// whose fate chat never learned (it is leaving) is reported as discarded to its
// sender rather than left hanging (design m1).
func TestChatExitDiscardsPendingSendSteers(t *testing.T) {
	h := newSendSteerHarness(t, newToolGateProv())
	settled := make(chan error, 1)
	h.state.rememberSendSteer("s1", settled)

	h.state.discardPendingSendSteers()
	assert.ErrIs(t, settledOrFail(t, settled, "that the message was discarded"), personalprocess.ErrDiscarded)
	// The sender is forgotten: a message whose fate was reported is not reported
	// twice (a turn cancelled afterwards only reaches the trace).
	assert.Eq(t, 0, len(h.state.sendSettled))
	h.state.settleSendSteer("s1", nil)
	assert.Eq(t, 0, len(h.state.sendSettled))
}
