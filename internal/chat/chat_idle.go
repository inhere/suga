package chat

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/schedule"
)

// idleTaskName names the idle consolidation task (scheduler events, trace).
const idleTaskName = "idle-consolidate"

// idleMark prefixes every idle consolidation line, so they stand out from the
// other chat hints (the Ctrl-C hint, the config notes). A single-width rune,
// like the ❯ prompt and the ▣ answer prefix.
const idleMark = "✦ "

// idleConsolidation is the chat idle consolidation (design "空闲整理（chat）",
// D5/D6): an Idle task fires idle_after after the last activity and, when the
// last request's context reached idle_min_ratio of the segment threshold,
// starts a new segment in the background (reason idle).
//
// Timing: input, deliveries and :compact Touch it; a turn Pauses it from its
// start until its Stop hook went out, so a turn longer than idle_after never
// triggers it and the quiet time counts from the turn end. A Run or Compact
// arriving while it runs preempts it through the session gate (the runner
// cancels it before its commit point and waits), so chat itself only has to
// keep its status lines tidy.
//
// A nil *idleConsolidation (idle_after 0) is valid and does nothing.
type idleConsolidation struct {
	runner   *personal.Runner
	sid      string
	minRatio float64
	render   *Renderer
	// onEvent, when set, sees each scheduler event synchronously (tests).
	onEvent func(schedule.Event)
	sched   schedule.Scheduler
	task    schedule.Handle
	// lastInput is the input tokens of the last request (RunResult.LastInputTokens);
	// reset to 0 after a segment break, so only a new turn re-arms the check.
	lastInput atomic.Int64

	// Scheduler events are written by one background goroutine, in order:
	// Touch/Pause/Resume run on the chat loop between a turn and the next
	// prompt, which must not wait for the database.
	eventsMu     sync.Mutex
	eventsClosed bool
	events       chan schedule.Event
	eventsDone   chan struct{}
}

// idleEventBuffer bounds the pending scheduler events; beyond it they are
// dropped (events are best-effort).
const idleEventBuffer = 256

// newIdleConsolidation registers the idle task; nil when after <= 0.
// extra scheduler options come first, so the observer here always wins
// (tests pass a fake clock only, and onEvent to see events synchronously).
func newIdleConsolidation(runner *personal.Runner, sid string, after time.Duration, minRatio float64,
	render *Renderer, extra []schedule.Option, onEvent func(schedule.Event)) *idleConsolidation {
	if after <= 0 || sid == "" || runner == nil {
		return nil
	}
	c := &idleConsolidation{runner: runner, sid: sid, minRatio: minRatio, render: render, onEvent: onEvent,
		events: make(chan schedule.Event, idleEventBuffer), eventsDone: make(chan struct{})}
	go c.writeEvents()
	opts := append(append([]schedule.Option(nil), extra...), schedule.WithObserver(c.observe))
	c.sched = schedule.New(opts...)
	c.task = c.sched.Idle(idleTaskName, after, c.run)
	return c
}

// Touch records activity (keyboard input, a delivery, :compact): the quiet
// time starts again.
func (c *idleConsolidation) Touch() {
	if c != nil {
		c.sched.Touch()
	}
}

// Pause stops the clock while a turn or :compact runs.
func (c *idleConsolidation) Pause() {
	if c != nil {
		c.task.Pause()
	}
}

// Resume restarts the clock from now (the end of a turn or :compact).
func (c *idleConsolidation) Resume() {
	if c != nil {
		c.task.Resume()
	}
}

// EndTurn remembers the turn's context size and restarts the clock; call it
// after the Stop hook went out, right before waiting for input.
func (c *idleConsolidation) EndTurn(result personal.RunResult) {
	if c == nil {
		return
	}
	if result.LastInputTokens > 0 {
		c.lastInput.Store(int64(result.LastInputTokens))
	}
	c.task.Resume()
}

// Compacted notes a segment break by :compact: the old context size no
// longer applies.
func (c *idleConsolidation) Compacted() {
	if c != nil {
		c.lastInput.Store(0)
	}
}

// Close cancels the task, waits for a running consolidation to stop and
// flushes the pending scheduler events.
func (c *idleConsolidation) Close() {
	if c == nil {
		return
	}
	_ = c.sched.Close()
	c.eventsMu.Lock()
	if !c.eventsClosed {
		c.eventsClosed = true
		close(c.events)
	}
	c.eventsMu.Unlock()
	<-c.eventsDone
}

// run is the Idle task. Its error goes to the scheduler event only.
func (c *idleConsolidation) run(ctx context.Context) error {
	threshold := c.runner.ContextThreshold()
	// Below idle_min_ratio of the threshold the segment is cheap to keep.
	if threshold <= 0 || float64(c.lastInput.Load()) < c.minRatio*float64(threshold) {
		return nil
	}
	c.render.Hint(idleMark + c.render.tr().T("status.idle.running"))
	out, err := c.runner.ConsolidateIdle(ctx, c.sid)
	switch {
	case err == nil:
		c.lastInput.Store(0)
		c.render.Hint(idleMark + c.render.tr().T("status.idle.done", map[string]any{"before": out.TokensBefore, "after": out.TokensAfter}))
	case idleQuiet(err):
		// A turn, :compact or exit took over, or there was nothing to do:
		// stay quiet, the scheduler event records it.
	default:
		c.render.Hint(idleMark + c.render.tr().T("status.idle.failed", map[string]any{"error": truncateLine(err.Error(), resultSummaryLimit)}))
	}
	return err
}

// idleQuiet reports the outcomes the person does not need to see.
func idleQuiet(err error) bool {
	return errors.Is(err, personal.ErrBusy) || errors.Is(err, personal.ErrNothingToCompact) ||
		errors.Is(err, context.Canceled)
}

// observe queues a scheduler event for writeEvents; never blocks. An event
// arriving after Close (a late "fired" of a racing timer) is dropped.
func (c *idleConsolidation) observe(ev schedule.Event) {
	if c.onEvent != nil {
		c.onEvent(ev)
	}
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if c.eventsClosed {
		return
	}
	select {
	case c.events <- ev:
	default:
	}
}

// writeEvents writes the queued scheduler events to the events table (kind
// schedule, name = task, status = what happened; attrs.event keeps the raw
// scheduler event). A quiet failure is recorded as skipped (busy, nothing to
// do) or cancelled (preempted, exit) rather than failed.
func (c *idleConsolidation) writeEvents() {
	defer close(c.eventsDone)
	for ev := range c.events {
		c.writeEvent(ev)
	}
}

func (c *idleConsolidation) writeEvent(ev schedule.Event) {
	status, errText := string(ev.Kind), ""
	if ev.Err != nil {
		errText = ev.Err.Error()
		switch {
		case errors.Is(ev.Err, context.Canceled):
			status = string(schedule.Cancelled)
		case idleQuiet(ev.Err):
			status = string(schedule.Skipped)
		}
	}
	c.runner.RecordEvent(context.Background(), c.sid, personal.HostEvent{
		Kind: store.EventSchedule, Name: ev.Name, Status: status, Duration: ev.Duration, Error: errText,
		Attrs: map[string]any{"event": string(ev.Kind)},
	})
}
