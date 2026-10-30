package personal

import (
	"context"
	"log/slog"
	"sync"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// usageCollector sums the usage of every successful model call of one Run via
// the AfterModel hook. Unlike agentcore.RunResult.Usage it also covers turns
// that fail or are cancelled, and it never includes usage of an earlier
// invocation when a yielded run is resumed.
type usageCollector struct {
	agentcore.NopHook
	mu    sync.Mutex
	sum   agentcore.Usage
	calls int
	// lastInput is the input_tokens of the most recent model call; the context
	// builder uses it to estimate the next request size.
	lastInput int
	// est is the context builder's estimate of the most recent request (it
	// records it on every BuildMessages); haveEst is false before one.
	// finish reports it as the turn's context occupancy, so stream-json carries
	// the same figure the context trace event was written with.
	est     int
	haveEst bool
	// cost prices a call whose provider reported no cost (nil: leave as is).
	// It uses the turn's main model, so a fallback model's call is priced at
	// the main rate here; the per-attempt model events price it exactly.
	cost func(agentcore.Usage) float64
}

func (c *usageCollector) AfterModel(_ context.Context, event *agentcore.ModelEvent, _ *agentcore.Control) error {
	if event == nil || event.Result == nil {
		return nil
	}
	c.mu.Lock()
	usage := event.Result.Usage
	if usage.CostUSD == 0 && c.cost != nil {
		usage.CostUSD = c.cost(usage)
	}
	c.sum = c.sum.Add(usage)
	c.calls++
	c.lastInput = event.Result.Usage.InputTokens
	c.mu.Unlock()
	return nil
}

// lastInputTokens returns the latest call's input_tokens; ok is false before
// any call or when the provider reported none.
func (c *usageCollector) lastInputTokens() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastInput, c.lastInput > 0
}

// recordEstimate keeps the context builder's estimate of the request it just
// assembled (see contextBuilder.estimate).
func (c *usageCollector) recordEstimate(est int) {
	c.mu.Lock()
	c.est, c.haveEst = est, true
	c.mu.Unlock()
}

// lastEstimate returns the estimate of the most recent request; ok is false
// when no request was assembled with this collector.
func (c *usageCollector) lastEstimate() (int, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.est, c.haveEst
}

// snapshot returns the usage sum, the call count and the latest call's input tokens.
func (c *usageCollector) snapshot() (agentcore.Usage, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sum, c.calls, c.lastInput
}

// usageFromSum converts the store's per-session model-event total back to the
// provider-normalized shape (input includes cache), so ReportUsage subtracts the
// cache part exactly as for a single turn. Total follows the same convention as
// providers: input + output.
func usageFromSum(s store.Usage) agentcore.Usage {
	return agentcore.Usage{
		InputTokens: int(s.InputTokens), OutputTokens: int(s.OutputTokens),
		TotalTokens:     int(s.InputTokens + s.OutputTokens),
		CacheReadTokens: int(s.CacheReadTokens), CacheWriteTokens: int(s.CacheWriteTokens),
		CostUSD: s.CostUSD,
	}
}

// ReportedUsage is the usage shape emitted on the output layer (stream-json
// result/input_required/error lines and ACP usage_update), in Anthropic-style
// accounting: input_tokens excludes cache, cache tokens are
// reported separately and total = input + output + cache_read + cache_write.
type ReportedUsage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
}

// ReportUsage converts internal usage to the reported shape. The provider
// layer normalizes agentcore.Usage.InputTokens to INCLUDE cache read/write
// tokens (OpenAI prompt_tokens already contains cached_tokens; the Claude
// provider adds cache_read/cache_creation to input_tokens), so the cache part
// is subtracted here instead of changing the provider normalization.
func ReportUsage(u agentcore.Usage) ReportedUsage {
	input := max(u.InputTokens-u.CacheReadTokens-u.CacheWriteTokens, 0)
	return ReportedUsage{
		InputTokens: input, OutputTokens: u.OutputTokens,
		CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens,
		TotalTokens: input + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens,
		CostUSD:     u.CostUSD,
	}
}

