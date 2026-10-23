package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// Pool manages multiple LLM providers with automatic failover.
// It tries each provider in order until one succeeds, and only while another
// model still has a chance (see ShouldFallback).
type Pool struct {
	mu        sync.RWMutex
	providers []Provider
	observer  AttemptObserver // optional; sees every provider attempt
}

// NewPool creates a Pool from an ordered list of providers.
// The first provider is the primary; subsequent ones are fallbacks.
func NewPool(providers []Provider) *Pool {
	return &Pool{providers: providers}
}

// SetObserver installs a callback invoked once per provider attempt (fallbacks
// included). Pass nil to remove it.
func (p *Pool) SetObserver(obs AttemptObserver) {
	p.mu.Lock()
	p.observer = obs
	p.mu.Unlock()
}

// Name implements Provider.
func (p *Pool) Name() string { return "pool" }

// SupportsToolChoice implements api.ToolChooser: it reports true only when
// every provider honours tool_choice, since one fallback rejecting the field
// would fail the request the runtime sends it for.
func (p *Pool) SupportsToolChoice() bool {
	p.mu.RLock()
	providers := p.providers
	p.mu.RUnlock()
	if len(providers) == 0 {
		return false
	}
	for _, prov := range providers {
		chooser, ok := prov.(agentcoreapi.ToolChooser)
		if !ok || !chooser.SupportsToolChoice() {
			return false
		}
	}
	return true
}

