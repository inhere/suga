package process

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// messagesServer starts a session socket of a receiver that knows message IDs
// (StartMessages): every request is handed to requests, the answer is a queued one
// and its fate is taken from the channel the test sends on.
func messagesServer(t *testing.T, requests chan DeliverRequest, fates chan error) Record {
	t.Helper()
	server, err := StartMessages(shortProcessRoot(t), "session-m1", "messages", t.TempDir(), nil,
		func(req DeliverRequest) (Delivery, <-chan error, error) {
			requests <- req
			return Delivery{Queued: true}, fates, nil
		},
		func(id string) (string, bool) {
			if id == "known" {
				return StateProcessed, true
			}
			return "", false
		})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })
	return server.Record()
}

// TestMessages_Protocol: the fields a message ID and a receipt add travel through
// the session socket, and a request without them is answered exactly as before.
func TestMessages_Protocol(t *testing.T) {
	newChannels := func(t *testing.T) (chan DeliverRequest, chan error) {
		t.Helper()
		// Buffered: the server goroutine must not block before the assertions run.
		return make(chan DeliverRequest, 1), make(chan error, 1)
	}

	t.Run("an older request gets the older answer", func(t *testing.T) {
		requests, fates := newChannels(t)
		record := messagesServer(t, requests, fates)
		response, err := Send(record, Request{Text: "你好"})
		assert.NoErr(t, err)
		assert.True(t, response.OK)
		assert.True(t, response.Queued)
		// The new fields stay empty, so an older client reads the same bytes.
		assert.Eq(t, "", response.MessageID)
		assert.Eq(t, "", response.State)
		assert.Eq(t, "", (<-requests).ID)
	})

	t.Run("the message ID comes back with its state", func(t *testing.T) {
		requests, fates := newChannels(t)
		record := messagesServer(t, requests, fates)
		response, err := Send(record, Request{Text: "你好", ID: "m-1"})
		assert.NoErr(t, err)
		assert.Eq(t, "m-1", response.MessageID)
		assert.Eq(t, StateQueued, response.State)
		assert.Eq(t, "m-1", (<-requests).ID)
	})

	t.Run("wait-processed reports the processed state", func(t *testing.T) {
		requests, fates := newChannels(t)
		record := messagesServer(t, requests, fates)
		go func() {
			<-requests
			fates <- nil
		}()
		response, err := Send(record, Request{Text: "你好", ID: "m-2", WaitProcessed: true})
		assert.NoErr(t, err)
		assert.Eq(t, StateProcessed, response.State)
		assert.True(t, response.OK)
	})

	t.Run("wait-processed reports a failed turn", func(t *testing.T) {
		requests, fates := newChannels(t)
		record := messagesServer(t, requests, fates)
		go func() {
			<-requests
			fates <- ErrFailed
		}()
		response, err := Send(record, Request{Text: "你好", ID: "m-3", WaitProcessed: true})
		assert.NoErr(t, err)
		assert.Eq(t, StateFailed, response.State)
		assert.False(t, response.OK)
	})

	t.Run("wait-processed reports a held message", func(t *testing.T) {
		requests, fates := newChannels(t)
		record := messagesServer(t, requests, fates)
		go func() {
			<-requests
			fates <- ErrHeld
		}()
		response, err := Send(record, Request{Text: "你好", ID: "m-4", WaitProcessed: true})
		assert.NoErr(t, err)
		assert.Eq(t, StateHeld, response.State)
		assert.True(t, response.OK)
	})

	t.Run("wait keeps its own semantics for a failed message", func(t *testing.T) {
		requests, fates := newChannels(t)
		record := messagesServer(t, requests, fates)
		go func() {
			<-requests
			fates <- ErrFailed
		}()
		// --wait still exits 3 (ErrNotOnline) for a message that never ran: the new
		// state does not leak into the receipt --wait has always read.
		_, err := Send(record, Request{Text: "你好", Wait: true})
		assert.ErrIs(t, err, ErrNotOnline)
	})

	t.Run("a status query answers the known ID only", func(t *testing.T) {
		requests, fates := newChannels(t)
		record := messagesServer(t, requests, fates)
		state, err := Status(record, "known")
		assert.NoErr(t, err)
		assert.Eq(t, StateProcessed, state)
		_, err = Status(record, "unknown")
		assert.ErrIs(t, err, ErrUnknownMessage)
	})
}
