package wiring

import (
	"context"
	"errors"
	"sync"

	"github.com/inhere/suga/internal/personal"
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
