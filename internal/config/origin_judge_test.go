package axconfig

import (
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

func TestReadOriginJudgeSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		gkconfig.Reset()
		got, err := ReadOriginJudgeSettings()
		assert.NoErr(t, err)
		assert.True(t, got.Enabled)
		assert.Eq(t, 20*time.Second, got.Timeout)
	})
	t.Run("explicit values", func(t *testing.T) {
		gkconfig.Reset()
		assert.NoErr(t, gkconfig.Set(keyOriginJudgeEnabled, false))
		assert.NoErr(t, gkconfig.Set(keyOriginJudgeTimeout, "5s"))
		got, err := ReadOriginJudgeSettings()
		assert.NoErr(t, err)
		assert.False(t, got.Enabled)
		assert.Eq(t, 5*time.Second, got.Timeout)
	})
	for _, bad := range []string{"0s", "-3s", "abc", ""} {
		t.Run("bad timeout "+bad, func(t *testing.T) {
			gkconfig.Reset()
			assert.NoErr(t, gkconfig.Set(keyOriginJudgeTimeout, bad))
			_, err := ReadOriginJudgeSettings()
			assert.Err(t, err)
			assert.StrContains(t, err.Error(), keyOriginJudgeTimeout)
		})
	}
}

func TestOriginJudgeKeysAreNotWritable(t *testing.T) {
	for _, key := range []string{keyOriginJudgeEnabled, keyOriginJudgeTimeout} {
		d, err := MatchConfigKey(key)
		assert.Require(t, assert.NoErr(t, err), key)
		assert.False(t, d.Writable, key)
	}
	d, err := MatchConfigKey("models.judge")
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, d.Writable)
}
