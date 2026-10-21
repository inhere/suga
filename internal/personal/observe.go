package personal

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/llm"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// Event statuses written to the events table.
const (
	eventOK     = "ok"
	eventError  = "error"
	eventDenied = "denied"
)

// maxEventError caps the error text kept on one event row.
const maxEventError = 2000

// eventSink is the slice of *store.Store the observer needs. It is an
// interface so tests can inject write failures.
type eventSink interface {
	AppendEvent(ctx context.Context, e store.Event) (store.Event, error)
}

type scopeKey struct{}

// observeScope identifies the turn whose work is being observed. It travels in
// ctx so provider and tool wrappers (which only see ctx) can attribute events.
type observeScope struct{ sessionID, turnID string }

// The session id is also handed to the llm providers for llm.headers values
// that use the {{session_id}} placeholder.
func withObserveScope(ctx context.Context, sessionID, turnID string) context.Context {
	ctx = api.WithSessionID(ctx, sessionID)
	return context.WithValue(ctx, scopeKey{}, observeScope{sessionID: sessionID, turnID: turnID})
}

type purposeKey struct{}

// purposeCompact marks model calls made for context compaction, so the eval
// can tell them from the main-loop calls (event attrs "purpose").
const purposeCompact = "compact"

// withPurpose tags provider calls made with the returned ctx.
func withPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, purposeKey{}, purpose)
}

func purposeFrom(ctx context.Context) string {
	p, _ := ctx.Value(purposeKey{}).(string)
	return p
}

func observeScopeFrom(ctx context.Context) observeScope {
	s, _ := ctx.Value(scopeKey{}).(observeScope)
	return s
}

// recorder writes observability events best-effort: a failed write is logged
// and never affects the turn. Events are written outside any entry transaction.
type recorder struct {
	sink eventSink
	// price looks up a configured model price; nil means no pricing.
	price func(provider, model string) (Price, bool)
	// taint flags the session when a tool result brings in external
	// content; nil disables it.
	taint *taintMarker
	// prefix compares each request with the session's previous one; its
	// report goes on the model event (cache attribution).
	prefix *prefixTracker
	// workspace is shortened to <ws> in recorded argument summaries.
	workspace string
	// vision is the live capability of the current model: view_image leaves the
	// tool table of a model that cannot see images (a runtime lists the tools at
	// every step, so a :model switch takes effect on the next one).
	vision *tools.VisionGate
}

func (r *recorder) record(ctx context.Context, e store.Event) {
	if r == nil || r.sink == nil {
		return
	}
	scope := observeScopeFrom(ctx)
	if e.SessionID == "" {
		e.SessionID = scope.sessionID
	}
	if e.TurnID == "" {
		e.TurnID = scope.turnID
	}
	if e.SessionID == "" {
		return // not inside a turn; nothing to attribute the event to
	}
	if len(e.Error) > maxEventError {
		e.Error = e.Error[:maxEventError]
	}
	// A cancelled turn must still persist its events.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := r.sink.AppendEvent(wctx, e); err != nil {
		slog.Warn("record observability event", "kind", e.Kind, "name", e.Name, "session_id", e.SessionID, "err", err)
	}
}

