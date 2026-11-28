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
// cut to 200 runes, and a call that was not judged gets no such attrs. The
// origin_remembered attr of an "allow this snippet for the session" answer or a
// skipped question is recorded even when no judge ran.
func TestObserveWritesOriginJudgeAttrs(t *testing.T) {
	repo, err := store.Open(newStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	registry := tools.NewRegistry()
	for _, name := range []string{"judged", "remembered", "plain"} {
		registry.Register(mock.NewTool(name, mock.WithHandler(func(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
			return &agentcore.ToolResult{Content: "ok"}, nil
		})))
	}
	approver := tools.ApproverFunc(func(ctx context.Context, call agentcore.ToolCall, _ agentcore.Tool) error {
		switch call.Name {
		case "judged":
			tools.ReportOriginJudge(ctx, "ask", "token=sk-live-abcdef123456 "+strings.Repeat("理由", 200), 42)
			tools.ReportOriginRemembered(ctx, "added")
		case "remembered":
			// No judge verdict: the attr must still be written.
			tools.ReportOriginRemembered(ctx, "hit")
		}
		return nil
	})
	provider := &scriptedProvider{steps: []*agentcore.GenerateResult{
		{FinishReason: agentcore.FinishToolCalls, ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "judged"}, {CallID: "c2", Name: "remembered"}, {CallID: "c3", Name: "plain"}}},
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
	judged, reminded, plain := byName["judged"], byName["remembered"], byName["plain"]
	assert.Require(t, assert.NotNil(t, judged))
	assert.Require(t, assert.NotNil(t, reminded))
	assert.Eq(t, "ask", judged["origin_judge"])
	assert.EqInt(t, 42, judged["origin_judge_tokens"])
	assert.Eq(t, "added", judged["origin_remembered"])
	// A skipped question without any verdict still shows why it was skipped.
	assert.Eq(t, "hit", reminded["origin_remembered"])
	_, judgedByJudge := reminded["origin_judge"]
	assert.False(t, judgedByJudge, "origin_judge")
	reason, _ := judged["origin_judge_reason"].(string)
	assert.StrNotContains(t, reason, "sk-live-abcdef123456")
	assert.StrContains(t, reason, "[REDACTED]")
	assert.Eq(t, 200, len([]rune(reason)))
	for _, k := range []string{"origin_judge", "origin_judge_reason", "origin_judge_tokens", "origin_remembered"} {
		_, has := plain[k]
		assert.False(t, has, k)
	}
}
