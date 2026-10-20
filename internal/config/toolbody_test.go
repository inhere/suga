package axconfig

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestReadToolBodySettings: store.tool_body_inline_bytes is the size above which
// a tool result body moves to sessions/<id>/tools.jsonl; 0 keeps every body in
// the ledger and a negative value is an error.
func TestReadToolBodySettings(t *testing.T) {
	loadYAML(t, `
store:
  tool_body_inline_bytes: 2048
`)
	settings, err := ReadToolBodySettings()
	assert.NoErr(t, err)
	assert.Eq(t, int64(2048), settings.InlineBytes)

	t.Run("unset keeps the shipped default", func(t *testing.T) {
		loadYAML(t, "model: gw/m1\n")
		settings, err := ReadToolBodySettings()
		assert.NoErr(t, err)
		assert.Eq(t, DefaultToolBodySettings().InlineBytes, settings.InlineBytes)
	})

	t.Run("zero keeps every body inline", func(t *testing.T) {
		loadYAML(t, `
store:
  tool_body_inline_bytes: 0
`)
		settings, err := ReadToolBodySettings()
		assert.NoErr(t, err)
		assert.Eq(t, int64(0), settings.InlineBytes)
	})

	t.Run("the new name wins over the old one", func(t *testing.T) {
		loadYAML(t, `
store:
  body_inline_bytes: 4096
  tool_body_inline_bytes: 2048
`)
		settings, err := ReadToolBodySettings()
		assert.NoErr(t, err)
		assert.Eq(t, int64(4096), settings.InlineBytes)
		resolved, err := ReadConfigValue("store.body_inline_bytes")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(4096), resolved.Value)
		assert.Eq(t, SourceConfig, resolved.Source)
	})

	t.Run("a value under the old name still applies", func(t *testing.T) {
		loadYAML(t, `
store:
  tool_body_inline_bytes: 3072
`)
		settings, err := ReadToolBodySettings()
		assert.NoErr(t, err)
		assert.Eq(t, int64(3072), settings.InlineBytes)
		// The old name is a documented alias, so `suga config` must not call it the
		// default: the value does come from the config file.
		resolved, err := ReadConfigValue("store.body_inline_bytes")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(3072), resolved.Value)
		assert.Eq(t, SourceConfig, resolved.Source)
	})

	t.Run("a negative value is an error", func(t *testing.T) {
		loadYAML(t, `
store:
  tool_body_inline_bytes: -1
`)
		_, err := ReadToolBodySettings()
		assert.ErrMsgContains(t, err, "store.tool_body_inline_bytes must be 0 or more, got -1")
	})
}