// attemptObserver turns a provider attempt into a model event. defModel and
// defName fill in what the provider itself does not report.
func (r *recorder) attemptObserver(defName, defModel string) llm.AttemptObserver {
	return func(ctx context.Context, a llm.Attempt) {
		name, model := a.Provider, a.Model
		if defName != "" && (name == "" || name == "pool") {
			name = defName
		}
		if model == "" {
			model = defModel
		}
		e := store.Event{
			Kind: store.EventModel, Name: name, Status: eventOK,
			StartedAt: a.StartedAt, DurationMs: a.Duration.Milliseconds(), TTFTMs: ttftMillis(a.TTFT),
			// Provider-normalized: InputTokens includes cache (see ReportUsage).
			InputTokens: int64(a.Usage.InputTokens), OutputTokens: int64(a.Usage.OutputTokens),
			CacheReadTokens: int64(a.Usage.CacheReadTokens), CacheWriteTokens: int64(a.Usage.CacheWriteTokens),
			Attrs: map[string]any{"model": model, "attempt": a.Index, "streamed": a.Streamed,
				"tool_calls": a.ToolCalls, "reasoning_chars": a.ReasoningChars, "text_chars": a.TextChars},
		}
		if a.Finish != "" {
			e.Attrs["finish"] = a.Finish
		}
		if a.EmptyFinal {
			e.Attrs[api.EmptyFinalMetaKey] = true
		}
		if a.OutputLimit > 0 {
			// How many output tokens the request allowed: names the limit of a
			// reply the model cut off (finish=truncated).
			e.Attrs["output_limit"] = a.OutputLimit
		}
		if a.Retries > 0 { // transient failures the provider retried (llm.RetryPolicy)
			e.Attrs["retries"], e.Attrs["retry_wait_ms"], e.Attrs["retry_reason"] = a.Retries, a.RetryWait.Milliseconds(), a.RetryReason
		}
		if a.RequestID != "" {
			e.Attrs["request_id"] = a.RequestID
		}
		if a.Estimated {
			e.Attrs["estimated"] = true
			if a.EstimatedInputTokens > 0 {
				e.Attrs["est_input_tokens"] = a.EstimatedInputTokens
			}
			if a.EstimatedOutputTokens > 0 {
				e.Attrs["est_output_tokens"] = a.EstimatedOutputTokens
			}
		}
		if a.Fallback != "" { // what the pool did with this attempt's failure
			e.Attrs["fallback"] = a.Fallback
		}
		if p := purposeFrom(ctx); p != "" { // ordinary calls carry no purpose
			e.Attrs["purpose"] = p
		} else if rep, ok := r.prefix.report(observeScopeFrom(ctx).sessionID); ok {
			// How this request relates to the previous one (cache attribution).
			e.Attrs["prefix"] = rep.Reason
			e.Attrs["prefix_reused"], e.Attrs["prefix_rebuilt"], e.Attrs["prefix_fresh"] = rep.Reused, rep.Rebuilt, rep.Fresh
		}
		// Provider-reported cost wins; otherwise price the call from config.
		// The store's session/stats totals sum this attr.
		cost := costOf(r.price, name, model, a.Usage)
		if cost != 0 {
			e.Attrs["cost_usd"] = cost
		}
		if a.Err != nil {
			e.Status, e.Error = eventError, a.Err.Error()
			if reset := llm.ResetAt(a.Err); reset != "" {
				e.Attrs["reset_at"] = reset
			}
		}
		r.record(ctx, e)
		// The run-scoped total (stream-json "usage" lines, ACP usage_update)
		// counts exactly what this model event recorded, so it covers
		// compaction calls and failed attempts that reported usage.
		priced := a.Usage
		priced.CostUSD = cost
		runUsageFrom(ctx).addModelWithEstimates(ctx, a.Usage, priced, a.Err != nil, a.Estimated, a.EstimatedInputTokens, a.EstimatedOutputTokens)
	}
}

// observedProvider reports every Generate call as attempt 1.
type observedProvider struct {
	inner agentcore.Provider
	obs   llm.AttemptObserver
}

func (p *observedProvider) Name() string { return p.inner.Name() }

// SupportsToolChoice reports the wrapped provider's capability: the runtime
// must get the same answer with and without observation.
func (p *observedProvider) SupportsToolChoice() bool {
	chooser, ok := p.inner.(agentcore.ToolChooser)
	return ok && chooser.SupportsToolChoice()
}

func (p *observedProvider) Generate(ctx context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	return llm.GenerateObserved(ctx, p.inner, req, 1, p.obs)
}

// observedStreamingProvider additionally implements Stream. It is only used
// when the inner provider streams, so the runtime's StreamingProvider type
// assertion gives the same answer with and without observation.
type observedStreamingProvider struct {
	observedProvider
	stream agentcore.StreamingProvider
}

func (p *observedStreamingProvider) Stream(ctx context.Context, req *agentcore.GenerateRequest, emit agentcore.StreamFunc) (*agentcore.GenerateResult, error) {
	return llm.StreamObserved(ctx, p.stream, req, emit, 1, p.obs)
}

