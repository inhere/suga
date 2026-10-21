package axconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
)

// AgentSettings configure sub-agents (agents.*; the agent tool).
type AgentSettings struct {
	// Enabled registers the agent tool (agents.enabled, default true).
	Enabled bool
	// Model is the sub-agents' model, <provider>/<model> (agents.model; empty:
	// the main model).
	Model string
	// MaxSteps, MaxTokens (uncached input plus output) and Timeout make a
	// sub-agent wrap up its report (agents.max_steps 30, agents.max_tokens
	// 400000, agents.timeout 10m).
	MaxSteps  int
	MaxTokens int
	Timeout   time.Duration
}

// DefaultAgentSettings are the agents.* defaults.
var DefaultAgentSettings = AgentSettings{Enabled: true, MaxSteps: 30, MaxTokens: 400_000, Timeout: 10 * time.Minute}

// ReadNudgeSetting reads agents.nudge (default on): whether a turn that looks
// finished too early is checked (personal.CheckNudge). "on"/"true" and
// "off"/"false" are accepted; off makes every host skip the check.
func ReadNudgeSetting() (bool, error) {
	const key = "agents.nudge"
	if gkconfig.Get(key) == nil {
		return true, nil
	}
	raw := strings.TrimSpace(gkconfig.String(key))
	switch strings.ToLower(raw) {
	case "on", "true", "yes", "1":
		return true, nil
	case "off", "false", "no", "0":
		return false, nil
	}
	return true, fmt.Errorf("%s must be on or off, got %q", key, raw)
}

// ReadAgentSettings reads agents.*; unset keys take DefaultAgentSettings.
func ReadAgentSettings() (AgentSettings, error) {
	out := DefaultAgentSettings
	if raw := gkconfig.Get("agents.enabled"); raw != nil {
		b, ok := raw.(bool)
		if !ok {
			return out, fmt.Errorf("agents.enabled must be true or false, got %v", raw)
		}
		out.Enabled = b
	}
	out.Model = strings.TrimSpace(gkconfig.String("agents.model"))
	for key, dst := range map[string]*int{"agents.max_steps": &out.MaxSteps, "agents.max_tokens": &out.MaxTokens} {
		if gkconfig.Get(key) == nil {
			continue
		}
		raw := strings.TrimSpace(gkconfig.String(key))
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return out, fmt.Errorf("%s must be a whole number >= 1, got %q", key, raw)
		}
		*dst = n
	}
	if gkconfig.Get("agents.timeout") != nil {
		d, err := parseIdleDuration(strings.TrimSpace(gkconfig.String("agents.timeout")))
		if err != nil || d == 0 {
			if err == nil {
				err = fmt.Errorf("must be above 0")
			}
			return out, fmt.Errorf("agents.timeout: %w", err)
		}
		out.Timeout = d
	}
	return out, nil
}
