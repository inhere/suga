package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Small window so a few KB of text crosses the threshold:
// threshold = 3000-500 = 2500 tokens (~10000 chars).
const (
	testWindow  = 3000
	testReserve = 500
)

// scriptProv answers summary requests itself and delegates ordinary model
// calls to normal(n, req) with n counting only ordinary calls.
type scriptProv struct {
	mu      sync.Mutex
	name    string
	normal  func(n int, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error)
	reqs    []*agentcore.GenerateRequest // ordinary calls
	sumReqs []*agentcore.GenerateRequest // summary calls
	sumErr  error                        // when set, summary calls fail with it
}

func (p *scriptProv) Name() string { return p.name }

func (p *scriptProv) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	cp := *req
	cp.Messages = append([]agentcore.Message(nil), req.Messages...)
	p.mu.Lock()
	if req.System == compactSystemPrompt {
		p.sumReqs = append(p.sumReqs, &cp)
		n, sumErr := len(p.sumReqs), p.sumErr
		p.mu.Unlock()
		if sumErr != nil {
			return nil, sumErr
		}
		return &agentcore.GenerateResult{Content: fmt.Sprintf("SUMMARY-%s-%d", p.name, n), FinishReason: agentcore.FinishFinal}, nil
	}
	p.reqs = append(p.reqs, &cp)
	n := len(p.reqs)
	p.mu.Unlock()
	return p.normal(n, &cp)
}

func finalText(s string) (*agentcore.GenerateResult, error) {
	return &agentcore.GenerateResult{Content: s, FinishReason: agentcore.FinishFinal}, nil
}

func callTool(id string) (*agentcore.GenerateResult, error) {
	return &agentcore.GenerateResult{
		ToolCalls:    []agentcore.ToolCall{{CallID: id, Name: "big", Arguments: map[string]any{}}},
		FinishReason: agentcore.FinishToolCalls,
	}, nil
}

func bigToolRegistry(size int, onCall func(int)) agentcore.ToolRegistry {
	reg := tools.NewRegistry()
	var mu sync.Mutex
	n := 0
	reg.Register(fnTool{name: "big", fn: func() (*agentcore.ToolResult, error) {
		mu.Lock()
		n++
		i := n
		mu.Unlock()
		if onCall != nil {
			onCall(i)
		}
		return &agentcore.ToolResult{Name: "big", Content: strings.Repeat("x", size)}, nil
	}})
	return reg
}

func compactRunner(t *testing.T, repo *store.Store, prov agentcore.Provider, reg agentcore.ToolRegistry, mut func(*Config)) *Runner {
	t.Helper()
	cfg := Config{
		Repository: repo, Provider: prov, Tools: reg, Model: "m", ProviderName: "p", MaxSteps: 80,
		ContextWindow: testWindow, CompactReserveTokens: testReserve,
	}
	if mut != nil {
		mut(&cfg)
	}
	r, err := New(cfg)
	assert.NoErr(t, err)
	return r
}

// assertWellFormed checks a request's messages: first is user (or a leading
// system notice then user) and every call is directly followed by its result.
func assertWellFormed(t *testing.T, msgs []agentcore.Message) {
	t.Helper()
	assert.Eq(t, true, len(msgs) > 0)
	first := 0
	for first < len(msgs) && msgs[first].Role == "system" {
		first++
	}
	assert.Eq(t, "user", msgs[first].Role)
	for i, m := range msgs {
		switch {
		case m.Role == "tool":
			assert.Eq(t, true, i > 0 && msgs[i-1].Role == "assistant" && msgs[i-1].ToolCall != nil && m.ToolCall != nil && msgs[i-1].ToolCall.CallID == m.ToolCall.CallID)
		case m.Role == "assistant" && m.ToolCall != nil:
			assert.Eq(t, true, i+1 < len(msgs) && msgs[i+1].Role == "tool" && msgs[i+1].ToolCall.CallID == m.ToolCall.CallID)
		}
	}
}

// compactionEntries lists the session's compaction records that start a
// segment with a summary (the reason=start record of a new session excluded,
// see segmentRecords).
func compactionEntries(t *testing.T, repo *store.Store, sessionID string) []compactionRecord {
	t.Helper()
	var out []compactionRecord
	for _, rec := range segmentRecords(t, repo, sessionID) {
		if rec.Reason != segmentReasonStart {
			out = append(out, rec)
		}
	}
	return out
}

