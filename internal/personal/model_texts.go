package personal

import "github.com/inhere/suga/internal/i18n"

// Catalog keys of the model-facing wording this package injects into a
// request (i18n phase 1b, catalog/*/model.ini). They are kept apart from the
// UI entries because the values carry the format verbs of their line.
const (
	modelDeadlinePrefix    = "model.deadline.prefix"
	modelDeadlineOverdue   = "model.deadline.overdue"
	modelDeadlineNear      = "model.deadline.near"
	modelDeadlineRemaining = "model.deadline.remaining"
	modelBudgetCost        = "model.budget.cost"
	modelBudgetTokens      = "model.budget.tokens"
	// modelHandoffReuseScope is the clause of the reuse path's handoff
	// instruction that replaces the transcript: how far the summary reaches
	// (the tail is kept verbatim) and that the call must not go on working.
	modelHandoffReuseScope = "model.handoff.reuse_scope"
	// modelHandoffSection prefixes the catalog key of one handoff package
	// section's accepted heading names (model.handoff.section.<suffix>).
	modelHandoffSection = "model.handoff.section."
)

// modelTr returns the catalog the model-facing wording of a run is read from
// (the deadline hint and the budget wrap-up instruction). A run without an
// injected catalog gets the Chinese compatibility fallback, so `language: zh`
// and callers that inject nothing keep the historical text byte for byte.
func modelTr(tr i18n.Translator) i18n.Translator {
	if tr == nil {
		return i18n.Fallback()
	}
	return tr
}
