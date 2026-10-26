package axconfig

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestLanguageSettingIsRegistered pins the descriptor the config commands rely
// on: a writable top-level enum validated by the language reader.
func TestLanguageSettingIsRegistered(t *testing.T) {
	descriptor, err := MatchConfigKey("language")
	assert.NoErr(t, err)
	assert.Eq(t, ConfigTypeEnum, descriptor.Type)
	assert.True(t, descriptor.Writable)
	assert.False(t, descriptor.Sensitive)
	assert.Eq(t, "language", descriptor.Reader)
}

// TestParseConfigValueNormalizesLanguage covers the spellings `config set
// language` accepts and the canonical value it stores.
func TestParseConfigValueNormalizesLanguage(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{name: "auto", raw: "auto", want: "auto"},
		{name: "canonical zh", raw: "zh", want: "zh"},
		{name: "canonical en", raw: "en", want: "en"},
		{name: "locale alias", raw: "en_US", want: "en"},
		{name: "dash alias with spaces", raw: " zh-CN ", want: "zh"},
		{name: "chinese name", raw: "中文", want: "zh"},
		{name: "english name", raw: "English", want: "en"},
		{name: "system", raw: "system", want: "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseConfigValue("language", tc.raw)
			assert.NoErr(t, err)
			assert.Eq(t, tc.want, got)
		})
	}

	t.Run("invalid value is rejected", func(t *testing.T) {
		for _, raw := range []string{"fr", "japanese", ""} {
			raw := raw
			t.Run("value "+raw, func(t *testing.T) {
				_, err := ParseConfigValue("language", raw)
				if raw == "" {
					// An empty value normalizes to auto (the default) instead of
					// being an error: `config set language ""` means "follow the
					// system locale", the same as removing the key.
					assert.NoErr(t, err)
					return
				}
				assert.Err(t, err)
				assert.StrContains(t, err.Error(), "language")
			})
		}
	})
}

// TestReadLanguage covers the environment/config/default precedence and the
// normalization of hand-written aliases.
func TestReadLanguage(t *testing.T) {
	t.Run("absent setting is auto", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\n")
		t.Setenv("SUGA_LANGUAGE", "")
		value, err := ReadLanguage()
		assert.NoErr(t, err)
		assert.Eq(t, LanguageAuto, value)
	})

	t.Run("configured alias reads back canonically", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage: 中文\n")
		t.Setenv("SUGA_LANGUAGE", "")
		value, err := ReadLanguage()
		assert.NoErr(t, err)
		assert.Eq(t, LanguageZH, value)
	})

	t.Run("environment wins over config", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage: zh\n")
		t.Setenv("SUGA_LANGUAGE", "en_GB")
		value, err := ReadLanguage()
		assert.NoErr(t, err)
		assert.Eq(t, LanguageEN, value)
	})

	t.Run("invalid environment value fails closed", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage: zh\n")
		t.Setenv("SUGA_LANGUAGE", "fr")
		_, err := ReadLanguage()
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "SUGA_LANGUAGE")
	})

	t.Run("invalid config value fails closed", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage: fr\n")
		t.Setenv("SUGA_LANGUAGE", "")
		_, err := ReadLanguage()
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "language")
	})

	t.Run("non-string config value fails closed", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage:\n  - zh\n")
		t.Setenv("SUGA_LANGUAGE", "")
		_, err := ReadLanguage()
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "must be a string")
	})
}

// TestReadConfigValueLanguageSource checks the value and source the config
// commands print: always the canonical setting, attributed to env, config or
// the default.
func TestReadConfigValueLanguageSource(t *testing.T) {
	t.Run("default source", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\n")
		t.Setenv("SUGA_LANGUAGE", "")
		value, err := ReadConfigValue("language")
		assert.NoErr(t, err)
		assert.Eq(t, LanguageAuto, value.Value)
		assert.Eq(t, SourceDefault, value.Source)
	})

	t.Run("config source with alias value", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage: en_US\n")
		t.Setenv("SUGA_LANGUAGE", "")
		value, err := ReadConfigValue("language")
		assert.NoErr(t, err)
		assert.Eq(t, LanguageEN, value.Value)
		assert.Eq(t, SourceConfig, value.Source)
	})

	t.Run("environment source", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage: en\n")
		t.Setenv("SUGA_LANGUAGE", "中文")
		value, err := ReadConfigValue("language")
		assert.NoErr(t, err)
		assert.Eq(t, LanguageZH, value.Value)
		assert.Eq(t, SourceEnv, value.Source)
		assert.Eq(t, "SUGA_LANGUAGE", value.SourceName)
	})
}

// TestValidateConfigKeyLanguage: the candidate written by `config set` must pass
// the same reader a later process start uses.
func TestValidateConfigKeyLanguage(t *testing.T) {
	t.Run("canonical value is accepted", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage: zh\n")
		t.Setenv("SUGA_LANGUAGE", "")
		assert.NoErr(t, ValidateConfigKey("language"))
	})

	t.Run("unregistered alias is rejected", func(t *testing.T) {
		loadYAML(t, "model: deepseek/m\nlanguage: fr\n")
		t.Setenv("SUGA_LANGUAGE", "")
		err := ValidateConfigKey("language")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "language")
	})
}
