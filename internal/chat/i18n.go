package chat

import "github.com/inhere/suga/internal/i18n"

// trOf returns t, or the Chinese fallback catalog when no translator was
// injected (tests and callers built before the language setting existed). Run
// always injects the startup-resolved translator through Options.Translator.
func trOf(t i18n.Translator) i18n.Translator {
	if t == nil {
		return i18n.Fallback()
	}
	return t
}

// tr is the translator for the renderer's human-facing text.
func (r *Renderer) tr() i18n.Translator {
	if r == nil {
		return i18n.Fallback()
	}
	return trOf(r.opts.Translator)
}

// tr is the translator for the chat loop's human-facing text.
func (s *chatState) tr() i18n.Translator {
	if s == nil {
		return i18n.Fallback()
	}
	return trOf(s.translator)
}

// Catalog keys of the model-facing refusals of the PreToolUse hook
// (catalog/*/model.ini, i18n phase 1b). The denial is a tool error the model
// reads, so it follows the same language as the rest of the request.
const (
	modelHookPreToolError  = "model.hook.pretool_error"
	modelHookPreToolDenied = "model.hook.pretool_denied"
	modelHookDeniedDefault = "model.hook.pretool_denied_default"
)
