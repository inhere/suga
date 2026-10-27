package personal

import (
	"context"
	"encoding/json"
	"fmt"
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
// call through handoff. It reports SupportsToolChoice, so a reuse request that
// forbade tools would show it - the reuse path must not (H5: tool_choice:none
// changes the rendered prefix and the provider cache stops matching). It keeps
// every request it was handed.
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

// toolsJSON encodes a tool list for a byte-for-byte comparison.
func toolsJSON(t *testing.T, schemas []agentcore.ToolSchema) string {
	t.Helper()
	raw, err := json.Marshal(schemas)
	assert.Require(t, assert.NoErr(t, err))
	return string(raw)
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
	assert.Eq(t, "", got.ToolChoice)
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
	t.Run("prompt needs state", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := newProv()
		ctx := context.Background()
		sid := oneTurn(t, reusedRunner(t, repo, prov))
		// A break between turns has no run state: a prompt builder that renders it
		// cannot reproduce the head of the last request, so the old path is taken.
		other := compactRunner(t, repo, prov, nil, func(c *Config) {
			c.ContextWindow, c.CompactReserveTokens = 100000, 1000
			c.HandoffReusePrefix = true
			c.PromptBuilder = stateTestPrompt{}
		})
		_, err := other.Compact(ctx, sid)
		assert.NoErr(t, err)
		_, old := prov.handoffCalls()
		assert.Eq(t, 1, len(old))
		assert.Eq(t, []string{handoffReuseFallbackPrefix + handoffFallbackPromptState}, handoffReuseAttrs(t, repo, sid))
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
			want := handoffReuseFallbackPrefix + reason
			if reason == handoffFallbackMissingSections {
				// The reason names the sections the answer dropped, in template order
				// (H5): this answer has the task and the next step only.
				want += ":" + handoffSectionCoords + "," + handoffSectionVerified + "," +
					handoffSectionProgress + "," + handoffSectionRefs
			}
			assert.Eq(t, []string{handoffReusePrefix, want}, handoffReuseAttrs(t, repo, sid))
			// The stored package is the old path's good answer.
			assert.StrContains(t, latestSegment(t, repo, sid).Summary, "修解析器")
		})
	}
}

// The model may write the package's headings one level deeper - what the answers
// discarded in the pre-run looked like (H5): such an answer is a package, the
// old path is not asked again, and the headings are stored in the "## " form the
// store, the budgets and the fold read.
func TestHandoffReuseAcceptsDeeperHeadings(t *testing.T) {
	repo := openObserveRepo(t)
	deep := strings.ReplaceAll(reuseBody, "## ", "### ")
	prov := &reuseProv{main: reuseMain, handoff: func(_ int, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if isReuseCall(req) {
			return finalText(deep)
		}
		return finalText("old path answer")
	}}
	runner := reusedRunner(t, repo, prov)
	ctx := context.Background()
	sid := oneTurn(t, runner)
	_, err := runner.Compact(ctx, sid)
	assert.NoErr(t, err)
	reuse, old := prov.handoffCalls()
	assert.Eq(t, 1, len(reuse))
	assert.Eq(t, 0, len(old))
	assert.Eq(t, []string{handoffReusePrefix}, handoffReuseAttrs(t, repo, sid))
	summary := latestSegment(t, repo, sid).Summary
	assert.StrContains(t, summary, "## "+handoffSectionTask)
	assert.StrContains(t, summary, "## "+handoffSectionRefs)
	assert.Eq(t, false, strings.Contains(summary, "### "))
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

// rebuildRunner is a runner whose threshold break needs a context of about the
// given size: window 20000 with a reserve of 8000 means the break fires above
// 12000 tokens, well above the small turn used to fill the context and well
// below the window the reuse request has to fit (H2).
func rebuildRunner(t *testing.T, repo *store.Store, prov agentcore.Provider) *Runner {
	t.Helper()
	return compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 30000, 12000
		c.HandoffReusePrefix = true
		c.PromptBuilder = staticTestPrompt("STABLE-SYSTEM-PROMPT")
	})
}