// RunUsage is the run-scoped usage counted at the observe layer (design D8).
// Unlike usageCollector (runtime AfterModel hook) it sees every model attempt
// of the run: main turn calls, compaction calls and failed attempts that
// reported usage. A failed attempt without usage (e.g. a cancelled request) is
// not counted.
type RunUsage struct {
	// Usage is provider-normalized (input includes cache, see ReportUsage);
	// the cost of each attempt is the one written on its model event.
	Usage agentcore.Usage
	// ToolCalls counts the run's tool calls, refused ones included.
	ToolCalls int
}

// UsageUpdate is carried by the "usage" event (after each counted model call)
// and by "run_finished" (the final value).
type UsageUpdate struct {
	Run RunUsage
	// Session is the session-cumulative usage: earlier runs plus this run so far.
	Session agentcore.Usage
	// LastInputTokens is the input (cache included) of the model call behind a
	// "usage" event, i.e. that request's context size; 0 on run_finished.
	LastInputTokens int
	// Estimated is true when one or more counted model calls used estimated
	// provider usage.
	Estimated bool
	// Budget is the optional budget snapshot at this usage point.
	Budget *BudgetSnapshot
	// Context is the turn's context occupancy (nil: no request was assembled).
	// It is set on run_finished only, so stream-json can report it on the
	// turn_end and result lines.
	Context *ContextOccupancy
}

// ReportedRunUsage is the reported shape of RunUsage on the stream-json
// usage/turn_end/result/input_required/error lines and the ACP usage_update
// _meta. Accounting matches ReportedUsage (input_tokens excludes cache), but
// every field is always present and tool_calls is added.
type ReportedRunUsage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	ToolCalls        int     `json:"tool_calls"`
}

// ReportRunUsage converts a run total to the reported shape.
func ReportRunUsage(u RunUsage) ReportedRunUsage {
	rep := ReportUsage(u.Usage)
	return ReportedRunUsage{
		InputTokens: rep.InputTokens, OutputTokens: rep.OutputTokens,
		CacheReadTokens: rep.CacheReadTokens, CacheWriteTokens: rep.CacheWriteTokens,
		TotalTokens: rep.TotalTokens, CostUSD: rep.CostUSD, ToolCalls: u.ToolCalls,
	}
}

type runUsageKey struct{}

// runUsageMeter accumulates RunUsage for one Run. It travels in ctx so the
// observe wrappers (provider attempts, tool calls) can find it; outside a Run
// (e.g. a manual compaction) there is none and nothing is counted.
type runUsageMeter struct {
	// notifyMu serializes notifications so the "usage" events of one run are
	// emitted in counting order (monotonic for the reader).
	notifyMu sync.Mutex
	mu       sync.Mutex
	run      RunUsage
	// base is the session-cumulative usage before this run.
	base agentcore.Usage
	// onModel receives the update after each counted model call (nil: none).
	onModel func(context.Context, UsageUpdate)
	// parentSession is the session that started this run as a sub-agent:
	// every model call counted here counts in that session's run as well
	// (forwardChildUsage).
	parentSession string
	// parentBackground marks a background child. Background children still
	// forward usage, but they must not use the parent's budget to stop early.
	parentBackground bool
	// held is pending child usage moved into this run for stream/ACP accounting;
	// turn budgets subtract it until the pending-aware session policy is applied.
	held                  agentcore.Usage
	budget                BudgetSettings
	estimated             bool
	estimatedInputTokens  int
	estimatedOutputTokens int
}

type childRunKey struct{}
type childBackgroundKey struct{}

// withChildOf marks ctx as the start of a sub-agent run of parentSession: its
// meter forwards its model usage to that session (forwardChildUsage).
func withChildOf(ctx context.Context, parentSession string) context.Context {
	return context.WithValue(ctx, childRunKey{}, parentSession)
}

