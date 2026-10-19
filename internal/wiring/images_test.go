package wiring

import (
	"context"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestOpenImageSettings pins the images.* wiring: a non-positive limit is a
// startup error, and images.max_side reaches view_image's default.
func TestOpenImageSettings(t *testing.T) {
	t.Run("a non-positive limit is refused at startup", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("model", "fake/m1"))
		setProviders(t, map[string]any{"fake": map[string]any{"type": "openai", "api_key": "k"}})
		assert.NoErr(t, gkconfig.Set("images", map[string]any{"max_request_count": 0}))
		_, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir()})
		assert.Require(t, assert.Err(t, err))
		assert.Contains(t, err.Error(), "images.max_request_count")
	})
	t.Run("images.max_side is the view_image default", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("model", "fake/m1"))
		setProviders(t, map[string]any{"fake": map[string]any{"type": "openai", "api_key": "k"}})
		assert.NoErr(t, gkconfig.Set("images", map[string]any{"max_side": 640}))
		rt := openTestRuntime(t, "")
		// The base registry the native catalog filled (the stack above it only
		// forwards Get when the layer supports lookup).
		tool, err := rt.capabilities.Registry.Get("view_image")
		assert.Require(t, assert.NoErr(t, err))
		assert.Contains(t, tool.Description(), "640 px")
	})
}
