package hooks

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// pipeline runs the compatible hooks.command next to the hooks.commands entries.
// It exists as soon as one entry is configured; with only hooks.command
// configured, New keeps returning the single client itself.
type pipeline struct {
	// compat is the compatible hooks.command, nil when only entries are configured.
	compat *commandClient
	// entries are the hooks.commands clients in configuration order; their entry
	// field is the YAML position plus 1.
	entries []*commandClient
	// compatWaiting says the compatible entry's Stop waits for its own bound
	// (hooks.stop_wait_sec, or stop_background), so it runs as a second phase
	// after the immediate entries instead of answering at once.
	compatWaiting bool
}

// newPipeline returns the pipeline of the compatible entry (nil when absent) and
// the configured entries.
func newPipeline(compat *commandClient, entries []*commandClient, compatWaiting bool) *pipeline {
	return &pipeline{compat: compat, entries: entries, compatWaiting: compatWaiting}
}

// Call runs an ordinary event of the pipeline. PostToolUse belongs to the
// compatible hooks.command alone (the entries carry no tool filter, so they would
// see every tool result); SessionStart, UserPromptSubmit, SessionEnd and unknown
// names run the compatible entry first and then the entries in configuration
// order. A failing entry is logged and skipped, so one broken hook never keeps
// the next one from running.
func (p *pipeline) Call(ctx context.Context, event Event) Response {
	if event.Name == "PostToolUse" {
		if p.compat == nil {
			return Response{}
		}
		return p.compat.Call(ctx, event)
	}
	answers := make([]Response, 0, len(p.entries)+1)
	if p.compat != nil {
		answers = append(answers, p.compat.Call(ctx, event))
	}
	for _, entry := range p.entries {
		answer, err := entry.call(ctx, event, false)
		if err != nil {
			// The answer of a failed command is empty but its run record is kept, so
			// the trace still shows that the entry ran and why it did not answer.
			slog.Warn("hook command failed", "event", event.Name, "entry", entry.entry, "err", err)
		}
		answers = append(answers, answer)
	}
	return mergePipelineAnswers(answers)
}

// mergePipelineAnswers joins the ordinary-event answers in execution order: the
// additional context blocks and the notices are joined, the runs are
// concatenated. An ordinary event has no decision, reason or updated input of its
// own, so those stay empty even when an entry sent them.
func mergePipelineAnswers(answers []Response) Response {
	merged := Response{}
	contexts := make([]string, 0, len(answers))
	notices := make([]string, 0, len(answers))
	for _, answer := range answers {
		if text := strings.TrimSpace(answer.AdditionalContext); text != "" {
			contexts = append(contexts, text)
		}
		if notice := strings.TrimSpace(answer.Notice); notice != "" {
			notices = append(notices, notice)
		}
		merged.Runs = append(merged.Runs, answer.Runs...)
	}
	merged.AdditionalContext = strings.Join(contexts, "\n\n")
	merged.Notice = strings.Join(notices, "\n")
	return merged
}

type pipelineState uint8

const (
	pipelineBetween pipelineState = iota
	pipelineRunning
	pipelineFinished
)

// pipelinePoint is a deterministic synchronization point used only by tests.
// Production leaves pipelineTestHook nil; every call is made after unlocking.
type pipelinePoint int

const (
	pointEntryExited pipelinePoint = iota
	pointBeforeStartNext
	pointBeforeDone
	pointInterruptFrozen
)

var pipelineTestHook func(point pipelinePoint, entry int)

func callPipelinePoint(point pipelinePoint, entry int) {
	if pipelineTestHook != nil {
		pipelineTestHook(point, entry)
	}
}

// pipelineWait owns all Stop state. The mutex protects both the state machine and
// the completed runs, so Interrupt can choose exactly one target and compute its
// reply without racing the runner.
type pipelineWait struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan Result
	finished chan struct{}

	mu      sync.Mutex
	state   pipelineState
	frozen  bool
	running int
	last    int
	runs    []EntryRun
	result  Result
	event   Event
	clients map[int]*commandClient
}

