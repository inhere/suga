package personal

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
)

func TestForkHeadHook(t *testing.T) {
	t.Run("keeps seed prefix and appends child messages", func(t *testing.T) {
		seed := &ForkSeed{Model: "seed-model", Request: &agentcore.GenerateRequest{
			System:   "seed-system",
			Tools:    []agentcore.ToolSchema{{Name: "file_read"}},
			Messages: []agentcore.Message{{Role: "user", Content: "base"}},
		}}
		event := &agentcore.ModelEvent{Request: &agentcore.GenerateRequest{
			System: "child-system", Messages: []agentcore.Message{
				{Role: "user", Content: "task"}, {Role: "assistant", Content: "child"},
			}, ToolChoice: agentcore.ToolChoiceNone,
		}}
		ctrl := agentcore.NewControl()
		h := forkHeadHook{seed: seed, prompt: "task", maxInput: 10, instruction: "wrap"}
		assert.NoErr(t, h.BeforeModel(context.Background(), event, ctrl))
		assert.Eq(t, "seed-system", event.Request.System)
		assert.Eq(t, "seed-model", event.Request.Model)
		assert.Eq(t, 2, len(event.Request.Messages))
		assert.Eq(t, "base", event.Request.Messages[0].Content)
		assert.Eq(t, "child", event.Request.Messages[1].Content)
		assert.Eq(t, agentcore.ToolChoiceNone, event.Request.ToolChoice)

		assert.NoErr(t, h.AfterModel(context.Background(), &agentcore.ModelEvent{
			Result: &agentcore.GenerateResult{Usage: agentcore.Usage{InputTokens: 10}},
		}, ctrl))
		assert.Eq(t, StopContext, ctrl.WrapUpReason)
		assert.Eq(t, "wrap", ctrl.WrapUpInstruction)
	})
}

func TestForkHeadHookPrecedesPrefixHook(t *testing.T) {
	r := &Runner{cfg: Config{ForkSeed: &ForkSeed{Request: &agentcore.GenerateRequest{}}}, rec: &recorder{prefix: newPrefixTracker()}}
	hooks := r.turnHooks(nil, nil, nil, nil, RunResult{}, "task")
	forkAt, prefixAt := -1, -1
	for i, hook := range hooks {
		switch hook.(type) {
		case forkHeadHook:
			forkAt = i
		case prefixHook:
			prefixAt = i
		}
	}
	assert.Gte(t, forkAt, 0)
	assert.Gte(t, prefixAt, 0)
	assert.Lt(t, forkAt, prefixAt)
}

func TestForkReuseFor(t *testing.T) {
	r := &recorder{forkReuse: "rebuilt", forkReuseNote: "resume", forkReuseWritten: make(map[string]bool)}
	reuse, note, ok := r.forkReuseFor("session")
	assert.True(t, ok)
	assert.Eq(t, "rebuilt", reuse)
	assert.Eq(t, "resume", note)
	_, _, ok = r.forkReuseFor("session")
	assert.False(t, ok)
	_, _, ok = (&recorder{forkReuse: "prefix"}).forkReuseFor("other")
	assert.True(t, ok)
}

func TestForkContextMode(t *testing.T) {
	b := &contextBuilder{fork: true, hookContext: "hook", ephemeral: "ephemeral",
		seg: &segmentContext{Rules: "segment"}, summary: "summary"}
	state := &agentcore.RunState{Messages: []agentcore.Message{{Role: "user", Content: "task"}}}
	view := b.compose(state)
	assert.Eq(t, 1, len(view))
	assert.Eq(t, "task", view[0].Content)
}
