package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestConfigSetRefusesOriginJudgeKeys: the judge switches are protected keys
// (design 2026-11-10 B, 2026-11-11 §1) - a suga session cannot change them from
// the shell, while a human at a terminal outside suga still can. models.judge
// stays an ordinary writable key.
func TestConfigSetRefusesOriginJudgeKeys(t *testing.T) {
	t.Run("a session is refused and nothing is written", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "session-1")
		configPath := filepath.Join(home, "config.yaml")
		before := readConfigFile(t, configPath)
		for _, tc := range [][]string{
			{"config", "set", "tools.origin_judge.enabled", "false"},
			{"config", "set", "tools.origin_judge.timeout", "1h"},
			{"config", "unset", "tools.origin_judge.enabled"},
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
			{"config", "set", "tools.origin_judge.enabled", "false"},
			{"config", "set", "tools.origin_judge.timeout", "1h"},
		} {
			t.Run(strings.Join(tc, " "), func(t *testing.T) {
				assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), tc...))
			})
		}
		body := readConfigFile(t, configPath)
		assert.StrContains(t, body, "origin_judge:")
		assert.StrContains(t, body, "enabled: false")
		assert.StrContains(t, body, "timeout: 1h")

		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "unset", "tools.origin_judge.enabled"))
		_, statErr := os.Stat(filepath.Join(home, "config.yaml"))
		assert.NoErr(t, statErr)
	})
}