// TestRebuildDiffNamesTheDifference covers the diagnostic of H4: a rebuild that
// does not reproduce the persisted request says which part differs, so a
// fallback:prefix_changed attr tells system, tools and message apart.
func TestRebuildDiffNamesTheDifference(t *testing.T) {
	req := &agentcore.GenerateRequest{System: "PROMPT",
		Tools:    []agentcore.ToolSchema{{Name: "file_read"}},
		Messages: []agentcore.Message{{Role: "user", Content: "one"}, {Role: "assistant", Content: "two"}}}
	rec := printRequest(req)

	t.Run("same request", func(t *testing.T) {
		assert.Eq(t, "", rebuildDiff(rec, req))
		assert.True(t, rebuildMatches(rec, req))
	})
	t.Run("system prompt", func(t *testing.T) {
		other := *req
		other.System = "OTHER"
		assert.Eq(t, diffSystem, rebuildDiff(rec, &other))
	})
	t.Run("tool table", func(t *testing.T) {
		other := *req
		other.Tools = []agentcore.ToolSchema{{Name: "file_read"}, {Name: "task"}}
		assert.Eq(t, diffTools, rebuildDiff(rec, &other))
	})
	t.Run("first differing message", func(t *testing.T) {
		other := *req
		other.Messages = append([]agentcore.Message{{Role: "user", Content: "zero"}}, req.Messages...)
		assert.Eq(t, "msg:0", rebuildDiff(rec, &other))
	})
	t.Run("second differing message", func(t *testing.T) {
		other := *req
		other.Messages = []agentcore.Message{{Role: "user", Content: "one"}, {Role: "assistant", Content: "CHANGED"}}
		assert.Eq(t, "msg:1", rebuildDiff(rec, &other))
	})
	t.Run("shorter rebuild", func(t *testing.T) {
		other := *req
		other.Messages = req.Messages[:1]
		assert.Eq(t, diffLength, rebuildDiff(rec, &other))
	})
	t.Run("no fingerprint", func(t *testing.T) {
		assert.Eq(t, "", rebuildDiff(nil, req))
	})
}

// A process that resumed the session breaks the segment before it sends any
// request of its own, so no request exists in memory (H2): the handoff call
// continues the prefix through the request the main loop is about to send - the
// previous process's System and Tools byte for byte, its messages as a prefix,
// then this turn's request and the instruction.
func TestHandoffReuseRebuildsResumedPrefix(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText("done")
	}, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	ctx := context.Background()
	// The first process fills the context to ~12000 tokens: below the break
	// threshold, so it breaks nothing and its last request is the segment prefix.
	first := rebuildRunner(t, repo, prov)
	res, err := first.Run(ctx, RunRequest{Prompt: strings.Repeat("a", 48000)})
	assert.Require(t, assert.NoErr(t, err))
	sid := res.SessionID
	assert.Eq(t, 0, len(compactionEntries(t, repo, sid)))
	last := prov.lastMainReq()
	assert.Require(t, assert.NotNil(t, last))

	// A second process resumes the session: this turn's request crosses the
	// threshold, so the segment break happens before this process sent anything.
	second := rebuildRunner(t, repo, prov)
	prompt := strings.Repeat("b", 32000)
	_, err = second.Run(ctx, RunRequest{SessionID: sid, Prompt: prompt})
	assert.NoErr(t, err)

	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(old))
	assert.Require(t, assert.Eq(t, 1, len(reuse)))
	assert.Eq(t, []string{handoffReusePrefix}, handoffReuseAttrs(t, repo, sid))
	got := reuse[0]
	// System and Tools are exactly what the previous process last sent.
	assert.Eq(t, last.System, got.System)
	assert.Eq(t, toolsJSON(t, last.Tools), toolsJSON(t, got.Tools))
	// Its messages are the request's prefix; the rest is only what came after it
	// in this turn - the previous answer, this turn's request - and the
	// instruction.
	assert.Require(t, assert.True(t, len(got.Messages) > len(last.Messages)))
	assert.Eq(t, messagesJSON(t, last.Messages), messagesJSON(t, got.Messages[:len(last.Messages)]))
	rest := got.Messages[len(last.Messages):]
	assert.Require(t, assert.Eq(t, 3, len(rest)))
	assert.Eq(t, "assistant", rest[0].Role)
	assert.Eq(t, agentcore.Message{Role: "user", Content: prompt}, rest[1])
	instruction := rest[2]
	assert.Eq(t, "user", instruction.Role)
	assert.StrContains(t, instruction.Content, reuseMarker)
	assert.StrContains(t, instruction.Content, compactTemplate)
	assert.Eq(t, false, strings.Contains(instruction.Content, "# 待压缩的对话"))
	assert.Eq(t, "", got.ToolChoice)
}