// segmentRecords lists every compaction (segment start) record of the session.
func segmentRecords(t *testing.T, repo *store.Store, sessionID string) []compactionRecord {
	t.Helper()
	es, err := repo.ListEntries(context.Background(), sessionID, store.EntryFilter{Types: []string{store.EntryCompaction}})
	assert.NoErr(t, err)
	var out []compactionRecord
	for _, e := range es {
		var rec compactionRecord
		assert.NoErr(t, json.Unmarshal(e.JSON, &rec))
		out = append(out, rec)
	}
	return out
}

func TestCompactAcrossTurnsExceedingThreshold(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(fmt.Sprintf("answer-%d %s", n, strings.Repeat("a", 800)))
	}}
	runner := compactRunner(t, repo, prov, nil, nil)
	sid := ""
	for i := 1; i <= 14; i++ {
		res, err := runner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: fmt.Sprintf("question-%d %s", i, strings.Repeat("q", 800))})
		assert.NoErr(t, err)
		sid = res.SessionID
	}
	assert.Eq(t, true, len(prov.sumReqs) >= 1)
	recs := compactionEntries(t, repo, sid)
	assert.Eq(t, true, len(recs) >= 1)
	for _, rec := range recs {
		assert.Eq(t, true, rec.TokensAfter < rec.TokensBefore && rec.FirstKeptSeq > 0)
	}
	evs := eventsOf(t, repo, sid, store.EventCompaction)
	assert.Eq(t, len(recs), len(evs))
	assert.Eq(t, "ok", evs[0].Status)
	// Every ordinary request fits the window and is well formed; the latest holds
	// the summary as first message plus the newest turn.
	for _, req := range prov.reqs {
		assertWellFormed(t, req.Messages)
		assert.Eq(t, true, charsOfMessages(req.Messages)/4 <= testWindow)
	}
	last := prov.reqs[len(prov.reqs)-1]
	assert.Eq(t, true, strings.Contains(last.Messages[0].Content, "SUMMARY-main-"))
	assert.Eq(t, true, strings.Contains(last.Messages[len(last.Messages)-1].Content, "question-14"))
}

func TestCompactInsideSingleLongTurn(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if n <= 40 {
			return callTool(fmt.Sprintf("c%d", n))
		}
		return finalText("done")
	}}
	runner := compactRunner(t, repo, prov, bigToolRegistry(2000, nil), nil)
	res, err := runner.Run(context.Background(), RunRequest{Prompt: "do the long job"})
	assert.NoErr(t, err)
	assert.Eq(t, "done", res.Output)
	assert.Eq(t, 41, len(prov.reqs))
	assert.Eq(t, true, len(prov.sumReqs) >= 2) // repeated compactions in one turn
	for _, req := range prov.reqs {
		assertWellFormed(t, req.Messages)
		assert.Eq(t, true, charsOfMessages(req.Messages)/4 <= testWindow)
	}
	// All of the turn's messages stay persisted in full.
	turnMsgs, err := repo.AgentStorage().LoadMessages(context.Background(), res.TurnID)
	assert.NoErr(t, err)
	assert.Eq(t, 1+80+1, len(turnMsgs))
	// The cut maps to a persisted message entry of this turn.
	recs := compactionEntries(t, repo, res.SessionID)
	assert.Eq(t, true, len(recs) >= 2 && recs[1].FirstKeptSeq > recs[0].FirstKeptSeq)
}

