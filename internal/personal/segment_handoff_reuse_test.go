package personal

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// reuseMarker opens the reuse path's instruction (the zh catalog entry; a test
// config injects no translator, so the compatibility fallback text applies).
const reuseMarker = "# 需要概括的范围"

// reuseBody is a well-formed handoff package body: every section compactTemplate
// asks for, so it passes the reuse path's shape check.
const reuseBody = "## 当前任务\n修解析器\n## 坐标\n- internal/personal/compact.go 的 summarize\n## 采纳与否决\n- 用户原话：不要改协议\n" +
	"## 验证到哪\n- go test ./internal/personal 通过\n## 进展与阻塞\n无\n## 下一步\n无\n" + refsHeading + "\n- 无\n"

// reuseProv answers the main-loop calls through main and every handoff-package
// call through handoff. It reports SupportsToolChoice, so the reuse request must
// forbid tools. It keeps every request it was handed.
type reuseProv struct {
	mu      sync.Mutex
	all     []*agentcore.GenerateRequest
	main    func(n int, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error)
	handoff func(n int, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error)
}

func (p *reuseProv) Name() string { return "reuse" }

func (p *reuseProv) SupportsToolChoice() bool { return true }

func (p *reuseProv) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	cp := *req
	cp.Messages = append([]agentcore.Message(nil), req.Messages...)
	p.mu.Lock()
	p.all = append(p.all, &cp)
	n := len(p.all)
	p.mu.Unlock()
	// A handoff call either keeps the main system prompt (the reuse path) or uses
	// compactSystemPrompt (the old path).
	if req.System == compactSystemPrompt || isReuseCall(&cp) {
		return p.handoff(n, &cp)
	}
	return p.main(n, &cp)
}

// isReuseCall reports whether req is the reuse path's handoff instruction.
func isReuseCall(req *agentcore.GenerateRequest) bool {
	if len(req.Messages) == 0 {
		return false
	}
	return strings.Contains(req.Messages[len(req.Messages)-1].Content, reuseMarker)
}

// handoffCalls splits the provider's requests into the reuse-path calls and the
// old-path ones, in the order they were sent.
func (p *reuseProv) handoffCalls() (reuse, old []*agentcore.GenerateRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, req := range p.all {
		switch {
		case isReuseCall(req):
			reuse = append(reuse, req)
		case req.System == compactSystemPrompt:
			old = append(old, req)
		}
	}
	return reuse, old
}

// lastMainReq is the provider's last main-loop request.
func (p *reuseProv) lastMainReq() *agentcore.GenerateRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.all) - 1; i >= 0; i-- {
		req := p.all[i]
		if req.System != compactSystemPrompt && !isReuseCall(req) {
			return req
		}
	}
	return nil
}

// handoffReuseAttrs lists the handoff_reuse attr of the session's handoff-package
// model events, oldest first.
func handoffReuseAttrs(t *testing.T, repo *store.Store, sessionID string) []string {
	t.Helper()
	out := []string{}
	for _, e := range eventsOf(t, repo, sessionID, store.EventModel) {
		if _, ok := e.Attrs["purpose"]; !ok {
			continue
		}
		if v, ok := e.Attrs["handoff_reuse"].(string); ok {
			out = append(out, v)
		}
	}
	return out
}

// reusedRunner is a runner whose segment breaks may continue the segment
// prefix, with a window wide enough for the small test views.
func reusedRunner(t *testing.T, repo *store.Store, prov agentcore.Provider) *Runner {
	t.Helper()
	return compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 100000, 1000
		c.HandoffReusePrefix = true
	})
}

// oneTurn runs one ordinary turn (tool call, then final answer) and returns the
// session id: the segment then holds two main-loop requests.
func oneTurn(t *testing.T, runner *Runner) string {
	t.Helper()
	ctx := context.Background()
	res, err := runner.Run(ctx, RunRequest{Prompt: "fix the parser in compact.go"})
	assert.NoErr(t, err)
	return res.SessionID
}

// reuseMain answers a main-loop call: a tool call first, then a final answer.
func reuseMain(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	if n == 1 {
		return callTool("c1")
	}
	return finalText("done")
}

