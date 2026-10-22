package sugasdk

// Optional idempotent sends: the tests drive the real Send path (fake provider
// over httptest, e2e_test.go's sdkTestHome) for replay of a finished id, the
// running and uncertain cases, the status query, and the unchanged no-id path.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// plainChatServer answers every model request with finalText. gate (nil =
// never blocks) makes the handler wait until it is closed, so a test can hold
// a turn open; bodies counts the received model requests.
func plainChatServer(t *testing.T, finalText string, bodies *int32, gate <-chan struct{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		atomic.AddInt32(bodies, 1)
		if gate != nil {
			<-gate
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"`+finalText+`"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// openTestStore opens the data root's session store for the record checks
// (the engine opens its own handles, they do not interfere).
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// writeTestRecord plants a request record as a crashed send would have left it.
func writeTestRecord(t *testing.T, ctx context.Context, repo *store.Store, sessionID, requestID string, rec requestRecord) {
	t.Helper()
	value, err := encodeRequestRecord(rec)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.PatchSessionMeta(ctx, sessionID, map[string]any{requestMetaKey(requestID): value}))
}

// openTestSession is one engine and one session the tests share; url is the
// fake provider the engine's model talks to.
func openTestSession(t *testing.T, ctx context.Context, url string) (*Engine, *Session) {
	t.Helper()
	engine, err := Open(ctx, EngineOptions{SugaHome: sdkTestHome(t, url)})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })
	session, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir(), Approver: allowAll{}})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = session.Close() })
	return engine, session
}

func TestSendRequestID(t *testing.T) {
	ctx := context.Background()
	var bodies int32
	engine, session := func() (*Engine, *Session) {
		server := plainChatServer(t, "done-1", &bodies, nil)
		return openTestSession(t, ctx, server.URL)
	}()

	first, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-1"}, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "COMPLETED", first.State)
	assert.Eq(t, "done-1", first.Output)
	assert.False(t, first.Replayed)

	t.Run("a finished id replays the stored result", func(t *testing.T) {
		again, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-1"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, first.Output, again.Output)
		assert.Eq(t, first.State, again.State)
		assert.Eq(t, first.TurnID, again.TurnID)
		assert.Eq(t, first.Usage, again.Usage)
		assert.Eq(t, first.SessionUsage, again.SessionUsage)
		assert.True(t, again.Replayed)
		// The model was called once: no second turn ran.
		assert.Eq(t, int32(1), atomic.LoadInt32(&bodies))
	})
	t.Run("the id maps to the turn it ran", func(t *testing.T) {
		status, err := session.RequestStatus(ctx, "req-1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RequestStatusDone, status)
		repo := openTestStore(t)
		rec, err := session.readRequestRecord(ctx, repo, "req-1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, rec != nil)
		assert.Eq(t, requestStateDone, rec.State)
		assert.Eq(t, first.TurnID, rec.TurnID)
	})
	t.Run("an unclaimed id reads unknown", func(t *testing.T) {
		status, err := session.RequestStatus(ctx, "req-none")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RequestStatusUnknown, status)
	})
	t.Run("a blank id is refused", func(t *testing.T) {
		_, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "   "}, nil)
		assert.Err(t, err)
	})
	t.Run("a send without an id behaves as before", func(t *testing.T) {
		plain, err := session.Send(ctx, Prompt{Text: "hi"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "COMPLETED", plain.State)
		assert.False(t, plain.Replayed)
		assert.Eq(t, int32(2), atomic.LoadInt32(&bodies))
	})
	t.Run("a reopened session handle replays the id too", func(t *testing.T) {
		assert.NoErr(t, session.Close())
		reopened, err := engine.OpenSession(ctx, session.ID(), SessionOptions{Workspace: t.TempDir(), Approver: allowAll{}})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = reopened.Close() })
		again, err := reopened.Send(ctx, Prompt{Text: "hi", RequestID: "req-1"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, again.Replayed)
		assert.Eq(t, first.Output, again.Output)
		assert.Eq(t, int32(2), atomic.LoadInt32(&bodies))
	})
}

