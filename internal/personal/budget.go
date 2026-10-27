package personal

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// BudgetSettings is the personal-layer form of the resolved budget settings.
// A zero value disables budget accounting and preserves the pre-budget path.
type BudgetSettings struct {
	Tokens         int
	CostUSD        float64
	Scope          string
	Estimate       string
	EstimateFactor float64
	NoticeRatio    float64
}

// Budget is kept as an alias for callers that describe the setting as a budget
// rather than a set of configuration values.
type Budget = BudgetSettings

// BudgetMetric is one budget dimension's limit and current usage.
type BudgetMetric struct {
	Limit     float64 `json:"limit"`
	Used      float64 `json:"used"`
	Remaining float64 `json:"remaining"`
}

// BudgetSnapshot is attached to usage updates and budget events. It is an
// optional observation object: old callers receive nil when no budget exists.
type BudgetSnapshot struct {
	Scope    string       `json:"scope"`
	Tokens   BudgetMetric `json:"tokens,omitempty"`
	CostUSD  BudgetMetric `json:"cost_usd,omitempty"`
	Exceeded bool         `json:"exceeded"`
	Reason   string       `json:"reason,omitempty"`
	// Parent identifies a child event caused by the foreground parent's budget,
	// rather than by the child's own agents.max_tokens limit.
	Parent bool `json:"parent,omitempty"`
	// NoticeRatio is the configured fraction at which chat should remind the user.
	NoticeRatio           float64 `json:"-"`
	Estimated             bool    `json:"estimated,omitempty"`
	EstimatedInputTokens  int     `json:"est_input_tokens,omitempty"`
	EstimatedOutputTokens int     `json:"est_output_tokens,omitempty"`
}

func (b BudgetSettings) enabled() bool { return b.Tokens > 0 || b.CostUSD > 0 }

func (b BudgetSettings) normalized() BudgetSettings {
	if !b.enabled() {
		return BudgetSettings{}
	}
	if b.Scope == "" {
		b.Scope = "turn"
	}
	if b.Estimate == "" {
		b.Estimate = "auto"
	}
	if b.EstimateFactor <= 0 || math.IsNaN(b.EstimateFactor) || math.IsInf(b.EstimateFactor, 0) {
		b.EstimateFactor = 1
	}
	return b
}

// budgetHook requests the existing soft wrap-up after a model attempt crosses
// either configured limit. It deliberately has no AfterTool check: agentcore
// only accepts run-level control at model boundaries.
type budgetHook struct {
	agentcore.NopHook
	settings       BudgetSettings
	meter          *runUsageMeter
	usage          *usageCollector
	priceAvailable bool
	sessionID      string
	turnID         string
	reported       bool
	warnedCost     bool
	warnedEstimate bool
	onBudget       func(context.Context, BudgetSnapshot, string)
	onWarning      func(context.Context, string)
	// tr is Config.Translator, the catalog the wrap-up instruction is read
	// from (i18n phase 1b); nil is the Chinese compatibility fallback.
	tr i18n.Translator
}

// BeforeModel short-circuits only the first model request of a session turn.
// A turn that crossed the limit in AfterModel still gets its normal wrap-up
// request; the usage collector distinguishes that case from an already-spent
// session entering a new turn.
func (h *budgetHook) BeforeModel(ctx context.Context, _ *agentcore.ModelEvent, ctrl *agentcore.Control) error {
	if h == nil || h.isChild() || !h.settings.enabled() || h.settings.Scope != "session" || h.meter == nil || h.usage == nil {
		return nil
	}
	if _, calls, _ := h.usage.snapshot(); calls > 0 {
		return nil
	}
	snapshot := h.meter.budgetSnapshot()
	if !snapshot.Exceeded || h.reported {
		return nil
	}
	h.reported = true
	reason := budgetReason(h.settings, snapshot)
	snapshot.Reason = reason
	if h.onBudget != nil {
		h.onBudget(ctx, snapshot, reason)
	}
	// Keep the user's input in state.Messages, but do not synthesize an
	// assistant message: the caller gets the ordinary wrap-up notice from the
	// result while the next turn sees a valid user-to-user continuation.
	ctrl.ShortCircuit(&agentcore.RunResult{Status: agentcore.RunCompleted, WrapUp: reason})
	return nil
}