// The switch off (the default of the Config value, and the rollback) keeps the
// handoff request exactly what it always was: the compact system prompt and one
// user message carrying the transcript section.
func TestHandoffReuseDisabledKeepsOldRequest(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: reuseMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	runner := compactRunner(t, repo, prov, nil, func(c *Config) { c.ContextWindow, c.CompactReserveTokens = 100000, 1000 })
	ctx := context.Background()
	sid := oneTurn(t, runner)
	_, err := runner.Compact(ctx, sid)
	assert.NoErr(t, err)

	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(reuse))
	assert.Require(t, assert.Eq(t, 1, len(old)))
	req := old[0]
	assert.Eq(t, compactSystemPrompt, req.System)
	assert.Eq(t, 0, len(req.Tools))
	assert.Eq(t, 0, req.MaxTokens)
	assert.Eq(t, "", req.ToolChoice)
	assert.Require(t, assert.Eq(t, 1, len(req.Messages)))
	assert.Eq(t, "user", req.Messages[0].Role)
	assert.StrContains(t, req.Messages[0].Content, compactTemplate)
	assert.StrContains(t, req.Messages[0].Content, "# 待压缩的对话")
	assert.Eq(t, false, strings.Contains(req.Messages[0].Content, reuseMarker))
	// The trace names the switch as the reason of the old path.
	assert.Eq(t, []string{handoffReuseFallbackPrefix + handoffFallbackDisabled}, handoffReuseAttrs(t, repo, sid))
}

// The switch on sends the segment's own request plus one instruction: the same
// System, the same tools and messages byte for byte, and no tool use.
func TestHandoffReuseContinuesSegmentPrefix(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: reuseMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	runner := reusedRunner(t, repo, prov)
	ctx := context.Background()
	sid := oneTurn(t, runner)
	_, err := runner.Compact(ctx, sid)
	assert.NoErr(t, err)

	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(old))
	assert.Require(t, assert.Eq(t, 1, len(reuse)))
	got, want := reuse[0], prov.lastMainReq()
	assert.Require(t, assert.NotNil(t, want))
	assert.Eq(t, want.System, got.System)
	wantTools, err := json.Marshal(want.Tools)
	assert.Require(t, assert.NoErr(t, err))
	gotTools, err := json.Marshal(got.Tools)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(wantTools), string(gotTools))
	// Every message but the instruction is the last main-loop request's.
	assert.Require(t, assert.Eq(t, len(want.Messages)+1, len(got.Messages)))
	wantMsgs, err := json.Marshal(want.Messages)
	assert.Require(t, assert.NoErr(t, err))
	gotMsgs, err := json.Marshal(got.Messages[:len(got.Messages)-1])
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(wantMsgs), string(gotMsgs))
	instruction := got.Messages[len(got.Messages)-1]
	assert.Eq(t, "user", instruction.Role)
	assert.StrContains(t, instruction.Content, reuseMarker)
	assert.StrContains(t, instruction.Content, compactTemplate)
	assert.Eq(t, false, strings.Contains(instruction.Content, "# 待压缩的对话"))
	assert.Eq(t, agentcore.ToolChoiceNone, got.ToolChoice)
	// The package the answer produced is stored as usual.
	rec := latestSegment(t, repo, sid)
	assert.StrContains(t, rec.Summary, "修解析器")
	assert.Eq(t, []string{handoffReusePrefix}, handoffReuseAttrs(t, repo, sid))
}

// The four reuse conditions: each one falls back to the old path and says why.
func TestHandoffReuseConditionFallbacks(t *testing.T) {
	newProv := func() *reuseProv {
		return &reuseProv{main: reuseMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return finalText(reuseBody)
		}}
	}
	t.Run("model differs", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov, compact := newProv(), newProv()
		runner := compactRunner(t, repo, prov, nil, func(c *Config) {
			c.ContextWindow, c.CompactReserveTokens = 100000, 1000
			c.HandoffReusePrefix = true
			// Another model for summaries: its cache cannot serve this prefix.
			c.CompactProvider, c.CompactModel = compact, "other"
		})
		ctx := context.Background()
		sid := oneTurn(t, runner)
		_, err := runner.Compact(ctx, sid)
		assert.NoErr(t, err)
		_, old := compact.handoffCalls()
		assert.Eq(t, 1, len(old))
		assert.Eq(t, []string{handoffReuseFallbackPrefix + handoffFallbackModelDiffers}, handoffReuseAttrs(t, repo, sid))
	})
	t.Run("no request", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := newProv()
		runner := reusedRunner(t, repo, prov)
		ctx := context.Background()
		sid := oneTurn(t, runner)
		// A new process resumes the session: its tracker holds no request.
		other := reusedRunner(t, repo, prov)
		_, err := other.Compact(ctx, sid)
		assert.NoErr(t, err)
		_, old := prov.handoffCalls()
		assert.Eq(t, 1, len(old))
		assert.Eq(t, []string{handoffReuseFallbackPrefix + handoffFallbackNoRequest}, handoffReuseAttrs(t, repo, sid))
	})
	t.Run("window", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := newProv()
		runner := compactRunner(t, repo, prov, nil, func(c *Config) {
			// A window too small for the whole segment view, the instruction and the
			// reserve (while the view itself still passes the break threshold).
			c.ContextWindow, c.CompactReserveTokens = 1000, 0
			c.HandoffReusePrefix = true
		})
		ctx := context.Background()
		res, err := runner.Run(ctx, RunRequest{Prompt: strings.Repeat("a", 3000)})
		assert.NoErr(t, err)
		_, err = runner.Compact(ctx, res.SessionID)
		assert.NoErr(t, err)
		_, old := prov.handoffCalls()
		assert.Eq(t, 1, len(old))
		assert.Eq(t, []string{handoffReuseFallbackPrefix + handoffFallbackWindow}, handoffReuseAttrs(t, repo, res.SessionID))
	})
}

