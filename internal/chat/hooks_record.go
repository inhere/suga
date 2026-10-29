package chat

import (
	"context"
	"errors"
	"time"

	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

// recordingHooks records every hook run as a "hook" event of the session:
// event name, duration and outcome (a Stop block and its reason length, the
// additional context injected, a notice, a failure). Hooks change what the
// model sees (additional context, a Stop reply starting a turn), so trace
// shows them with the turns.
type recordingHooks struct {
	sugahooks.Client
	runner eventRecorder
}

// eventRecorder is what recordingHooks writes to (a *personal.Runner).
type eventRecorder interface {
	RecordEvent(ctx context.Context, sessionID string, ev personal.HostEvent)
}

func recordHooks(inner sugahooks.Client, runner *personal.Runner) sugahooks.Client {
	if inner == nil || runner == nil {
		return inner
	}
	return &recordingHooks{Client: inner, runner: runner}
}

func (h *recordingHooks) Call(ctx context.Context, ev sugahooks.Event) sugahooks.Response {
	start := time.Now()
	resp := h.Client.Call(ctx, ev)
	h.record(ev, sugahooks.Result{Response: resp}, time.Since(start))
	return resp
}

// StartStop wraps the Stop wait: its result is recorded when it arrives and
// passed on unchanged.
func (h *recordingHooks) StartStop(ctx context.Context, ev sugahooks.Event) sugahooks.Wait {
	inner := h.Client.StartStop(ctx, ev)
	w := &recordingWait{inner: inner, out: make(chan sugahooks.Result, 1)}
	start := time.Now()
	go func() {
		defer close(w.out)
		r, ok := <-inner.Done()
		if !ok {
			return
		}
		h.record(ev, r, time.Since(start))
		w.out <- r
	}()
	return w
}

func (h *recordingHooks) record(ev sugahooks.Event, r sugahooks.Result, d time.Duration) {
	recordHookRun(h.runner, ev, r, d, nil)
}

// recordHookRun writes one hook run as a "hook" event of the session: the event
// name, the duration and the outcome (decision, reason and context lengths, a
// notice, a failure). The PreToolUse gate records through it too, so a refused
// or failed call hook is visible in trace.
func recordHookRun(recorder eventRecorder, ev sugahooks.Event, r sugahooks.Result, d time.Duration, extra map[string]any) {
	if recorder == nil || ev.SessionID == "" {
		return
	}
	attrs := map[string]any{}
	if r.Response.Decision != "" {
		attrs["decision"] = r.Response.Decision
	}
	if n := len(r.Response.Reason); n > 0 {
		attrs["reason_chars"] = n
	}
	if n := len(sugahooks.AdditionalContext(r.Response)); n > 0 {
		attrs["additional_chars"] = n
	}
	if r.Response.Notice != "" {
		attrs["notice"] = true
	}
	for name, value := range extra {
		attrs[name] = value
	}
	status, errText := "ok", ""
	if r.Err != nil {
		status, errText = "error", r.Err.Error()
		// A hook that reached its own bound, or was cancelled with the turn, did not
		// fail: a Stop wait ends that way by configuration (hooks.stop_wait_sec),
		// so the trace must not show it as an error (issue my-tools-4x8a).
		if errors.Is(r.Err, sugahooks.ErrTimedOut) || errors.Is(r.Err, context.Canceled) || errors.Is(r.Err, context.DeadlineExceeded) {
			status = "cancelled"
		}
	}
	recorder.RecordEvent(context.Background(), ev.SessionID, personal.HostEvent{
		Kind: store.EventHook, Name: ev.Name, Status: status, Duration: d, Error: errText, Attrs: attrs})
}

// recordingWait forwards a Stop wait through its own channel. Interrupt keeps
// the inner contract: the inner wait sends Interrupt and is cancelled, and a
// reply that arrived but was not taken from Done is handed back.
type recordingWait struct {
	inner sugahooks.Wait
	out   chan sugahooks.Result
}

func (w *recordingWait) Done() <-chan sugahooks.Result { return w.out }

func (w *recordingWait) Interrupt(ctx context.Context) string {
	reply := w.inner.Interrupt(ctx)
	// What the forwarding goroutine took from the inner wait and nobody read
	// from Done is the unconsumed reply. A cancelled wait ends at once; the
	// grace keeps a wait that never closes from hanging the interrupt.
	select {
	case r, ok := <-w.out:
		if ok && r.Err == nil && r.Response.Decision == "block" && reply == "" {
			reply = r.Response.Reason
		}
	case <-time.After(interruptGrace):
	}
	return reply
}

// interruptGrace bounds how long Interrupt waits for the forwarded result.
const interruptGrace = 200 * time.Millisecond