// withChildBackground marks whether a child is running as an asynchronous
// background task. The usage still belongs to the parent session, but the
// parent's soft budget only governs foreground children.
func withChildBackground(ctx context.Context, background bool) context.Context {
	return context.WithValue(ctx, childBackgroundKey{}, background)
}

func childOf(ctx context.Context) string {
	parent, _ := ctx.Value(childRunKey{}).(string)
	return parent
}

func childIsBackground(ctx context.Context) bool {
	background, _ := ctx.Value(childBackgroundKey{}).(bool)
	return background
}

// Sub-agent usage reaches the session that started it through the run in
// progress there (a sub-agent run synchronously inside a turn, or in the
// background while one runs); with none it is held until the session's next
// run starts. Sessions and their runs belong to this process, so the
// registry is process-wide (a sub-agent runs on a Runner of its own).
var (
	childUsageMu  sync.Mutex
	activeMeters  = map[string]*runUsageMeter{} // session id -> its run in progress
	pendingUsages = map[string]agentcore.Usage{}
)

// activateMeter registers m as sessionID's run in progress and takes the
// sub-agent usage held for the session into it; the returned func ends it.
// The held usage is already in the session's cumulative total (its model
// events are stored), so it moves from base to run instead of adding twice.
func activateMeter(sessionID string, m *runUsageMeter) func() {
	childUsageMu.Lock()
	activeMeters[sessionID] = m
	held, ok := pendingUsages[sessionID]
	delete(pendingUsages, sessionID)
	childUsageMu.Unlock()
	if ok {
		m.mu.Lock()
		m.run.Usage = m.run.Usage.Add(held)
		m.base = subUsage(m.base, held)
		m.held = held
		m.mu.Unlock()
	}
	return func() {
		childUsageMu.Lock()
		if activeMeters[sessionID] == m {
			delete(activeMeters, sessionID)
		}
		childUsageMu.Unlock()
	}
}

// forwardChildUsage counts a sub-agent's model call in parentSession's run in
// progress, or holds it for the session's next run.
func forwardChildUsage(ctx context.Context, parentSession string, priced agentcore.Usage, estimated ...bool) {
	if parentSession == "" {
		return
	}
	childUsageMu.Lock()
	m := activeMeters[parentSession]
	if m == nil {
		pendingUsages[parentSession] = pendingUsages[parentSession].Add(priced)
	}
	childUsageMu.Unlock()
	m.addChild(ctx, priced, estimated...)
}

// activeMeterFor returns the run currently active for a session. A foreground
// child uses this meter for its parent-budget check; a missing meter means the
// child is detached from an in-progress parent run and must not guess.
func activeMeterFor(sessionID string) *runUsageMeter {
	childUsageMu.Lock()
	defer childUsageMu.Unlock()
	return activeMeters[sessionID]
}

// subUsage is a minus b, field by field (never below zero).
func subUsage(a, b agentcore.Usage) agentcore.Usage {
	return agentcore.Usage{
		InputTokens: max(a.InputTokens-b.InputTokens, 0), OutputTokens: max(a.OutputTokens-b.OutputTokens, 0),
		TotalTokens: max(a.TotalTokens-b.TotalTokens, 0), CacheReadTokens: max(a.CacheReadTokens-b.CacheReadTokens, 0),
		CacheWriteTokens: max(a.CacheWriteTokens-b.CacheWriteTokens, 0), CostUSD: max(a.CostUSD-b.CostUSD, 0),
	}
}

func withRunUsage(ctx context.Context, m *runUsageMeter) context.Context {
	return context.WithValue(ctx, runUsageKey{}, m)
}

func runUsageFrom(ctx context.Context) *runUsageMeter {
	m, _ := ctx.Value(runUsageKey{}).(*runUsageMeter)
	return m
}