// The same process breaks the segment after it sent requests: the request in
// memory is the base (it carries the hook changes of this turn, such as the
// deadline hint), so the instruction continues exactly what was sent and not a
// rebuild of a request this process never sent.
func TestHandoffReuseContinuesSentRequestInProcess(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText("done")
	}, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	runner := rebuildRunner(t, repo, prov)
	ctx := context.Background()
	// The first turn fills the context to ~12000 tokens: no break.
	first, err := runner.Run(ctx, RunRequest{Prompt: strings.Repeat("a", 48000)})
	assert.Require(t, assert.NoErr(t, err))
	base := prov.lastMainReq()
	assert.Require(t, assert.NotNil(t, base))

	// The next turn of the same process crosses the threshold, so the segment
	// breaks before this turn's request is sent.
	prompt := strings.Repeat("b", 32000)
	_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: prompt})
	assert.NoErr(t, err)

	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(old))
	assert.Require(t, assert.Eq(t, 1, len(reuse)))
	assert.Eq(t, []string{handoffReusePrefix}, handoffReuseAttrs(t, repo, first.SessionID))
	got := reuse[0]
	assert.Eq(t, base.System, got.System)
	assert.Eq(t, toolsJSON(t, base.Tools), toolsJSON(t, got.Tools))
	assert.Require(t, assert.Eq(t, len(base.Messages)+1, len(got.Messages)))
	assert.Eq(t, messagesJSON(t, base.Messages), messagesJSON(t, got.Messages[:len(base.Messages)]))
	// This turn's request is not in the base: the request in memory stops at what
	// was sent, and the new request stays in the kept tail.
	assert.Eq(t, false, strings.Contains(messagesJSON(t, got.Messages[:len(got.Messages)-1]), strings.Repeat("b", 64)))
	instruction := got.Messages[len(got.Messages)-1]
	assert.StrContains(t, instruction.Content, reuseMarker)
	assert.Eq(t, "", got.ToolChoice)
}

// A host hook may rewrite the request head in a way a rebuilt request cannot
// reproduce (and the reuse call does not go through the hook chain), so a
// session with a host hook keeps the old path when there is no request in
// memory to continue.
func TestHandoffReuseRebuildRefusedWithHostHook(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText("done")
	}, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	ctx := context.Background()
	res, err := rebuildRunner(t, repo, prov).Run(ctx, RunRequest{Prompt: strings.Repeat("a", 48000)})
	assert.Require(t, assert.NoErr(t, err))
	sid := res.SessionID
	// The second process carries a host hook: no rebuild it could trust.
	second := compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 30000, 12000
		c.HandoffReusePrefix = true
		c.PromptBuilder = staticTestPrompt("STABLE-SYSTEM-PROMPT")
		c.Hooks = []agentcore.Hook{agentcore.NopHook{}}
	})
	_, err = second.Run(ctx, RunRequest{SessionID: sid, Prompt: strings.Repeat("b", 32000)})
	assert.NoErr(t, err)
	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(reuse))
	assert.Eq(t, 1, len(old))
	assert.Eq(t, []string{handoffReuseFallbackPrefix + handoffFallbackHostHook}, handoffReuseAttrs(t, repo, sid))
}

