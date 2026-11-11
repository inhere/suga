package personal

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// promptProbeTool records the turn prompt its call's ctx carries.
type promptProbeTool struct{ seen []string }

func (t *promptProbeTool) Name() string        { return "probe" }
func (t *promptProbeTool) Description() string { return "turn prompt probe" }
func (t *promptProbeTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: "probe", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (t *promptProbeTool) ToolMeta() tools.Meta { return tools.Meta{ReadOnly: true} }
func (t *promptProbeTool) Execute(ctx context.Context, call agentcore.ToolCall) (*agentcore.ToolResult, error) {
	t.seen = append(t.seen, api.TurnPromptFrom(ctx))
	return &agentcore.ToolResult{CallID: call.CallID, Name: call.Name, Content: "ok"}, nil
}

// probeProvider calls the probe tool once per turn, then answers.
type probeProvider struct{}

func (probeProvider) Name() string { return "probe" }
func (probeProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == "tool" {
		return &agentcore.GenerateResult{Content: "done", FinishReason: agentcore.FinishFinal}, nil
	}
	return &agentcore.GenerateResult{FinishReason: agentcore.FinishToolCalls,
		ToolCalls: []agentcore.ToolCall{{CallID: "p1", Name: "probe", Arguments: map[string]any{}}}}, nil
}

// newProbeRunner builds a runner over repo; every runner of a test has its own
// probe tool, so a "restart" is simply a second runner over the same store.
func newProbeRunner(t *testing.T, repo *store.Store) (*Runner, *promptProbeTool) {
	t.Helper()
	probe := &promptProbeTool{}
	reg := tools.NewRegistry()
	reg.Register(probe)
	runner, err := New(Config{Repository: repo, Provider: probeProvider{}, Tools: reg})
	assert.Require(t, assert.NoErr(t, err))
	return runner, probe
}

// lastSeen returns the prompt the latest probe call saw.
func (p *promptProbeTool) lastSeen(t *testing.T) string {
	t.Helper()
	assert.Require(t, assert.NotEmpty(t, p.seen))
	return p.seen[len(p.seen)-1]
}

func TestTurnPromptInRun(t *testing.T) {
	ctx := context.Background()

	t.Run("user turn is its own prompt", func(t *testing.T) {
		repo := openObserveRepo(t)
		runner, probe := newProbeRunner(t, repo)
		_, err := runner.Run(ctx, RunRequest{Prompt: "deploy the site"})
		assert.NoErr(t, err)
		assert.Eq(t, "deploy the site", probe.lastSeen(t))
	})

	t.Run("injected turns take the latest user request, across a restart", func(t *testing.T) {
		repo := openObserveRepo(t)
		runner, probe := newProbeRunner(t, repo)
		first, err := runner.Run(ctx, RunRequest{Prompt: "first typed request"})
		assert.NoErr(t, err)
		second, err := runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "second typed request"})
		assert.NoErr(t, err)

		// Two injected turns in a row: the second must not take the first's text.
		_, err = runner.Run(ctx, RunRequest{SessionID: second.SessionID, Prompt: "hook reply one", Injected: true})
		assert.NoErr(t, err)
		assert.Eq(t, "second typed request", probe.lastSeen(t))
		_, err = runner.Run(ctx, RunRequest{SessionID: second.SessionID, Prompt: "hook reply two", Injected: true})
		assert.NoErr(t, err)
		assert.Eq(t, "second typed request", probe.lastSeen(t))

		// A new Runner over the same store (a restarted process) reads it back.
		restarted, probe2 := newProbeRunner(t, repo)
		_, err = restarted.Run(ctx, RunRequest{SessionID: second.SessionID, Prompt: "after restart", Injected: true})
		assert.NoErr(t, err)
		assert.Eq(t, "second typed request", probe2.lastSeen(t))
	})

	t.Run("only injected turns give an empty prompt", func(t *testing.T) {
		repo := openObserveRepo(t)
		runner, probe := newProbeRunner(t, repo)
		first, err := runner.Run(ctx, RunRequest{Prompt: "delivery text", Injected: true})
		assert.NoErr(t, err)
		assert.Eq(t, "", probe.lastSeen(t))
		_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "another delivery", Injected: true})
		assert.NoErr(t, err)
		assert.Eq(t, "", probe.lastSeen(t))
	})
}

// turnPrompt skips rewound requests, context updates and steer entries the same
// way recentRequests does.
func TestTurnPromptFilters(t *testing.T) {
	f := newMaskFixture(t)
	injectedReq := RunRequest{Injected: true}

	t.Run("steer and context update are not requests", func(t *testing.T) {
		f.mixedSession("s1") // ends with "third typed request" (u3)
		f.turn("s1", "later", store.TurnCompleted)
		f.meta("s1", "later", "steer after", metaSteer)
		f.meta("s1", "later", "ctx update after", metaContextUpdate)
		assert.Eq(t, "third typed request", f.runner.turnPrompt(f.ctx, "s1", injectedReq))
	})
	t.Run("rewound request is dropped", func(t *testing.T) {
		f.session("s2")
		f.turn("s2", "t1", store.TurnCompleted)
		f.msg("s2", "t1", "user", "kept request")
		cut := f.msg("s2", "t1", "assistant", "answer")
		f.turn("s2", "t2", store.TurnCompleted)
		f.msg("s2", "t2", "user", "undone request")
		f.rewind("s2", cut)
		assert.Eq(t, "kept request", f.runner.turnPrompt(f.ctx, "s2", injectedReq))
	})
	t.Run("user turn ignores history", func(t *testing.T) {
		assert.Eq(t, "typed now", f.runner.turnPrompt(f.ctx, "s1", RunRequest{Prompt: "typed now"}))
	})
}
