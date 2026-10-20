package acpserver

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// growingUsageProvider reports a larger input context on every model call, so
// "last request" and "sum of requests" are distinguishable.
type growingUsageProvider struct {
	*mock.Provider
	calls int
}

func (p *growingUsageProvider) Stream(ctx context.Context, req *agentcore.GenerateRequest, emit agentcore.StreamFunc) (*agentcore.GenerateResult, error) {
	result, err := p.Provider.Stream(ctx, req, emit)
	if result != nil {
		p.calls++
		in := 10 * p.calls
		result.Usage = agentcore.Usage{InputTokens: in, OutputTokens: 1, TotalTokens: in + 1, CostUSD: 0.5}
	}
	return result, err
}

func usageUpdates(client *testACPClient) []*acp.SessionUsageUpdate {
	client.mu.Lock()
	defer client.mu.Unlock()
	var out []*acp.SessionUsageUpdate
	for _, n := range client.updates {
		if n.Update.UsageUpdate != nil {
			out = append(out, n.Update.UsageUpdate)
		}
	}
	return out
}

// Each model call sends a usage_update mid-turn and the turn sends one more at
// its end. The flat _meta keys and "run" are this prompt's run total; "session"
// is the session-cumulative total.
func TestUsageUpdateRunAndSession(t *testing.T) {
	provider := &growingUsageProvider{Provider: mock.NewProvider(
		mock.ToolCall("w1", "file_write", map[string]any{"path": "x.txt", "content": "x"}),
		mock.Final("one"), mock.Final("two"),
	)}
	// The client rejects the write: a refused call still counts in tool_calls.
	client := &testACPClient{selected: "reject"}
	tool := &countingTool{}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, tool), client)
	ctx := testCtx(t)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("a")}})
	assert.NoErr(t, err)
	assert.Eq(t, int32(0), tool.calls.Load())
	// Mid-turn: the update of the first model call arrived before the turn ended.
	assert.Require(t, assert.Eq(t, 3, len(usageUpdates(client))))
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("b")}})
	assert.NoErr(t, err)
	updates := usageUpdates(client)
	assert.Require(t, assert.Eq(t, 5, len(updates)))

	run := func(u *acp.SessionUsageUpdate) map[string]any { return u.Meta["run"].(map[string]any) }
	session := func(u *acp.SessionUsageUpdate) map[string]any { return u.Meta["session"].(map[string]any) }
	t.Run("flat keys equal run", func(t *testing.T) {
		for _, u := range updates {
			for _, key := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "total_tokens", "cost_usd", "tool_calls"} {
				assert.Eq(t, run(u)[key], u.Meta[key], key)
			}
		}
	})
	t.Run("first turn grows per model call", func(t *testing.T) {
		assert.Eq(t, float64(10), updates[0].Meta["input_tokens"])
		assert.Eq(t, float64(0), updates[0].Meta["tool_calls"]) // the tool runs after the call
		assert.Eq(t, 10, updates[0].Used)
		assert.Eq(t, float64(30), updates[1].Meta["input_tokens"])
		assert.Eq(t, float64(2), updates[1].Meta["output_tokens"])
		assert.Eq(t, float64(1), updates[1].Meta["tool_calls"])
		assert.Eq(t, float64(1), updates[1].Meta["cost_usd"])
		assert.Eq(t, float64(30), session(updates[1])["input_tokens"])
	})
	t.Run("turn end repeats the final run total", func(t *testing.T) {
		assert.Eq(t, updates[1].Meta["run"], updates[2].Meta["run"])
		assert.Eq(t, 20, updates[2].Used) // last request context, not the sum
		assert.Gt(t, updates[2].Size, 0)
	})
	t.Run("second turn: run restarts, session accumulates", func(t *testing.T) {
		for _, u := range updates[3:] {
			assert.Eq(t, float64(30), u.Meta["input_tokens"]) // the third call alone
			assert.Eq(t, float64(1), u.Meta["output_tokens"])
			assert.Eq(t, float64(31), u.Meta["total_tokens"])
			assert.Eq(t, float64(0.5), u.Meta["cost_usd"])
			assert.Eq(t, float64(0), u.Meta["tool_calls"])
			assert.Eq(t, float64(60), session(u)["input_tokens"]) // 10 + 20 + 30
			assert.Eq(t, float64(3), session(u)["output_tokens"])
			assert.Eq(t, float64(63), session(u)["total_tokens"])
			assert.Eq(t, float64(1.5), session(u)["cost_usd"])
		}
		assert.Eq(t, float64(1.5), updates[4].Cost.Amount) // ACP cost stays session-cumulative
	})
}

