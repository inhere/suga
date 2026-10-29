package personal

import (
	"context"
	"errors"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// streamOnlyProvider refuses Generate, the way a gateway that accepts only
// stream=true requests does, and answers through Stream.
type streamOnlyProvider struct {
	streamed int
	// scope is the prompt cache scope the request carried (api.PromptCacheScope*).
	scope string
}

func (p *streamOnlyProvider) Name() string { return "stream-only" }

func (p *streamOnlyProvider) Generate(context.Context, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	return nil, errors.New(`{"detail":"Stream must be set to true"}`)
}

func (p *streamOnlyProvider) Stream(ctx context.Context, _ *agentcore.GenerateRequest, emit agentcore.StreamFunc) (*agentcore.GenerateResult, error) {
	p.streamed++
	p.scope = agentcoreapi.PromptCacheScopeFrom(ctx)
	if err := emit(ctx, agentcore.StreamEvent{Kind: agentcore.StreamEventTextDelta, Delta: "## 当前任务\n"}); err != nil {
		return nil, err
	}
	return &agentcore.GenerateResult{Content: "## 当前任务\nsummary"}, nil
}

// TestGenerateQuietlyStreamsWhenTheProviderStreams: a summary request goes
// through Stream on a streaming provider, so a stream-only gateway works.
func TestGenerateQuietlyStreamsWhenTheProviderStreams(t *testing.T) {
	p := &streamOnlyProvider{}
	res, err := generateQuietly(context.Background(), p, &agentcore.GenerateRequest{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "## 当前任务\nsummary", res.Content)
	assert.Eq(t, 1, p.streamed)
	// The request is marked as a summary, so the providers keep it on its own
	// upstream prompt cache key instead of the conversation's.
	assert.Eq(t, agentcoreapi.PromptCacheScopeCompact, p.scope)
}