// observeProvider makes provider calls emit model events. A provider that can
// report its own attempts (llm.Pool, with per-fallback attempts) gets the
// observer installed instead of being wrapped.
func (r *recorder) observeProvider(p agentcore.Provider, name, model string) agentcore.Provider {
	obs := r.attemptObserver(name, model)
	if settable, ok := p.(interface{ SetObserver(llm.AttemptObserver) }); ok {
		settable.SetObserver(obs)
		return p
	}
	base := observedProvider{inner: p, obs: obs}
	if s, ok := p.(agentcore.StreamingProvider); ok {
		return &observedStreamingProvider{observedProvider: base, stream: s}
	}
	return &base
}

// errToolWithoutVision refuses view_image for a model that cannot see images.
var errToolWithoutVision = errors.New("view_image: current model cannot see images: switch to a model with vision (chat: :model)")

// observedTools is the outermost tool-registry layer (observe -> visibility ->
// approval -> base). It records a tool event per call and a denial event when
// an inner layer refused the call with tools.ErrDenied.
type observedTools struct {
	inner agentcore.ToolRegistry
	rec   *recorder
}

func (r *recorder) observeTools(inner agentcore.ToolRegistry) agentcore.ToolRegistry {
	if inner == nil {
		return nil
	}
	return &observedTools{inner: inner, rec: r}
}

func (o *observedTools) ListTools(ctx context.Context) ([]agentcore.ToolSchema, error) {
	schemas, err := o.inner.ListTools(ctx)
	if err != nil || o.rec.vision.CanSee() {
		return schemas, err
	}
	// A model without vision does not get the tool: showing it would only
	// invite a call that must fail (design 0.3, filter view_image by model).
	out := make([]agentcore.ToolSchema, 0, len(schemas))
	for _, schema := range schemas {
		if schema.Name == "view_image" {
			continue
		}
		out = append(out, schema)
	}
	return out, nil
}

// Get passes tool lookup through to the wrapped layers.
func (o *observedTools) Get(name string) (agentcore.Tool, error) {
	if l, ok := o.inner.(tools.Lookup); ok {
		return l.Get(name)
	}
	return nil, errors.New("tool lookup is not supported by the wrapped registry")
}

func (o *observedTools) ExecuteTool(ctx context.Context, call agentcore.ToolCall) (*agentcore.ToolResult, error) {
	start := time.Now()
	ctx, approval := tools.WithApprovalReport(ctx)
	ctx, snapshot := native.WithSnapshotReport(ctx)
	var (
		res *agentcore.ToolResult
		err error
	)
	if call.Name == "view_image" && !o.rec.vision.CanSee() {
		// A model switched away from vision mid-turn can still emit a cached call:
		// refuse it here instead of storing bytes the model cannot read.
		err = errToolWithoutVision
	} else {
		res, err = o.inner.ExecuteTool(ctx, call)
	}
	e := store.Event{
		Kind: store.EventTool, Name: call.Name, Status: eventOK, StartedAt: start,
		DurationMs: time.Since(start).Milliseconds(),
		Attrs:      map[string]any{"call_id": call.CallID},
	}
	switch {
	case err != nil && errors.Is(err, tools.ErrDenied):
		e.Kind, e.Status, e.Error = store.EventDenial, eventDenied, err.Error()
		// Feeds the per-turn denial limit and RunResult.DeniedTools.
		turnDenialsFrom(ctx).add(call.Name)
	case err != nil:
		e.Status, e.Error = eventError, err.Error()
	case res != nil && res.IsError:
		e.Status, e.Error = eventError, "tool reported an error result"
	}
	if err == nil && res != nil {
		addOutputAttrs(e.Attrs, res)
	}
	addCallAttrs(e.Attrs, call, res, o.rec.workspace)
	// Who let the call run (or refused it) and how long the person took:
	// the event's duration includes that wait.
	if approval.Source != "" {
		e.Attrs["approval"] = approval.Source
	}
	if approval.Rule != "" {
		e.Attrs["approval_rule"] = approval.Rule
	}
	if approval.Scope != "" {
		e.Attrs["approval_scope"] = approval.Scope
	}
	if approval.Wait > 0 {
		e.Attrs["approval_wait_ms"] = approval.Wait.Milliseconds()
	}
	if approval.Danger != "" {
		e.Attrs["danger"] = approval.Danger
	}
	if snapshot.Status != "" {
		e.Attrs["snapshot"] = snapshotEventAttrs(snapshot)
	}
	o.rec.record(ctx, e)
	// Every call reaching the registry counts, refused ones included.
	runUsageFrom(ctx).addTool()
	// Any returned result (IsError included) entered the context; refused
	// and failed calls carry only our own error text.
	if err == nil && res != nil && o.rec.taint != nil {
		tool, _ := o.Get(call.Name) // nil when unknown: no declared meta
		o.rec.taint.observe(ctx, tool, call.Name, res)
	}
	return res, err
}

