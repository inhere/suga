package axconfig

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestReadImageSettings pins the images.* section: the defaults, an explicit
// value and the refusal of a non-positive cap.
func TestReadImageSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		loadYAML(t, "")
		settings, err := ReadImageSettings()
		assert.NoErr(t, err)
		assert.Eq(t, 3, settings.HistoryTurns)
		assert.Eq(t, 8, settings.MaxRequestCount)
		assert.Eq(t, 20<<20, settings.MaxRequestBytes)
		assert.Eq(t, 1568, settings.MaxSide)
	})
	t.Run("configured values", func(t *testing.T) {
		loadYAML(t, `
images:
  history_turns: 5
  max_request_count: 2
  max_request_bytes: 1048576
  max_side: 800
`)
		settings, err := ReadImageSettings()
		assert.NoErr(t, err)
		assert.Eq(t, ImageSettings{HistoryTurns: 5, MaxRequestCount: 2, MaxRequestBytes: 1 << 20, MaxSide: 800}, settings)
	})
	t.Run("a non-positive cap is refused", func(t *testing.T) {
		for _, key := range []string{"history_turns", "max_request_count", "max_request_bytes", "max_side"} {
			t.Run(key, func(t *testing.T) {
				loadYAML(t, "images:\n  "+key+": 0\n")
				_, err := ReadImageSettings()
				assert.Require(t, assert.Err(t, err))
				assert.Contains(t, err.Error(), "images."+key)
			})
		}
	})
}
