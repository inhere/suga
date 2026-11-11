package personal

import (
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// TestObserveWritesOriginJudgeAttrs: the verdict an approver puts in the
// approval report reaches the tool event's attrs; the reason is redacted and
// cut to 200 runes, and a call that was not judged gets no such attrs.
func TestObserveWritesOriginJudgeAttrs(t *testing.T) {
	repo, err := store.Open(newStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	registry := tools.NewRegistry()
	for _, name := range []string{"judged", "plain"} {
		registry.Register(mock.NewTool(name, mock.WithHandler(func(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
			return &agentcore.ToolResult{Content: "ok"}, nil
		})))
	}
	approver := tools.ApproverFunc(func(ctx context.Context, call agentcore.ToolCall, _ agentcore.Tool) error {
		if call.Name == "judged" {
			tools.ReportOriginJudge(ctx, "ask", "token=sk-live-abcdef123456 "+strings.Repeat("理由", 200), 42)
		}
		return nil
	})
	provider := &scriptedProvider{steps: []*agentcore.GenerateResult{
		{FinishReason: agentcore.FinishToolCalls, ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "judged"}, {CallID: "c2", Name: "plain"}}},
		{Content: "done", FinishReason: agentcore.FinishFinal},
	}}
	runner, err := New(Config{Repository: repo, Provider: provider, Tools: tools.NewApproval(registry, approver)})
	assert.Require(t, assert.NoErr(t, err))
	res, err := runner.Run(context.Background(), RunRequest{Prompt: "go"})
	assert.Require(t, assert.NoErr(t, err))

	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: res.SessionID, Kinds: []string{store.EventTool}})
	assert.Require(t, assert.NoErr(t, err))
	byName := map[string]map[string]any{}
	for _, e := range events {
		byName[e.Name] = e.Attrs
	}
	judged, plain := byName["judged"], byName["plain"]
	assert.Require(t, assert.NotNil(t, judged))
	assert.Eq(t, "ask", judged["origin_judge"])
	assert.EqInt(t, 42, judged["origin_judge_tokens"])
	reason, _ := judged["origin_judge_reason"].(string)
	assert.StrNotContains(t, reason, "sk-live-abcdef123456")
	assert.StrContains(t, reason, "[REDACTED]")
	assert.Eq(t, 200, len([]rune(reason)))
	for _, k := range []string{"origin_judge", "origin_judge_reason", "origin_judge_tokens"} {
		_, has := plain[k]
		assert.False(t, has, k)
	}
}
