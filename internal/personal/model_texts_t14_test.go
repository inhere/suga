package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// scriptedProvider plays one result per request and records every request it
// receives, so a test can pin the exact text the model would read (i18n phase
// 1b: the zh snapshot of the model-facing wording).
type scriptedProvider struct {
	steps    []*agentcore.GenerateResult
	requests []*agentcore.GenerateRequest
}

func (*scriptedProvider) Name() string { return "scripted-test" }

func (p *scriptedProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.requests = append(p.requests, req)
	if len(p.steps) == 0 {
		return &agentcore.GenerateResult{Content: "done", FinishReason: agentcore.FinishFinal}, nil
	}
	step := p.steps[0]
	p.steps = p.steps[1:]
	return step, nil
}

// snapshotSpiller stands in for the session spiller: every spilled output gets
// the same ref, which keeps the ref header deterministic.
type snapshotSpiller struct{}

func (snapshotSpiller) Spill(context.Context, agentcore.ToolCall, string) (string, error) {
	return "out:snapshot", nil
}

// humanApprover answers every call as the person allowed it right now, the way
// the chat and ACP approvers do, so the approved note goes into the result.
func humanApprover() tools.Approver {
	return tools.ApproverFunc(func(ctx context.Context, _ agentcore.ToolCall, _ agentcore.Tool) error {
		tools.ReportApprovalAnswer(ctx, tools.ApprovedByUser, time.Second)
		return nil
	})
}

// newSnapshotRunner builds a real runner (observe -> output -> approval -> base,
// deadline and budget hooks) over a scripted provider.
func newSnapshotRunner(t *testing.T, tr i18n.Translator, steps ...*agentcore.GenerateResult) (*Runner, *scriptedProvider) {
	t.Helper()
	repo, err := store.Open(newStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	registry := tools.NewRegistry()
	registry.Register(mock.NewTool("note_tool", mock.WithHandler(func(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
		return &agentcore.ToolResult{Content: "note ok"}, nil
	})))
	long := strings.Repeat("line of the long output\n", 2000)
	registry.Register(mock.NewTool("long_out", mock.WithHandler(func(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
		return &agentcore.ToolResult{Content: long}, nil
	})))
	provider := &scriptedProvider{steps: steps}
	runner, err := New(Config{
		Repository: repo, Provider: provider,
		Tools:             tools.NewApproval(registry, humanApprover()),
		InlineOutputBytes: 1000, Spiller: snapshotSpiller{},
		Translator:  tr,
		OutputTexts: toolTextsOf(tr),
	})
	assert.Require(t, assert.NoErr(t, err))
	return runner, provider
}

// toolTextsOf resolves the tool-stack wording the way internal/wiring does for
// a runtime: from the catalog, so the zh entry is the historical text.
func toolTextsOf(tr i18n.Translator) tools.ModelTexts {
	t := tr
	if t == nil {
		t = i18n.Fallback()
	}
	return tools.ModelTexts{
		ApprovedNote:       t.T("model.approved_note"),
		OutputSavedRef:     t.T("model.output.saved_ref"),
		OutputCondensed:    t.T("model.output.condensed"),
		OutputCondensedRef: t.T("model.output.condensed_ref"),
	}
}

// TestModelTextsZhRequestUnchanged pins the model-facing wording of the paths
// phase 1b touches: an approved tool result and an oversized result stored
// under a ref. language: zh - and a run that injects nothing at all - must put
// exactly the historical Chinese text into the request, byte for byte.
func TestModelTextsZhRequestUnchanged(t *testing.T) {
	// The wording before phase 1b (approved.go's ApprovedResultNote and the
	// output layer's header literal), verbatim.
	const (
		wantNote  = "[已经人工批准执行]"
		wantSaved = "[输出 %d 字节 / %d 行已保存为 ref %s；先用 session_search(query, ref) 定位相关行，再用 recall(ref, offset, limit) 读附近窗口，不要逐页读完]"
	)
	zh, err := i18n.New(i18n.LanguageZH)
	assert.Require(t, assert.NoErr(t, err))

	run := func(t *testing.T, tr i18n.Translator) string {
		t.Helper()
		runner, provider := newSnapshotRunner(t, tr,
			&agentcore.GenerateResult{FinishReason: agentcore.FinishToolCalls,
				ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "note_tool"}}},
			&agentcore.GenerateResult{FinishReason: agentcore.FinishToolCalls,
				ToolCalls: []agentcore.ToolCall{{CallID: "c2", Name: "long_out"}}},
			&agentcore.GenerateResult{Content: "done", FinishReason: agentcore.FinishFinal})
		_, err := runner.Run(context.Background(), RunRequest{Prompt: "go"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, provider.requests, 3))
		// The last request carries both results: the approved note and the ref
		// header of the spilled output.
		raw, err := json.Marshal(provider.requests[2].Messages)
		assert.Require(t, assert.NoErr(t, err))
		return string(raw)
	}

	golden := run(t, zh)
	t.Run("language zh keeps the historical text", func(t *testing.T) {
		assert.StrContains(t, golden, "note ok\\n"+wantNote)
		assert.StrContains(t, golden, fmt.Sprintf(wantSaved, len(strings.Repeat("line of the long output\n", 2000)), 2000, "out:snapshot"))
	})
	t.Run("no injected catalog is byte-identical to zh", func(t *testing.T) {
		assert.Eq(t, golden, run(t, nil))
	})
	t.Run("language en translates only the injected lines", func(t *testing.T) {
		en, err := i18n.New(i18n.LanguageEN)
		assert.Require(t, assert.NoErr(t, err))
		english := run(t, en)
		assert.StrContains(t, english, "note ok\\n[executed after manual approval]")
		assert.StrContains(t, english, "saved as ref out:snapshot")
		assert.StrContains(t, english, "session_search(query, ref)")
		// The protocol parts stay: the tool result body and the ref marker.
		assert.StrContains(t, english, "note ok")
		assert.StrContains(t, english, "out:snapshot")
		assert.False(t, strings.Contains(english, "已经人工批准执行"))
		assert.False(t, strings.Contains(english, "已保存为 ref"))
	})
}

