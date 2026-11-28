package personal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

func TestForkSeedForCall(t *testing.T) {
	t.Run("uses the current request and places the target call in the middle", func(t *testing.T) {
		tracker := newPrefixTracker()
		base := &agentcore.GenerateRequest{System: "system", Tools: []agentcore.ToolSchema{{Name: "file_read"}}, Messages: []agentcore.Message{{Role: "user", Content: "background"}}}
		tracker.observeRequest("s", base)
		tracker.observeStep("s", "turn-1", &agentcore.GenerateResult{Content: "lead", Reasoning: "think", Metadata: map[string]any{"sig": "x"}, ToolCalls: []agentcore.ToolCall{
			{CallID: "c1", Name: "file_read", Arguments: map[string]any{"path": "a"}},
			{CallID: "c2", Name: "file_read", Arguments: map[string]any{"path": "b"}},
			{CallID: "c3", Name: "file_read", Arguments: map[string]any{"path": "c"}},
		}})
		tracker.observeToolResult("s", "c1", agentcore.ToolResult{CallID: "c1", Name: "file_read", Content: "real result"})
		tr, err := i18n.New(i18n.LanguageEN)
		assert.NoErr(t, err)
		r := &Runner{cfg: Config{Model: "model", ProviderName: "provider", Translator: tr}, rec: &recorder{prefix: tracker}}
		seed, err := r.ForkSeedForCall(context.Background(), "s", "c2", "inspect b")
		assert.NoErr(t, err)
		assert.Eq(t, "turn-1", seed.BaseTurn)
		assert.Eq(t, "call", seed.Source)
		assert.Eq(t, "prefix", seed.Reuse)
		assert.Eq(t, "background", seed.Request.Messages[0].Content)
		assert.Eq(t, "real result", seed.Request.Messages[2].Content)
		assert.True(t, strings.Contains(seed.Request.Messages[4].Content, "inspect b"))
		assert.True(t, strings.Contains(seed.Request.Messages[6].Content, "not run"))
		assert.Eq(t, "background", base.Messages[0].Content)
	})

	t.Run("returns a translated context hint when the seed is too large", func(t *testing.T) {
		tracker := newPrefixTracker()
		tracker.observeRequest("s", &agentcore.GenerateRequest{System: strings.Repeat("x", 100), Messages: []agentcore.Message{{Role: "user", Content: "context"}}})
		tracker.observeStep("s", "turn-1", &agentcore.GenerateResult{ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "read"}}})
		tr, err := i18n.New(i18n.LanguageEN)
		assert.NoErr(t, err)
		r := &Runner{cfg: Config{ContextWindow: 1, Translator: tr}, rec: &recorder{prefix: tracker}}
		_, err = r.ForkSeedForCall(context.Background(), "s", "c1", "task")
		var unavailable *ForkUnavailableError
		assert.True(t, errors.As(err, &unavailable))
		assert.Eq(t, "context", unavailable.Reason)
		assert.NotEmpty(t, unavailable.Hint())
	})
}

// TestForkSeedForCallIgnoresHostHook: a model-triggered fork keeps starting
// with a host hook configured (only :bg refuses; BF11/PF11).
func TestForkSeedForCallIgnoresHostHook(t *testing.T) {
	tracker := newPrefixTracker()
	tracker.observeRequest("s", &agentcore.GenerateRequest{System: "system", Messages: []agentcore.Message{{Role: "user", Content: "background"}}})
	tracker.observeStep("s", "turn-1", &agentcore.GenerateResult{ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "read"}}})
	r := &Runner{cfg: Config{Model: "m", Hooks: []agentcore.Hook{agentcore.NopHook{}}}, rec: &recorder{prefix: tracker}}
	seed, err := r.ForkSeedForCall(context.Background(), "s", "c1", "task")
	assert.NoErr(t, err)
	assert.Eq(t, "prefix", seed.Reuse)
	assert.Eq(t, "call", seed.Source)
}

// bgReason unpacks the Reason of a ForkUnavailableError ("" for any other result).
func bgReason(err error) string {
	var unavailable *ForkUnavailableError
	if errors.As(err, &unavailable) {
		return unavailable.Reason
	}
	return ""
}

