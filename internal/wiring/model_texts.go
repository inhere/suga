package wiring

import (
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// This file resolves the model-facing wording of a runtime (i18n phase 1b,
// T14). The strings a model reads come from catalog/*/model.ini, kept apart
// from the UI entries: language: zh (and a run that injects no catalog at all)
// yields exactly the historical Chinese text, so its requests stay
// byte-identical, and language: en yields the English entries. The agentcore
// packages never read the catalog themselves (pkg/agentcore cannot import
// internal/i18n); the host injects the resolved values as plain strings, which
// are then part of the request prefix exactly like the Chinese ones were.
//
// English values carry the format verbs of their line: a translation must keep
// %d, %s, %v, %q and %w, and it never renames a tool, an action or a ref marker
// (those are the model's protocol).

// resolveToolTexts reads the tool-stack wording (the human-approved note and
// the output ref/condensed headers) for the run's language.
func resolveToolTexts(tr i18n.Translator) tools.ModelTexts {
	t := approvalTr(tr)
	return tools.ModelTexts{
		ApprovedNote:       t.T("model.approved_note"),
		OutputSavedRef:     t.T("model.output.saved_ref"),
		OutputCondensed:    t.T("model.output.condensed"),
		OutputCondensedRef: t.T("model.output.condensed_ref"),
	}
}

// resolveMemoryTexts reads the refusals and errors of the memory tool for the
// run's language.
func resolveMemoryTexts(tr i18n.Translator) native.MemoryTexts {
	t := approvalTr(tr)
	return native.MemoryTexts{
		ReadOnly:          t.T("model.memory.read_only"),
		NeedsConfirmation: t.T("model.memory.needs_confirmation"),
		Denied:            t.T("model.memory.denied"),
		Tombstoned:        t.T("model.memory.tombstoned"),
		OverBudget:        t.T("model.memory.over_budget"),
		NotFound:          t.T("model.memory.not_found"),
		NoBackend:         t.T("model.memory.no_backend"),
		Invalid:           t.T("model.memory.invalid"),
		Failed:            t.T("model.memory.failed"),
	}
}
