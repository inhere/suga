package axconfig

import (
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestSessionMessageEnabled: the tool is on by default and off through
// tools.session_message.enabled or the SUGA_SESSION_MESSAGE=off process switch.
func TestSessionMessageEnabled(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.True(t, SessionMessageEnabled())
	t.Run("config off", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.enabled", false)))
		t.Cleanup(func() { _ = gkconfig.Set("tools.session_message.enabled", true) })
		assert.False(t, SessionMessageEnabled())
	})
	t.Run("env off", func(t *testing.T) {
		t.Setenv("SUGA_SESSION_MESSAGE", "off")
		assert.False(t, SessionMessageEnabled())
	})
	t.Run("env on", func(t *testing.T) {
		t.Setenv("SUGA_SESSION_MESSAGE", "on")
		assert.True(t, SessionMessageEnabled())
	})
}

// TestSessionMessageMaxHops: the automatic-reply limit defaults to 3, follows the
// config, and never drops below 1 (the loop guard cannot be switched off by
// accident).
func TestSessionMessageMaxHops(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.Eq(t, DefaultSessionMessageMaxHops, SessionMessageMaxHops())
	t.Run("configured", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.max_hops", 5)))
		t.Cleanup(func() { _ = gkconfig.Set("tools.session_message.max_hops", 3) })
		assert.Eq(t, 5, SessionMessageMaxHops())
	})
	t.Run("below one keeps the default", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.max_hops", 0)))
		t.Cleanup(func() { _ = gkconfig.Set("tools.session_message.max_hops", 3) })
		assert.Eq(t, DefaultSessionMessageMaxHops, SessionMessageMaxHops())
	})
}
