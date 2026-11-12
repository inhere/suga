package hooks

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

// pipeline runs the compatible hooks.command next to the hooks.commands entries.
// It exists as soon as one entry is configured; with only hooks.command
// configured, New keeps returning the single client itself.
//
// Call and the tool-name methods are complete: an ordinary event runs the
// compatible entry first and the entries in configuration order, while the tool
// events belong to the compatible entry alone. StartStop is still the skeleton
// (every entry in order, no waiting phase): the two-phase Stop and the interrupt
// handling replace it.
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
	// A context block is a paragraph of its own; a notice is one line each.
	merged.AdditionalContext = strings.Join(contexts, "\n\n")
	merged.Notice = strings.Join(notices, "\n")
	return merged
}

// StartStop runs the Stop event of the pipeline. Checkpoint implementation: it
// runs the compatible entry and then the entries in order, merges their answers
// and finishes. The waiting phase of a Stop that waits for its own bound and the
// interrupt state machine are the next change; until then a waiting compatible
// Stop is bounded by its own timeout like every other call.
func (p *pipeline) StartStop(ctx context.Context, event Event) Wait {
	waitCtx, cancel := context.WithCancel(ctx)
	wait := &checkpointWait{cancel: cancel, done: make(chan Result, 1)}
	go func() {
		answers := make([]Response, 0, len(p.entries)+1)
		var err error
		if p.compat != nil {
			var answer Response
			answer, err = p.compat.call(waitCtx, event, true)
			logStopFailure(waitCtx, event, p.compat.entry, err)
			answers = append(answers, answer)
		}
		for _, entry := range p.entries {
			answer, entryErr := entry.call(waitCtx, event, true)
			logStopFailure(waitCtx, event, entry.entry, entryErr)
			if err == nil {
				err = entryErr
			}
			answers = append(answers, answer)
		}
		wait.done <- Result{Response: mergeStopAnswers(answers), Err: err}
		close(wait.done)
	}()
	return wait
}

// mergeStopAnswers merges the Stop answers of the checkpoint pipeline: a block of
// any entry blocks, the reasons joined in execution order; the other fields merge
// like an ordinary event.
func mergeStopAnswers(answers []Response) Response {
	merged := mergePipelineAnswers(answers)
	reasons := make([]string, 0, len(answers))
	for _, answer := range answers {
		if answer.Decision == "block" && strings.TrimSpace(answer.Reason) != "" {
			reasons = append(reasons, strings.TrimSpace(answer.Reason))
		}
	}
	if len(reasons) > 0 {
		merged.Decision = "block"
		merged.Reason = strings.Join(reasons, "\n\n")
	}
	return merged
}

// logStopFailure reports a Stop entry that failed. A cancel and a reached bound
// are the expected ways a Stop ends (the person typed, chat exits, stop_wait_sec
// ran out), so they stay at debug level like the single client's Stop.
func logStopFailure(ctx context.Context, event Event, entry int, err error) {
	switch {
	case err == nil:
	case ctx.Err() != nil, errors.Is(err, ErrTimedOut):
		slog.Debug("stop hook ended without a reply", "event", event.Name, "err", err)
	default:
		slog.Warn("hook command failed", "event", event.Name, "entry", entry, "err", err)
	}
}

// PostToolName reports the PostToolUse event of the compatible entry: the
// entries get no tool events.
func (p *pipeline) PostToolName(tool string) (string, bool) {
	if p.compat == nil {
		return "", false
	}
	return p.compat.PostToolName(tool)
}

// PreToolName reports the PreToolUse event of the compatible entry: the entries
// get no tool events.
func (p *pipeline) PreToolName(tool string) (string, bool) {
	if p.compat == nil {
		return "", false
	}
	return p.compat.PreToolName(tool)
}

// PreTool asks the compatible entry about one call; without one the pipeline has
// no objection and the ordinary approval decides.
func (p *pipeline) PreTool(ctx context.Context, event Event) (Response, error) {
	if p.compat == nil {
		return Response{}, nil
	}
	return p.compat.PreTool(ctx, event)
}

// checkpointWait is the Stop wait of the checkpoint pipeline: it hands the merged
// result over once, and Interrupt cancels the remaining entries.
type checkpointWait struct {
	cancel context.CancelFunc
	done   chan Result
}

func (w *checkpointWait) Done() <-chan Result { return w.done }

func (w *checkpointWait) Interrupt(context.Context) string {
	w.cancel()
	return ""
}

// finishedWait is the Stop wait of a pipeline without a compatible entry: it is
// already done and answers nothing.
type finishedWait struct{}

func (finishedWait) Done() <-chan Result { return closedResult() }

func (finishedWait) Interrupt(context.Context) string { return "" }

// closedResult returns a channel already holding the empty result.
func closedResult() <-chan Result {
	done := make(chan Result, 1)
	done <- Result{}
	close(done)
	return done
}