// A reuse answer that is no handoff package is thrown away: the old path asks
// again, and the trace names the reason on the second call.
func TestHandoffReuseDiscardsBadAnswer(t *testing.T) {
	for name, reason := range map[string]string{
		"tool call":        handoffFallbackToolCall,
		"empty":            handoffFallbackEmpty,
		"missing sections": handoffFallbackMissingSections,
	} {
		t.Run(name, func(t *testing.T) {
			repo := openObserveRepo(t)
			bad := map[string]func() (*agentcore.GenerateResult, error){
				handoffFallbackToolCall: func() (*agentcore.GenerateResult, error) { return callTool("c9") },
				handoffFallbackEmpty:    func() (*agentcore.GenerateResult, error) { return finalText("  ") },
				handoffFallbackMissingSections: func() (*agentcore.GenerateResult, error) {
					return finalText("## 当前任务\n修解析器\n## 下一步\n无")
				},
			}[reason]
			prov := &reuseProv{main: reuseMain, handoff: func(_ int, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
				if isReuseCall(req) {
					return bad()
				}
				return finalText(reuseBody)
			}}
			runner := reusedRunner(t, repo, prov)
			ctx := context.Background()
			sid := oneTurn(t, runner)
			_, err := runner.Compact(ctx, sid)
			assert.NoErr(t, err)
			reuse, old := prov.handoffCalls()
			assert.Eq(t, 1, len(reuse))
			assert.Eq(t, 1, len(old))
			assert.Eq(t, []string{handoffReusePrefix, handoffReuseFallbackPrefix + reason}, handoffReuseAttrs(t, repo, sid))
			// The stored package is the old path's good answer.
			assert.StrContains(t, latestSegment(t, repo, sid).Summary, "修解析器")
		})
	}
}

// A truncated reuse answer is asked for once more on the same path, with the
// budgets restated, and the second answer becomes the package.
func TestHandoffReuseTruncatedAsksAgain(t *testing.T) {
	repo := openObserveRepo(t)
	var calls int
	prov := &reuseProv{main: reuseMain, handoff: func(_ int, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if !isReuseCall(req) {
			return finalText("wrong path")
		}
		calls++
		if calls == 1 {
			return &agentcore.GenerateResult{Content: reuseBody, FinishReason: agentcore.FinishFinal, Truncated: true}, nil
		}
		return finalText(strings.Replace(reuseBody, "修解析器", "二次-修解析器", 1))
	}}
	runner := reusedRunner(t, repo, prov)
	ctx := context.Background()
	sid := oneTurn(t, runner)
	_, err := runner.Compact(ctx, sid)
	assert.NoErr(t, err)
	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(old))
	assert.Require(t, assert.Eq(t, 2, len(reuse)))
	second := reuse[1].Messages[len(reuse[1].Messages)-1].Content
	assert.StrContains(t, second, "上一次的输出被输出上限截断")
	assert.StrContains(t, latestSegment(t, repo, sid).Summary, "二次-修解析器")
	assert.Eq(t, []string{handoffReusePrefix, handoffReusePrefix}, handoffReuseAttrs(t, repo, sid))
}

// The paths outside the scope (H1) keep their request and record no reuse attr:
// the cross-session handoff and the idle consolidation.
func TestHandoffReuseOutOfScope(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: reuseMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	runner := reusedRunner(t, repo, prov)
	ctx := context.Background()
	sid := oneTurn(t, runner)

	t.Run("cross-session handoff", func(t *testing.T) {
		sess, err := runner.Handoff(ctx, HandoffRequest{SourceID: sid, NewID: "handed-off-1"})
		assert.NoErr(t, err)
		reuse, old := prov.handoffCalls()
		assert.Eq(t, 0, len(reuse))
		assert.Eq(t, 1, len(old))
		assert.Eq(t, 0, len(handoffReuseAttrs(t, repo, sess.ID)))
	})
	t.Run("idle consolidation", func(t *testing.T) {
		_, err := runner.ConsolidateIdle(ctx, sid)
		if err != nil {
			// Nothing left to cut after the handoff read the view: not a failure of
			// this test's subject.
			assert.Eq(t, ErrNothingToCompact, err)
			return
		}
		reuse, _ := prov.handoffCalls()
		assert.Eq(t, 0, len(reuse))
		assert.Eq(t, 0, len(handoffReuseAttrs(t, repo, sid)))
	})
}