func TestCompactHugeToolResultTruncatesOrFails(t *testing.T) {
	t.Run("truncated and continues", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			if n == 1 {
				return callTool("c1")
			}
			return finalText("ok")
		}}
		// The output layer would cut the result first; lift its threshold so
		// the huge result reaches the context builder's truncation ladder.
		runner := compactRunner(t, repo, prov, bigToolRegistry(60000, nil), func(c *Config) { c.InlineOutputBytes = 1 << 20 })
		res, err := runner.Run(context.Background(), RunRequest{Prompt: "read it"})
		assert.NoErr(t, err)
		assert.Eq(t, "ok", res.Output)
		second := prov.reqs[1].Messages
		toolMsg := second[len(second)-1]
		assert.Eq(t, "tool", toolMsg.Role)
		assert.Eq(t, true, len(toolMsg.Content) <= 8200 && strings.Contains(toolMsg.Content, "truncated"))
		assert.Eq(t, true, len(toolMsg.ToolResult.Content) <= 8200)
		assert.Eq(t, 0, len(prov.sumReqs)) // nothing cuttable
		evs := eventsOf(t, repo, res.SessionID, store.EventCompaction)
		assert.Eq(t, 1, len(evs))
		assert.Eq(t, "error", evs[0].Status)
		// The original is kept in full.
		msgs, err := repo.AgentStorage().LoadMessages(context.Background(), res.TurnID)
		assert.NoErr(t, err)
		assert.Eq(t, 60000, len(msgs[2].Content))
	})
	t.Run("still too big fails the turn", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			if n == 1 {
				return callTool("c1")
			}
			return finalText("never")
		}}
		runner := compactRunner(t, repo, prov, bigToolRegistry(60000, nil), func(c *Config) {
			c.ContextWindow, c.CompactReserveTokens = 600, 100 // threshold 500 tokens < 2000 chars floor
			c.InlineOutputBytes = 1 << 20
		})
		res, err := runner.Run(context.Background(), RunRequest{Prompt: "read it"})
		assert.Eq(t, true, errors.Is(err, ErrContextOverflow))
		assert.Eq(t, RunFailed, res.State)
		assert.Eq(t, 1, len(prov.reqs)) // no retry loop
	})
}

func TestSegmentCutKeepsLastGroup(t *testing.T) {
	user := func(s string) ctxItem { return ctxItem{msg: agentcore.Message{Role: "user", Content: s}} }
	call := func(id string) ctxItem {
		return ctxItem{msg: agentcore.Message{Role: "assistant", ToolCall: &agentcore.ToolCall{CallID: id, Name: "t"}}}
	}
	result := func(id string) ctxItem {
		return ctxItem{msg: agentcore.Message{Role: "tool", Content: strings.Repeat("r", 400),
			ToolCall: &agentcore.ToolCall{CallID: id}, ToolResult: &agentcore.ToolResult{CallID: id, Content: strings.Repeat("r", 400)}}}
	}
	final := ctxItem{msg: agentcore.Message{Role: "assistant", Content: "fin"}}
	// user, A1,T1, A2,T2 (parallel calls of one response), final, user
	items := []ctxItem{user("u1"), call("1"), result("1"), call("2"), result("2"), final, user("u2")}
	t.Run("legal cut positions", func(t *testing.T) {
		legal := map[int]bool{}
		for i := 1; i < len(items); i++ {
			if validCut(items, i) {
				legal[i] = true
			}
		}
		// before u2(6), before final(5), between T1 and A2(3); never before T(2,4) or A1/A2 after a user(1).
		assert.Eq(t, map[int]bool{3: true, 5: true, 6: true}, legal)
	})
	t.Run("mid-turn: only the last call/result group stays", func(t *testing.T) {
		assert.Eq(t, 3, segmentCut(items[:5], false)) // A2,T2
		assert.Eq(t, 3, segmentCut(items[:5], true))
		// A group right after the user cannot be split from it: keep u1 too.
		assert.Eq(t, 0, segmentCut(items[:3], false))
		assert.Eq(t, 1, segmentCut([]ctxItem{user("u0"), user("u1"), call("1"), result("1")}, false))
	})
	t.Run("a finished group is history", func(t *testing.T) {
		assert.Eq(t, 6, segmentCut(items, false)) // pending u2 alone
		assert.Eq(t, 5, segmentCut(items[:6], false))
	})
	t.Run("between turns the whole view may go", func(t *testing.T) {
		assert.Eq(t, 6, segmentCut(items[:6], true))
		noTools := []ctxItem{user("a"), final, user("b"), final}
		assert.Eq(t, 4, segmentCut(noTools, true))
		assert.Eq(t, 2, segmentCut(noTools, false)) // the last exchange
		assert.Eq(t, 0, segmentCut([]ctxItem{user("a")}, true))
	})
	t.Run("every cut is legal", func(t *testing.T) {
		for n := 1; n <= len(items); n++ {
			for _, end := range []bool{false, true} {
				cut := segmentCut(items[:n], end)
				if cut == 0 || cut == n {
					continue
				}
				assert.Eq(t, true, validCut(items[:n], cut))
				assert.Eq(t, true, items[cut].msg.Role != "tool")
			}
		}
	})
}

