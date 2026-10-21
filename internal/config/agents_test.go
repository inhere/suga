package axconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

func TestReadAgentSettings(t *testing.T) {
	load := func(t *testing.T, body string) {
		t.Helper()
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		root := t.TempDir()
		t.Setenv("SUGA_HOME", root)
		t.Setenv("SUGA_CONFIG", "")
		t.Chdir(t.TempDir())
		assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0600))
		assert.NoErr(t, Load())
	}
	t.Run("defaults", func(t *testing.T) {
		load(t, "model: openai/m\n")
		got, err := ReadAgentSettings()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, DefaultAgentSettings, got)
	})
	t.Run("set", func(t *testing.T) {
		load(t, "agents:\n  enabled: false\n  model: openai/small\n  max_steps: 12\n  max_tokens: 1000\n  timeout: 90s\n")
		got, err := ReadAgentSettings()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, AgentSettings{Enabled: false, Model: "openai/small", MaxSteps: 12, MaxTokens: 1000, Timeout: 90 * time.Second}, got)
	})
	t.Run("errors", func(t *testing.T) {
		for body, msg := range map[string]string{
			"agents:\n  enabled: maybe\n":   "agents.enabled must be true or false",
			"agents:\n  max_steps: 0\n":     "agents.max_steps must be a whole number >= 1",
			"agents:\n  max_tokens: lots\n": "agents.max_tokens must be a whole number >= 1",
			"agents:\n  timeout: 0\n":       "agents.timeout",
		} {
			load(t, body)
			_, err := ReadAgentSettings()
			assert.ErrMsgContains(t, err, msg)
		}
	})
}

func TestReadNudgeSetting(t *testing.T) {
	reset := func(t *testing.T) {
		t.Helper()
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
	}
	t.Run("default on", func(t *testing.T) {
		reset(t)
		on, err := ReadNudgeSetting()
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, on)
	})
	t.Run("values", func(t *testing.T) {
		for raw, want := range map[string]bool{"on": true, "true": true, "off": false, "false": false, " OFF ": false} {
			reset(t)
			assert.NoErr(t, gkconfig.Set("agents.nudge", raw))
			on, err := ReadNudgeSetting()
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, want, on)
		}
	})
	t.Run("invalid value", func(t *testing.T) {
		reset(t)
		assert.NoErr(t, gkconfig.Set("agents.nudge", "sometimes"))
		_, err := ReadNudgeSetting()
		assert.ErrMsgContains(t, err, "agents.nudge must be on or off")
	})
}
