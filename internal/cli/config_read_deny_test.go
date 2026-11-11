package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestConfigSetRefusesReadDenyKeys: the model can run `suga config set` through
// the shell, so the ban keys must not be writable there (design 2026-11-10 C).
func TestConfigSetRefusesReadDenyKeys(t *testing.T) {
	home := setupConfigCLI(t, "model: openai/config\n")
	configPath := filepath.Join(home, "config.yaml")
	before, err := os.ReadFile(configPath)
	assert.Require(t, assert.NoErr(t, err))
	for _, tc := range [][]string{
		{"config", "set", "tools.read_deny_defaults", "false"},
		{"config", "set", "tools.read_deny", "[]"},
		{"config", "unset", "tools.read_deny"},
	} {
		t.Run(strings.Join(tc, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs(tc)
			assert.Require(t, assert.Err(t, err))
			assert.StrContains(t, err.Error(), "read-only")
			assert.Eq(t, 2, ExitCode(err))
		})
	}
	after, err := os.ReadFile(configPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(before), string(after))
}