// snapshotEventAttrs projects only the non-sensitive snapshot outcome. The
// before-image stays on disk and never enters event attrs, model context or
// transcript data.
func snapshotEventAttrs(report *native.SnapshotReport) map[string]any {
	return map[string]any{
		"kind": report.Kind, "status": report.Status, "bytes": report.Bytes,
		"unrecoverable": report.Unrecoverable,
	}
}

// addCallAttrs records what a tool call was about, for trace and analysis:
// arg (a redacted one-line summary of the key arguments, the workspace path
// shortened to <ws>), target (the file or directory paths of file tools),
// changes (the replacements of a file_edit call) and, for shell results,
// exit_code, stderr (the command wrote to stderr) and test_failed (a test
// command whose output reports a failure, whatever the exit code: a pipe or a
// later command in a ";" list hides a failing test's exit code).
func addCallAttrs(attrs map[string]any, call agentcore.ToolCall, res *agentcore.ToolResult, workspace string) {
	if summary := summarizeToolArgs(call.Arguments); summary != "" {
		attrs["arg"] = truncateRunes(redact.Secrets(shortenWorkspace(summary, workspace)), 160)
	}
	switch call.Name {
	case "file_edit":
		edits := []any{call.Arguments}
		if list, ok := call.Arguments["edits"].([]any); ok && len(list) > 0 {
			edits = list
		}
		attrs["changes"] = len(edits)
		added, removed := 0, 0
		for _, item := range edits {
			if edit, ok := item.(map[string]any); ok {
				removed += lineCount(edit["old_string"])
				added += lineCount(edit["new_string"])
			}
		}
		attrs["lines_added"], attrs["lines_removed"] = added, removed
	case "file_write":
		attrs["lines_added"] = lineCount(call.Arguments["content"])
	case "shell_exec":
		if command, ok := call.Arguments["command"].(string); ok && command != "" {
			// The command the check can match against (personal/nudge.go's
			// write rules name it check_command); capped at a sane length.
			attrs["command"] = truncateRunes(command, 500)
		}
	case "task":
		// The action and task id as attrs, so --analyze and the web page need
		// not parse them out of the argument summary.
		for _, key := range []string{"action", "id"} {
			if v, _ := call.Arguments[key].(string); v != "" {
				attrs[key] = v
			}
		}
	}
	var targets []string
	for _, key := range []string{"path", "dir"} {
		if p, ok := call.Arguments[key].(string); ok && p != "" {
			targets = append(targets, filepath.ToSlash(p))
		}
	}
	if list, ok := call.Arguments["paths"].([]any); ok {
		for _, item := range list {
			if p, ok := item.(string); ok && p != "" {
				targets = append(targets, filepath.ToSlash(p))
			}
		}
	}
	if len(targets) > 0 {
		attrs["target"] = targets
	}
	if res != nil {
		// shell_exec with background: true started a task (its end is a task event).
		if id, _ := res.Metadata["task_id"].(string); id != "" {
			attrs["task_id"] = id
		}
		// agent: the sub-agent session and what it spent; its report is prose,
		// never parsed for an exit code.
		if call.Name == "agent" {
			for _, key := range agentAttrKeys {
				if v, ok := res.Metadata[key]; ok {
					attrs[key] = v
				}
			}
			return
		}
		if m := exitCodePattern.FindAllStringSubmatch(res.Content, -1); len(m) > 0 {
			if code, err := strconv.Atoi(m[len(m)-1][1]); err == nil {
				attrs["exit_code"] = code
			}
			if strings.HasPrefix(res.Content, "[stderr]\n") || strings.Contains(res.Content, "\n[stderr]\n") {
				attrs["stderr"] = true
			}
			if command, _ := call.Arguments["command"].(string); native.TestCommandPattern().MatchString(command) {
				attrs["test_failed"] = testFailurePattern.MatchString(res.Content)
			}
		}
	}
}

