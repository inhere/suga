package wiring

import (
	"context"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

// TestCompactKnobsReachTheRunner: the three compaction knobs set in the config
// (context.pin_first_user, context.keep_user_words_budget,
// context.summary_max_tokens) take effect through the assembled runtime: the
// first request rides in the package, the user-words budget decides how many
// requests it carries, and the cap lands on the summary request.
func TestCompactKnobsReachTheRunner(t *testing.T) {
	prompts := []string{
		"first task: port the order parser to the new API",
		"second request: add tests for the order parser",
		"third request: fix the failing parser test",
		"fourth request: update the parser docs",
		"fifth request: run the full test suite",
	}
	run := func(t *testing.T, set map[string]any) (summary string, bodies []string) {
		t.Helper()
		workspace := manifestConfig(t)
		srv, got := scriptedChildServer(t, nil)
		setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k", "base_url": srv.URL}})
		for k, v := range set {
			assert.NoErr(t, gkconfig.Set(k, v))
		}
		rt, err := Open(context.Background(), Options{Workspace: workspace, RuleCWD: workspace})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		var session string
		for _, p := range prompts {
			res, err := rt.Runner.Run(context.Background(), personal.RunRequest{SessionID: session, CreateIfMissing: true, Prompt: p})
			assert.Require(t, assert.NoErr(t, err))
			session = res.SessionID
		}
		_, err = rt.Runner.Compact(context.Background(), session)
		assert.Require(t, assert.NoErr(t, err))
		es, err := rt.repo.ListEntries(context.Background(), session, store.EntryFilter{Types: []string{store.EntryCompaction}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.True(t, len(es) > 0))
		return string(es[len(es)-1].JSON), *got
	}

	t.Run("defaults keep the three latest and set no cap", func(t *testing.T) {
		summary, bodies := run(t, nil)
		assert.NotContains(t, summary, "first task: port")
		assert.NotContains(t, summary, "second request")
		assert.StrContains(t, summary, "third request")
		last := bodies[len(bodies)-1]
		assert.NotContains(t, last, "max_tokens")
		assert.NotContains(t, last, "max_completion_tokens")
	})
	t.Run("pin, budget and cap from the config", func(t *testing.T) {
		summary, bodies := run(t, map[string]any{
			"context.pin_first_user":         true,
			"context.keep_user_words_budget": 80,
			"context.summary_max_tokens":     3000,
		})
		assert.StrContains(t, summary, "first task: port")
		assert.StrContains(t, summary, "fifth request")
		assert.StrContains(t, summary, "fourth request")
		assert.NotContains(t, summary, "third request")
		last := bodies[len(bodies)-1]
		assert.True(t, strings.Contains(last, `"max_tokens":3000`) || strings.Contains(last, `"max_completion_tokens":3000`), last)
	})
	t.Run("an invalid value fails the assembly", func(t *testing.T) {
		workspace := manifestConfig(t)
		assert.NoErr(t, gkconfig.Set("context.keep_user_words_budget", -1))
		_, err := Open(context.Background(), Options{Workspace: workspace, RuleCWD: workspace})
		assert.ErrMsgContains(t, err, "context.keep_user_words_budget")
	})
}