func TestCompactedTurnCancelledIsNotAdopted(t *testing.T) {
	for _, mode := range []string{"cancelled", "failed"} {
		t.Run(mode, func(t *testing.T) {
			repo := openObserveRepo(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var prov *scriptProv
			turn2 := false
			prov = &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
				if !turn2 {
					return finalText("first answer " + strings.Repeat("f", 100))
				}
				// Doomed turn: keep calling the tool until a compaction happened,
				// then die in the requested way.
				if len(prov.sumReqs) > 0 {
					if mode == "failed" {
						return nil, errors.New("provider exploded")
					}
					cancel()
				}
				return callTool(fmt.Sprintf("c%d", n))
			}}
			runner := compactRunner(t, repo, prov, bigToolRegistry(2000, nil), nil)
			first, err := runner.Run(context.Background(), RunRequest{Prompt: "first question"})
			assert.NoErr(t, err)
			turn2 = true
			bad, err := runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "doomed question"})
			assert.Eq(t, true, err != nil)
			want := RunFailed
			if mode == "cancelled" {
				want = RunCanceled
			}
			assert.Eq(t, want, bad.State)
			// The doomed turn did write a compaction entry ...
			es, err := repo.ListEntries(context.Background(), first.SessionID, store.EntryFilter{TurnID: bad.TurnID, Types: []string{store.EntryCompaction}})
			assert.NoErr(t, err)
			assert.Eq(t, true, len(es) >= 1)

			// ... which the next turn must not use, nor the doomed turn's messages.
			turn2 = false
			_, err = runner.Run(context.Background(), RunRequest{SessionID: first.SessionID, Prompt: "third question"})
			assert.NoErr(t, err)
			req := prov.reqs[len(prov.reqs)-1]
			var texts []string
			for _, m := range req.Messages {
				texts = append(texts, m.Role+":"+m.Content)
			}
			joined := strings.Join(texts, "\n")
			// The rendered package is this turn's own, never the doomed turn's: the
			// interrupted turn replays (suga-y0sk) and brings back the content that
			// made the doomed turn compact, so this turn compacts again as it starts
			// and folds that content into a summary of its own.
			var doomed compactionRecord
			assert.Require(t, assert.NoErr(t, json.Unmarshal(es[0].JSON, &doomed)))
			assert.Eq(t, false, strings.Contains(joined, doomed.Summary))
			assert.Eq(t, false, strings.Contains(joined, "doomed question"))
			assert.Eq(t, true, strings.Contains(joined, "first question"))
			assertWellFormed(t, req.Messages)
		})
	}
}