func (h *budgetHook) AfterModel(ctx context.Context, event *agentcore.ModelEvent, ctrl *agentcore.Control) error {
	if h == nil || !h.settings.enabled() || h.meter == nil {
		return nil
	}
	if h.isChild() {
		if h.meter.parentBackground {
			return nil
		}
		if event != nil && event.Result != nil {
			h.warnIfNeeded(ctx, event.Result.Usage)
		}
		return h.afterForegroundChild(ctx, ctrl)
	}
	if event != nil && event.Result != nil {
		h.warnIfNeeded(ctx, event.Result.Usage)
	}
	snapshot := h.meter.budgetSnapshot()
	if !snapshot.Exceeded || h.reported {
		return nil
	}
	h.reported = true
	reason := budgetReason(h.settings, snapshot)
	snapshot.Reason = reason
	if h.onBudget != nil {
		h.onBudget(ctx, snapshot, reason)
	}
	ctrl.RequestWrapUp(reason, budgetWrapUpInstruction(h.tr, snapshot, reason))
	return nil
}

// isChild separates the parent's budget from agents.max_tokens. A child runner
// receives the parent settings so it can inspect the same limits, but it must
// not apply those settings to its own session or to a background task.
func (h *budgetHook) isChild() bool {
	return h.meter != nil && h.meter.parentSession != ""
}

// afterForegroundChild checks the active parent after every child model call.
// The parent runtime can only observe the forwarded usage at its next model
// boundary, so the child is the timely soft-stop point for foreground work.
func (h *budgetHook) afterForegroundChild(ctx context.Context, ctrl *agentcore.Control) error {
	if h.meter.parentBackground || h.reported {
		return nil
	}
	parent := activeMeterFor(h.meter.parentSession)
	if parent == nil || !parent.budget.enabled() {
		return nil
	}
	snapshot := parent.budgetSnapshot()
	if !snapshot.Exceeded {
		return nil
	}
	h.reported = true
	snapshot.Parent = true
	reason := budgetReason(parent.budget, snapshot)
	snapshot.Reason = reason
	if h.onBudget != nil {
		h.onBudget(ctx, snapshot, reason)
	}
	ctrl.RequestWrapUp(reason, budgetWrapUpInstruction(h.tr, snapshot, reason))
	return nil
}

func budgetReason(settings BudgetSettings, snapshot BudgetSnapshot) string {
	switch {
	case settings.Tokens > 0 && snapshot.Tokens.Used >= snapshot.Tokens.Limit:
		return "max_tokens"
	case settings.CostUSD > 0 && snapshot.CostUSD.Used >= snapshot.CostUSD.Limit:
		return "max_cost_usd"
	default:
		return ""
	}
}

func (h *budgetHook) warnIfNeeded(ctx context.Context, usage agentcore.Usage) {
	if h.settings.CostUSD > 0 && usage.CostUSD == 0 && !h.priceAvailable && !h.warnedCost {
		h.warnedCost = true
		h.warn(ctx, "成本预算已配置，但当前 provider/model 既未配置 price 也不返回 cost_usd，本次成本预算不会生效")
	}
	if h.settings.Estimate == "off" && usage.InputTokens == 0 && usage.OutputTokens == 0 && !h.warnedEstimate {
		h.warnedEstimate = true
		h.warn(ctx, "provider 不报用量且已关闭估算，本次预算按 0 计")
	}
}

func (h *budgetHook) warn(ctx context.Context, message string) {
	slog.Warn(message, "session_id", h.sessionID, "turn_id", h.turnID)
	if h.onWarning != nil {
		h.onWarning(ctx, message)
	}
}