// TestModelTextsDeadlineAndBudget pins the wording of the two request-only
// hooks: the deadline hint and the budget wrap-up instruction, in zh (the
// historical text) and en.
func TestModelTextsDeadlineAndBudget(t *testing.T) {
	deadline := time.Date(2026, 1, 1, 22, 28, 0, 0, time.FixedZone("CST", 8*3600))
	now := time.Date(2026, 1, 1, 21, 40, 0, 0, time.FixedZone("CST", 8*3600))
	snapshot := BudgetSnapshot{
		Tokens:  BudgetMetric{Limit: 1000, Used: 1200},
		CostUSD: BudgetMetric{Limit: 1, Used: 1.5},
	}
	zh, err := i18n.New(i18n.LanguageZH)
	assert.Require(t, assert.NoErr(t, err))
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("deadline hint zh is the historical text", func(t *testing.T) {
		state := &deadlineState{deadline: deadline, now: fixedClock(now), tr: zh}
		assert.Eq(t, "[时间] 现在 21:40（+08:00），本任务截止 22:28，剩余约 48 分钟。", state.hint())
		// No injected catalog keeps the same bytes.
		assert.Eq(t, state.hint(), (&deadlineState{deadline: deadline, now: fixedClock(now)}).hint())
	})
	t.Run("deadline hint en", func(t *testing.T) {
		state := &deadlineState{deadline: deadline, now: fixedClock(now), tr: en}
		assert.Eq(t, "[Time] Now 21:40 (+08:00).\nThe task deadline is 22:28, about 48 minutes left.", state.hint())
	})
	t.Run("budget instruction zh and en", func(t *testing.T) {
		assert.StrContains(t, budgetWrapUpInstruction(nil, snapshot, "max_cost_usd"), "本回合已达到成本预算上限（$1.0000，已用 $1.5000）")
		assert.StrContains(t, budgetWrapUpInstruction(zh, snapshot, "max_tokens"), "本回合已达到 token 预算上限（1000，已用 1200）")
		assert.StrContains(t, budgetWrapUpInstruction(en, snapshot, "max_cost_usd"), "This turn has reached its cost budget ($1.0000, used $1.5000)")
		assert.StrContains(t, budgetWrapUpInstruction(en, snapshot, "max_tokens"), "This turn has reached its token budget (1000, used 1200)")
	})
}