// StartStop runs immediate entries first. A waiting compatible entry is started
// only when every immediate entry produced no block decision.
func (p *pipeline) StartStop(ctx context.Context, event Event) Wait {
	waitCtx, cancel := context.WithCancel(ctx)
	wait := &pipelineWait{
		ctx: waitCtx, cancel: cancel,
		done: make(chan Result, 1), finished: make(chan struct{}),
		state: pipelineBetween, running: -1, last: -1,
		event: event, clients: pipelineClients(p),
	}
	go wait.run(p, event)
	return wait
}

type pipelineStep struct {
	client *commandClient
	phase1 bool
}

func (w *pipelineWait) run(p *pipeline, event Event) {
	steps := make([]pipelineStep, 0, len(p.entries)+1)
	if p.compat != nil && !p.compatWaiting {
		steps = append(steps, pipelineStep{client: p.compat, phase1: true})
	}
	for _, entry := range p.entries {
		steps = append(steps, pipelineStep{client: entry, phase1: true})
	}
	if p.compat != nil && p.compatWaiting {
		steps = append(steps, pipelineStep{client: p.compat})
	}

	stageOneBlock := false
	for index, step := range steps {
		if !w.startEntry(step.client.entry) {
			return
		}
		response, err := step.client.call(w.ctx, event, true)
		logStopFailure(w.ctx, event, step.client.entry, err)
		callPipelinePoint(pointEntryExited, step.client.entry)

		w.mu.Lock()
		w.recordLocked(response, err)
		w.last = step.client.entry
		if step.phase1 && hasBlock(response, err) {
			stageOneBlock = true
		}

		// R0 is deliberately checked in this same critical section as the
		// command result. A cancel after this point cannot rewrite a finished R1.
		if w.frozen || w.ctx.Err() != nil {
			w.finishLocked(Result{Response: w.mergeLocked("", ""), Err: contextError(w.ctx, w.frozen)}, step.client.entry)
			return
		}
		if step.phase1 && index == len(steps)-1 {
			if stageOneBlock {
				w.finishLocked(Result{Response: w.mergeLocked("block", w.blockReasonLocked())}, step.client.entry)
			} else {
				w.finishLocked(Result{Response: w.mergeLocked("", "")}, step.client.entry)
			}
			return
		}
		if !step.phase1 {
			// The only phase-two step is the compatible waiting entry; its own
			// response and error are the R2 result.
			w.finishLocked(Result{Response: w.mergeLocked(response.Decision, response.Reason), Err: err}, step.client.entry)
			return
		}
		if step.phase1 && stageOneBlock && (index+1 == len(steps) || !steps[index+1].phase1) {
			// Every immediate entry has already run; a block skips phase two.
			w.finishLocked(Result{Response: w.mergeLocked("block", w.blockReasonLocked())}, step.client.entry)
			return
		}
		w.state = pipelineBetween
		w.mu.Unlock()
	}

	// A pipeline always has at least one configured entry, but retain a safe
	// empty completion for a malformed in-memory construction.
	w.mu.Lock()
	w.finishLocked(Result{Response: w.mergeLocked("", "")}, -1)
}

// startEntry performs the second half of the between state transition. A frozen
// pipeline is finalized without starting the entry that was pending.
func (w *pipelineWait) startEntry(entry int) bool {
	callPipelinePoint(pointBeforeStartNext, entry)
	w.mu.Lock()
	defer func() {
		if w.state != pipelineFinished {
			w.mu.Unlock()
		}
	}()
	if w.frozen || w.ctx.Err() != nil {
		w.finishLocked(Result{Response: w.mergeLocked("", ""), Err: contextError(w.ctx, w.frozen)}, entry)
		return false
	}
	w.state = pipelineRunning
	w.running = entry
	return true
}

func (w *pipelineWait) recordLocked(response Response, err error) {
	if len(response.Runs) > 0 {
		w.runs = append(w.runs, response.Runs...)
	}
	// commandClient.call always supplies Runs. Keep the state machine robust if a
	// future client implementation returns a response without a run record.
	if len(response.Runs) == 0 {
		w.runs = append(w.runs, EntryRun{Entry: w.running, Response: response, Err: err})
	}
}