func TestCancelledTurnStillSendsUsageUpdate(t *testing.T) {
	provider := &blockingACPProvider{started: make(chan struct{})}
	client := &testACPClient{}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, nil), client)
	ctx := testCtx(t)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	done := make(chan acp.PromptResponse, 1)
	go func() {
		resp, _ := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("wait")}})
		done <- resp
	}()
	<-provider.started
	assert.NoErr(t, conn.Cancel(ctx, acp.CancelNotification{SessionId: created.SessionId}))
	assert.Eq(t, acp.StopReasonCancelled, (<-done).StopReason)
	updates := usageUpdates(client)
	assert.Eq(t, 1, len(updates))
	assert.Gt(t, updates[0].Size, 0)
}

// toolThenBlockProvider answers the first call with a read tool call carrying
// usage, then blocks the next call until the turn is cancelled.
type toolThenBlockProvider struct {
	calls   atomic.Int32
	blocked chan struct{}
}

func (*toolThenBlockProvider) Name() string { return "tool-then-block" }
func (p *toolThenBlockProvider) Generate(ctx context.Context, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	if p.calls.Add(1) == 1 {
		return &agentcore.GenerateResult{
			ToolCalls:    []agentcore.ToolCall{{CallID: "read-1", Name: "file_read", Arguments: map[string]any{"path": "a.txt"}}},
			FinishReason: agentcore.FinishToolCalls,
			Usage:        agentcore.Usage{InputTokens: 40, OutputTokens: 2, TotalTokens: 42},
		}, nil
	}
	close(p.blocked)
	<-ctx.Done()
	return nil, ctx.Err()
}

// A turn cancelled after a completed model call reports that call's usage and
// its request size, taken from personal.RunResult.
func TestCancelledTurnReportsCompletedCallUsage(t *testing.T) {
	provider := &toolThenBlockProvider{blocked: make(chan struct{})}
	client := &testACPClient{}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, &countingTool{name: "file_read"}), client)
	ctx := testCtx(t)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	done := make(chan acp.PromptResponse, 1)
	go func() {
		resp, _ := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("read then wait")}})
		done <- resp
	}()
	<-provider.blocked
	assert.NoErr(t, conn.Cancel(ctx, acp.CancelNotification{SessionId: created.SessionId}))
	assert.Eq(t, acp.StopReasonCancelled, (<-done).StopReason)
	updates := usageUpdates(client)
	// One mid-turn update for the completed call; the blocked call reported
	// no usage and is not counted; then the final update.
	assert.Require(t, assert.Eq(t, 2, len(updates)))
	assert.Eq(t, float64(40), updates[0].Meta["input_tokens"])
	assert.Eq(t, float64(0), updates[0].Meta["tool_calls"]) // the read ran after the update
	assert.Eq(t, 40, updates[1].Used)
	assert.Eq(t, float64(40), updates[1].Meta["input_tokens"])
	assert.Eq(t, float64(2), updates[1].Meta["output_tokens"])
	assert.Eq(t, float64(1), updates[1].Meta["tool_calls"])
}

func TestContextWindowForModel(t *testing.T) {
	cases := map[string]int{
		"claude-sonnet-4": 200000, "gpt-4o-mini": 128000, "gpt-4.1": 1000000,
		"deepseek-chat": 128000, "unknown-model": appconfig.DefaultContextWindow, "": appconfig.DefaultContextWindow,
	}
	for model, want := range cases {
		t.Run(model, func(t *testing.T) {
			assert.Eq(t, want, appconfig.ContextWindowForModel(model))
		})
	}
}

