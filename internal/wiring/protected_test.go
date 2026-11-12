package wiring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
)

// TestProtectedCheck covers the wiring of the startup comparison: the switch
// that turns it off and the state Open exposes.
func TestProtectedCheck(t *testing.T) {
	t.Run("skipped never compares", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("SUGA_HOME", home)
		t.Setenv("SUGA_CONFIG", "")
		t.Cleanup(appconfig.ResetProtectedStateForTest)
		state := protectedCheck(Options{SkipProtectedCheck: true})
		assert.True(t, state == nil)
		// compact must not create the snapshot of a process that runs no session.
		_, err := os.Stat(filepath.Join(home, "run", "protected-config"))
		assert.True(t, os.IsNotExist(err), "skipped check created %v", err)
	})

	t.Run("a configuration this process never loaded has nothing to report", func(t *testing.T) {
		appconfig.ResetLoadedStateForTest()
		t.Cleanup(func() {
			appconfig.ResetLoadedStateForTest()
			appconfig.ResetProtectedStateForTest()
		})
		state := protectedCheck(Options{})
		assert.Require(t, assert.NotNil(t, state))
		assert.True(t, state.Change.Empty())
		assert.Eq(t, true, protectedNoticeCallback(nil) == nil)
		callback := protectedNoticeCallback(state)
		_, _, _, ok := callback("s1")
		assert.False(t, ok)
	})
}

// TestProtectedNoticeCallback: the personal runner only records what the config
// state reports, and a session is told once.
func TestProtectedNoticeCallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_CONFIG", "")
	configPath := filepath.Join(home, "config.yaml")
	assert.NoErr(t, os.WriteFile(configPath, []byte("tools:\n  read_roots: [a]\n"), 0o600))
	// The snapshot the user last confirmed holds another value of a protected key.
	snapshotDir := filepath.Join(home, "run", "protected-config")
	assert.NoErr(t, os.MkdirAll(snapshotDir, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(snapshotDir, "default.json"),
		[]byte(`{"version":1,"mode":"keys","keys":{"tools.read_roots":"other"},"file":""}`), 0o600))
	appconfig.ResetProtectedStateForTest()
	appconfig.ResetLoadedStateForTest()
	t.Cleanup(func() {
		appconfig.ResetProtectedStateForTest()
		appconfig.ResetLoadedStateForTest()
	})
	assert.NoErr(t, appconfig.Load())

	state := protectedCheck(Options{})
	assert.Require(t, assert.NotNil(t, state))
	assert.Eq(t, []string{"tools.read_roots"}, state.Change.Keys)

	callback := protectedNoticeCallback(state)
	keys, wholeFile, unreadable, ok := callback("s1")
	assert.True(t, ok)
	assert.Eq(t, []string{"tools.read_roots"}, keys)
	assert.False(t, wholeFile)
	assert.False(t, unreadable)
	// The same session asks again on its next prompt (ACP opens a runtime per
	// request) and gets nothing.
	_, _, _, ok = callback("s1")
	assert.False(t, ok)
	_, _, _, ok = callback("s2")
	assert.True(t, ok)
}
