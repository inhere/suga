package acpserver

import "github.com/inhere/suga/internal/i18n"

// ServeOption configures the agent Serve builds; the host assembly passes the
// startup-resolved catalog it resolved once, so client-facing notices read the
// same language as the rest of the process.
type ServeOption func(*Agent)

// WithTranslator installs the host UI catalog of the ACP server. Without it the
// historical Chinese text stays (compatibility runtimes built by tests).
func WithTranslator(tr i18n.Translator) ServeOption {
	return func(a *Agent) { a.setTranslator(tr) }
}

// trOrFallback keeps the historical Chinese text when no catalog was injected
// (a runtime opened without wiring, or a test runtime).
func trOrFallback(tr i18n.Translator) i18n.Translator {
	if tr == nil {
		return i18n.Fallback()
	}
	return tr
}

// setTranslator installs the catalog the agent's own notices use (session/list
// hints, session/load notes). It is called at serving time and from every
// runtime the agent opens, so a session/list before the first session already
// speaks the configured language.
func (a *Agent) setTranslator(tr i18n.Translator) {
	if tr == nil {
		return
	}
	a.mu.Lock()
	a.translator = tr
	a.mu.Unlock()
}

// tr is the catalog of this agent (must not be called with mu held).
func (a *Agent) tr() i18n.Translator {
	a.mu.Lock()
	defer a.mu.Unlock()
	return trOrFallback(a.translator)
}

// tr is the catalog of this prompt's runtime; server.go sets it right after the
// runtime opens (the hook exists before it, for the open callback itself).
func (h *permissionHook) tr() i18n.Translator { return trOrFallback(h.translator) }
