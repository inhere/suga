package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// protectedTestKey is a writable protected key: a session must not change it,
// a human at a terminal outside suga still can.
const protectedTestKey = "tools.origin_judge.enabled"

// runConfigProtected runs `suga config ...` through the real root dispatch
// (GR110): the gate lives in the leaf handler, so a direct handler call would
// not prove the command path.
func runConfigProtected(t *testing.T, stdin io.Reader, args ...string) error {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return newCLI(context.Background(), stdin, &stdout, &stderr).RunWithArgs(args)
}

func readConfigFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	return string(body)
}

// TestConfigProtected covers design 2026-11-11 §2 at the command layer: a
// protected key is only writable by a human at a terminal outside suga, and a
// refused write leaves the config file byte-identical.
func TestConfigProtected(t *testing.T) {
	refused := func(t *testing.T, args ...string) {
		t.Helper()
		err := runConfigProtected(t, strings.NewReader(""), args...)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), args[2])
		assert.StrContains(t, err.Error(), "config.yaml")
	}

	t.Run("a suga session refuses set and unset", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "session-1")
		configPath := filepath.Join(home, "config.yaml")
		before := readConfigFile(t, configPath)

		t.Run("set", func(t *testing.T) {
			refused(t, "config", "set", protectedTestKey, "false")
		})
		t.Run("unset", func(t *testing.T) {
			refused(t, "config", "unset", protectedTestKey)
		})
		assert.Eq(t, before, readConfigFile(t, configPath))
	})

	t.Run("a piped stdin is refused outside a session", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configStdinIsTerminal = func(io.Reader) bool { return false }
		configPath := filepath.Join(home, "config.yaml")
		before := readConfigFile(t, configPath)

		t.Run("set", func(t *testing.T) {
			refused(t, "config", "set", protectedTestKey, "false")
		})
		t.Run("unset", func(t *testing.T) {
			refused(t, "config", "unset", protectedTestKey)
		})
		assert.Eq(t, before, readConfigFile(t, configPath))
	})

	t.Run("a terminal outside a session writes and unsets", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configPath := filepath.Join(home, "config.yaml")

		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "set", protectedTestKey, "false"))
		body := readConfigFile(t, configPath)
		assert.StrContains(t, body, "origin_judge:")
		assert.StrContains(t, body, "enabled: false")

		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "unset", protectedTestKey))
		body = readConfigFile(t, configPath)
		assert.NotContains(t, body, "origin_judge:")
	})

	t.Run("a non-protected key is writable without a terminal", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configStdinIsTerminal = func(io.Reader) bool { return false }
		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "set", "max_steps", "8"))
		assert.StrContains(t, readConfigFile(t, filepath.Join(home, "config.yaml")), "max_steps: 8")
	})

	t.Run("a read-only protected key stays read-only", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		err := runConfigProtected(t, strings.NewReader(""), "config", "set", "hooks.pre_tool_use.tools", "Bash")
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), "read-only")
	})
}
