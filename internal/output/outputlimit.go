package output

import "github.com/inhere/suga/internal/i18n"

// OutputLimitNotice is the text-mode stderr line of a turn whose answer was cut
// off at the model's output token limit (personal.RunResult.Truncated): the
// answer just stops, and the reason (a limit the configuration may raise) is
// invisible in the text itself. limit is how many tokens were allowed; 0 means
// neither the configuration nor the provider named one (an OpenAI-compatible
// server's own default applied). tr is the startup-resolved host catalog.
//
// host.output.truncated* is this whole sentence with the remedy, not the chat
// status line's short segment (status.turn.truncated*, internal/chat/layout.go):
// the two are deliberately kept apart, since a status line shares its row with
// the other turn fields.
func OutputLimitNotice(tr i18n.Translator, limit int) string {
	t := trOrFallback(tr)
	fix := t.T("host.output.truncated_fix")
	if limit > 0 {
		return t.T("host.output.truncated", map[string]any{"limit": limit, "fix": fix})
	}
	return t.T("host.output.truncated_bare", map[string]any{"fix": fix})
}