// TestForkSeedBetweenTurns: one expected value per row of the T5 table.
func TestForkSeedBetweenTurns(t *testing.T) {
	ctx := context.Background()
	newProv := func() *reuseProv { return &reuseProv{main: finalMain, handoff: finalMain} }
	runner := func(t *testing.T, repo *store.Store, prov agentcore.Provider, mut func(*Config)) *Runner {
		return compactRunner(t, repo, prov, nil, func(c *Config) {
			c.ContextWindow, c.CompactReserveTokens = 100000, 1000
			if mut != nil {
				mut(c)
			}
		})
	}
	// wantRebuilt checks a rebuilt seed: the persisted view plus the directive.
	wantRebuilt := func(t *testing.T, seed ForkSeed, note string) {
		t.Helper()
		assert.Eq(t, "rebuilt", seed.Reuse)
		assert.Eq(t, note, seed.ReuseNote)
		assert.Eq(t, "bg", seed.Source)
		last := seed.Request.Messages[len(seed.Request.Messages)-1]
		assert.Eq(t, "user", last.Role)
		assert.StrContains(t, last.Content, "inspect x")
	}

	t.Run("normal turn continues the last request", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := newProv()
		r := runner(t, repo, prov, nil)
		sid := oneTurn(t, r)
		seed, err := r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "prefix", seed.Reuse)
		assert.Eq(t, "", seed.ReuseNote)
		assert.Eq(t, "bg", seed.Source)
		sess, err := r.LoadSession(ctx, sid)
		assert.NoErr(t, err)
		assert.Eq(t, sess.LastTurnID, seed.BaseTurn)
		want := prov.lastMainReq()
		n := len(seed.Request.Messages)
		assert.Eq(t, len(want.Messages)+2, n)
		assert.Eq(t, want.System, seed.Request.System)
		assert.Eq(t, "assistant", seed.Request.Messages[n-2].Role)
		assert.Eq(t, "done", seed.Request.Messages[n-2].Content)
		assert.StrContains(t, seed.Request.Messages[n-1].Content, "inspect x")
	})
	t.Run("resume with a new runner rebuilds", func(t *testing.T) {
		repo := openObserveRepo(t)
		sid := oneTurn(t, runner(t, repo, newProv(), nil))
		seed, err := runner(t, repo, newProv(), nil).ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Require(t, assert.NoErr(t, err))
		wantRebuilt(t, seed, "no_tracked_request")
		assert.Eq(t, "bg", seed.Source)
	})
	t.Run("cancelled turn rebuilds", func(t *testing.T) {
		repo := openObserveRepo(t)
		r := runner(t, repo, newProv(), nil)
		sid := oneTurn(t, r)
		// A later turn cancelled before the model answered: its request is the
		// newest one, but no completed step belongs to it.
		block := &blockingProvider{started: make(chan struct{})}
		r.cfg.Provider = block
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { _, err := r.Run(cctx, RunRequest{SessionID: sid, Prompt: "again"}); done <- err }()
		<-block.started
		cancel()
		assert.True(t, errors.Is(<-done, context.Canceled))
		seed, err := r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Require(t, assert.NoErr(t, err))
		wantRebuilt(t, seed, "last_turn_not_completed")
	})
	t.Run("compact rebuilds", func(t *testing.T) {
		repo := openObserveRepo(t)
		r := runner(t, repo, &reuseProv{main: finalMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return finalText(reuseBody)
		}}, nil)
		sid := oneTurn(t, r)
		_, err := r.Compact(ctx, sid)
		assert.NoErr(t, err)
		seed, err := r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Require(t, assert.NoErr(t, err))
		wantRebuilt(t, seed, "no_tracked_request")
		// The compacted view starts with the handoff package.
		assert.StrContains(t, seed.Request.Messages[0].Content, "修解析器")
	})
	t.Run("clear context rebuilds", func(t *testing.T) {
		repo := openObserveRepo(t)
		r := runner(t, repo, newProv(), nil)
		sid := oneTurn(t, r)
		_, err := r.ClearContext(ctx, sid)
		assert.NoErr(t, err)
		// Nothing of the closed segment is left to continue: no_request.
		_, err = r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Eq(t, "no_request", bgReason(err))
	})
	t.Run("rewind rebuilds", func(t *testing.T) {
		repo := openObserveRepo(t)
		r := runner(t, repo, newProv(), nil)
		sid := oneTurn(t, r)
		_, err := r.Run(ctx, RunRequest{SessionID: sid, Prompt: "second"})
		assert.NoErr(t, err)
		turns, err := r.RewindTurns(ctx, sid)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, 2, len(turns)))
		snaps := NewSessionSnapshots(t.TempDir())
		plan, err := r.RewindPlan(ctx, snaps, t.TempDir(), sid, turns[1].TurnID, RewindScopeConversation)
		assert.Require(t, assert.NoErr(t, err))
		_, err = r.ApplyRewind(ctx, snaps, t.TempDir(), plan)
		assert.Require(t, assert.NoErr(t, err))
		seed, err := r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Require(t, assert.NoErr(t, err))
		wantRebuilt(t, seed, "no_tracked_request")
		// Only the first turn's request survives the rewind.
		assert.Eq(t, "fix the parser in compact.go", seed.Request.Messages[0].Content)
	})
	t.Run("model switch rebuilds", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := newProv()
		r := runner(t, repo, prov, nil)
		sid := oneTurn(t, r)
		assert.NoErr(t, r.SwitchModel(ctx, sid, prov, "p", "m2", 100000, 0))
		seed, err := r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Require(t, assert.NoErr(t, err))
		wantRebuilt(t, seed, "model_switched")
		assert.Eq(t, "m2", seed.Model)
	})
	t.Run("host hook refuses after a normal turn", func(t *testing.T) {
		repo := openObserveRepo(t)
		r := runner(t, repo, newProv(), func(c *Config) { c.Hooks = []agentcore.Hook{agentcore.NopHook{}} })
		sid := oneTurn(t, r)
		_, err := r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Eq(t, "host_hook", bgReason(err))
	})
	t.Run("host hook refuses after compact", func(t *testing.T) {
		repo := openObserveRepo(t)
		r := runner(t, repo, &reuseProv{main: finalMain, handoff: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
			return finalText(reuseBody)
		}}, func(c *Config) { c.Hooks = []agentcore.Hook{agentcore.NopHook{}} })
		sid := oneTurn(t, r)
		_, err := r.Compact(ctx, sid)
		assert.NoErr(t, err)
		_, err = r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Eq(t, "host_hook", bgReason(err))
	})
	t.Run("state-reading prompt refuses", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := newProv()
		sid := oneTurn(t, runner(t, repo, prov, nil))
		r := runner(t, repo, prov, func(c *Config) { c.PromptBuilder = stateTestPrompt{} })
		_, err := r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		assert.Eq(t, "prompt_state", bgReason(err))
	})
	t.Run("session without a completed turn", func(t *testing.T) {
		repo := openObserveRepo(t)
		r := runner(t, repo, newProv(), nil)
		assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "empty", Cwd: t.TempDir()}))
		_, err := r.ForkSeedBetweenTurns(ctx, "empty", "inspect x")
		assert.Eq(t, "no_request", bgReason(err))
	})
	t.Run("seed over the threshold refuses with the hint", func(t *testing.T) {
		repo := openObserveRepo(t)
		prov := newProv()
		r := runner(t, repo, prov, nil)
		sid := oneTurn(t, r)
		tr, err := i18n.New(i18n.LanguageEN)
		assert.NoErr(t, err)
		r.cfg.Translator = tr
		r.cfg.ContextWindow = 10
		_, err = r.ForkSeedBetweenTurns(ctx, sid, "inspect x")
		var unavailable *ForkUnavailableError
		assert.Require(t, assert.True(t, errors.As(err, &unavailable)))
		assert.Eq(t, "context", unavailable.Reason)
		assert.StrContains(t, unavailable.Hint(), "explore")
	})
}