// usageUpdateLocked builds one transport update while m.mu is held.
func (m *runUsageMeter) usageUpdateLocked(lastInput int) UsageUpdate {
	update := UsageUpdate{Run: m.run, Session: m.base.Add(m.run.Usage), LastInputTokens: lastInput, Estimated: m.estimated}
	if m.budget.enabled() {
		snapshot := m.budgetSnapshotLocked()
		snapshot.EstimatedInputTokens = m.estimatedInputTokens
		snapshot.EstimatedOutputTokens = m.estimatedOutputTokens
		update.Budget = &snapshot
	}
	return update
}

func (m *runUsageMeter) usageUpdate() UsageUpdate {
	if m == nil {
		return UsageUpdate{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usageUpdateLocked(0)
}

// addModel counts one model attempt. raw is the usage the provider reported
// (used to skip failed attempts without usage); priced is raw with the cost
// recorded on the model event.
func (m *runUsageMeter) addModel(ctx context.Context, raw, priced agentcore.Usage, failed bool, estimated ...bool) {
	est := len(estimated) > 0 && estimated[0]
	m.addModelWithEstimates(ctx, raw, priced, failed, est, 0, 0)
}

// addModelWithEstimates records the provider estimate details before publishing
// the usage update, so the model and budget events share one observation.
func (m *runUsageMeter) addModelWithEstimates(ctx context.Context, raw, priced agentcore.Usage, failed, estimated bool, estimatedInput, estimatedOutput int) {
	if m == nil || (failed && raw == (agentcore.Usage{})) {
		return
	}
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	m.mu.Lock()
	if estimated {
		m.estimated = true
		if estimatedInput > 0 {
			m.estimatedInputTokens = estimatedInput
		}
		if estimatedOutput > 0 {
			m.estimatedOutputTokens = estimatedOutput
		}
	}
	m.run.Usage = m.run.Usage.Add(priced)
	update := m.usageUpdateLocked(raw.InputTokens)
	notify := m.onModel
	m.mu.Unlock()
	if notify != nil {
		notify(ctx, update)
	}
	forwardChildUsage(ctx, m.parentSession, priced, estimated)
}

// addChild counts a sub-agent's model call in this run (the session total
// includes the sub-agent sessions, see Store.SumUsageTree). The update
// carries no LastInputTokens: the sub-agent's request size is not this
// session's context size.
func (m *runUsageMeter) addChild(ctx context.Context, priced agentcore.Usage, estimated ...bool) {
	if m == nil {
		return
	}
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	m.mu.Lock()
	if len(estimated) > 0 && estimated[0] {
		m.estimated = true
	}
	m.run.Usage = m.run.Usage.Add(priced)
	update := m.usageUpdateLocked(0)
	notify := m.onModel
	m.mu.Unlock()
	if notify != nil {
		notify(ctx, update)
	}
}

// addTool counts one tool call (executed, failed or refused).
func (m *runUsageMeter) addTool() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.run.ToolCalls++
	m.mu.Unlock()
}

// snapshot returns the run total so far (zero for a nil meter).
func (m *runUsageMeter) snapshot() RunUsage {
	if m == nil {
		return RunUsage{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.run
}

// newRunUsageMeter starts the run-scoped meter of one turn: base is the
// session total before the turn, and every counted model call is published
// as a "usage" event. Emit failures are only logged, like tool events.
func (r *Runner) newRunUsageMeter(ctx context.Context, sessionID, turnID string, sink EmitFunc) *runUsageMeter {
	m := &runUsageMeter{base: r.withSessionUsage(ctx, sessionID, RunResult{}).SessionUsage, budget: r.cfg.Budget.normalized()}
	m.parentSession = childOf(ctx) // the session that started this sub-agent
	m.parentBackground = childIsBackground(ctx)
	if sink != nil {
		m.onModel = func(ctx context.Context, u UsageUpdate) {
			event := Event{Kind: "usage", SessionID: sessionID, TurnID: turnID, RunID: turnID, Usage: &u, Budget: u.Budget}
			if err := emit(ctx, sink, event); err != nil {
				slog.Warn("emit usage event", "session_id", sessionID, "err", err)
			}
		}
	}
	return m
}
