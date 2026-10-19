package llm

import (
	"net/url"
	"sort"
	"strings"
)

// Compat describes how a gateway or a model family departs from the plain
// OpenAI API. Each adapter lives in its own compat_<name>.go file and is
// matched by the base URL host and the model id (ResolveCompat), so the
// request builders never name a gateway; a new gateway is a new file.
type Compat struct {
	// Names lists the adapters applied, for logs and the manifest.
	Names []string
	// ReasoningSummary asks the Responses API for a reasoning summary
	// (reasoning.summary "auto").
	ReasoningSummary bool
	// EncryptedReasoning asks the Responses API for the encrypted reasoning
	// (include reasoning.encrypted_content), sent back on later requests.
	EncryptedReasoning bool
	// ReasoningEveryTurn: in thinking mode every assistant turn must carry a
	// reasoning item with its text when replayed (DeepSeek rejects a turn
	// without it); a missing one is filled in.
	ReasoningEveryTurn bool
	// NoToolChoice leaves tool_choice out of the request and makes the
	// provider report no ToolChooser support (the runtime skips its tool-less
	// wrap-up retry then).
	NoToolChoice bool
	// MaxOutputTokens caps the output when the request sets no limit; 0
	// sends none.
	MaxOutputTokens int
	// CacheHitField names a usage field with the cached prompt tokens, read
	// when the standard one is missing or 0.
	CacheHitField string
	// ToolOutputImages sends a tool result's pictures inside the Responses
	// function_call_output (the API accepts an array of input_text and
	// input_image there). Without it the pictures follow in a user item after
	// the tool batch, which is what the gateways accept.
	ToolOutputImages bool
}

// compatAdapter is one registered adapter (see the compat_*.go files).
type compatAdapter struct {
	name string
	// match reports whether the adapter applies to a host (lower case, no
	// port) and model id (lower case); nil = only when named explicitly.
	match func(host, model string) bool
	apply func(c *Compat)
}

var compatAdapters []compatAdapter

// registerCompat adds an adapter; called from the compat_*.go files' init.
func registerCompat(a compatAdapter) { compatAdapters = append(compatAdapters, a) }

// ResolveCompat returns the adapters matching baseURL and model, plus the
// ones named in extra (providers.<id>.compat), applied in name order so the
// result does not depend on registration order. Unknown names are ignored.
func ResolveCompat(baseURL, model string, extra ...string) Compat {
	host := ""
	if u, err := url.Parse(strings.TrimSpace(baseURL)); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	model = strings.ToLower(model)
	named := map[string]bool{}
	for _, n := range extra {
		named[strings.ToLower(strings.TrimSpace(n))] = true
	}
	var chosen []compatAdapter
	for _, a := range compatAdapters {
		if named[a.name] || (a.match != nil && a.match(host, model)) {
			chosen = append(chosen, a)
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].name < chosen[j].name })
	var c Compat
	for _, a := range chosen {
		a.apply(&c)
		c.Names = append(c.Names, a.name)
	}
	return c
}

// cacheHit returns the cached prompt tokens: the standard count, else the
// compat field from the raw usage object.
func (c Compat) cacheHit(standard int, usage map[string]any) int {
	if standard > 0 || c.CacheHitField == "" {
		return standard
	}
	if v, ok := usage[c.CacheHitField].(float64); ok && v > 0 {
		return int(v)
	}
	return standard
}