// Scenario-model routing of the summary calls: models.idle serves the idle
// consolidation and models.handoff the --from package; unset scenarios keep
// falling back to the compact model, then the turn's own provider.
func TestScenarioModels(t *testing.T) {
	newMain := func() *scriptProv {
		return &scriptProv{name: "main", normal: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return finalText(strings.Repeat("a", 900))
		}}
	}

	t.Run("idle consolidation uses the idle model", func(t *testing.T) {
		repo := openObserveRepo(t)
		main := newMain()
		idle := &scriptProv{name: "idle", normal: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return nil, errors.New("idle provider must only summarize")
		}}
		runner := compactRunner(t, repo, main, nil, func(c *Config) {
			c.IdleProvider, c.IdleModel = idle, "idle-model"
		})
		var sid string
		for i := 0; i < 2; i++ {
			res, err := runner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: strings.Repeat("q", 900)})
			assert.NoErr(t, err)
			sid = res.SessionID
		}
		_, err := runner.ConsolidateIdle(context.Background(), sid)
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(idle.reqs)) // ordinary calls stay on the turn provider
		assert.Eq(t, 1, len(idle.sumReqs))
		// The summary call is observed with the idle model, the compaction event
		// reports it too.
		foundModel := false
		for _, e := range eventsOf(t, repo, sid, store.EventModel) {
			foundModel = foundModel || e.Attrs["model"] == "idle-model"
		}
		assert.Eq(t, true, foundModel)
		foundCompact := false
		for _, e := range eventsOf(t, repo, sid, store.EventCompaction) {
			if e.Attrs["model"] == "idle-model" {
				foundCompact = compactTrigger(segmentReasonIdle) == e.Attrs["trigger"]
			}
		}
		assert.Eq(t, true, foundCompact)
	})

	t.Run("handoff uses the handoff model", func(t *testing.T) {
		repo := openObserveRepo(t)
		main := newMain()
		hand := &scriptProv{name: "handoff", normal: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return nil, errors.New("handoff provider must only summarize")
		}}
		runner := compactRunner(t, repo, main, nil, func(c *Config) {
			c.HandoffProvider, c.HandoffModel = hand, "handoff-model"
		})
		res, err := runner.Run(context.Background(), RunRequest{Prompt: strings.Repeat("q", 900)})
		assert.NoErr(t, err)
		newSess, err := runner.Handoff(context.Background(), HandoffRequest{SourceID: res.SessionID})
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(hand.sumReqs))
		found := false
		for _, e := range eventsOf(t, repo, newSess.ID, store.EventModel) {
			found = found || e.Attrs["model"] == "handoff-model"
		}
		assert.Eq(t, true, found)
		es, err := repo.ListEntries(context.Background(), newSess.ID, store.EntryFilter{Types: []string{store.EntryHandoff}})
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(es))
		assert.Eq(t, true, strings.Contains(string(es[0].JSON), "SUMMARY-handoff-")) // the package came from the handoff provider
	})

	t.Run("idle unset falls back to the compact model", func(t *testing.T) {
		repo := openObserveRepo(t)
		main := newMain()
		comp := &scriptProv{name: "compactor", normal: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return nil, errors.New("compact provider must only summarize")
		}}
		runner := compactRunner(t, repo, main, nil, func(c *Config) {
			c.CompactProvider, c.CompactModel = comp, "small-model"
		})
		var sid string
		for i := 0; i < 2; i++ {
			res, err := runner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: strings.Repeat("q", 900)})
			assert.NoErr(t, err)
			sid = res.SessionID
		}
		_, err := runner.ConsolidateIdle(context.Background(), sid)
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(main.sumReqs))
		assert.Eq(t, 1, len(comp.sumReqs))
	})
}

func TestCompactModelConfigured(t *testing.T) {
	run := func(t *testing.T, withCompact bool) (main, comp *scriptProv, repo *store.Store, sid string) {
		repo = openObserveRepo(t)
		main = &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return finalText(strings.Repeat("a", 900))
		}}
		comp = &scriptProv{name: "compactor", normal: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return nil, errors.New("compact provider must only summarize")
		}}
		runner := compactRunner(t, repo, main, nil, func(c *Config) {
			if withCompact {
				c.CompactProvider, c.CompactModel = comp, "small-model"
			}
		})
		for i := 0; i < 12; i++ {
			res, err := runner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: strings.Repeat("q", 900)})
			assert.NoErr(t, err)
			sid = res.SessionID
		}
		return
	}
	t.Run("configured uses compact provider", func(t *testing.T) {
		main, comp, repo, sid := run(t, true)
		assert.Eq(t, 0, len(main.sumReqs))
		assert.Eq(t, true, len(comp.sumReqs) >= 1)
		assert.Eq(t, true, strings.Contains(main.reqs[len(main.reqs)-1].Messages[0].Content, "SUMMARY-compactor-"))
		// The summary call is observed: a model event carries the compact model.
		found := false
		for _, e := range eventsOf(t, repo, sid, store.EventModel) {
			found = found || e.Attrs["model"] == "small-model"
		}
		assert.Eq(t, true, found)
	})
	t.Run("not configured uses the turn provider", func(t *testing.T) {
		main, comp, _, _ := run(t, false)
		assert.Eq(t, 0, len(comp.sumReqs))
		assert.Eq(t, true, len(main.sumReqs) >= 1)
	})
}