// Generate tries each provider in turn and returns the first success. It moves
// on only when another model has a chance (ShouldFallback): a transient failure
// (broken stream, network error, 408/429/5xx), a context that does not fit this
// provider's window, or a rejection that names this provider (401/403/404 - its
// credentials or its models). A request the provider refused on the request's
// own account (400/422), a bad response or an unclassified failure is returned
// as it is, so the remaining models - and the timeouts and tokens they would
// spend - are not wasted on an error they would hit just the same.
func (p *Pool) Generate(ctx context.Context, req *agentcoreapi.GenerateRequest) (*agentcoreapi.GenerateResult, error) {
	p.mu.RLock()
	providers, obs := p.providers, p.observer
	p.mu.RUnlock()
	if len(providers) == 0 {
		return nil, errors.New("no llm provider is configured")
	}

	var lastErr error
	for i := 0; i < len(providers); i++ {
		prov := providers[i]
		if remaining, skip := coolingSkip(providers, i); skip {
			// This candidate's endpoint failed to connect a moment ago; a working
			// candidate is left, so the request does not pay its connect timeout again.
			lastErr = fmt.Errorf("provider %s skipped: cooling down for %s after a connect failure", prov.Name(), remaining.Round(time.Second))
			slog.Warn("llm provider cooling down after a connect failure, skipping", "provider", prov.Name(),
				"endpoint", providerEndpoint(prov), "remaining", remaining.Round(time.Second))
			continue
		}
		// A later model takes over a connection-phase failure at once (postWithRetry):
		// retrying an unreachable endpoint only waits the connect timeout again.
		attemptCtx := ctx
		if i+1 < len(providers) {
			attemptCtx = withFallbackAvailable(ctx)
		}
		// The attempt is reported after the decision below, so its model event
		// can say what the pool did with the failure (Attempt.Fallback).
		attempt := &attemptCollector{obs: obs}
		res, err := GenerateObserved(attemptCtx, prov, req, i+1, attempt.observer())
		if err == nil {
			attempt.deliver(ctx, "")
			return res, nil
		}
		next := nextProviderIndex(providers, i, err)
		decision := decideFallback(err, next < 0)
		attempt.deliver(ctx, decision.Reason)
		coolDownAfterConnectFailure(prov, attempt, err)
		if !decision.Next {
			slog.Warn("llm provider failed, not trying another", "provider", prov.Name(), "reason", decision.Reason, "err", err)
			if i == 0 {
				return nil, err // the first provider failed on the request itself
			}
			return nil, fmt.Errorf("all providers failed: %w", err)
		}
		slog.Warn("llm provider failed, trying next", "provider", prov.Name(), "reason", decision.Reason, "err", err)
		lastErr = err
		i = next - 1 // the loop's i++ lands on next
	}
	// Unreachable: decideFallback never asks for the next provider after the
	// last one, which returns above.
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

// Stream implements the public streaming contract.
func (p *Pool) Stream(ctx context.Context, req *agentcoreapi.GenerateRequest, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	p.mu.RLock()
	providers, obs := p.providers, p.observer
	p.mu.RUnlock()
	if len(providers) == 0 {
		return nil, errors.New("no llm provider is configured")
	}

	// Once a provider has emitted text the caller has shown it, so a later
	// failure is returned as is: falling back would repeat or mix the answer.
	emitted := false
	tracked := func(ctx context.Context, ev agentcoreapi.StreamEvent) error {
		if ev.Kind == agentcoreapi.StreamEventTextDelta && ev.Delta != "" {
			emitted = true
		}
		if emit == nil {
			return nil
		}
		return emit(ctx, ev)
	}
	var lastErr error
	for i := 0; i < len(providers); i++ {
		prov := providers[i]
		streaming, ok := prov.(agentcoreapi.StreamingProvider)
		if !ok {
			// A provider without streaming says nothing about the request, and
			// the next one may well support it.
			lastErr = fmt.Errorf("provider %s does not support streaming", prov.Name())
			slog.Warn("llm provider cannot stream, trying next", "provider", prov.Name())
			continue
		}
		attempt := &attemptCollector{obs: obs}
		// See Generate: a connection-phase failure moves on to the next model at once.
		attemptCtx := ctx
		if i+1 < len(providers) {
			attemptCtx = withFallbackAvailable(ctx)
		}
		res, err := StreamObserved(attemptCtx, streaming, req, tracked, i+1, attempt.observer())
		if err == nil {
			attempt.deliver(ctx, "")
			return res, nil
		}
		switch {
		case emitted:
			attempt.deliver(ctx, "stop, the answer was already shown")
			return nil, err
		case ctx.Err() != nil:
			attempt.deliver(ctx, "stop, the turn was cancelled")
			return nil, err
		}
		next := nextProviderIndex(providers, i, err)
		decision := decideFallback(err, next < 0)
		attempt.deliver(ctx, decision.Reason)
		coolDownAfterConnectFailure(prov, attempt, err)
		if !decision.Next {
			slog.Warn("llm provider stream failed, not trying another", "provider", prov.Name(), "reason", decision.Reason, "err", err)
			if i == 0 {
				return nil, err // the first provider failed on the request itself
			}
			return nil, fmt.Errorf("all providers failed for stream: %w", err)
		}
		slog.Warn("llm provider stream failed, trying next", "provider", prov.Name(), "reason", decision.Reason, "err", err)
		lastErr = err
		i = next - 1 // the loop's i++ lands on next
	}
	// Unreachable: decideFallback never asks for the next provider after the
	// last one, which returns above.
	return nil, fmt.Errorf("all providers failed for stream: %w", lastErr)
}

// coolDownAfterConnectFailure cools a candidate down after an attempt that never
// reached the provider, so the following steps of this run ask a working model
// first (cooldown.go). The log line carries the phase and the attempt's
// duration, which the trace shows as well.
func coolDownAfterConnectFailure(prov Provider, attempt *attemptCollector, err error) {
	if !isConnectFailure(err) {
		return
	}
	markConnectFailed(prov)
	slog.Warn("llm provider connect failed, cooling the candidate down", "provider", prov.Name(),
		"endpoint", providerEndpoint(prov), "reason", phaseConnectFailed,
		"cool_for", ConnectFailureCooldown.String(), "elapsed", attempt.attempt.Duration.Round(time.Millisecond), "err", err)
}

// nextProviderIndex is the index of the provider the pool would ask after
// providers[i] failed with err, or -1 when none is left. An exhausted allowance
// (ErrorKindQuota) belongs to the provider's account, not to one model (and a
// context-length error belongs to that provider's window), so the other models
// of the same provider are skipped and only a different provider is asked.
func nextProviderIndex(providers []Provider, i int, err error) int {
	skipProvider := ErrorKind(err) == ErrorKindQuota || isContextLengthFailure(err)
	for j := i + 1; j < len(providers); j++ {
		if skipProvider && providers[j].Name() == providers[i].Name() {
			continue
		}
		return j
	}
	return -1
}

func isContextLengthFailure(err error) bool {
	var contextLength *contextLengthError
	return errors.As(err, &contextLength)
}

// AddProvider appends a provider to the pool (thread-safe).
func (p *Pool) AddProvider(prov Provider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.providers = append(p.providers, prov)
}

// fallbackDecision is what the pool does with a failed attempt: whether the
// next provider is asked, and the reason - the log line's "reason" and the
// model event's fallback attr (Attempt.Fallback).
type fallbackDecision struct {
	Next   bool
	Reason string
}

// decideFallback is the pool's rule for a failed attempt (ShouldFallback); last
// tells whether the failing provider was the pool's last one.
func decideFallback(err error, last bool) fallbackDecision {
	reason := fallbackReason(err)
	switch {
	case !ShouldFallback(err):
		return fallbackDecision{Reason: "stop, " + reason + " would repeat on the next model"}
	case last:
		return fallbackDecision{Reason: "stop, no provider left after " + reason}
	default:
		return fallbackDecision{Next: true, Reason: "next, " + reason}
	}
}

// attemptCollector defers one pool attempt's report to the observer until the
// pool knows what it does with the failure, so the failed attempt's model event
// carries the decision (Attempt.Fallback). The report itself is otherwise the
// observer's ordinary one. With no observer installed both halves are no-ops.
type attemptCollector struct {
	obs      AttemptObserver
	attempt  Attempt
	reported bool
}

// observer is what the pool hands to GenerateObserved/StreamObserved.
func (c *attemptCollector) observer() AttemptObserver {
	if c.obs == nil {
		return nil
	}
	return func(_ context.Context, a Attempt) {
		c.attempt, c.reported = a, true
	}
}

// deliver reports the collected attempt with the decision the pool made
// ("" for an attempt that succeeded).
func (c *attemptCollector) deliver(ctx context.Context, decision string) {
	if !c.reported {
		return
	}
	c.attempt.Fallback = decision
	c.obs(ctx, c.attempt)
}
