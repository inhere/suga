package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// newDeliveryState builds a chat state on the fake terminal of the ask harness: the
// real input source and runner of a chat without the Run loop around them, plus the
// delivery endpoint the session socket registers (deliverRequested).
func newDeliveryState(t *testing.T, provider agentcore.Provider) (*chatState, *chatInputSource) {
	t.Helper()
	h := newAskHarness(t)
	runner, _ := newCLITestRunner(t, provider)
	model, err := runner.NewSession(context.Background(), "", "delivery test")
	assert.Require(t, assert.NoErr(t, err))
	render := newRenderer(rendererOptions{Out: h.out, Err: h.out})
	state := &chatState{
		ctx: context.Background(), runner: runner, source: h.source, render: render,
		stderr: h.out, sessionID: model.ID, stats: &chatStats{},
		idle: newIdleConsolidation(runner, model.ID, 0, 0, render, nil, nil),
	}
	t.Cleanup(func() { state.idle.Close() })
	return state, h.source
}

// deliverIn queues one message through the endpoint the session socket calls, the
// way `suga send` reaches a chat.
func deliverIn(t *testing.T, state *chatState, req personalprocess.DeliverRequest) <-chan error {
	t.Helper()
	if req.Mode == "" {
		req.Mode = personalprocess.ModeFollowUp
	}
	_, settled, err := state.deliverRequested(req)
	assert.Require(t, assert.NoErr(t, err))
	return settled
}

// TestNextChatInputMergesQueuedDeliveries: the messages queued while the chat was
// busy become one turn (design session-message-delivery D3): every line keeps its
// own source label, and the turn runs at the deepest hop depth of the batch.
func TestNextChatInputMergesQueuedDeliveries(t *testing.T) {
	state, source := newDeliveryState(t, mock.NewProvider(mock.Final("ok")))
	for _, hops := range []int{0, 2, 1} {
		deliverIn(t, state, personalprocess.DeliverRequest{
			Text: fmt.Sprintf("第 %d 条", hops),
			From: personalprocess.SessionFromPrefix + "peer", FromName: "suga-k7", Hops: hops,
		})
	}
	assert.Eq(t, 3, len(source.deliveries))

	var lost string
	in, err := nextChatInput(context.Background(), source, state.render, nil, &lost)
	assert.NoErr(t, err)
	// One turn for all three, each line labelled with its own source.
	assert.Eq(t, 3, len(in.deliveries))
	assert.Eq(t, 3, strings.Count(in.line, "[来自会话 suga-k7"))
	assert.True(t, strings.Contains(in.line, "第 0 条") && strings.Contains(in.line, "第 1 条"))
	// The hop depth of the batch is the deepest one, so the reply limit sees it.
	assert.Eq(t, 2, in.hops)
	assert.Eq(t, 0, len(source.deliveries))
}

// TestDeliveryIDSkipsADuplicate: a message whose ID this process already took is not
// queued a second time (design D2: at most once).
func TestDeliveryIDSkipsADuplicate(t *testing.T) {
	state, source := newDeliveryState(t, mock.NewProvider(mock.Final("ok")))
	req := personalprocess.DeliverRequest{Text: "你好", ID: "m-1"}
	deliverIn(t, state, req)

	delivery, settled, err := state.deliverRequested(req)
	assert.NoErr(t, err)
	assert.True(t, delivery.Queued)
	assert.Nil(t, settled)
	// Still one message in the queue, and the state table names the copy it kept.
	assert.Eq(t, 1, len(source.deliveries))
	stateNow, known := source.DeliveryState("m-1")
	assert.True(t, known)
	assert.Eq(t, personalprocess.StateQueued, stateNow)
}

// TestDeliveryReceiptProcessed: the sender of a --wait-processed message learns that
// its turn completed (design D2), and the state table says processed.
func TestDeliveryReceiptProcessed(t *testing.T) {
	state, source := newDeliveryState(t, mock.NewProvider(mock.Final("回答")))
	settled := deliverIn(t, state, personalprocess.DeliverRequest{Text: "一", ID: "p-1", WaitProcessed: true})

	var lost string
	in, err := nextChatInput(context.Background(), source, state.render, nil, &lost)
	assert.NoErr(t, err)
	// What the chat loop does with the input nextChatInput returned.
	state.turnHops = in.hops
	state.turnDeliveries = in.deliveries
	assert.NoErr(t, state.runTurn(in.line, in.injected, in.images))

	assert.NoErr(t, settledOrFail(t, settled, "processed"))
	stateNow, known := source.DeliveryState("p-1")
	assert.True(t, known)
	assert.Eq(t, personalprocess.StateProcessed, stateNow)
}

// TestDeliveryFailureHoldsQueuedMessages: a failed turn reports failed to the message
// it carried, moves what is still queued into sessionHeld (design D2 failure barrier)
// and never retries on its own; the person's next completed turn processes the held
// message.
func TestDeliveryFailureHoldsQueuedMessages(t *testing.T) {
	state, source := newDeliveryState(t,
		mock.NewProvider(mock.Error(errors.New("boom")), mock.Final("下一回合的答复")))
	carried := deliverIn(t, state, personalprocess.DeliverRequest{Text: "坏消息", ID: "f-1", WaitProcessed: true})
	queued := deliverIn(t, state, personalprocess.DeliverRequest{Text: "后面这条", ID: "f-2", WaitProcessed: true})

	// Only the first message reaches the failing turn; the second is still queued.
	first := <-source.deliveries
	state.turnDeliveries = []chatInputEvent{first}
	assert.NoErr(t, state.runTurn(first.line, first.injected, first.images))

	assert.ErrIs(t, settledOrFail(t, carried, "failed"), personalprocess.ErrFailed)
	assert.ErrIs(t, settledOrFail(t, queued, "held"), personalprocess.ErrHeld)
	// The barrier moved the queued message to the person's next turn.
	assert.Eq(t, 1, state.heldSessionCount())
	assert.Eq(t, 0, len(source.deliveries))
	heldState, known := source.DeliveryState("f-2")
	assert.True(t, known)
	assert.Eq(t, personalprocess.StateHeld, heldState)

	// The person's next turn carries the held message and processes it.
	state.turnDeliveries = nil
	assert.NoErr(t, state.runTurn("继续", false, nil))
	heldState, _ = source.DeliveryState("f-2")
	assert.Eq(t, personalprocess.StateProcessed, heldState)
}

// TestDiscardQueuedReceipt: chat exits with messages still queued: every waiting
// sender reads discarded, and a status query says discarded (design D3).
func TestDiscardQueuedReceipt(t *testing.T) {
	state, source := newDeliveryState(t, mock.NewProvider(mock.Final("ok")))
	settled := deliverIn(t, state, personalprocess.DeliverRequest{Text: "排队中", ID: "d-1", WaitProcessed: true})
	assert.Eq(t, 1, source.DiscardQueued())
	assert.ErrIs(t, settledOrFail(t, settled, "discarded"), personalprocess.ErrDiscarded)
	stateNow, known := source.DeliveryState("d-1")
	assert.True(t, known)
	assert.Eq(t, personalprocess.StateDiscarded, stateNow)
}