// Compaction calls are tagged purpose=compact on their model events; the main
// loop's calls carry no purpose (eval's first_call_uncached skips compact).
func TestCompactModelEventPurpose(t *testing.T) {
	repo := openObserveRepo(t)
	main := &scriptProv{name: "main", normal: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(strings.Repeat("a", 900))
	}}
	runner := compactRunner(t, repo, main, nil, nil)
	var sid string
	for i := 0; i < 12; i++ {
		res, err := runner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: strings.Repeat("q", 900)})
		assert.NoErr(t, err)
		sid = res.SessionID
	}
	assert.Eq(t, true, len(main.sumReqs) >= 1)
	var compact, plain int
	for _, e := range eventsOf(t, repo, sid, store.EventModel) {
		switch e.Attrs["purpose"] {
		case "compact":
			compact++
		case nil:
			plain++
		default:
			t.Fatalf("unexpected purpose %v", e.Attrs["purpose"])
		}
	}
	assert.Eq(t, len(main.sumReqs), compact)
	assert.Eq(t, 12, plain)
}

func TestCompactResumeRebuildsSameView(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if n <= 30 {
			return callTool(fmt.Sprintf("c%d", n))
		}
		return finalText("long job done")
	}}
	runner := compactRunner(t, repo, prov, bigToolRegistry(2000, nil), nil)
	res, err := runner.Run(context.Background(), RunRequest{Prompt: "long job"})
	assert.NoErr(t, err)
	assert.Eq(t, true, len(compactionEntries(t, repo, res.SessionID)) >= 1)
	lastView := prov.reqs[len(prov.reqs)-1].Messages // the view of the final model call

	// A brand-new Runner (resume) must rebuild: that view + final answer + new prompt.
	fresh := compactRunner(t, repo, prov, bigToolRegistry(2000, nil), nil)
	prov.normal = func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) { return finalText("next") }
	_, err = fresh.Run(context.Background(), RunRequest{SessionID: res.SessionID, Prompt: "continue"})
	assert.NoErr(t, err)
	got := prov.reqs[len(prov.reqs)-1].Messages
	want := append(append([]agentcore.Message(nil), lastView...),
		agentcore.Message{Role: "assistant", Content: "long job done"},
		agentcore.Message{Role: "user", Content: "continue"})
	assert.Eq(t, len(want), len(got))
	for i := range want {
		assert.Eq(t, want[i].Role, got[i].Role)
		assert.Eq(t, want[i].Content, got[i].Content)
	}
	assertWellFormed(t, got)
}

func TestManualCompactAndPlanInSummary(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText(fmt.Sprintf("reply-%d", n))
	}}
	runner := compactRunner(t, repo, prov, nil, func(c *Config) { c.ContextWindow, c.CompactReserveTokens = 100000, 1000 })
	t.Run("empty session has nothing to compact", func(t *testing.T) {
		model, err := runner.NewSession(context.Background(), "empty-one", "t")
		assert.NoErr(t, err)
		_, err = runner.Compact(context.Background(), model.ID)
		assert.Eq(t, true, err != nil)
	})
	res, err := runner.Run(context.Background(), RunRequest{Prompt: "hello there " + strings.Repeat("h", 2000)})
	assert.NoErr(t, err)
	_, err = repo.AppendEntries(context.Background(), res.SessionID, store.NewEntry{Type: store.EntryPlan, JSON: json.RawMessage(`{"plan":[{"step":"ship T06","status":"in_progress"}]}`)})
	assert.NoErr(t, err)

	out, err := runner.Compact(context.Background(), res.SessionID)
	assert.NoErr(t, err)
	// The 2000-char request stays verbatim in the package, so the estimate
	// does not shrink here; the dropped reply is gone.
	assert.Eq(t, true, out.TokensAfter > 0 && out.TokensBefore > 0)
	assert.Eq(t, 1, len(prov.sumReqs))
	assert.Eq(t, true, strings.Contains(prov.sumReqs[0].Messages[0].Content, "ship T06"))
	assert.Eq(t, true, strings.Contains(prov.sumReqs[0].Messages[0].Content, "hello there"))

	_, err = runner.Run(context.Background(), RunRequest{SessionID: res.SessionID, Prompt: "after compact"})
	assert.NoErr(t, err)
	msgs := prov.reqs[len(prov.reqs)-1].Messages
	assert.Eq(t, 2, len(msgs))
	assert.Eq(t, true, strings.Contains(msgs[0].Content, "SUMMARY-main-1"))
	assert.StrContains(t, msgs[0].Content, recentRequestsHeading+"\n1. hello there ")
	assert.StrContains(t, msgs[0].Content, packagePlanHeading+"\n- ▶ ship T06")
	assert.NotContains(t, msgs[0].Content, "reply-1")
	assert.Eq(t, "after compact", msgs[1].Content)

	// A second compact folds the earlier summary in.
	_, err = runner.Compact(context.Background(), res.SessionID)
	assert.NoErr(t, err)
	assert.Eq(t, true, strings.Contains(prov.sumReqs[1].Messages[0].Content, "SUMMARY-main-1"))
}

