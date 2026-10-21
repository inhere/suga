package axconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	goccyyaml "github.com/goccy/go-yaml"
	"github.com/gookit/goutil/x/assert"
)

func TestReferenceConfigLeavesAreRegistered(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	assert.True(t, ok)
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "config", "config.yaml")
	content, err := os.ReadFile(path)
	assert.Require(t, assert.NoError(t, err))
	var document map[string]any
	assert.Require(t, assert.NoError(t, goccyyaml.Unmarshal(content, &document)))

	var check func(any, []string)
	check = func(value any, prefix []string) {
		if mapping, ok := value.(map[string]any); ok {
			for key, child := range mapping {
				check(child, append(prefix, key))
			}
			return
		}
		key := strings.Join(prefix, ".")
		_, matchErr := MatchConfigKey(key)
		assert.NoErr(t, matchErr)
	}
	check(document, nil)
}

func TestDocumentedSimpleKeysRemainWritable(t *testing.T) {
	for _, key := range []string{
		"tools.allow_write", "tools.approval", "tools.deferred", "tools.parallel_reads",
		"tools.minimize_output", "tools.web_fetch.allow_private", "tools.web_fetch.user_agent", "workspace",
		"mcp.call_timeout_sec", "memory.write", "memory.write_tainted", "memory.taint_tools",
		"memory.user.backend", "memory.project.backend", "hooks.timeout_ms", "hooks.stop_background",
		"skills.list_max", "skills.always_load",
	} {
		t.Run(key, func(t *testing.T) {
			descriptor, err := MatchConfigKey(key)
			assert.NoErr(t, err)
			assert.True(t, descriptor.Writable)
			if descriptor.Reader == "" {
				assert.True(t, IsSimpleConfigType(descriptor.Type))
			}
		})
	}
}
func TestMatchConfigKey(t *testing.T) {
	known := KnownConfigKeys()
	assert.True(t, len(known) > 0)

	cases := []struct {
		name      string
		key       string
		wantType  ConfigValueType
		secret    bool
		wantError bool
	}{
		{name: "fixed model", key: "model", wantType: ConfigTypeString},
		{name: "provider field", key: "providers.gateway.base_url", wantType: ConfigTypeString},
		{name: "model field with dot", key: "providers.gateway.models.gpt-4.1.context_window", wantType: ConfigTypeInt},
		{name: "provider secret", key: "providers.gateway.api_key", wantType: ConfigTypeString, secret: true},
		{name: "unknown provider field", key: "providers.gateway.unknown", wantError: true},
		{name: "unknown nested structure", key: "providers.gateway.models.gpt.headers.value", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			descriptor, err := MatchConfigKey(tc.key)
			if tc.wantError {
				assert.Err(t, err)
				return
			}
			assert.NoErr(t, err)
			assert.Eq(t, tc.wantType, descriptor.Type)
			assert.Eq(t, tc.secret, descriptor.Sensitive)
		})
	}
}

func TestParseConfigValue(t *testing.T) {
	cases := []struct {
		name string
		key  string
		raw  string
		want any
		err  bool
	}{
		{name: "bool", key: "chat.markdown", raw: "off", want: false},
		{name: "integer", key: "retry.max_attempts", raw: "4", want: 4},
		{name: "duration", key: "retry.base_delay", raw: "2s", want: 2 * time.Second},
		{name: "list", key: "fallback_models", raw: "openai/a, deepseek/b", want: []string{"openai/a", "deepseek/b"}},
		{name: "yaml list", key: "tools.auto_allow", raw: "[\"a/b\"]", want: []string{"a/b"}},
		{name: "yaml list unquoted items", key: "tools.auto_allow", raw: "[a/b, c/*]", want: []string{"a/b", "c/*"}},
		{name: "yaml list with spaces and quotes", key: "tools.read_roots", raw: "[\"a b\", 'c d']", want: []string{"a b", "c d"}},
		{name: "yaml empty list", key: "skills.always_load", raw: "[]", want: []string{}},
		{name: "yaml list keeps glob brackets inside quotes", key: "tools.auto_allow", raw: "[\"[everything/echo]\"]", want: []string{"[everything/echo]"}},
		{name: "interpreter switch", key: "tools.interpreters", raw: "off", want: false},
		{name: "enum", key: "providers.gateway.thinking", raw: "HIGH", want: "high"},
		{name: "yaml map", key: "providers.gateway.headers", raw: "{X-Test: value}", want: map[string]string{"X-Test": "value"}},
		{name: "bad type", key: "retry.max_attempts", raw: "many", err: true},
		{name: "bad enum", key: "tools.deferred", raw: "sometimes", err: true},
		{name: "broken yaml list", key: "tools.auto_allow", raw: "[a/b", err: true},
		{name: "yaml list item is not a string", key: "tools.auto_allow", raw: "[1]", err: true},
		{name: "yaml list item is empty", key: "tools.auto_allow", raw: "[\"\"]", err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseConfigValue(tc.key, tc.raw)
			if tc.err {
				assert.Err(t, err)
				assert.StrContains(t, err.Error(), tc.key)
				return
			}
			assert.NoErr(t, err)
			assert.Eq(t, tc.want, got)
		})
	}
}

