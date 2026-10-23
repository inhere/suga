package llm

import (
	"sync"
	"time"
)

// ConnectFailureCooldown is how long a candidate whose endpoint failed to
// connect is skipped by the Pool: the following steps of the same run then ask
// the working model first instead of paying the connect timeout again on every
// step. The state is process-wide (not per turn) and keyed by provider name and
// endpoint, so a second provider configuration pointing at another host is not
// affected.
const ConnectFailureCooldown = 60 * time.Second

// candidateKey identifies one candidate for the cooldown: the provider name and
// its endpoint (base URL). Two models of the same provider on the same endpoint
// share a cooldown; another endpoint of the same provider does not.
type candidateKey struct{ provider, endpoint string }

// cooldownState is the process-wide cooldown registry; now is replaceable by
// tests.
var cooldownState = struct {
	sync.Mutex
	until map[candidateKey]time.Time
	now   func() time.Time
}{until: map[candidateKey]time.Time{}, now: time.Now}

// endpointNamer is optionally implemented by providers that know their
// endpoint; it is part of the cooldown key.
type endpointNamer interface{ Endpoint() string }

// providerEndpoint returns p's endpoint for the cooldown key ("" when the
// provider does not report one, which still separates candidates by name).
func providerEndpoint(p Provider) string {
	if named, ok := p.(endpointNamer); ok {
		return named.Endpoint()
	}
	return ""
}

// candidateKeyOf builds p's cooldown key.
func candidateKeyOf(p Provider) candidateKey {
	return candidateKey{provider: p.Name(), endpoint: providerEndpoint(p)}
}

// markConnectFailed cools p's candidate down for ConnectFailureCooldown. It is
// called by the Pool after an attempt failed in the connection phase.
func markConnectFailed(p Provider) {
	cooldownState.Lock()
	defer cooldownState.Unlock()
	cooldownState.until[candidateKeyOf(p)] = cooldownState.now().Add(ConnectFailureCooldown)
}

// coolingRemaining reports how long p's candidate stays cooled down, and
// whether it is cooling at all right now. An expired entry is dropped.
func coolingRemaining(p Provider) (time.Duration, bool) {
	cooldownState.Lock()
	defer cooldownState.Unlock()
	key := candidateKeyOf(p)
	until, ok := cooldownState.until[key]
	if !ok {
		return 0, false
	}
	remaining := until.Sub(cooldownState.now())
	if remaining <= 0 {
		delete(cooldownState.until, key)
		return 0, false
	}
	return remaining, true
}

// coolingSkip reports whether the Pool should skip providers[i]: its candidate
// is cooling down and a later candidate that is not cooling can take the
// request instead. When no later candidate is available the provider is still
// asked - a single dead endpoint must not fail a turn on its own - so the
// returned flag is false for the last remaining candidate.
func coolingSkip(providers []Provider, i int) (time.Duration, bool) {
	remaining, cooling := coolingRemaining(providers[i])
	if !cooling {
		return 0, false
	}
	for j := i + 1; j < len(providers); j++ {
		if _, laterCooling := coolingRemaining(providers[j]); !laterCooling {
			return remaining, true
		}
	}
	return remaining, false
}

// resetCooldowns forgets every cooldown; tests use it so one case's connect
// failure does not follow the next.
func resetCooldowns() {
	cooldownState.Lock()
	defer cooldownState.Unlock()
	cooldownState.until = map[candidateKey]time.Time{}
}
