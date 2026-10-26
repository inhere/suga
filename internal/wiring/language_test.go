package wiring

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestAssembleTranslator covers the startup language assembly Open and the
// manifest share: config and SUGA_LANGUAGE decide the catalog, the setting and
// its source, and a missing key is logged through slog instead of stderr.
func TestAssembleTranslator(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)

	for _, tc := range []struct {
		name     string
		config   any
		env      string
		setting  string
		resolved string
		source   string
		greeting string
	}{
		{name: "config en", config: "en", setting: "en", resolved: "en", source: "config", greeting: "Hello suga"},
		{name: "config alias zh", config: "zh_CN", setting: "zh", resolved: "zh", source: "config", greeting: "你好 suga"},
		{name: "env wins over config", config: "zh", env: "english", setting: "en", resolved: "en", source: "env", greeting: "Hello suga"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gkconfig.Reset()
			if tc.config != nil {
				assert.NoErr(t, gkconfig.Set("language", tc.config))
			}
			t.Setenv("SUGA_LANGUAGE", tc.env)
			tr, info, err := assembleTranslator()
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, tc.setting, info.Setting)
			assert.Eq(t, tc.resolved, info.Resolved)
			assert.Eq(t, tc.source, info.Source)
			assert.Eq(t, "", info.Locale, "the locale is only reported for auto")
			assert.Eq(t, tc.greeting, tr.T("common.language.greeting", map[string]any{"name": "suga"}))
		})
	}

	t.Run("auto reports the probed locale", func(t *testing.T) {
		gkconfig.Reset()
		t.Setenv("SUGA_LANGUAGE", "")
		_, info, err := assembleTranslator()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "auto", info.Setting)
		assert.Eq(t, "default", info.Source)
		assert.True(t, info.Resolved == "zh" || info.Resolved == "en")
	})

	t.Run("an invalid value fails the assembly", func(t *testing.T) {
		gkconfig.Reset()
		assert.NoErr(t, gkconfig.Set("language", "klingon"))
		t.Setenv("SUGA_LANGUAGE", "")
		_, _, err := assembleTranslator()
		assert.Err(t, err)
	})

	t.Run("manifest json keeps stable field names", func(t *testing.T) {
		raw, err := json.Marshal(ManifestLanguage{Setting: "auto", Resolved: "en", Source: "default", Locale: "C"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, `{"setting":"auto","resolved":"en","source":"default","locale":"C"}`, string(raw))
	})

	t.Run("a missing key is logged through slog", func(t *testing.T) {
		gkconfig.Reset()
		assert.NoErr(t, gkconfig.Set("language", "zh"))
		t.Setenv("SUGA_LANGUAGE", "")
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		t.Cleanup(func() { slog.SetDefault(prev) })
		tr, _, err := assembleTranslator()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "common.no.such.key", tr.T("common.no.such.key"))
		assert.StrContains(t, buf.String(), "common.no.such.key")
	})
}
