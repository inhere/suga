package acpserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
)

// blockingTurnProvider keeps a turn's one model call blocked until the turn's
// context is cancelled — the turn stops mid-flight the way a hung worker or
// provider holds it, and session/cancel (as session/close does) must wind it
// down through that block.
type blockingTurnProvider struct{ started chan struct{} }

func (p *blockingTurnProvider) Name() string { return "blocking-turn" }

func (p *blockingTurnProvider) Generate(ctx context.Context, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

// stubbornTurnProvider is the blocking one that does not react to the turn's
// cancellation at all: only the test lets its call go. close waits in vain
// and must report the wait timeout instead of hanging forever.
type stubbornTurnProvider struct {
	started chan struct{}
	letGo   chan struct{}
	once    sync.Once
}

func newStubbornTurnProvider() *stubbornTurnProvider {
	return &stubbornTurnProvider{started: make(chan struct{}), letGo: make(chan struct{})}
}

func (p *stubbornTurnProvider) Name() string { return "stubborn-turn" }

func (p *stubbornTurnProvider) Generate(_ context.Context, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	close(p.started)
	<-p.letGo // ignores ctx.Done() on purpose: the close timeout is what is tested
	return nil, context.Canceled
}

// release unblocks the provider; safe to call more than once (the test and
// its cleanup both drive it).
func (p *stubbornTurnProvider) release() { p.once.Do(func() { close(p.letGo) }) }

// turnOutcome carries one prompt turn's result across the goroutines.
type turnOutcome struct {
	response acp.PromptResponse
	err      error
}

// TestACPCloseDuringTurnEndsItCancelled: a turn stopped mid-flight on a
// blocked model call — session/close runs like session/cancel on it, comes
// back only after the turn has wound down (reported cancelled to the prompt),
// and leaves the session closed: the next prompt is refused and the process
// state is released. Reverse verification: dropping close's cancel leaves the
// turn running, so close fails at its wait timeout and the close-and-prompt
// assertions fail.
func TestACPCloseDuringTurnEndsItCancelled(t *testing.T) {
	provider := &blockingTurnProvider{started: make(chan struct{})}
	client := &testACPClient{}
	agent, conn := connectPersonalACPAgent(t, testACPRuntime(t, provider, nil), client)
	ctx := testCtx(t)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))

	// The turn runs on the goroutine a client would own; close is called while
	// it is stuck inside the model call.
	turned := make(chan struct{})
	turns := make(chan turnOutcome, 1)
	go func() {
		response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId,
			Prompt: []acp.ContentBlock{acp.TextBlock("卡在模型上")}})
		turns <- turnOutcome{response: response, err: err}
		close(turned)
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, closeErr := conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: created.SessionId})
	assert.Require(t, assert.NoErr(t, closeErr))
	// close waited for the turn: by the time it returned, the prompt had ended
	// on the agent's side. The prompt's response still travels to this client
	// goroutine on its own, so it may land just after close's (seen under load
	// on Windows): allow it a short delivery time. A turn close did not stop
	// stays blocked in the provider and never answers.
	select {
	case <-turned:
	case <-time.After(2 * time.Second):
		t.Fatal("session/close returned while the turn was still running")
	}
	outcome := <-turns
	assert.Require(t, assert.NoErr(t, outcome.err))
	assert.Eq(t, acp.StopReasonCancelled, outcome.response.StopReason)

	// A prompt on the closed session is refused as invalid params ...
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId,
		Prompt: []acp.ContentBlock{acp.TextBlock("还在吗")}})
	assert.Require(t, assert.Err(t, err))
	var rpcErr *acp.RequestError
	assert.Require(t, assert.True(t, errors.As(err, &rpcErr)))
	assert.Eq(t, invalidParamsCode, rpcErr.Code)
	// ... and the released state is what makes the refusal stick.
	agent.mu.Lock()
	_, alive := agent.sessions[created.SessionId]
	agent.mu.Unlock()
	assert.Eq(t, false, alive)
}

// TestACPCloseDuringTurnTimesOut: a turn that ignores its cancellation can
// hold close only for closeWaitTimeout — then the close fails with an error
// naming the wait, the session stays put (its state is not released behind a
// running turn), and letting the turn go afterwards still ends it cancelled.
func TestACPCloseDuringTurnTimesOut(t *testing.T) {
	original := closeWaitTimeout
	closeWaitTimeout = 150 * time.Millisecond
	t.Cleanup(func() { closeWaitTimeout = original })

	provider := newStubbornTurnProvider()
	// The guard unblocks the turn even if an assertion above did not: the
	// blocked Generate would otherwise outlive the test.
	t.Cleanup(provider.release)
	client := &testACPClient{}
	agent, conn := connectPersonalACPAgent(t, testACPRuntime(t, provider, nil), client)
	ctx := testCtx(t)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	turns := make(chan turnOutcome, 1)
	go func() {
		response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId,
			Prompt: []acp.ContentBlock{acp.TextBlock("无视取消")}})
		turns <- turnOutcome{response: response, err: err}
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	_, closeErr := conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: created.SessionId})
	assert.Require(t, assert.Err(t, closeErr))
	assert.StrContains(t, closeErr.Error(), "did not end within")
	// The state is not released behind a running turn.
	agent.mu.Lock()
	_, alive := agent.sessions[created.SessionId]
	agent.mu.Unlock()
	assert.Eq(t, true, alive)

	// Release the stubborn turn: it ends cancelled, and the session closes
	// cleanly afterwards.
	provider.release()
	outcome := <-turns
	assert.Require(t, assert.NoErr(t, outcome.err))
	assert.Eq(t, acp.StopReasonCancelled, outcome.response.StopReason)
	_, closeErr = conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: created.SessionId})
	assert.NoErr(t, closeErr)
}
