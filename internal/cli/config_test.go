package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
)

func TestConfigCommandsUseTheRealCappPath(t *testing.T) {
	t.Run("get reports the effective value and source", func(t *testing.T) {
		home := setupConfigCLI(t, "# keep this heading\nmodel: openai/old # keep inline\n")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "get", "model"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "model = openai/old")
		assert.StrContains(t, stdout.String(), "source = config:")
		assert.StrContains(t, stdout.String(), filepath.Join(home, "config.yaml"))
	})

	t.Run("environment takes precedence without changing yaml", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_MODEL", "deepseek/environment")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "get", "model"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "model = deepseek/environment")
		assert.StrContains(t, stdout.String(), "source = env:SUGA_MODEL")
	})

	t.Run("set preserves comments and keeps one backup", func(t *testing.T) {
		home := setupConfigCLI(t, "# keep this heading\nmodel: openai/old # keep inline\n")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "model", "openai/new"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "已写入")
		configPath := filepath.Join(home, "config.yaml")
		body, readErr := os.ReadFile(configPath)
		assert.Require(t, assert.NoErr(t, readErr))
		assert.StrContains(t, string(body), "# keep this heading")
		assert.StrContains(t, string(body), "model: openai/new # keep inline")
		assert.NoErr(t, os.WriteFile(configPath, body, 0o600))
		err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "model", "openai/latest"})
		assert.NoErr(t, err)
		_, statErr := os.Stat(configPath + ".bak.bak")
		assert.True(t, os.IsNotExist(statErr))
		backup, readErr := os.ReadFile(configPath + ".bak")
		assert.Require(t, assert.NoErr(t, readErr))
		assert.StrContains(t, string(backup), "openai/new")
	})

	t.Run("bad type and secret are input errors without writes", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/old\n")
		configPath := filepath.Join(home, "config.yaml")
		before, err := os.ReadFile(configPath)
		assert.Require(t, assert.NoErr(t, err))

		var stdout, stderr bytes.Buffer
		err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "retry.max_attempts", "many"})
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))

		err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "providers.openai.api_key", "do-not-write"})
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		after, readErr := os.ReadFile(configPath)
		assert.Require(t, assert.NoErr(t, readErr))
		assert.Eq(t, string(before), string(after))
	})

	t.Run("unset removes a leaf but leaves environment precedence alone", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\nchat:\n  history_size: 42\n")
		t.Setenv("SUGA_MODEL", "deepseek/environment")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "unset", "model"})
		assert.NoErr(t, err)
		body, readErr := os.ReadFile(filepath.Join(home, "config.yaml"))
		assert.Require(t, assert.NoErr(t, readErr))
		assert.NotContains(t, string(body), "model:")

		stdout.Reset()
		err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "get", "model"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "model = deepseek/environment")
	})

	t.Run("explicit config is the write target", func(t *testing.T) {
		home := t.TempDir()
		external := filepath.Join(t.TempDir(), "chosen.yaml")
		t.Setenv("SUGA_HOME", home)
		t.Setenv("SUGA_CONFIG", external)
		assert.NoErr(t, os.WriteFile(external, []byte("model: openai/external\n"), 0o600))
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		assert.NoErr(t, appconfig.Load())
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "model", "deepseek/changed"})
		assert.NoErr(t, err)
		body, readErr := os.ReadFile(external)
		assert.Require(t, assert.NoErr(t, readErr))
		assert.StrContains(t, string(body), "deepseek/changed")
		_, statErr := os.Stat(filepath.Join(home, "config.yaml"))
		assert.True(t, os.IsNotExist(statErr))
		matches, globErr := filepath.Glob(filepath.Join(home, "run", "locks", "config-*.lock"))
		assert.Require(t, assert.NoError(t, globErr))
		assert.Eq(t, 1, len(matches))
		_, statErr = os.Stat(external + ".lock")
		assert.True(t, os.IsNotExist(statErr))
	})
	t.Run("path and list are stable command outputs", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "path"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "home = "+home)
		assert.StrContains(t, stdout.String(), "source = home")

		stdout.Reset()
		err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "list"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "model = openai/config")
		assert.StrNotContains(t, stdout.String(), "do-not-write")
	})
}

