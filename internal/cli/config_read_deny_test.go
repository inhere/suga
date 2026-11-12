package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestConfigSetRefusesReadDenyKeys: the ban keys are protected keys (design
// 2026-11-10 C, 2026-11-11 §1) - a suga session cannot switch them off from the
// shell, while a human at a terminal outside suga still can.
func TestConfigSetRefusesReadDenyKeys(t *testing.T) {
	t.Run("a session is refused and nothing is written", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "session-1")
		configPath := filepath.Join(home, "config.yaml")
		before := readConfigFile(t, configPath)
		for _, tc := range [][]string{
			{"config", "set", "tools.read_deny_defaults", "false"},
			{"config", "set", "tools.read_deny", "a/b"},
			{"config", "unset", "tools.read_deny"},
		} {
			t.Run(strings.Join(tc, " "), func(t *testing.T) {
				err := runConfigProtected(t, strings.NewReader(""), tc...)
				assert.Require(t, assert.Err(t, err))
				assert.Eq(t, 2, ExitCode(err))
				assert.StrContains(t, err.Error(), tc[2])
			})
		}
		assert.Eq(t, before, readConfigFile(t, configPath))
	})

	t.Run("a terminal outside a session can change them", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configPath := filepath.Join(home, "config.yaml")
		for _, tc := range [][]string{
			{"config", "set", "tools.read_deny_defaults", "false"},
			{"config", "set", "tools.read_deny", "a/b"},
		} {
			t.Run(strings.Join(tc, " "), func(t *testing.T) {
				assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), tc...))
			})
		}
		body := readConfigFile(t, configPath)
		assert.StrContains(t, body, "read_deny_defaults: false")
		assert.StrContains(t, body, "read_deny:")
		assert.StrContains(t, body, "- a/b")

		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "unset", "tools.read_deny"))
		assert.NotContains(t, readConfigFile(t, filepath.Join(home, "config.yaml")), "read_deny:")
	})
}
