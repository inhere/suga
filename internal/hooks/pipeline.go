package hooks

import "context"

// pipeline runs the compatible hooks.command next to the hooks.commands entries.
// It exists as soon as one entry is configured; with only hooks.command
// configured, New keeps returning the single client itself.
//
// The methods below are still the skeleton of the pipeline: they delegate
// everything to the compatible entry (an empty answer without one). The
// multi-entry dispatch is filled in by the following changes.
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

// Call runs the events of the pipeline. The skeleton keeps the single client
// behaviour: only the compatible entry answers.
func (p *pipeline) Call(ctx context.Context, event Event) Response {
	if p.compat == nil {
		return Response{}
	}
	return p.compat.Call(ctx, event)
}

// StartStop runs the Stop event of the pipeline. The skeleton delegates to the
// compatible entry (an already finished wait without one).
func (p *pipeline) StartStop(ctx context.Context, event Event) Wait {
	if p.compat == nil {
		return finishedWait{}
	}
	return p.compat.StartStop(ctx, event)
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