// compactSections are the fixed sections of a handoff package, in order; the
// last one is refsHeading, which parseRefs reads the refs back from.
var compactSections = []string{
	"## 当前任务", "## 坐标", "## 采纳与否决", "## 验证到哪", "## 进展与阻塞", "## 下一步", refsHeading,
}

// compactConstraints are the three hard constraints of the summary instruction.
// They are spelled out here on purpose: dropping one from compactSystemPrompt
// must fail the test below.
var compactConstraints = []string{"一律原样保留", "不得写成已执行", "不得夸大为"}

// compactSectionNames lists the "## " headings of text in order, without their
// parenthetical note.
func compactSectionNames(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		name, _, _ := strings.Cut(line, "（")
		out = append(out, strings.TrimSpace(name))
	}
	return out
}

// TestCompactTemplateSections: the handoff package has fixed sections (the
// plan stays out of the template, renderPackage appends it) and the summary
// instruction carries the three hard constraints; summaries written with the
// older, freer structure stay readable.
func TestCompactTemplateSections(t *testing.T) {
	t.Run("fixed sections in order, refs last", func(t *testing.T) {
		assert.Eq(t, compactSections, compactSectionNames(compactTemplate))
	})

	t.Run("summary request carries the template and the constraints", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return finalText(fmt.Sprintf("reply-%d", n))
		}}
		runner := compactRunner(t, repo, prov, nil, func(c *Config) { c.ContextWindow, c.CompactReserveTokens = 100000, 1000 })
		res, err := runner.Run(context.Background(), RunRequest{Prompt: "hello there"})
		assert.NoErr(t, err)
		_, err = runner.Compact(context.Background(), res.SessionID)
		assert.NoErr(t, err)
		assert.Require(t, assert.Len(t, prov.sumReqs, 1))
		req := prov.sumReqs[0]
		assert.StrContains(t, req.Messages[0].Content, compactTemplate)
		assert.Eq(t, compactSections, compactSectionNames(req.Messages[0].Content))
		// The constraints are the fixed instruction outside the template, so the
		// model is told them rather than writing them out as a section.
		for _, c := range compactConstraints {
			assert.StrContains(t, req.System, c)
		}
	})

	t.Run("refs of a summary written before the sections were fixed", func(t *testing.T) {
		// legacyRefsHeading is spelled out: it is what already stored summaries
		// contain, so parseRefs must keep reading that heading.
		const legacyRefsHeading = "## 与当前任务相关的引用"
		body := "## 涉及文件\n- internal/x.go\n## 关键决策\n- 无\n" + legacyRefsHeading + "\n- sess_a/12 请求\n"
		assert.Eq(t, []string{"sess_a/12"}, parseRefs(body, "sess_a", nil))
	})
}

func TestTruncateMiddleKeepsUTF8(t *testing.T) {
	s := strings.Repeat("中文字符", 3000)
	out := truncateMiddle(s, 2001)
	assert.Eq(t, true, len(out) < 2200)
	for _, r := range out {
		assert.Eq(t, true, r != '�')
	}
	assert.Eq(t, "short", truncateMiddle("short", 100))
}