// Cumulative usage is persisted by personal.Runner, so a new ACP process that
// loads the session continues from the stored total instead of from zero.
func TestUsageUpdateContinuesAfterSessionLoad(t *testing.T) {
	provider := &growingUsageProvider{Provider: mock.NewProvider(mock.Final("one"), mock.Final("two"))}
	open := testACPRuntime(t, provider, nil)
	ctx := testCtx(t)
	cwd := t.TempDir()

	firstClient := &testACPClient{}
	first := connectPersonalACP(t, open, firstClient)
	created, err := first.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = first.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("a")}})
	assert.NoErr(t, err)
	assert.Eq(t, float64(10), usageUpdates(firstClient)[1].Meta["input_tokens"])

	// A second connection stands in for a restarted ACP process.
	secondClient := &testACPClient{}
	second := connectPersonalACP(t, open, secondClient)
	_, err = second.LoadSession(ctx, acp.LoadSessionRequest{SessionId: created.SessionId, Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = second.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("b")}})
	assert.NoErr(t, err)
	updates := usageUpdates(secondClient)
	assert.Require(t, assert.Eq(t, 2, len(updates))) // mid-turn + turn end
	session := updates[1].Meta["session"].(map[string]any)
	assert.Eq(t, float64(30), session["input_tokens"]) // 10 persisted + 20 new, not 20
	assert.Eq(t, float64(32), session["total_tokens"])
	assert.Eq(t, float64(20), updates[1].Meta["input_tokens"]) // this run only
}

// A first turn after session/load that fails before the runner finishes it must
// still report the persisted total, not reset the client to zero.
func TestUsageUpdateAfterSessionLoadEarlyFailure(t *testing.T) {
	provider := &growingUsageProvider{Provider: mock.NewProvider(mock.Final("one"), mock.Final("two"))}
	failRules := &atomic.Bool{}
	open := testACPRuntimeWith(t, provider, nil, acpTestOptions{failRules: failRules})
	ctx := testCtx(t)
	cwd := t.TempDir()

	first := connectPersonalACP(t, open, &testACPClient{})
	created, err := first.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = first.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("a")}})
	assert.NoErr(t, err)

	secondClient := &testACPClient{}
	second := connectPersonalACP(t, open, secondClient)
	_, err = second.LoadSession(ctx, acp.LoadSessionRequest{SessionId: created.SessionId, Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	failRules.Store(true)
	_, err = second.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("b")}})
	assert.Err(t, err)
	updates := usageUpdates(secondClient)
	assert.Eq(t, 1, len(updates))
	session := updates[0].Meta["session"].(map[string]any)
	assert.Eq(t, float64(10), session["input_tokens"]) // the persisted first-turn total
	assert.Eq(t, float64(11), session["total_tokens"])
	assert.Eq(t, float64(0), updates[0].Meta["input_tokens"]) // the failed run made no call
}

func TestUsageUpdateInputExcludesCache(t *testing.T) {
	u := usageUpdate(personal.RunUsage{Usage: agentcore.Usage{InputTokens: 100, OutputTokens: 5, TotalTokens: 105, CacheReadTokens: 60, CacheWriteTokens: 30}, ToolCalls: 2}, agentcore.Usage{}, 100, 200000).UsageUpdate
	assert.Eq(t, 10, u.Meta["input_tokens"])
	assert.Eq(t, 60, u.Meta["cache_read_tokens"])
	assert.Eq(t, 30, u.Meta["cache_write_tokens"])
	assert.Eq(t, 105, u.Meta["total_tokens"])
	assert.Eq(t, 2, u.Meta["tool_calls"])
}

// goferUsage mirrors how gofer reads an ACP usage_update (gofer
// internal/runner/acp/usage.go usageFromUpdate and internal/runner/usage.go
// UsageFromObject): the update object's own fields, overlaid by the TOP-LEVEL
// flat keys of _meta (nested objects are ignored); a zero counter never
// overrides. It is copied here on purpose: suga must not import gofer.
type goferUsage struct {
	Input, Output, CacheRead, CacheWrite, Total int64
	Cost                                        float64
}

func goferNum(obj map[string]any, keys ...string) float64 {
	for _, k := range keys {
		switch n := obj[k].(type) {
		case float64:
			return n
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
				return f
			}
		}
	}
	return 0
}

func goferFromObject(obj map[string]any) *goferUsage {
	u := &goferUsage{
		Input:  int64(goferNum(obj, "inputTokens", "input_tokens", "input")),
		Output: int64(goferNum(obj, "outputTokens", "output_tokens", "output")),
		CacheRead: int64(goferNum(obj, "cacheReadTokens", "cache_read_tokens", "cacheReadInputTokens",
			"cache_read_input_tokens", "cacheRead", "cache_read")),
		CacheWrite: int64(goferNum(obj, "cacheWriteTokens", "cache_write_tokens", "cacheCreationInputTokens",
			"cache_creation_input_tokens", "cacheWrite", "cache_write")),
		Total: int64(goferNum(obj, "totalTokens", "total_tokens", "total", "used")),
	}
	if u.Total == 0 {
		u.Total = u.Input + u.Output + u.CacheRead + u.CacheWrite
	}
	u.Cost = goferNum(obj, "cost_usd", "costUsd", "total_cost_usd", "totalCostUsd", "cost")
	if block, ok := obj["cost"].(map[string]any); ok && u.Cost == 0 {
		u.Cost = goferNum(block, "total", "amount", "usd")
	}
	if *u == (goferUsage{}) {
		return nil
	}
	return u
}

