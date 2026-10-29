package api

import "context"

// promptCacheScopeKey is the context key of a prompt-cache scope; unexported so
// only WithPromptCacheScope can set the value.
type promptCacheScopeKey struct{}

// PromptCacheScopeCompact marks a compaction, summary or idle-consolidation
// request: system prompt plus the messages to summarize. It becomes a suffix of
// the prompt cache key so the summary's own prompt does not evict the
// conversation's cache entry turn after turn.
const PromptCacheScopeCompact = "compact"

// WithPromptCacheScope marks ctx as a request whose prompt is not the
// conversation's own (a compaction or a handoff package). The providers keep
// such a request on its own upstream cache key.
func WithPromptCacheScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, promptCacheScopeKey{}, scope)
}

// PromptCacheScopeFrom returns the scope set by WithPromptCacheScope, or "".
func PromptCacheScopeFrom(ctx context.Context) string {
	scope, _ := ctx.Value(promptCacheScopeKey{}).(string)
	return scope
}
