package acpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
)

// retryingProvider streams a retry announcement, then the answer.
type retryingProvider struct{}

func (retryingProvider) Name() string { return "retrying" }
func (retryingProvider) Generate(context.Context, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	return &agentcore.GenerateResult{Content: "ok", FinishReason: agentcore.FinishFinal}, nil
}
func (retryingProvider) Stream(ctx context.Context, _ *agentcore.GenerateRequest, emit agentcore.StreamFunc) (*agentcore.GenerateResult, error) {
	if err := emit(ctx, agentcore.StreamEvent{Kind: agentcore.StreamEventRetry, Delta: "HTTP 503",
		Metadata: map[string]any{"attempt": 1, "wait_ms": int64(2000), "restart": false}}); err != nil {
		return nil, err
	}
	if err := emit(ctx, agentcore.StreamEvent{Kind: agentcore.StreamEventTextDelta, Delta: "ok"}); err != nil {
		return nil, err
	}
	return &agentcore.GenerateResult{Content: "ok", FinishReason: agentcore.FinishFinal}, nil
}

// TestPersonalACPRetryIsAThought: a model retry reaches the client as a
// thought chunk, never as part of the answer text.
func TestPersonalACPRetryIsAThought(t *testing.T) {
	client := &testACPClient{}
	conn := connectPersonalACP(t, testACPRuntime(t, retryingProvider{}, nil), client)
	ctx := testCtx(t)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("hi")}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)

	client.mu.Lock()
	defer client.mu.Unlock()
	var thoughts, answer strings.Builder
	for _, u := range client.updates {
		if c := u.Update.AgentThoughtChunk; c != nil && c.Content.Text != nil {
			thoughts.WriteString(c.Content.Text.Text)
		}
		if c := u.Update.AgentMessageChunk; c != nil && c.Content.Text != nil {
			answer.WriteString(c.Content.Text.Text)
		}
	}
	assert.StrContains(t, thoughts.String(), "↻ 模型请求失败（HTTP 503），2s 后重试（第 1 次）")
	assert.Eq(t, "ok", answer.String())
}
