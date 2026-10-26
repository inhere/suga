package output

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

func TestStreamJSONBudgetFields(t *testing.T) {
	budget := &personal.BudgetSnapshot{
		Scope: "session", Tokens: personal.BudgetMetric{Limit: 100, Used: 120, Remaining: 0},
		CostUSD: personal.BudgetMetric{Limit: 2, Used: 1.25, Remaining: 0.75}, Exceeded: true, Estimated: true,
	}
	result := personal.RunResult{RunID: "r1", State: personal.RunCompleted,
		RunUsage: personal.RunUsage{Usage: agentcore.Usage{InputTokens: 80, OutputTokens: 4}}}

	t.Run("budget is omitted when not configured", func(t *testing.T) {
		line := ResultLine("s1", result)
		_, hasBudget := line["budget"]
		_, hasEstimated := line["estimated"]
		assert.False(t, hasBudget)
		assert.False(t, hasEstimated)
	})
	t.Run("result carries configured budget and estimate marker", func(t *testing.T) {
		line := ResultLine("s1", result, budget)
		got, ok := line["budget"].(map[string]any)
		assert.True(t, ok)
		assert.Eq(t, "session", got["scope"])
		assert.Eq(t, true, got["exceeded"])
		assert.Eq(t, true, line["estimated"])
		assert.NotNil(t, got["tokens"])
		assert.NotNil(t, got["cost_usd"])
	})
	t.Run("usage and budget events use the same optional shape", func(t *testing.T) {
		usage, ok := EventLine(personal.Event{Kind: "usage", SessionID: "s1", RunID: "r1", Usage: &personal.UsageUpdate{
			Run: result.RunUsage, Budget: budget, Estimated: true,
		}})
		assert.True(t, ok)
		assert.Eq(t, true, usage["estimated"])
		assert.NotNil(t, usage["budget"])
		event, ok := EventLine(personal.Event{Kind: "budget", SessionID: "s1", RunID: "r1", Text: "warning", Budget: budget})
		assert.True(t, ok)
		assert.Eq(t, "budget", event["type"])
		assert.Eq(t, "warning", event["message"])
		assert.NotNil(t, event["budget"])
	})
}

func TestBudgetWrapUpNotices(t *testing.T) {
	t.Run("cost limit names the cost reason", func(t *testing.T) {
		assert.StrContains(t, WrapUpNotice(nil, "max_cost_usd", 0), "成本预算用尽")
	})
	t.Run("truncated wrap-up names both limits", func(t *testing.T) {
		assert.StrContains(t, WrapUpTruncatedNotice(nil, 32), "收尾答复")
		assert.StrContains(t, WrapUpTruncatedNotice(nil, 32), "32 tokens")
	})
}