func goferFromUpdate(t *testing.T, raw []byte) *goferUsage {
	t.Helper()
	var obj map[string]any
	assert.Require(t, assert.NoErr(t, json.Unmarshal(raw, &obj)))
	usage := goferFromObject(obj)
	meta, ok := obj["_meta"].(map[string]any)
	if !ok {
		return usage
	}
	next := goferFromObject(meta)
	if next == nil {
		return usage
	}
	if usage == nil {
		return next
	}
	if _, has := meta["total_tokens"]; !has {
		next.Total = 0
	}
	if next.Input > 0 {
		usage.Input = next.Input
	}
	if next.Output > 0 {
		usage.Output = next.Output
	}
	if next.CacheRead > 0 {
		usage.CacheRead = next.CacheRead
	}
	if next.CacheWrite > 0 {
		usage.CacheWrite = next.CacheWrite
	}
	if next.Total > 0 {
		usage.Total = next.Total
	}
	if next.Cost > 0 {
		usage.Cost = next.Cost
	}
	return usage
}

// Gofer budgets a job by the flat _meta keys; they must give the run total
// even when the session total (and the ACP cost field) is larger.
func TestUsageUpdateGoferReadsRunTotal(t *testing.T) {
	run := personal.RunUsage{Usage: agentcore.Usage{InputTokens: 100, OutputTokens: 5, CacheReadTokens: 60, CacheWriteTokens: 30, CostUSD: 0.25}, ToolCalls: 3}
	session := agentcore.Usage{InputTokens: 900, OutputTokens: 50, CacheReadTokens: 600, CacheWriteTokens: 40, CostUSD: 2}
	raw, err := json.Marshal(usageUpdate(run, session, 100, 200000))
	assert.Require(t, assert.NoErr(t, err))
	got := goferFromUpdate(t, raw)
	assert.Require(t, assert.NotNil(t, got))
	assert.Eq(t, goferUsage{Input: 10, Output: 5, CacheRead: 60, CacheWrite: 30, Total: 105, Cost: 0.25}, *got)
}

func TestUsageUpdateBudgetMetadataIsOptionalAndComplete(t *testing.T) {
	run := personal.RunUsage{Usage: agentcore.Usage{InputTokens: 10, OutputTokens: 2}}
	session := agentcore.Usage{InputTokens: 20, OutputTokens: 4}
	t.Run("no budget keeps metadata unchanged", func(t *testing.T) {
		meta := usageUpdate(run, session, 10, 200000).UsageUpdate.Meta
		_, hasLimit := meta["budget_limit"]
		_, hasEstimated := meta["estimated"]
		assert.False(t, hasLimit)
		assert.False(t, hasEstimated)
	})
	t.Run("configured budget exposes both dimensions and estimate", func(t *testing.T) {
		budget := &personal.BudgetSnapshot{
			Scope: "session", Tokens: personal.BudgetMetric{Limit: 100, Used: 120, Remaining: 0},
			CostUSD:  personal.BudgetMetric{Limit: 2, Used: 1.25, Remaining: 0.75},
			Exceeded: true, Reason: "max_tokens", Estimated: true,
		}
		meta := usageUpdate(run, session, 10, 200000, budget).UsageUpdate.Meta
		assert.Eq(t, "session", meta["budget_scope"])
		assert.Eq(t, true, meta["budget_exceeded"])
		assert.Eq(t, "max_tokens", meta["budget_reason"])
		assert.Eq(t, true, meta["estimated"])
		limits := meta["budget_limit"].(map[string]any)
		used := meta["budget_used"].(map[string]any)
		remaining := meta["budget_remaining"].(map[string]any)
		assert.Eq(t, float64(100), limits["tokens"])
		assert.Eq(t, float64(1.25), used["cost_usd"])
		assert.Eq(t, float64(0.75), remaining["cost_usd"])
	})
}
