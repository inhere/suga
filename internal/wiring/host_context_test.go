package wiring

import (
	"context"
	"encoding/json"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
)

// segmentStartHostContext is the host context frozen in a session's segment
// start record ("" when the record has none).
type segmentStartHostContext struct {
	Context struct {
		HostContext     string `json:"host_context"`
		HostContextHash string `json:"host_context_hash"`
	} `json:"context"`
}

func segmentStartOf(t *testing.T, rt *Runtime, sessionID string) segmentStartHostContext {
	t.Helper()
	entries, err := rt.repo.ListEntries(context.Background(), sessionID, store.EntryFilter{Types: []string{store.EntryCompaction}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, entries, 1))
	var start segmentStartHostContext
	assert.NoErr(t, json.Unmarshal(entries[0].JSON, &start))
	return start
}

// TestOpenHostContext: Options.HostContext reaches the runner, so the first
// segment of a new session freezes the host's text and the resident report
// counts it. Without the option the record and the report stay empty.
func TestOpenHostContext(t *testing.T) {
	hostText := "人物设定：XH 是一位向导。\n主题索引：- A"
	open := func(t *testing.T, host func() (string, string)) *Runtime {
		t.Helper()
		setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
		assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
		rt, err := Open(context.Background(), Options{
			Workspace: t.TempDir(), RuleCWD: t.TempDir(), AllowWrite: false, HostContext: host,
		})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		return rt
	}

	t.Run("frozen into the first segment and measured", func(t *testing.T) {
		resetConfig(t)
		rt := open(t, func() (string, string) { return hostText, "h1" })
		_, err := rt.Runner.NewSession(context.Background(), "host-ctx", "t")
		assert.Require(t, assert.NoErr(t, err))
		start := segmentStartOf(t, rt, "host-ctx")
		assert.Eq(t, hostText, start.Context.HostContext)
		assert.Eq(t, "h1", start.Context.HostContextHash)

		report := rt.Resident(context.Background())
		assert.Eq(t, len(hostText), report.Context.HostContext.Bytes)
	})

	t.Run("no host context keeps both empty", func(t *testing.T) {
		resetConfig(t)
		rt := open(t, nil)
		_, err := rt.Runner.NewSession(context.Background(), "no-host-ctx", "t")
		assert.Require(t, assert.NoErr(t, err))
		start := segmentStartOf(t, rt, "no-host-ctx")
		assert.Eq(t, "", start.Context.HostContext)
		report := rt.Resident(context.Background())
		assert.Eq(t, 0, report.Context.HostContext.Bytes)
	})
}