func budgetWrapUpInstruction(tr i18n.Translator, snapshot BudgetSnapshot, reason string) string {
	t := modelTr(tr)
	if reason == "max_cost_usd" {
		return fmt.Sprintf(t.T(modelBudgetCost), snapshot.CostUSD.Limit, snapshot.CostUSD.Used)
	}
	return fmt.Sprintf(t.T(modelBudgetTokens), snapshot.Tokens.Limit, snapshot.Tokens.Used)
}

func (m *runUsageMeter) budgetSnapshot() BudgetSnapshot {
	if m == nil || !m.budget.enabled() {
		return BudgetSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.budgetSnapshotLocked()
}

func (m *runUsageMeter) budgetSnapshotLocked() BudgetSnapshot {
	settings := m.budget
	used := m.run.Usage
	if settings.Scope == "turn" {
		// Pending child usage is already present in the run for stream-json and
		// ACP, but it belongs to the previous turn for the turn budget.
		used = subUsage(used, m.held)
	} else {
		used = m.base.Add(m.run.Usage)
	}
	snapshot := BudgetSnapshot{Scope: settings.Scope, NoticeRatio: settings.NoticeRatio, Estimated: m.estimated}
	if settings.Tokens > 0 {
		spent := tokenSpend(used)
		snapshot.Tokens = BudgetMetric{Limit: float64(settings.Tokens), Used: float64(spent), Remaining: float64(max(settings.Tokens-spent, 0))}
		if spent >= settings.Tokens {
			snapshot.Exceeded = true
		}
	}
	if settings.CostUSD > 0 {
		remaining := math.Max(settings.CostUSD-used.CostUSD, 0)
		snapshot.CostUSD = BudgetMetric{Limit: settings.CostUSD, Used: used.CostUSD, Remaining: remaining}
		if used.CostUSD >= settings.CostUSD {
			snapshot.Exceeded = true
		}
	}
	return snapshot
}

func tokenSpend(u agentcore.Usage) int {
	return max(u.InputTokens-u.CacheReadTokens, 0) + u.OutputTokens
}

func budgetAttrs(snapshot BudgetSnapshot, reason string) map[string]any {
	attrs := map[string]any{
		"scope": snapshot.Scope, "exceeded": snapshot.Exceeded, "estimated": snapshot.Estimated,
		"reason": reason,
	}
	if snapshot.Parent {
		attrs["parent"] = true
	}
	if snapshot.Tokens.Limit > 0 {
		attrs["limit_tokens"] = snapshot.Tokens.Limit
		attrs["used_tokens"] = snapshot.Tokens.Used
		attrs["remaining_tokens"] = snapshot.Tokens.Remaining
	}
	if snapshot.CostUSD.Limit > 0 {
		attrs["limit_cost_usd"] = snapshot.CostUSD.Limit
		attrs["used_cost_usd"] = snapshot.CostUSD.Used
		attrs["remaining_cost_usd"] = snapshot.CostUSD.Remaining
	}
	if snapshot.EstimatedInputTokens > 0 {
		attrs["est_input_tokens"] = snapshot.EstimatedInputTokens
	}
	if snapshot.EstimatedOutputTokens > 0 {
		attrs["est_output_tokens"] = snapshot.EstimatedOutputTokens
	}
	return attrs
}

func budgetWarningAttrs(snapshot BudgetSnapshot, message string) map[string]any {
	attrs := budgetAttrs(snapshot, "warning")
	attrs["message"] = message
	return attrs
}

func budgetEvent(sessionID, turnID, name string, snapshot BudgetSnapshot, reason string) store.Event {
	return store.Event{SessionID: sessionID, TurnID: turnID, Kind: store.EventBudget, Name: name,
		Status: eventOK, StartedAt: time.Now(), Attrs: budgetAttrs(snapshot, reason)}
}

func budgetWarningEvent(sessionID, turnID string, snapshot BudgetSnapshot, message string) store.Event {
	return store.Event{SessionID: sessionID, TurnID: turnID, Kind: store.EventBudget, Name: "warning",
		Status: eventOK, StartedAt: time.Now(), Attrs: budgetWarningAttrs(snapshot, message)}
}
