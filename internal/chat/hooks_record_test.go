package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/personal"
)

type eventSink struct {
	mu     sync.Mutex
	events []personal.HostEvent
}

func (s *eventSink) RecordEvent(_ context.Context, _ string, ev personal.HostEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *eventSink) all() []personal.HostEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]personal.HostEvent(nil), s.events...)
}

// stubHooks answers Call with resp and StartStop with a wait on done.
type stubHooks struct {
	resp sugahooks.Response
	done chan sugahooks.Result
}

func (s *stubHooks) Call(context.Context, sugahooks.Event) sugahooks.Response { return s.resp }
func (s *stubHooks) StartStop(context.Context, sugahooks.Event) sugahooks.Wait {
	return &fakeStopWait{done: s.done, interrupt: func() {}}
}
func (s *stubHooks) PostToolName(tool string) (string, bool) { return tool, true }

func TestRecordingHooks(t *testing.T) {
	sink := &eventSink{}
	stub := &stubHooks{resp: sugahooks.Response{Notice: "taken over"}, done: make(chan sugahooks.Result, 1)}
	h := &recordingHooks{Client: stub, runner: sink}

	h.Call(context.Background(), sugahooks.Event{Name: "UserPromptSubmit", SessionID: "s1"})
	stub.done <- sugahooks.Result{Response: sugahooks.Response{Decision: "block", Reason: "go on"}}
	w := h.StartStop(context.Background(), sugahooks.Event{Name: "Stop", SessionID: "s1"})
	r := <-w.Done() // passed on unchanged
	assert.Eq(t, "go on", r.Response.Reason)
	h.Call(context.Background(), sugahooks.Event{Name: "SessionEnd"}) // no session: not recorded

	events := sink.all()
	assert.Require(t, assert.Len(t, events, 2))
	assert.Eq(t, "UserPromptSubmit", events[0].Name)
	assert.Eq(t, map[string]any{"notice": true}, events[0].Attrs)
	assert.Eq(t, "Stop", events[1].Name)
	assert.Eq(t, map[string]any{"decision": "block", "reason_chars": 5}, events[1].Attrs)

	t.Run("an interrupt hands back a reply nobody read", func(t *testing.T) {
		stub.done = make(chan sugahooks.Result, 1)
		stub.done <- sugahooks.Result{Response: sugahooks.Response{Decision: "block", Reason: "pending"}}
		w := h.StartStop(context.Background(), sugahooks.Event{Name: "Stop", SessionID: "s1"})
		time.Sleep(20 * time.Millisecond) // the forwarder took it
		assert.Eq(t, "pending", w.Interrupt(context.Background()))
	})

	// A Stop wait that reaches its configured bound is not a failure: the trace
	// records it as cancelled, while a hook command that broke stays an error.
	t.Run("a timeout is recorded as cancelled", func(t *testing.T) {
		sink := &eventSink{}
		recordHookRun(sink, sugahooks.Event{Name: "Stop", SessionID: "s1"},
			sugahooks.Result{Err: fmt.Errorf("hook command timed out after 60s: %w", sugahooks.ErrTimedOut)}, time.Second, nil)
		recordHookRun(sink, sugahooks.Event{Name: "Stop", SessionID: "s1"},
			sugahooks.Result{Err: errors.New("exit status 1")}, time.Second, nil)
		events := sink.all()
		assert.Require(t, assert.Len(t, events, 2))
		assert.Eq(t, "cancelled", events[0].Status)
		assert.Eq(t, "error", events[1].Status)
	})
}