func TestConfigSetAllowsSimpleRuntimeKeys(t *testing.T) {
	home := setupConfigCLI(t, "model: openai/config\n")
	workspace := filepath.Join(t.TempDir(), "workspace")
	for _, tc := range []struct {
		name, key, value string
	}{
		{name: "allow write", key: "tools.allow_write", value: "true"},
		{name: "approval mode", key: "tools.approval", value: "auto"},
		{name: "workspace", key: "workspace", value: workspace},
		{name: "memory write", key: "memory.write", value: "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", tc.key, tc.value})
			assert.NoErr(t, err)
			assert.StrContains(t, stdout.String(), "已写入")
		})
	}
	configPath := filepath.Join(home, "config.yaml")
	body, err := os.ReadFile(configPath)
	assert.Require(t, assert.NoError(t, err))
	text := string(body)
	assert.StrContains(t, text, "allow_write: true")
	assert.StrContains(t, text, "approval: auto")
	assert.StrContains(t, text, "memory:")
	// Missing parents are written block style, the way config.yaml is edited.
	assert.StrContains(t, text, "memory:\n  write: \"off\"")
	assert.StrContains(t, text, "tools:\n  allow_write: true\n  approval: auto")
	assert.StrContains(t, text, "workspace:")
	_, err = os.Stat(filepath.Join(home, "config.yaml.lock"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(home, "run", "locks", "config.lock"))
	assert.NoErr(t, err)
}

// TestConfigSetAcceptsYAMLListValues covers the two accepted spellings of a
// string list: the comma form and the YAML flow form copied from config.yaml.
// Both must end up as the same block-style list, and a value that starts with
// "[" but is not a list of strings must be rejected without touching the file.
func TestConfigSetAcceptsYAMLListValues(t *testing.T) {
	t.Run("yaml list writes the same bytes as the comma form", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		commaHome := setupConfigCLI(t, "model: openai/config\n")
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "tools.auto_allow", `a/b, c/*`})
		assert.Require(t, assert.NoErr(t, err))
		commaBody, readErr := os.ReadFile(filepath.Join(commaHome, "config.yaml"))
		assert.Require(t, assert.NoErr(t, readErr))
		assert.StrContains(t, string(commaBody), "auto_allow:\n  - a/b\n  - c/*\n")

		stdout.Reset()
		yamlHome := setupConfigCLI(t, "model: openai/config\n")
		err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "tools.auto_allow", `["a/b", 'c/*']`})
		assert.Require(t, assert.NoErr(t, err))
		yamlBody, readErr := os.ReadFile(filepath.Join(yamlHome, "config.yaml"))
		assert.Require(t, assert.NoErr(t, readErr))
		assert.Eq(t, string(commaBody), string(yamlBody))
		// The read-back goes through the same projection as the comma form.
		assert.StrContains(t, stdout.String(), "a/b")
		assert.StrContains(t, stdout.String(), "c/*")
	})

	t.Run("invalid yaml list is an input error without writes", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		configPath := filepath.Join(home, "config.yaml")
		before, readErr := os.ReadFile(configPath)
		assert.Require(t, assert.NoErr(t, readErr))

		for _, value := range []string{"[a/b", "[1]", "[[a/b]]", `[""]`} {
			var stdout, stderr bytes.Buffer
			err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "tools.auto_allow", value})
			assert.Require(t, assert.Err(t, err))
			assert.Eq(t, 2, ExitCode(err))
			assert.StrContains(t, err.Error(), "tools.auto_allow")
		}
		after, readErr := os.ReadFile(configPath)
		assert.Require(t, assert.NoErr(t, readErr))
		assert.Eq(t, string(before), string(after))
	})
}

func setupConfigCLI(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_CONFIG", "")
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(content), 0o600))
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, appconfig.Load())
	return home
}
