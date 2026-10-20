package axconfig

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestReadToolBodySettings: store.body_inline_bytes is the size above which a
// tool result body moves to sessions/<id>/bodies.jsonl; 0 keeps every body in
// the ledger and a negative value is an error.
func TestReadToolBodySettings(t *testing.T) {
	loadYAML(t, `
store:
  body_inline_bytes: 2048
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
  body_inline_bytes: 0
`)
		settings, err := ReadToolBodySettings()
		assert.NoErr(t, err)
		assert.Eq(t, int64(0), settings.InlineBytes)
	})

	t.Run("a negative value is an error", func(t *testing.T) {
		loadYAML(t, `
store:
  body_inline_bytes: -1
`)
		_, err := ReadToolBodySettings()
		assert.ErrMsgContains(t, err, "store.body_inline_bytes must be 0 or more, got -1")
	})
}

// TestRemovedToolBodyKey: store.tool_body_inline_bytes was removed in v0.41.0.
// A configuration that still sets it loads (a warning, never an error: an old
// file must not keep suga from starting) and the value is ignored.
func TestRemovedToolBodyKey(t *testing.T) {
	t.Run("the old name is warned about once and ignored", func(t *testing.T) {
		loadYAML(t, `
store:
  tool_body_inline_bytes: 3072
`)
		notices := DeprecatedKeyNotices()
		assert.Eq(t, 1, len(notices))
		assert.StrContains(t, notices[0], "警告：配置键 store.tool_body_inline_bytes 已在 "+DeprecatedConfigRemoval+" 移除并被忽略，改用 store.body_inline_bytes")
		settings, err := ReadToolBodySettings()
		assert.NoErr(t, err)
		assert.Eq(t, DefaultToolBodySettings().InlineBytes, settings.InlineBytes, "the removed value has no effect")
		resolved, err := ReadConfigValue("store.body_inline_bytes")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, DefaultToolBodySettings().InlineBytes, resolved.Value)
		assert.Eq(t, SourceDefault, resolved.Source)
	})

	t.Run("the new name wins and says nothing", func(t *testing.T) {
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

	t.Run("the current name alone is not reported", func(t *testing.T) {
		loadYAML(t, `
store:
  body_inline_bytes: 4096
`)
		assert.Eq(t, 0, len(DeprecatedKeyNotices()))
	})
}
