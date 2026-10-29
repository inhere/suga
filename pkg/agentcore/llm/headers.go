package llm

import (
	"context"
	"net/http"
	"strings"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// DefaultUserAgent identifies suga to model APIs; llm.headers can override it.
const DefaultUserAgent = "suga"

// Placeholders of an llm.headers value (providers.<id>.headers). A header whose
// value uses a placeholder with no value (a request outside a session or a turn)
// is left out, so a gateway never receives an empty routing value.
const (
	// SessionIDPlaceholder is the suga session id, e.g. a gateway's
	// per-conversation routing header.
	SessionIDPlaceholder = "{{session_id}}"
	// TurnIDPlaceholder is the current turn id ("" outside a turn).
	TurnIDPlaceholder = "{{turn_id}}"
	// ModelPlaceholder is the model id the request is sent with.
	ModelPlaceholder = "{{model}}"
	// ProviderPlaceholder is the configured provider id.
	ProviderPlaceholder = "{{provider}}"
)

// headerVars are the placeholder values that do not come from ctx: the provider
// knows its own model and id.
type headerVars struct {
	Model    string
	Provider string
}

// WithSessionID marks ctx with the suga session the model call belongs to.
//
// Deprecated: use agentcoreapi.WithSessionID. The session id belongs to the
// runtime contract, not to the provider layer (the tasks manager reads it
// too), so the key moved to api; this wrapper forwards to it and stays for
// existing embedders.
func WithSessionID(ctx context.Context, id string) context.Context {
	return agentcoreapi.WithSessionID(ctx, id)
}

// SessionIDFrom returns the session id set by WithSessionID, or "".
//
// Deprecated: use agentcoreapi.SessionIDFrom.
func SessionIDFrom(ctx context.Context) string { return agentcoreapi.SessionIDFrom(ctx) }

// applyHeaders sets the default User-Agent and then the configured extra
// headers, expanding the placeholders of each value. A header that needs a
// placeholder this request cannot fill is skipped.
func applyHeaders(ctx context.Context, req *http.Request, headers map[string]string, vars headerVars) {
	req.Header.Set("User-Agent", DefaultUserAgent)
	values := map[string]string{
		SessionIDPlaceholder: agentcoreapi.SessionIDFrom(ctx),
		TurnIDPlaceholder:    agentcoreapi.TurnIDFrom(ctx),
		ModelPlaceholder:     vars.Model,
		ProviderPlaceholder:  vars.Provider,
	}
	for name, value := range headers {
		expanded, ok := expandHeaderValue(value, values)
		if !ok {
			continue
		}
		req.Header.Set(name, expanded)
	}
}

// expandHeaderValue replaces every placeholder in value; ok is false when one of
// them has no value for this request.
func expandHeaderValue(value string, values map[string]string) (string, bool) {
	for placeholder, replacement := range values {
		if !strings.Contains(value, placeholder) {
			continue
		}
		if replacement == "" {
			return "", false
		}
		value = strings.ReplaceAll(value, placeholder, replacement)
	}
	return value, true
}
