package sugasdk

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

func TestSessionCancelStopsTurnAndAllowsNextTurn(t *testing.T) {
	gate := make(chan struct{})
	var requests int32
	server := plainChatServer(t, "done after cancel", &requests, gate)
	_, session := openTestSession(t, context.Background(), server.URL)

	assert.Require(t, assert.ErrIs(t, session.Cancel(), ErrNoRunningTurn))

	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := session.Send(context.Background(), Prompt{Text: "cancel me"}, nil)
		done <- outcome{result: result, err: err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt32(&requests) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Require(t, assert.Eq(t, int32(1), atomic.LoadInt32(&requests)))

	assert.NoErr(t, session.Cancel())
	close(gate)
	select {
	case got := <-done:
		assert.Require(t, assert.ErrIs(t, got.err, context.Canceled))
		assert.Eq(t, "CANCELED", got.result.State)
	case <-time.After(15 * time.Second):
		t.Fatal("Session.Cancel did not stop the turn")
	}
	assert.Require(t, assert.ErrIs(t, session.Cancel(), ErrNoRunningTurn))

	next, err := session.Send(context.Background(), Prompt{Text: "continue"}, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "COMPLETED", next.State)
	assert.Eq(t, "done after cancel", next.Output)
}
