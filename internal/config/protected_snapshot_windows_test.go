//go:build windows

package axconfig

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// Windows treats the same path in any case as one file, so the snapshot and
// the lock must resolve to one name as well.
func TestProtectedPathNormalizeWindowsCase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_CONFIG", "")
	configPath := filepath.Join(home, "config.yaml")

	t.Run("upper case path keeps the default snapshot", func(t *testing.T) {
		lowered, err := protectedSnapshotPath(configPath)
		assert.NoErr(t, err)
		upper, err := protectedSnapshotPath(strings.ToUpper(configPath))
		assert.NoErr(t, err)
		assert.Eq(t, lowered, upper)
		assert.Eq(t, filepath.Join(home, "run", "protected-config", "default.json"), lowered)
	})

	t.Run("another config file has one lock and one snapshot name", func(t *testing.T) {
		other := filepath.Join(home, "other", "chosen.yaml")
		snapshotPath, err := protectedSnapshotPath(other)
		assert.NoErr(t, err)
		mixedSnapshot, err := protectedSnapshotPath(strings.ToUpper(filepath.Dir(other)) + `\chosen.yaml`)
		assert.NoErr(t, err)
		assert.Eq(t, snapshotPath, mixedSnapshot)
		assert.Eq(t, configLockPath(other), configLockPath(strings.ToUpper(other)))
	})
}
