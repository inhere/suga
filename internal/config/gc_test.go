package axconfig

import (
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestReadGCRetentionSettings: the retention keys of `suga gc --retention` keep
// the documented defaults and accept both a day count and a duration.
func TestReadGCRetentionSettings(t *testing.T) {
	t.Run("defaults are 180d and 30d", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		settings, err := ReadGCRetentionSettings()
		assert.NoErr(t, err)
		assert.Eq(t, 180*24*time.Hour, settings.Interactive)
		assert.Eq(t, 30*24*time.Hour, settings.NonInteractive)
	})

	t.Run("a day count and a duration are both accepted", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		assert.NoErr(t, gkconfig.Set("gc.retention", map[string]any{"interactive": "90d", "noninteractive": "720h"}))
		settings, err := ReadGCRetentionSettings()
		assert.NoErr(t, err)
		assert.Eq(t, 90*24*time.Hour, settings.Interactive)
		assert.Eq(t, 30*24*time.Hour, settings.NonInteractive)
	})

	t.Run("unknown fields and negative periods are rejected", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		assert.NoErr(t, gkconfig.Set("gc.retention", map[string]any{"interactiv": "90d"}))
		_, err := ReadGCRetentionSettings()
		assert.ErrMsgContains(t, err, "not a recognized setting")

		gkconfig.Reset()
		assert.NoErr(t, gkconfig.Set("gc.retention", map[string]any{"interactive": "-1d"}))
		_, err = ReadGCRetentionSettings()
		assert.ErrMsgContains(t, err, "must not be negative")
	})

	t.Run("the registry knows the keys and parses a day count", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		for _, key := range []string{"gc.retention.interactive", "gc.retention.noninteractive"} {
			descriptor, err := MatchConfigKey(key)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, ConfigTypeDuration, descriptor.Type)
			value, err := ParseConfigValue(key, "180d")
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, 180*24*time.Hour, value)
		}
	})
}
