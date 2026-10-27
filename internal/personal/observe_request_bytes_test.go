package personal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
)

// recordingProvider keeps the request it was handed (marshaled) and answers a
// text reply: the observation layers must not touch the request bytes.
type recordingProvider struct {
	seen []byte
}

func (p *recordingProvider) Name() string { return "rec" }

func (p *recordingProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	p.seen = raw
	return &agentcore.GenerateResult{Content: "ok"}, nil
}

// TestObservedProviderKeepsRequestBytes locks the hard constraint of the cache
// instrumentation (issue suga-jh7b, second step): the cache attrs are read from
// the request, never written back, so what reaches the provider is the same
// request byte for byte.
func TestObservedProviderKeepsRequestBytes(t *testing.T) {
	req := &agentcore.GenerateRequest{
		System: "system prompt",
		Messages: []agentcore.Message{
			{Role: "user", Content: "do it"},
			{Role: "assistant", ToolCall: &agentcore.ToolCall{CallID: "c1", Name: "file_read", Arguments: map[string]any{"path": "a.go"}}},
			{Role: "tool", ToolResult: &agentcore.ToolResult{CallID: "c1", Name: "file_read", Content: "package a"}},
		},
		Tools: []agentcore.ToolSchema{{Name: "file_read"}},
	}
	want, err := json.Marshal(req)
	assert.Require(t, assert.NoErr(t, err))

	inner := &recordingProvider{}
	rec := &recorder{prefix: newPrefixTracker()}
	prov := rec.observeProvider(inner, "rec", "m1")
	res, err := prov.Generate(context.Background(), req)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "ok", res.Content)
	// The provider saw exactly the request that was built (the recorder only
	// read it: prefix.go fingerprints a copy).
	assert.Eq(t, string(want), string(inner.seen))
	// And the input request itself was not changed by the observation.
	after, err := json.Marshal(req)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(want), string(after))
}
