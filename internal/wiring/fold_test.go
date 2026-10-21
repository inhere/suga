package wiring

import (
	"context"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestRunnerFoldSettings checks that the runner gets the folding defaults
// (no step cadence, size trigger at half the threshold, keep 5) and the
// configured values.
func TestRunnerFoldSettings(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	fold := func(t *testing.T) (int, int, float64) {
		t.Helper()
		rt, err := OpenCLI(context.Background(), Options{})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		return rt.Runner.Fold()
	}

	every, keep, at := fold(t)
	assert.Eq(t, 0, every)
	assert.Eq(t, 5, keep)
	assert.Eq(t, 0.5, at)

	assert.NoErr(t, gkconfig.Set("context.fold_every", 10))
	every, _, at = fold(t)
	assert.Eq(t, 10, every)
	assert.Eq(t, 0.5, at)

	assert.NoErr(t, gkconfig.Set("context.fold_at", 0))
	_, _, at = fold(t)
	assert.Eq(t, 0.0, at)
}

// TestRunnerFoldOffForAnthropic: folding rewrites earlier messages, which
// would invalidate Claude's signed thinking, so it is off for such models.
func TestRunnerFoldOffForAnthropic(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"anthropic": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "anthropic/claude-opus-5-5"))
	assert.NoErr(t, gkconfig.Set("context.fold_every", 10)) // even when turned on
	rt, err := OpenCLI(context.Background(), Options{})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	every, _, at := rt.Runner.Fold()
	assert.Eq(t, 0, every)
	assert.Eq(t, 0.0, at) // the size trigger is off too
}