// TestCompactScalesWithWindow runs multi-turn sessions with default
// (unconfigured) reserve/keep on windows from tiny to large: each must compact
// and keep going, every request must fit, and a mid-size window must not
// compact on (nearly) every turn.
func TestCompactScalesWithWindow(t *testing.T) {
	for _, window := range []int{3000, 8000, 40000, 200000} {
		t.Run(fmt.Sprintf("window_%d", window), func(t *testing.T) {
			repo := openObserveRepo(t)
			// Each message is about window/10 tokens, so a turn is window/5 tokens.
			size := window * 4 / 10
			prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
				return finalText(fmt.Sprintf("answer-%d %s", n, strings.Repeat("a", size)))
			}}
			runner := compactRunner(t, repo, prov, nil, func(c *Config) {
				c.ContextWindow, c.CompactReserveTokens = window, 0
			})
			_, reserve := runner.compactLimits()
			thr := thresholdOf(window, reserve)

			turns, sid := 12, ""
			if window == 40000 {
				turns = 6
			}
			for i := 1; i <= turns; i++ {
				res, err := runner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: fmt.Sprintf("question-%d %s", i, strings.Repeat("q", size))})
				assert.NoErr(t, err)
				sid = res.SessionID
			}
			recs := compactionEntries(t, repo, sid)
			assert.Eq(t, true, len(recs) >= 1)
			if window == 40000 {
				assert.Eq(t, true, len(recs) <= 2)
			}
			for _, rec := range recs {
				assert.Eq(t, true, rec.TokensAfter*10 <= thr*6) // the last group + package: ample headroom
			}
			for _, req := range prov.reqs {
				assertWellFormed(t, req.Messages)
				assert.Eq(t, true, charsOfMessages(req.Messages)/4 <= window)
			}
		})
	}
}

// TestCompactLimitsExplicit checks an explicit reserve wins and the defaults.
func TestCompactLimitsExplicit(t *testing.T) {
	mk := func(w, rs int) *Runner {
		return &Runner{cfg: Config{ContextWindow: w, CompactReserveTokens: rs}}
	}
	_, rs := mk(100000, 1000).compactLimits()
	assert.Eq(t, 1000, rs)
	w, rs := mk(0, 0).compactLimits()
	assert.Eq(t, 128000, w)
	assert.Eq(t, 16000, rs)
	_, rs = mk(3000, 0).compactLimits()
	assert.Eq(t, 600, rs) // scales with a small window
}

// TestSegmentCutKeepsPendingRequest: a threshold break never drops the
// unanswered last user request, even when nothing else is kept.
func TestSegmentCutKeepsPendingRequest(t *testing.T) {
	msg := func(role string) ctxItem { return ctxItem{msg: agentcore.Message{Role: role, Content: "x"}} }
	items := []ctxItem{msg("user"), msg("assistant"), msg("user"), msg("assistant"), msg("user")}
	cut := segmentCut(items, false)
	assert.Eq(t, len(items)-1, cut) // only the pending request stays
	assert.Eq(t, 0, segmentCut(items[:1], true))
	assert.Eq(t, 4, segmentCut(items[:4], true)) // between turns: drop everything
}

// TestCompactSmallWindowRecoversAfterOverflow: a turn that overflows a small
// window fails, but its entries never enter history, so the next turn works.
func TestCompactSmallWindowRecoversAfterOverflow(t *testing.T) {
	repo := openObserveRepo(t)
	big := false
	prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if big {
			return callTool(fmt.Sprintf("c%d", n))
		}
		return finalText("ok " + strings.Repeat("a", 600))
	}}
	runner := compactRunner(t, repo, prov, bigToolRegistry(60000, nil), func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 3000, 0
		c.Tools = nil
	})
	first, err := runner.Run(context.Background(), RunRequest{Prompt: "hello " + strings.Repeat("h", 600)})
	assert.NoErr(t, err)
	// Same session on a runner whose tool result can never fit.
	big = true
	runner = compactRunner(t, repo, prov, bigToolRegistry(60000, nil), func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 600, 100
	})
	bad, err := runner.Run(context.Background(), RunRequest{SessionID: first.SessionID, Prompt: "read it"})
	assert.Eq(t, true, errors.Is(err, ErrContextOverflow))
	assert.Eq(t, RunFailed, bad.State)
	big = false
	runner = compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 3000, 0
	})
	for i := 0; i < 12; i++ {
		_, err = runner.Run(context.Background(), RunRequest{SessionID: first.SessionID, Prompt: fmt.Sprintf("again %d %s", i, strings.Repeat("g", 800))})
		assert.NoErr(t, err)
	}
	assert.Eq(t, true, len(compactionEntries(t, repo, first.SessionID)) >= 1)
}
