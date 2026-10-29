package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// TestResponsesPromptCacheKey: the session id keys the upstream prompt cache, a
// summary request gets a key of its own (so it does not evict the conversation's
// entry), and the API's 64 character cap is kept.
func TestResponsesPromptCacheKey(t *testing.T) {
	ctx := agentcoreapi.WithSessionID(context.Background(), "sg_1")
	assert.Eq(t, "sg_1", promptCacheKey(ctx))

	scoped := agentcoreapi.WithPromptCacheScope(ctx, agentcoreapi.PromptCacheScopeCompact)
	assert.Eq(t, "sg_1-compact", promptCacheKey(scoped))

	assert.Eq(t, "", promptCacheKey(context.Background()), "a request without a session gets no key")

	long := strings.Repeat("s", 80)
	assert.Eq(t, 64, len(promptCacheKey(agentcoreapi.WithSessionID(context.Background(), long))))
}

// TestResponsesPromptCacheRetention: providers.<id>.prompt_cache_retention is
// sent as prompt_cache_retention; unset leaves the field out so the API default
// applies.
func TestResponsesPromptCacheRetention(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}

	body := NewResponsesProvider(ResponsesConfig{PromptCacheRetention: "24h"}).
		buildResponsesBody(context.Background(), req, false)
	assert.Eq(t, "24h", body["prompt_cache_retention"])

	body = NewResponsesProvider(ResponsesConfig{}).buildResponsesBody(context.Background(), req, false)
	_, ok := body["prompt_cache_retention"]
	assert.False(t, ok, "no retention configured, no field")
}
