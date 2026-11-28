package wiring

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// forkHost is the late binding of the parent Runner for forks (design F3),
// the same pattern as memoryHost: the sub-agent factory is built before
// personal.New, the Runner exists only afterwards. The fork seed (current
// request, model, provider) is always read through the parent's own locked
// accessors; wiring never touches the prefix tracker.
type forkHost struct {
	mu     sync.Mutex
	runner *personal.Runner
}

// bind attaches the parent Runner once personal.New has returned.
func (h *forkHost) bind(r *personal.Runner) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.runner = r
	h.mu.Unlock()
}

func (h *forkHost) get() *personal.Runner {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runner
}

var _ native.ForkSeeder = (*subAgents)(nil)

// ForkSeed implements native.ForkSeeder: the seed is the parent Runner's request
// snapshot. An unbound host (the parent is not built yet) has no request to
// give, which is the same "no_request" refusal as a session without one.
func (s *subAgents) ForkSeed(ctx context.Context, req native.AgentRequest) (any, error) {
	runner := s.fork.get()
	if runner == nil {
		return nil, &personal.ForkUnavailableError{Reason: "no_request"}
	}
	return runner.ForkSeedForCall(ctx, req.ParentSession, req.CallID, req.Prompt)
}

// forkSeedOf unwraps the host's own seed type out of AgentRequest.Seed.
func forkSeedOf(v any) (*personal.ForkSeed, error) {
	switch seed := v.(type) {
	case personal.ForkSeed:
		return &seed, nil
	case *personal.ForkSeed:
		if seed != nil {
			return seed, nil
		}
	}
	return nil, errors.New("agent: fork has no seed")
}

// forkModel is the model reference a fork runs on: the seed's (the parent's
// current one, so a :model switch applies), never agents.model.
func forkModel(seed *personal.ForkSeed) string {
	if seed.ProviderName == "" {
		return seed.Model
	}
	return seed.ProviderName + "/" + seed.Model
}

// forkMeta is the session meta of a fork besides the ones RunChild sets:
// fork_base = "<turn of the base request>|<call or bg>". nil for a plain agent.
func forkMeta(seed *personal.ForkSeed) map[string]any {
	if seed == nil {
		return nil
	}
	return map[string]any{"fork_base": seed.BaseTurn + "|" + seed.Source}
}

// newStartFork is the :bg entry point (design D1): chat's task text becomes a
// fork task through the same native.AgentTool.StartFork the agent tool calls, so
// the task limit, the read-only fork itself and its notice cannot differ between
// the two entries. The seed is the one of a fork between turns (the session's
// last request, or a rebuild of it), since no turn runs to take a step from. A
// nil return leaves :bg out (agents disabled: there is no agent tool).
func newStartFork(run *personal.Runner, agentTool *native.AgentTool) func(ctx context.Context, task string) (tasks.Info, error) {
	if agentTool == nil || run == nil {
		return nil
	}
	return func(ctx context.Context, task string) (tasks.Info, error) {
		task = strings.TrimSpace(task)
		if task == "" {
			return tasks.Info{}, errors.New("agent: fork needs a task")
		}
		// The session is on the ctx (chat sets it) and the seed comes from the parent
		// Runner itself, so this path needs no late binding of its own.
		req := native.AgentRequest{Description: forkTaskLabel(task), Prompt: task,
			ParentSession: api.SessionIDFrom(ctx)}
		seed, err := run.ForkSeedBetweenTurns(ctx, req.ParentSession, task)
		if err != nil {
			return tasks.Info{}, err
		}
		req.Seed = seed
		return agentTool.StartFork(ctx, req)
	}
}

// forkTaskLabel is the task summary of a :bg fork: up to six words of the task,
// the label native's firstWords gives an agent call without a description (a
// plain :bg task carries no description of its own).
func forkTaskLabel(task string) string {
	words := strings.Fields(task)
	if len(words) > 6 {
		words = words[:6]
	}
	return strings.Join(words, " ")
}
