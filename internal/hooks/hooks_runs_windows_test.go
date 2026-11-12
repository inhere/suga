//go:build windows

package hooks

import (
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestHookRunsProgramFromWindowsPath checks that the program of a run is the base
// name of argv[0] for a backslash path too (filepath.Base splits the Windows
// separator, so the trace shows "hook.cmd" rather than the whole path).
func TestHookRunsProgramFromWindowsPath(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "")
	assert.NoErr(t, gkconfig.Set("hooks.command", []string{`C:\tools\hooks\review.cmd`, "--flag"}))
	client, err := New()
	assert.Require(t, assert.NoErr(t, err))
	compat, ok := client.(*commandClient)
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, "review.cmd", compat.program)
}
