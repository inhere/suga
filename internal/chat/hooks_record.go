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
	h.record(ev, sugahooks.Result{Response: resp}, time.Since(start), false)
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
		h.record(ev, r, time.Since(start), true)
		w.out <- r
	}()
	return w
}

// record writes the command runs behind one answer: a pipeline answers with one
// run per configured entry, a single command with the one run of its own, so
// trace shows every entry that ran (HF14). An answer without run records is
// traced as the single run it was, with the duration of the whole call.
// keepErr records the error of each run: a Stop wait carries it per entry,
// while an ordinary event answer reports no error of its own.
func (h *recordingHooks) record(ev sugahooks.Event, r sugahooks.Result, d time.Duration, keepErr bool) {
	if len(r.Response.Runs) == 0 {
		recordHookRun(h.runner, ev, r, d, nil)
		return
	}
	for _, run := range r.Response.Runs {
		result := sugahooks.Result{Response: run.Response}
		if keepErr {
			result.Err = run.Err
		}
		recordHookRun(h.runner, ev, result, run.Duration, entryRunAttrs(run))
	}
}

// entryRunAttrs names the configured entry and its program in a hook event, so
// trace tells which of the hooks.commands entries (0: hooks.command) produced it.
func entryRunAttrs(run sugahooks.EntryRun) map[string]any {
	return map[string]any{"entry": run.Entry, "program": run.Program}
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
