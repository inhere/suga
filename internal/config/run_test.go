package axconfig

import (
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

func TestRunDeadlineEnvSetting(t *testing.T) {
	t.Run("default is disabled", func(t *testing.T) {
		assert.Eq(t, DefaultRunDeadlineEnv, ReadRunDeadlineEnv())
	})
	t.Run("the key is a writable simple string", func(t *testing.T) {
		descriptor, err := MatchConfigKey(RunDeadlineEnvKey)
		assert.NoErr(t, err)
		assert.Eq(t, ConfigTypeString, descriptor.Type)
		assert.True(t, descriptor.Writable)
		assert.True(t, IsSimpleConfigType(descriptor.Type))
	})
	t.Run("a configured value is read trimmed", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set(RunDeadlineEnvKey, "GOFER_JOB_DEADLINE"))
		t.Cleanup(func() { _ = gkconfig.Set(RunDeadlineEnvKey, "") })
		assert.Eq(t, "GOFER_JOB_DEADLINE", ReadRunDeadlineEnv())
		value, err := ParseConfigValue(RunDeadlineEnvKey, " GOFER_JOB_ID ")
		assert.NoErr(t, err)
		assert.Eq(t, "GOFER_JOB_ID", value)
		resolved, err := ReadConfigValue(RunDeadlineEnvKey)
		assert.NoErr(t, err)
		assert.Eq(t, "GOFER_JOB_DEADLINE", resolved.Value)
	})
}