// betweenTurnRunner is a process that runs turns and may break the segment
// between them on the session prefix: a stable system prompt, the same tool
// registry and a window wide enough for the small test views.
func betweenTurnRunner(t *testing.T, repo *store.Store, prov agentcore.Provider, prompt string) *Runner {
	t.Helper()
	return compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 100000, 1000
		c.HandoffReusePrefix = true
		c.PromptBuilder = staticTestPrompt(prompt)
	})
}

// stateTestPrompt is a prompt builder that renders the run state (as a per-step
// prompt would): its bytes exist only inside a turn.
type stateTestPrompt struct{}

func (stateTestPrompt) BuildSystemPrompt(_ context.Context, state *agentcore.RunState) (string, error) {
	return fmt.Sprintf("STEP=%d", state.Step), nil
}

// finalMain answers every main-loop call with a final answer, so a turn is one
// request.
func finalMain(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	return finalText("done")
}

// twoTurns runs two turns of one session and returns its id.
func twoTurns(t *testing.T, runner *Runner) string {
	t.Helper()
	ctx := context.Background()
	res, err := runner.Run(ctx, RunRequest{Prompt: "fix the parser in compact.go"})
	assert.Require(t, assert.NoErr(t, err))
	_, err = runner.Run(ctx, RunRequest{SessionID: res.SessionID, Prompt: "and its tests"})
	assert.Require(t, assert.NoErr(t, err))
	return res.SessionID
}

// A break between turns in a fresh process (suga session compact, chat :compact
// after a restart): this process sent no request, so the handoff call rebuilds
// the session's last main-loop request from the persisted view - the same prompt
// builder and tool registry, the view as its messages - and continues it (H3).
func TestHandoffReuseBetweenTurnsRebuildsPrefix(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: finalMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	ctx := context.Background()
	sid := twoTurns(t, betweenTurnRunner(t, repo, prov, "STABLE-SYSTEM-PROMPT"))
	last := prov.lastMainReq()
	assert.Require(t, assert.NotNil(t, last))

	// The same store through a new runner: no request of this process exists.
	other := betweenTurnRunner(t, repo, prov, "STABLE-SYSTEM-PROMPT")
	assert.Eq(t, (*agentcore.GenerateRequest)(nil), other.rec.prefix.lastRequest(sid))
	_, err := other.Compact(ctx, sid)
	assert.NoErr(t, err)

	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(old))
	assert.Require(t, assert.Eq(t, 1, len(reuse)))
	assert.Eq(t, []string{handoffReusePrefix}, handoffReuseAttrs(t, repo, sid))
	got := reuse[0]
	// System and Tools are exactly what the previous process last sent.
	assert.Eq(t, last.System, got.System)
	assert.Eq(t, toolsJSON(t, last.Tools), toolsJSON(t, got.Tools))
	// Its messages are the request's prefix; the rest is the answer that turn
	// produced and the instruction.
	assert.Require(t, assert.True(t, len(got.Messages) > len(last.Messages)))
	assert.Eq(t, messagesJSON(t, last.Messages), messagesJSON(t, got.Messages[:len(last.Messages)]))
	rest := got.Messages[len(last.Messages):]
	assert.Require(t, assert.Eq(t, 2, len(rest)))
	assert.Eq(t, "assistant", rest[0].Role)
	assert.Eq(t, "done", rest[0].Content)
	instruction := rest[1]
	assert.Eq(t, "user", instruction.Role)
	assert.StrContains(t, instruction.Content, reuseMarker)
	assert.StrContains(t, instruction.Content, compactTemplate)
	assert.Eq(t, false, strings.Contains(instruction.Content, "# 待压缩的对话"))
	// No ToolChoice even though the provider supports it: tool_choice:none makes
	// the gateway render the prompt without the tool definitions, which breaks the
	// cache prefix the reuse path continues (H5). The instruction forbids tools and
	// a tool-asking answer falls back to the old path.
	assert.Eq(t, "", got.ToolChoice)
	// The package the answer produced is stored as usual.
	assert.StrContains(t, latestSegment(t, repo, sid).Summary, "修解析器")
}