// testFailurePattern finds a failure report in test output (go test, cargo,
// jest/vitest, pytest, maven, gradle), see addCallAttrs.
var testFailurePattern = regexp.MustCompile(`(?m)^\s*--- FAIL|^FAIL\b|^panic: |test result: FAILED|^\s*FAIL\s+\S|\b[1-9]\d* (failed|failing)\b|^(FAILED|ERROR) |BUILD FAILURE|BUILD FAILED|^\S+\.go:\d+:\d+: `)

// shortenWorkspace writes the workspace path (either slash form, any case on
// Windows paths) as <ws> in an argument summary.
func shortenWorkspace(s, workspace string) string {
	if workspace == "" {
		return s
	}
	lower := strings.ToLower(s)
	if len(lower) != len(s) {
		return s // case folding changed byte offsets (non-ASCII); leave it
	}
	for _, form := range []string{workspace, filepath.ToSlash(workspace), strings.ReplaceAll(workspace, "/", "\\")} {
		if i := strings.Index(lower, strings.ToLower(form)); i >= 0 && form != "" {
			s = s[:i] + "<ws>" + s[i+len(form):]
			return shortenWorkspace(s, workspace)
		}
	}
	return s
}

// agentAttrKeys are the agent tool's result metadata kept on its tool event.
var agentAttrKeys = []string{"child_session_id", "agent_type", "steps", "tool_calls", "input_tokens",
	"cache_read_tokens", "output_tokens", "cost_usd", "stop"}

// exitCodePattern finds shell_exec's "[exit code: N]" trailer.
var exitCodePattern = regexp.MustCompile(`\[exit code: (-?\d+)\]`)

// addOutputAttrs records the output size of a tool result: raw_bytes is what
// the tool produced, inline_bytes what entered the session, spilled whether
// the full text was saved under a ref. Results the output layer left alone
// have raw == inline.
func addOutputAttrs(attrs map[string]any, res *agentcore.ToolResult) {
	inline := len(res.Content)
	attrs["raw_bytes"], attrs["inline_bytes"], attrs["spilled"] = inline, inline, false
	if info, ok := tools.OutputInfoOf(res.Metadata); ok {
		attrs["raw_bytes"], attrs["spilled"] = info.Bytes, info.Ref != "" && !info.Minimized
		if info.Minimized {
			attrs["minimized"] = true
		}
	}
}

// ttftMillis rounds a measured sub-millisecond TTFT up to 1ms so "a first delta
// was seen" stays distinguishable from "no TTFT" (0).
func ttftMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return max(d.Milliseconds(), 1)
}

// HostEvent is an observability event produced by the host around the runner
// (chat's scheduler, for one) rather than by a turn.
type HostEvent struct {
	Kind, Name, Status string
	Duration           time.Duration
	Error              string
	Attrs              map[string]any
}

// RecordEvent writes a host event for sessionID outside any turn; best-effort
// like every event (a failed write is only logged).
func (r *Runner) RecordEvent(ctx context.Context, sessionID string, ev HostEvent) {
	r.rec.record(ctx, store.Event{SessionID: sessionID, Kind: ev.Kind, Name: ev.Name, Status: ev.Status,
		StartedAt: time.Now(), DurationMs: ev.Duration.Milliseconds(), Error: ev.Error, Attrs: ev.Attrs})
}

// lineCount is the number of lines of a string argument (0 for "").
func lineCount(v any) int {
	s, _ := v.(string)
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}
