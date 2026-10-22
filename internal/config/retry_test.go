package axconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/llm"
)

func TestReadRetryPolicy(t *testing.T) {
	load := func(t *testing.T, body string) {
		t.Helper()
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		root := t.TempDir()
		t.Setenv("SUGA_HOME", root)
		t.Setenv("SUGA_CONFIG", "")
		t.Chdir(t.TempDir())
		assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0600))
		assert.NoErr(t, Load())
	}
	t.Run("defaults", func(t *testing.T) {
		load(t, "model: openai/m\n")
		got, err := ReadRetryPolicy()
		assert.NoErr(t, err)
		assert.Eq(t, llm.DefaultRetryPolicy, got)
	})
	t.Run("set fields, the rest default", func(t *testing.T) {
		load(t, "model: openai/m\nretry:\n  max_attempts: 3\n  base_delay: 500ms\n  max_delay: 20\n  max_elapsed: 90s\n")
		got, err := ReadRetryPolicy()
		assert.NoErr(t, err)
		assert.Eq(t, 3, got.MaxAttempts)
		assert.Eq(t, 500*time.Millisecond, got.BaseDelay)
		assert.Eq(t, 20*time.Second, got.MaxDelay)
		assert.Eq(t, 90*time.Second, got.MaxElapsed)
		assert.Eq(t, llm.DefaultRetryPolicy.ResetAfter, got.ResetAfter)
	})
	t.Run("max_elapsed off removes the budget", func(t *testing.T) {
		load(t, "model: openai/m\nretry:\n  max_elapsed: off\n")
		got, err := ReadRetryPolicy()
		assert.NoErr(t, err)
		assert.Eq(t, llm.NoTimeout, got.MaxElapsed)
	})
	t.Run("a provider overrides max_elapsed", func(t *testing.T) {
		load(t, "model: openai/m\nretry:\n  max_elapsed: 2m\nproviders:\n  gw:\n    type: openai\n    base_url: http://x\n    retry:\n      max_elapsed: 30s\n")
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, 30*time.Second, ps["gw"].Retry.MaxElapsed)
		assert.Eq(t, 2*time.Minute, ps["openai"].Retry.MaxElapsed)
	})
	t.Run("every provider gets it", func(t *testing.T) {
		load(t, "model: openai/m\nretry:\n  max_attempts: 1\nproviders:\n  gw:\n    type: anthropic\n    base_url: http://x\n")
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, 1, ps["gw"].Retry.MaxAttempts)
		assert.Eq(t, 1, ps["openai"].Retry.MaxAttempts)
	})
	t.Run("a provider overrides the fields it names", func(t *testing.T) {
		load(t, "model: openai/m\nretry:\n  max_attempts: 5\n  base_delay: 2s\n"+
			"providers:\n  gw:\n    type: anthropic\n    base_url: http://x\n    retry:\n      max_attempts: 2\n      base_delay: 500ms\n      max_delay: 30\n")
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, 2, ps["gw"].Retry.MaxAttempts)
		assert.Eq(t, 500*time.Millisecond, ps["gw"].Retry.BaseDelay)
		assert.Eq(t, 30*time.Second, ps["gw"].Retry.MaxDelay)
		assert.Eq(t, llm.DefaultRetryPolicy.ResetAfter, ps["gw"].Retry.ResetAfter) // untouched
		// Everything else keeps the global policy, presets included.
		assert.Eq(t, 5, ps["openai"].Retry.MaxAttempts)
		assert.Eq(t, 2*time.Second, ps["openai"].Retry.BaseDelay)
		assert.Eq(t, 5, ps["deepseek"].Retry.MaxAttempts)
		assert.Eq(t, 2*time.Second, ps["deepseek"].Retry.BaseDelay)
	})
	for _, tc := range []struct{ name, body, want string }{
		{"zero attempts", "retry:\n  max_attempts: 0\n", "retry.max_attempts must be a whole number >= 1"},
		{"bad duration", "retry:\n  base_delay: soon\n", "retry.base_delay"},
		{"zero delay", "retry:\n  max_delay: 0\n", "retry.max_delay: must be above 0"},
		{"max below base", "retry:\n  base_delay: 10s\n  max_delay: 5s\n", "below retry.base_delay"},
		{"bad max_elapsed", "retry:\n  max_elapsed: soon\n", "retry.max_elapsed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			load(t, "model: openai/m\n"+tc.body)
			_, err := ReadRetryPolicy()
			assert.ErrMsgContains(t, err, tc.want)
			_, err = ReadProviders()
			assert.ErrMsgContains(t, err, tc.want)
		})
	}
	// The same fields are checked on a provider section, which names itself in
	// the error so a long config is easy to fix.
	for _, tc := range []struct {
		name, retry, want string
	}{
		{"zero attempts", "max_attempts: 0", "providers.gw.retry.max_attempts must be a whole number >= 1"},
		{"bad duration", "base_delay: soon", "providers.gw.retry.base_delay"},
		{"max below base", "base_delay: 10s\n      max_delay: 5s", "below providers.gw.retry.base_delay"},
	} {
		t.Run("provider section: "+tc.name, func(t *testing.T) {
			load(t, "model: openai/m\nproviders:\n  gw:\n    type: openai\n    base_url: http://x\n    retry:\n      "+tc.retry+"\n")
			_, err := ReadProviders()
			assert.ErrMsgContains(t, err, tc.want)
		})
	}
}
