package llm

import (
	"context"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// fakeCooldownClock replaces the cooldown clock so a test can advance the
// cooldown window; the registry is cleared before and after the case.
func fakeCooldownClock(t *testing.T) *time.Time {
	t.Helper()
	clock := time.Now()
	old := cooldownState.now
	cooldownState.now = func() time.Time { return clock }
	resetCooldowns()
	t.Cleanup(func() {
		cooldownState.now = old
		resetCooldowns()
	})
	return &clock
}

// coolingProvider is a provider identity for cooldown-key tests.
func coolingProvider(name, endpoint string) Provider {
	return namedEndpointProvider{name: name, endpoint: endpoint}
}

type namedEndpointProvider struct{ name, endpoint string }

func (p namedEndpointProvider) Name() string     { return p.name }
func (p namedEndpointProvider) Endpoint() string { return p.endpoint }
func (p namedEndpointProvider) Generate(context.Context, *agentcoreapi.GenerateRequest) (*agentcoreapi.GenerateResult, error) {
	return nil, nil
}

// TestConnectFailureCooldownWindow: a candidate stays skipped for
// ConnectFailureCooldown after a connect failure, then is asked again; the
// cooldown is keyed by provider name and endpoint.
func TestConnectFailureCooldownWindow(t *testing.T) {
	clock := fakeCooldownClock(t)
	primary := coolingProvider("command-code", "https://cc.example")

	_, cooling := coolingRemaining(primary)
	assert.False(t, cooling, "nothing is cooling before a failure")

	markConnectFailed(primary)
	remaining, cooling := coolingRemaining(primary)
	assert.True(t, cooling)
	assert.Eq(t, ConnectFailureCooldown, remaining)

	*clock = clock.Add(ConnectFailureCooldown - time.Second)
	remaining, cooling = coolingRemaining(primary)
	assert.True(t, cooling)
	assert.Eq(t, time.Second, remaining)

	*clock = clock.Add(time.Second)
	_, cooling = coolingRemaining(primary)
	assert.False(t, cooling, "the cooldown expired")

	// Another provider name or endpoint is a different candidate.
	markConnectFailed(primary)
	_, otherName := coolingRemaining(coolingProvider("opencode-go", "https://cc.example"))
	assert.False(t, otherName)
	_, otherEndpoint := coolingRemaining(coolingProvider("command-code", "https://other.example"))
	assert.False(t, otherEndpoint)
}

// TestCoolingSkipKeepsTheLastCandidate: a cooling candidate is skipped only
// while a later one can take the request.
func TestCoolingSkipKeepsTheLastCandidate(t *testing.T) {
	fakeCooldownClock(t)

	t.Run("later candidate available", func(t *testing.T) {
		first := coolingProvider("dead", "https://dead.example")
		second := coolingProvider("alive", "https://alive.example")
		markConnectFailed(first)
		providers := []Provider{first, second}
		remaining, skip := coolingSkip(providers, 0)
		assert.True(t, skip)
		assert.Eq(t, ConnectFailureCooldown, remaining)
		_, skip = coolingSkip(providers, 1)
		assert.False(t, skip)
	})

	t.Run("every later candidate cooling too", func(t *testing.T) {
		first := coolingProvider("dead", "https://dead.example")
		second := coolingProvider("dead2", "https://dead2.example")
		markConnectFailed(first)
		markConnectFailed(second)
		providers := []Provider{first, second}
		_, skip := coolingSkip(providers, 0)
		assert.False(t, skip, "the last resort is still asked")
		_, skip = coolingSkip(providers, 1)
		assert.False(t, skip)
	})
}

// TestPoolSkipsCoolingCandidate: after the primary's endpoint failed to connect,
// the following request of the same run asks the working model first instead of
// paying the connect attempt again; once the cooldown expires the primary is
// tried again.
//
// Reverse verification: removing the coolingSkip branch in Pool.Generate makes
// the second request attempt the dead primary again, so its attempt count is 2
// instead of 1 and the check fails.
func TestPoolSkipsCoolingCandidate(t *testing.T) {
	clock := fakeCooldownClock(t)
	backup, backupHits := jsonServer(t, wholeCompletion)
	primary := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: closedServerURL(t), Model: "m", Name: "command-code", Retry: fastRetry})
	pool := NewPool([]Provider{
		primary,
		NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: backup.URL, Model: "m", Name: "opencode-go", Retry: fastRetry}),
	})
	var attempts []Attempt
	pool.SetObserver(func(_ context.Context, a Attempt) { attempts = append(attempts, a) })
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}

	res, err := pool.Generate(context.Background(), req)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "backup", res.Content)
	assert.Require(t, assert.Eq(t, 2, len(attempts)), "the first request tries the dead primary once")
	assert.Eq(t, "command-code", attempts[0].Provider)

	attempts = nil
	res, err = pool.Generate(context.Background(), req)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "backup", res.Content)
	assert.Require(t, assert.Eq(t, 1, len(attempts)), "the cooling primary is skipped")
	assert.Eq(t, "opencode-go", attempts[0].Provider)
	assert.Eq(t, int32(2), backupHits.Load())

	// Beyond the cooldown the primary is asked again (and fails again).
	*clock = clock.Add(ConnectFailureCooldown)
	attempts = nil
	res, err = pool.Generate(context.Background(), req)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "backup", res.Content)
	assert.Require(t, assert.Eq(t, 2, len(attempts)), "the expired cooldown no longer skips the primary")
	assert.Eq(t, "command-code", attempts[0].Provider)
}