func TestReadConfigValueSourcesAndSecrets(t *testing.T) {
	t.Run("model environment wins over config", func(t *testing.T) {
		loadYAML(t, "model: deepseek/config-model\n")
		t.Setenv("SUGA_MODEL", "openai/env-model")
		value, err := ResolveConfigValue("model")
		assert.NoErr(t, err)
		assert.Eq(t, "openai/env-model", value.Value)
		assert.Eq(t, SourceEnv, value.Source)
	})

	t.Run("config value records config source", func(t *testing.T) {
		loadYAML(t, "model: deepseek/config-model\n")
		value, err := ReadConfigValue("model")
		assert.NoErr(t, err)
		assert.Eq(t, "deepseek/config-model", value.Value)
		assert.Eq(t, SourceConfig, value.Source)
	})

	t.Run("variable reference records variable name and masks secret", func(t *testing.T) {
		loadYAML(t, "model: gateway/m\nproviders:\n  gateway:\n    type: openai\n    api_key: \"${SUGA_TEST_REGISTRY_KEY}\"\n")
		t.Setenv("SUGA_TEST_REGISTRY_KEY", "not-for-output")
		value, err := ReadConfigValue("providers.gateway.api_key")
		assert.NoErr(t, err)
		assert.True(t, value.Sensitive)
		assert.Eq(t, "<set>", value.Value)
		assert.Eq(t, SourceEnv, value.Source)
		assert.Eq(t, "SUGA_TEST_REGISTRY_KEY", value.SourceName)
		assert.NotContains(t, value.Value, "not-for-output")
	})

	t.Run("unset chat switch uses reader default", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\n")
		value, err := ReadConfigValue("chat.markdown")
		assert.NoErr(t, err)
		assert.Eq(t, true, value.Value)
		assert.Eq(t, SourceDefault, value.Source)
	})

	t.Run("provider retry inherits config source", func(t *testing.T) {
		loadYAML(t, "model: gateway/m\nretry:\n  max_attempts: 7\nproviders:\n  gateway:\n    type: openai\n")
		value, err := ReadConfigValue("providers.gateway.retry.max_attempts")
		assert.NoErr(t, err)
		assert.Eq(t, 7, value.Value)
		assert.Eq(t, SourceConfig, value.Source)
	})

	t.Run("tool default is explicit and safe", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\n")
		value, err := ReadConfigValue("tools.allow_write")
		assert.NoErr(t, err)
		assert.Eq(t, false, value.Value)
		assert.Eq(t, SourceDefault, value.Source)
	})

	t.Run("configured tool value wins over default", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\ntools:\n  deferred: never\n")
		value, err := ReadConfigValue("tools.deferred")
		assert.NoErr(t, err)
		assert.Eq(t, "never", value.Value)
		assert.Eq(t, SourceConfig, value.Source)
	})
}

func TestValidateConfigKeyUsesExistingReaders(t *testing.T) {
	t.Run("provider reader rejects invalid protocol", func(t *testing.T) {
		loadYAML(t, "providers:\n  gateway:\n    type: not-a-provider\n")
		err := ValidateConfigKey("providers.gateway.type")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "unsupported type")
	})

	t.Run("chat reader rejects invalid history size", func(t *testing.T) {
		loadYAML(t, "chat:\n  history_size: invalid\n")
		err := ValidateConfigKey("chat.history_size")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "chat.history_size")
	})

	t.Run("simple key without reader still validates", func(t *testing.T) {
		loadYAML(t, "tools:\n  allow_write: false\n")
		err := ValidateConfigKey("tools.allow_write")
		assert.NoErr(t, err)
	})
}

// TestIsSecretKey: keys that hold credentials are recognized by path, so
// config set can refuse to write them into config.yaml.
func TestIsSecretKey(t *testing.T) {
	for key, want := range map[string]bool{
		"providers.gateway.api_key":       true,
		"serpapi_key":                     true,
		"providers.gateway.headers.token": true,
		"mcp.servers.x.env.DB_PASSWORD":   true,
		"model":                           false,
		"tools.allow_write":               false,
		"providers.gateway.base_url":      false,
	} {
		t.Run(key, func(t *testing.T) {
			assert.Eq(t, want, IsSecretKey(key))
		})
	}
}
