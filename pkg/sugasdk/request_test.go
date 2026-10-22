package sugasdk

// Optional idempotent sends: the tests drive the real Send path (fake provider
// over httptest, e2e_test.go's sdkTestHome) for replay of a finished id, the
// running and uncertain cases, the status query, and the unchanged no-id path.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
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

// writeTestAssistantEntry plants one assistant entry of a turn with content,
// the way the runner stores a model reply (an agentcore.Message JSON).
func writeTestAssistantEntry(t *testing.T, ctx context.Context, repo *store.Store, sessionID, turnID, content string) {
	t.Helper()
	body, err := json.Marshal(agentcore.Message{Role: "assistant", Content: content})
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(ctx, sessionID, store.NewEntry{TurnID: turnID, Type: store.EntryAssistant, JSON: body})
	assert.Require(t, assert.NoErr(t, err))
}

// sessionMetaJSON reads the session's whole meta back as one JSON document.
func sessionMetaJSON(t *testing.T, ctx context.Context, repo *store.Store, sessionID string) string {
	t.Helper()
	model, err := repo.GetSession(ctx, sessionID)
	assert.Require(t, assert.NoErr(t, err))
	raw, err := json.Marshal(model.Meta)
	assert.Require(t, assert.NoErr(t, err))
	return string(raw)
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
		// The record holds the small summary; the answer text lives in the
		// turn's entries and the replay rebuilds it there.
		writeTestAssistantEntry(t, ctx, repo, session.ID(), "turn-done", "stored")
		writeTestRecord(t, ctx, repo, session.ID(), "req-done",
			requestRecord{TurnID: "turn-done", State: requestStateDone,
				Summary: &requestSummary{State: "COMPLETED", Usage: Usage{InputTokens: 11, TotalTokens: 11}}})
		res, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-done"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "stored", res.Output)
		assert.Eq(t, "COMPLETED", res.State)
		assert.True(t, res.Replayed)
		assert.Eq(t, 11, res.Usage.InputTokens)
		status, err := session.RequestStatus(ctx, "req-done")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RequestStatusDone, status)
		assert.Eq(t, int32(1), atomic.LoadInt32(&bodies))
	})
	t.Run("a record whose entries are gone replays nothing", func(t *testing.T) {
		// A finished record whose turn's answer is no longer in the session log
		// cannot be rebuilt: a clear error instead of a silent empty answer.
		writeTestRecord(t, ctx, repo, session.ID(), "req-gone",
			requestRecord{TurnID: "no-such-turn", State: requestStateDone,
				Summary: &requestSummary{State: "COMPLETED"}})
		_, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-gone"}, nil)
		assert.Require(t, assert.ErrIs(t, err, ErrRequestOutputGone))
		status, err := session.RequestStatus(ctx, "req-gone")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RequestStatusDone, status)
		assert.Eq(t, int32(1), atomic.LoadInt32(&bodies))
	})
	t.Run("a stored failed record replays the failure", func(t *testing.T) {
		writeTestAssistantEntry(t, ctx, repo, session.ID(), "turn-fail", "cut off mid-call")
		writeTestRecord(t, ctx, repo, session.ID(), "req-fail",
			requestRecord{TurnID: "turn-fail", State: requestStateFailed,
				Summary: &requestSummary{State: "FAILED"}})
		res, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-fail"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "FAILED", res.State)
		assert.Eq(t, "cut off mid-call", res.Output)
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

// TestSendRequestRecordWindow covers the size cap of one session's request
// records: oldest finished records drop as newer claims arrive, running
// records are never dropped.
func TestSendRequestRecordWindow(t *testing.T) {
	ctx := context.Background()
	var bodies int32
	server := plainChatServer(t, "done-w", &bodies, nil)
	_, session := openTestSession(t, ctx, server.URL)
	repo := openTestStore(t)

	// A small window so the test can cross it without 200 records.
	requestRecordLimit = 3
	t.Cleanup(func() { requestRecordLimit = 200 })

	// Four claimed records, oldest first; the second one stayed running (a
	// crashed send) and must survive any trim.
	base := time.Now().UTC().Add(-2 * time.Hour)
	writeTestRecord(t, ctx, repo, session.ID(), "old-1",
		requestRecord{TurnID: "t1", State: requestStateDone, At: base.Add(1 * time.Minute),
			Summary: &requestSummary{State: "COMPLETED"}})
	writeTestRecord(t, ctx, repo, session.ID(), "old-2",
		requestRecord{TurnID: "t2", State: requestStateRunning, At: base.Add(2 * time.Minute)})
	writeTestRecord(t, ctx, repo, session.ID(), "old-3",
		requestRecord{TurnID: "t3", State: requestStateDone, At: base.Add(3 * time.Minute),
			Summary: &requestSummary{State: "COMPLETED"}})
	writeTestRecord(t, ctx, repo, session.ID(), "old-4",
		requestRecord{TurnID: "t4", State: requestStateDone, At: base.Add(4 * time.Minute),
			Summary: &requestSummary{State: "COMPLETED"}})

	// A fresh claim grows the registry past the window: the oldest finished
	// record (old-1) drops, the older running one (old-2) stays.
	res, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "new-1"}, nil)
	assert.Require(t, assert.NoError(t, err))
	assert.Eq(t, "COMPLETED", res.State)
	meta, err := repo.GetSession(ctx, session.ID())
	assert.Require(t, assert.NoErr(t, err))
	_, gone := meta.Meta[requestMetaKey("old-1")]
	assert.False(t, gone, "the oldest finished record is trimmed")
	for _, kept := range []string{"old-2", "old-3", "old-4", "new-1"} {
		_, ok := meta.Meta[requestMetaKey(kept)]
		assert.True(t, ok, "record stays: "+kept)
	}
	// The kept stale running record still reads unknown, never replayed.
	status, err := session.RequestStatus(ctx, "old-2")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, RequestStatusUnknown, status)
}

// TestSendRequestStoresSummary covers that the record registry holds no answer
// text: the meta of a session does not grow with the length of the outputs.
func TestSendRequestStoresSummary(t *testing.T) {
	ctx := context.Background()
	var bodies int32
	longOutput := strings.Repeat("answer ", 800)
	server := plainChatServer(t, longOutput, &bodies, nil)
	_, session := openTestSession(t, ctx, server.URL)
	repo := openTestStore(t)

	first, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-long"}, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, longOutput, first.Output)

	// The record stores turn id, state and numbers only: the whole meta of the
	// session contains none of the answer text, whatever its length was.
	metaJSON := sessionMetaJSON(t, ctx, repo, session.ID())
	assert.StrNotContains(t, metaJSON, longOutput)
	assert.True(t, len(metaJSON) < 4096, "the session meta stays small, got %d bytes", len(metaJSON))
	rec, err := session.readRequestRecord(ctx, repo, "req-long")
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, rec != nil)
	assert.Require(t, rec.Summary != nil)
	assert.True(t, rec.Summary.Usage.InputTokens > 0)
	assert.True(t, rec.At.After(time.Now().Add(-time.Hour)))

	// The replay rebuilds the full output of the same length from the entries.
	again, err := session.Send(ctx, Prompt{Text: "hi", RequestID: "req-long"}, nil)
	assert.Require(t, assert.NoError(t, err))
	assert.True(t, again.Replayed)
	assert.Eq(t, first.Output, again.Output)
	assert.Eq(t, int32(1), atomic.LoadInt32(&bodies))
}
