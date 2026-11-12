package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestConfigHooksCommands runs the real config command path (GR110) for the
// hooks.commands entry list: a human at a terminal can write it, the value is
// printed back as the single-line flow YAML that set accepts again, an invalid
// list is an input error without writes, and a session is refused like the other
// protected keys.
func TestConfigHooksCommands(t *testing.T) {
	// configRun runs one config command through the root dispatch and returns
	// stdout plus the command error.
	configRun := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs(args)
		return stdout.String(), err
	}

	t.Run("set writes the entries and prints them as flow yaml", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		out, err := configRun(t, "config", "set", "hooks.commands", `[{command: [x], timeout_ms: 10000}]`)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "hooks.commands = [{command: [x], timeout_ms: 10000}]")
		assert.StrContains(t, out, "source = config:")

		body := readConfigFile(t, filepath.Join(home, "config.yaml"))
		assert.StrContains(t, body, "commands:")

		out, err = configRun(t, "config", "get", "hooks.commands")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "hooks.commands = [{command: [x], timeout_ms: 10000}]")
		assert.StrContains(t, out, "source = config:")
		assert.Eq(t, 2, strings.Count(strings.TrimSpace(out), "\n")+1)
	})

	t.Run("set keeps entries without timeout_ms", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/config\n")
		out, err := configRun(t, "config", "set", "hooks.commands", `[{command: [gofer, hook, generic]}, {command: [idevstd, script], timeout_ms: 2000}]`)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "hooks.commands = [{command: [gofer, hook, generic]}, {command: [idevstd, script], timeout_ms: 2000}]")

		out, err = configRun(t, "config", "get", "hooks.commands")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "hooks.commands = [{command: [gofer, hook, generic]}, {command: [idevstd, script], timeout_ms: 2000}]")
	})

	t.Run("unset prints the unset marker", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/config\n")
		out, err := configRun(t, "config", "get", "hooks.commands")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "hooks.commands = <unset>")
		assert.StrContains(t, out, "source = default")
	})

	t.Run("an empty command is an input error without writes", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		configPath := filepath.Join(home, "config.yaml")
		before := readConfigFile(t, configPath)

		_, err := configRun(t, "config", "set", "hooks.commands", `[{command: []}]`)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), "hooks.commands[0].command")
		assert.Eq(t, before, readConfigFile(t, configPath))
	})

	t.Run("a session is refused and nothing is written", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		configPath := filepath.Join(home, "config.yaml")
		before := readConfigFile(t, configPath)
		t.Setenv("SUGA_SESSION_ID", "session-1")

		_, err := configRun(t, "config", "set", "hooks.commands", `[{command: [x]}]`)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), "hooks.commands")
		assert.Eq(t, before, readConfigFile(t, configPath))
	})

	t.Run("list shows the same single-line value", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/config\nhooks:\n  commands:\n    - command: [x]\n      timeout_ms: 10000\n")
		out, err := configRun(t, "config", "list")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "hooks.commands = [{command: [x], timeout_ms: 10000}]")
	})

	t.Run("other yaml keys keep their formatting", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/config\nmcp:\n  servers:\n    - id: srv\n      command: srv-bin\n")
		out, err := configRun(t, "config", "get", "mcp.servers")
		assert.Require(t, assert.NoErr(t, err))
		// mcp.servers still prints through formatConfigValue, not the flow-YAML
		// branch of hooks.commands.
		assert.StrContains(t, out, "mcp.servers = [map[")
		assert.NotContains(t, out, "mcp.servers = [{")
	})
}