// The same process breaks the segment between turns (chat :compact): the request
// it sent a moment ago is the base, exactly as sent.
func TestHandoffReuseBetweenTurnsSameRunner(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: finalMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	ctx := context.Background()
	runner := betweenTurnRunner(t, repo, prov, "STABLE-SYSTEM-PROMPT")
	sid := twoTurns(t, runner)
	last := prov.lastMainReq()
	assert.Require(t, assert.NotNil(t, last))

	_, err := runner.Compact(ctx, sid)
	assert.NoErr(t, err)

	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(old))
	assert.Require(t, assert.Eq(t, 1, len(reuse)))
	assert.Eq(t, []string{handoffReusePrefix}, handoffReuseAttrs(t, repo, sid))
	got := reuse[0]
	assert.Eq(t, last.System, got.System)
	assert.Eq(t, toolsJSON(t, last.Tools), toolsJSON(t, got.Tools))
	assert.Require(t, assert.Eq(t, len(last.Messages)+1, len(got.Messages)))
	assert.Eq(t, messagesJSON(t, last.Messages), messagesJSON(t, got.Messages[:len(last.Messages)]))
	instruction := got.Messages[len(got.Messages)-1]
	assert.Eq(t, "user", instruction.Role)
	assert.StrContains(t, instruction.Content, reuseMarker)
	assert.Eq(t, "", got.ToolChoice)
}

// A part of the last request that the store does not keep - the turn's hook
// context is sent for that turn only - cannot be reproduced between turns, so
// the old path is taken instead of sending a prefix the provider cache misses.
func TestHandoffReuseBetweenTurnsRefusesUnstoredMessages(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: finalMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	ctx := context.Background()
	res, err := betweenTurnRunner(t, repo, prov, "STABLE-SYSTEM-PROMPT").Run(ctx,
		RunRequest{Prompt: "one", SystemContext: "hook: this turn only"})
	assert.Require(t, assert.NoErr(t, err))

	other := betweenTurnRunner(t, repo, prov, "STABLE-SYSTEM-PROMPT")
	_, err = other.Compact(ctx, res.SessionID)
	assert.NoErr(t, err)

	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(reuse))
	assert.Eq(t, 1, len(old))
	// The attr names where the rebuild differs: the hook context message of the
	// turn is not in the persisted view, so a message differs (H4).
	attrs := handoffReuseAttrs(t, repo, res.SessionID)
	assert.Eq(t, 1, len(attrs))
	assert.True(t, strings.HasPrefix(attrs[0], handoffReuseFallbackPrefix+handoffFallbackPrefixChanged+":msg:"))
}

// A rebuilt base that does not reproduce what the last turn sent keeps the old
// path: another system prompt (a different configuration or binary) means the
// provider cache would miss most of the request anyway.
func TestHandoffReuseBetweenTurnsRefusesChangedPrefix(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &reuseProv{main: finalMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(reuseBody)
	}}
	ctx := context.Background()
	sid := twoTurns(t, betweenTurnRunner(t, repo, prov, "STABLE-SYSTEM-PROMPT"))

	other := betweenTurnRunner(t, repo, prov, "OTHER-SYSTEM-PROMPT")
	_, err := other.Compact(ctx, sid)
	assert.NoErr(t, err)

	reuse, old := prov.handoffCalls()
	assert.Eq(t, 0, len(reuse))
	assert.Eq(t, 1, len(old))
	// The attr names the system prompt as the part the rebuild cannot reproduce.
	assert.Eq(t, []string{handoffReuseFallbackPrefix + handoffFallbackPrefixChanged + ":" + diffSystem}, handoffReuseAttrs(t, repo, sid))
}

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
