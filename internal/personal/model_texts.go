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