func TestSendRequestIDConcurrency(t *testing.T) {
	ctx := context.Background()
	var bodies int32
	gate := make(chan struct{})
	server := plainChatServer(t, "done-c", &bodies, gate)
	_, session := openTestSession(t, ctx, server.URL)

	type sendOut struct {
		res Result
		err error
	}
	first := make(chan sendOut, 1)
	go func() {
		res, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-c"}, nil)
		first <- sendOut{res, err}
	}()
	// Wait until the first turn's request reached the model, so the slot and
	// the record are claimed by then.
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt32(&bodies) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Require(t, assert.Eq(t, int32(1), atomic.LoadInt32(&bodies)))

	t.Run("the same id while it runs reports running", func(t *testing.T) {
		_, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-c"}, nil)
		assert.Require(t, assert.ErrIs(t, err, ErrRequestRunning))
		status, err := session.RequestStatus(ctx, "req-c")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RequestStatusRunning, status)
	})

	// Release the turn and collect the first send.
	close(gate)
	out := <-first
	assert.Require(t, assert.NoErr(t, out.err))
	assert.Eq(t, "COMPLETED", out.res.State)
	assert.False(t, out.res.Replayed)

	t.Run("after the turn it replays", func(t *testing.T) {
		again, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-c"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, again.Replayed)
		assert.Eq(t, out.res.Output, again.Output)
		assert.Eq(t, int32(1), atomic.LoadInt32(&bodies))
	})
}

func TestSendRequestIDUncertain(t *testing.T) {
	ctx := context.Background()
	var bodies int32
	server := plainChatServer(t, "done-u", &bodies, nil)
	_, session := openTestSession(t, ctx, server.URL)
	repo := openTestStore(t)

	t.Run("a claim left running by a crash is uncertain", func(t *testing.T) {
		// A crash between the claim and the finish leaves the record running:
		// whether the turn was delivered is unknown, nothing replays.
		writeTestRecord(t, ctx, repo, session.ID(), "req-x",
			requestRecord{TurnID: "turn-x", State: requestStateRunning})
		_, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-x"}, nil)
		assert.Require(t, assert.ErrIs(t, err, ErrDeliveryUncertain))
		status, err := session.RequestStatus(ctx, "req-x")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RequestStatusUnknown, status)
		// No turn ran for the uncertain id.
		assert.Eq(t, int32(0), atomic.LoadInt32(&bodies))
	})
	t.Run("the caller moves on with a new id", func(t *testing.T) {
		res, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-y"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "COMPLETED", res.State)
		assert.False(t, res.Replayed)
		assert.Eq(t, int32(1), atomic.LoadInt32(&bodies))
	})
	t.Run("a stored done record replays without the model", func(t *testing.T) {
		writeTestRecord(t, ctx, repo, session.ID(), "req-done",
			requestRecord{TurnID: "turn-done", State: requestStateDone,
				Result: &Result{SessionID: session.ID(), TurnID: "turn-done", State: "COMPLETED", Output: "stored"}})
		res, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-done"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "stored", res.Output)
		assert.Eq(t, "COMPLETED", res.State)
		assert.True(t, res.Replayed)
		status, err := session.RequestStatus(ctx, "req-done")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RequestStatusDone, status)
		assert.Eq(t, int32(1), atomic.LoadInt32(&bodies))
	})
	t.Run("a stored failed record replays the failure", func(t *testing.T) {
		writeTestRecord(t, ctx, repo, session.ID(), "req-fail",
			requestRecord{TurnID: "turn-fail", State: requestStateFailed,
				Result: &Result{SessionID: session.ID(), TurnID: "turn-fail", State: "FAILED"}})
		res, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-fail"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "FAILED", res.State)
		assert.True(t, res.Replayed)
		status, err := session.RequestStatus(ctx, "req-fail")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RequestStatusFailed, status)
	})
	t.Run("a blank status id is refused", func(t *testing.T) {
		_, err := session.RequestStatus(ctx, "  ")
		assert.Err(t, err)
	})
}
