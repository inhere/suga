package output

import (
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
)

// trOrFallback keeps the historical Chinese notices when a caller did not pass
// the startup-resolved catalog (stream-json paths and old callers).
func trOrFallback(tr i18n.Translator) i18n.Translator {
	if tr == nil {
		return i18n.Fallback()
	}
	return tr
}

// WrapUpNotice is the text-mode stderr line of a turn whose answer was a
// wrap-up (result.WrapUp) rather than a normal one; it is empty for a normal
// answer. It names the real limit, because the answer itself is short for a
// reason a reader cannot see: without this line a wrapped-up answer is easily
// taken for a timeout or a finished task. stdout keeps the answer text only.
func WrapUpNotice(tr i18n.Translator, reason string, maxSteps int) string {
	t := trOrFallback(tr)
	switch reason {
	case "":
		return ""
	case personal.StopMaxSteps:
		if maxSteps > 0 {
			return t.T("host.wrapup.max_steps", map[string]any{"steps": maxSteps})
		}
		return t.T("host.wrapup.max_steps_bare")
	case personal.StopMaxTokens:
		return t.T("host.wrapup.max_tokens")
	case "max_cost_usd":
		return t.T("host.wrapup.max_cost")
	case personal.StopTimeout:
		return t.T("host.wrapup.timeout")
	default:
		return t.T("host.wrapup.reason", map[string]any{"reason": reason})
	}
}

// WrapUpTruncatedNotice calls out the less obvious combination where the
// budget's final wrap-up answer was itself cut by the per-request output cap.
func WrapUpTruncatedNotice(tr i18n.Translator, limit int) string {
	t := trOrFallback(tr)
	if limit > 0 {
		return t.T("host.wrapup.truncated", map[string]any{"limit": limit})
	}
	return t.T("host.wrapup.truncated_bare")
}