func (w *pipelineWait) mergeLocked(decision, reason string) Response {
	response := Response{}
	contexts := make([]string, 0, len(w.runs))
	notices := make([]string, 0, len(w.runs))
	for _, run := range w.runs {
		if value := strings.TrimSpace(run.Response.AdditionalContext); value != "" {
			contexts = append(contexts, value)
		}
		if value := strings.TrimSpace(run.Response.Notice); value != "" {
			notices = append(notices, value)
		}
	}
	response.AdditionalContext = strings.Join(contexts, "\n\n")
	response.Notice = strings.Join(notices, "\n")
	response.Runs = append([]EntryRun(nil), w.runs...)
	if decision == "block" && strings.TrimSpace(reason) != "" {
		response.Decision = "block"
		response.Reason = reason
	}
	return response
}

func (w *pipelineWait) blockReasonLocked() string {
	reasons := make([]string, 0, len(w.runs))
	for _, run := range w.runs {
		if run.Err == nil && run.Response.Decision == "block" && strings.TrimSpace(run.Response.Reason) != "" {
			reasons = append(reasons, strings.TrimSpace(run.Response.Reason))
		}
	}
	return strings.Join(reasons, "\n\n")
}

func hasBlock(response Response, err error) bool {
	return err == nil && response.Decision == "block" && strings.TrimSpace(response.Reason) != ""
}

func contextError(ctx context.Context, frozen bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if frozen {
		return context.Canceled
	}
	return nil
}

// finishLocked changes state while holding the state mutex, then performs the
// completion hook and channel operations without the mutex.
func (w *pipelineWait) finishLocked(result Result, entry int) {
	w.state = pipelineFinished
	w.result = result
	w.mu.Unlock()
	callPipelinePoint(pointBeforeDone, entry)
	w.done <- result
	close(w.done)
	close(w.finished)
}

func (w *pipelineWait) Done() <-chan Result { return w.done }

// Interrupt freezes the state machine before sending one Interrupt event. It
// waits on the private finished signal, never consuming Done itself.
func (w *pipelineWait) Interrupt(parent context.Context) string {
	w.mu.Lock()
	w.frozen = true
	target := -1
	switch w.state {
	case pipelineRunning:
		target = w.running
	case pipelineFinished:
		target = w.last
	}
	w.mu.Unlock()

	callPipelinePoint(pointInterruptFrozen, target)
	if target >= 0 {
		if client := w.client(target); client != nil {
			interrupt := w.event
			interrupt.Name = "Interrupt"
			interrupt.LastAssistant = ""
			interrupt.StopHookActive = false
			// The Stop event fields are supplied by commandClient's original event
			// through the caller's event in StartStop; only these fields matter to the
			// interrupt contract, and the client receives the session environment.

			ctx, cancel := context.WithTimeout(parent, time.Second)
			client.Call(ctx, interrupt)
			cancel()
		}
	}
	w.cancel()
	<-w.finished

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.blockReasonLocked()
}

func (w *pipelineWait) client(entry int) *commandClient {
	return w.clients[entry]
}

func pipelineClients(p *pipeline) map[int]*commandClient {
	clients := make(map[int]*commandClient, len(p.entries)+1)
	if p.compat != nil {
		clients[p.compat.entry] = p.compat
	}
	for _, entry := range p.entries {
		clients[entry.entry] = entry
	}
	return clients
}

// logStopFailure reports a Stop entry that failed. Cancellation and a reached
// bound are expected ways for a Stop to end, so they stay at debug level.
func logStopFailure(ctx context.Context, event Event, entry int, err error) {
	switch {
	case err == nil:
	case ctx.Err() != nil, errors.Is(err, ErrTimedOut):
		slog.Debug("stop hook ended without a reply", "event", event.Name, "err", err)
	default:
		slog.Warn("hook command failed", "event", event.Name, "entry", entry, "err", err)
	}
}

// PostToolName reports the PostToolUse event of the compatible entry.
func (p *pipeline) PostToolName(tool string) (string, bool) {
	if p.compat == nil {
		return "", false
	}
	return p.compat.PostToolName(tool)
}

// PreToolName reports the PreToolUse event of the compatible entry.
func (p *pipeline) PreToolName(tool string) (string, bool) {
	if p.compat == nil {
		return "", false
	}
	return p.compat.PreToolName(tool)
}

// PreTool asks the compatible entry about one call; without one the pipeline
// has no objection and the ordinary approval decides.
func (p *pipeline) PreTool(ctx context.Context, event Event) (Response, error) {
	if p.compat == nil {
		return Response{}, nil
	}
	return p.compat.PreTool(ctx, event)
}
