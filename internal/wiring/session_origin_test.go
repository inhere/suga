package wiring

import (
	"context"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
)

// TestOptionsSessionOrigin: an embedded host declares the origin its sessions
// get through wiring.Options.SessionOrigin (xhen uses OriginXhen). Without one,
// each entry point keeps its own origin.
func TestOptionsSessionOrigin(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	openCLI := func(t *testing.T, opts Options) *Runtime {
		t.Helper()
		rt, err := OpenCLI(context.Background(), opts)
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		return rt
	}

	t.Run("the host origin overrides the entry point default", func(t *testing.T) {
		rt := openCLI(t, Options{SessionOrigin: personal.OriginXhen})
		assert.Eq(t, personal.OriginXhen, rt.Runner.SessionOrigin(personal.OriginRun))
		assert.Eq(t, personal.OriginXhen, rt.Runner.SessionOrigin(personal.OriginChat))
	})
	t.Run("without one the entry point keeps its own", func(t *testing.T) {
		rt := openCLI(t, Options{})
		assert.Eq(t, personal.OriginRun, rt.Runner.SessionOrigin(personal.OriginRun))
		assert.Eq(t, personal.OriginACP, rt.Runner.SessionOrigin(personal.OriginACP))
	})
}
