package axconfig

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestReadLogSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		loadYAML(t, "")
		s, err := ReadLogSettings()
		assert.NoErr(t, err)
		assert.Eq(t, LogSettings{
			Level: LogLevelInfo, File: "", Stderr: LogStderrAuto,
			MaxSize: DefaultLogMaxSize, Keep: DefaultLogKeep, Compress: false,
		}, s)
	})
	t.Run("configured", func(t *testing.T) {
		loadYAML(t, `log:
  level: debug
  file: off
  stderr: never
  max_size: 65536
  keep: 2
  compress: true
`)
		s, err := ReadLogSettings()
		assert.NoErr(t, err)
		assert.Eq(t, LogSettings{
			Level: LogLevelDebug, File: LogFileOff, Stderr: LogStderrNever,
			MaxSize: 65536, Keep: 2, Compress: true,
		}, s)
	})
	t.Run("bad values name their key", func(t *testing.T) {
		for doc, want := range map[string]string{
			"log:\n  level: verbose\n":      "log.level must be one of",
			"log:\n  stderr: sometimes\n":   "log.stderr must be one of",
			"log:\n  max_size: 10MB\n":      "log.max_size must be a whole number >= 1",
			"log:\n  max_size: 0\n":         "log.max_size must be a whole number >= 1",
			"log:\n  keep: -1\n":            "log.keep must be a whole number >= 0",
			"log:\n  compress: sometimes\n": "log.compress must be true or false",
		} {
			loadYAML(t, doc)
			_, err := ReadLogSettings()
			assert.ErrMsgContains(t, err, want)
		}
	})
}

// TestLogConfigKeysAreRegistered: `suga config set log.*` must validate through
// the same reader, so a bad value is rejected before it reaches config.yaml.
func TestLogConfigKeysAreRegistered(t *testing.T) {
	t.Run("known keys", func(t *testing.T) {
		for _, key := range []string{"log.level", "log.file", "log.stderr", "log.max_size", "log.keep", "log.compress"} {
			descriptor, err := MatchConfigKey(key)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, "log", descriptor.Reader)
			assert.Eq(t, true, descriptor.Writable)
		}
	})
	t.Run("enum values", func(t *testing.T) {
		for _, value := range []string{"debug", "info", "warn", "error"} {
			_, err := ParseConfigValue("log.level", value)
			assert.NoErr(t, err)
		}
		_, err := ParseConfigValue("log.level", "trace")
		assert.ErrMsgContains(t, err, "want one of debug, info, warn, error")
		for _, value := range []string{"auto", "always", "never"} {
			_, err := ParseConfigValue("log.stderr", value)
			assert.NoErr(t, err)
		}
		_, err = ParseConfigValue("log.stderr", "sometimes")
		assert.ErrMsgContains(t, err, "want one of auto, always, never")
	})
	t.Run("bad value fails validation", func(t *testing.T) {
		loadYAML(t, "log:\n  level: loud\n")
		assert.ErrMsgContains(t, ValidateConfigKey("log.level"), "log.level must be one of")
		loadYAML(t, "log:\n  max_size: 0\n")
		assert.ErrMsgContains(t, ValidateConfigKey("log.max_size"), "log.max_size must be a whole number >= 1")
	})
}
