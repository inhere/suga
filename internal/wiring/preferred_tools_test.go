package wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/tools"
	toolsmcp "github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

func TestResolvePreferredTools(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, gkconfig.Set("tools.preferred", []map[string]any{
		{"name": "go", "kind": "command", "when": "run Go"},
		{"name": "missing-preferred-command", "kind": "command", "when": "never"},
		{"name": "connected", "kind": "mcp", "when": "graph"},
		{"name": "offline", "kind": "mcp", "when": "other"},
		{"name": "unconfigured", "kind": "mcp", "when": "none"},
	}))

	registry := tools.NewRegistry()
	registry.Register(newServerStubTool("connected", "graph_search"))
	got, err := ResolvePreferredTools(registry, []toolsmcp.ServerSettings{
		{ID: "connected", Command: "server"},
		{ID: "offline", Command: "server"},
	})
	assert.NoErr(t, err)
	assert.Eq(t, []PreferredToolStatus{
		{Name: "go", Kind: "command", When: "run Go", Present: true, Reason: preferredReasonFoundPath},
		{Name: "missing-preferred-command", Kind: "command", When: "never", Reason: preferredReasonMissingPath},
		{Name: "connected", Kind: "mcp", When: "graph", Present: true, Reason: preferredReasonMCPConnected},
		{Name: "offline", Kind: "mcp", When: "other", Reason: preferredReasonMCPUnavailable},
		{Name: "unconfigured", Kind: "mcp", When: "none", Reason: preferredReasonMCPNotConfigured},
	}, got)

	preferred, readErr := appconfig.ReadPreferredTools()
	assert.NoError(t, readErr)
	assert.Eq(t, 5, len(preferred))
}

// TestBuildManifestPreferredTools: the manifest keeps every declaration (also
// the unavailable ones) with kind, scenario and a discovery reason, in
// configuration order, without carrying server arguments.
func TestBuildManifestPreferredTools(t *testing.T) {
	m := BuildManifest(ManifestInput{Preferred: []PreferredToolStatus{
		{Name: "rg", Kind: "command", When: "search", Present: true, Reason: preferredReasonFoundPath},
		{Name: "gone", Kind: "command", When: "never", Reason: preferredReasonMissingPath},
	}})
	assert.Eq(t, []ManifestPreferred{
		{Name: "rg", Kind: "command", When: "search", Present: true, Reason: preferredReasonFoundPath},
		{Name: "gone", Kind: "command", When: "never", Reason: preferredReasonMissingPath},
	}, m.Preferred)

	t.Run("no declaration is an empty list", func(t *testing.T) {
		empty := BuildManifest(ManifestInput{})
		assert.Eq(t, []ManifestPreferred{}, empty.Preferred)
		raw, err := json.Marshal(empty)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, string(raw), `"preferred_tools":[]`)
	})
}

// TestLoadManifestPreferredTools: the manifest command resolves PATH itself and
// never starts an MCP server: a configured server is reported as configured.
func TestLoadManifestPreferredTools(t *testing.T) {
	manifestConfig(t)
	dir, command := writePreferredCommandFixture(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	assert.NoErr(t, gkconfig.Set("tools.preferred", []map[string]any{
		{"name": command, "kind": "command", "when": "fixture search"},
		{"name": "suga-missing-preferred-command", "kind": "command", "when": "never"},
		{"name": "srv", "kind": "mcp", "when": "graph"},
		{"name": "unconfigured", "kind": "mcp", "when": "none"},
	}))
	m, err := LoadManifest(ManifestOptions{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, []ManifestPreferred{
		{Name: command, Kind: "command", When: "fixture search", Present: true, Reason: preferredReasonFoundPath},
		{Name: "suga-missing-preferred-command", Kind: "command", When: "never", Reason: preferredReasonMissingPath},
		{Name: "srv", Kind: "mcp", When: "graph", Reason: preferredReasonMCPConfigured},
		{Name: "unconfigured", Kind: "mcp", When: "none", Reason: preferredReasonMCPNotConfigured},
	}, m.Preferred)
	// The declared server's arguments (manifestConfig sets --token secret) stay
	// out of the preferred entries.
	raw, err := json.Marshal(m)
	assert.Require(t, assert.NoErr(t, err))
	assert.NotContains(t, string(raw), "secret")

	t.Run("a malformed declaration fails the manifest", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.preferred", []map[string]any{{"name": "rg", "kind": "binary", "when": "x"}}))
		t.Cleanup(func() { _ = gkconfig.Set("tools.preferred", []map[string]any{}) })
		_, err := LoadManifest(ManifestOptions{})
		assert.Err(t, err)
	})
}

// writePreferredCommandFixture puts an executable-looking file on a temporary
// PATH. LookPath only stats it, so the content never runs; the name follows the
// platform (Windows resolves .exe through PATHEXT).
func writePreferredCommandFixture(t *testing.T) (dir, name string) {
	t.Helper()
	dir = t.TempDir()
	name = "suga-preferred-probe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return dir, name
}

func TestRefreshPreferredCommands(t *testing.T) {
	got := refreshPreferredCommands([]PreferredToolStatus{
		{Name: "go", Kind: "command", Present: false, Reason: preferredReasonMissingPath},
		{Name: "missing-preferred-command", Kind: "command", Present: true, Reason: preferredReasonFoundPath},
		{Name: "server", Kind: "mcp", Present: true, Reason: preferredReasonMCPConnected},
	})
	assert.True(t, got[0].Present)
	assert.Eq(t, preferredReasonFoundPath, got[0].Reason)
	assert.False(t, got[1].Present)
	assert.Eq(t, preferredReasonMissingPath, got[1].Reason)
	assert.Eq(t, PreferredToolStatus{Name: "server", Kind: "mcp", Present: true, Reason: preferredReasonMCPConnected}, got[2])
}

func TestPreferredMCPToolNames(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(newServerStubTool("graph", "z_search"))
	registry.Register(newServerStubTool("graph", "a_lookup"))
	registry.Register(newServerStubTool("other", "other_tool"))
	got := preferredMCPToolNames(registry, []PreferredToolStatus{
		{Name: "graph", Kind: "mcp", Present: true},
		{Name: "other", Kind: "mcp", Present: false},
	})
	assert.Eq(t, []string{"a_lookup", "z_search"}, got)
}

func TestResolvePreferredToolsDoesNotExposeServerDetails(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoError(t, gkconfig.Set("tools.preferred", []map[string]any{{"name": "offline", "kind": "mcp", "when": "graph"}}))
	got, err := ResolvePreferredTools(nil, []toolsmcp.ServerSettings{{ID: "offline", Command: "secret-server", Args: []string{"--token", "secret"}, Env: map[string]string{"TOKEN": "secret"}}})
	assert.NoError(t, err)
	assert.Eq(t, false, got[0].Present)
	assert.NotContains(t, got[0].Reason, "secret")
	assert.NotContains(t, got[0].Reason, "token")
}

// TestResolvePreferredToolsWithoutRuntime: a nil registry means no runtime to
// observe (the standalone manifest), which never starts MCP: a configured
// server is reported as configured, not as failed.
func TestResolvePreferredToolsWithoutRuntime(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoError(t, gkconfig.Set("tools.preferred", []map[string]any{
		{"name": "srv", "kind": "mcp", "when": "graph"},
		{"name": "gone", "kind": "mcp", "when": "none"},
	}))
	got, err := ResolvePreferredTools(nil, []toolsmcp.ServerSettings{{ID: "srv", Command: "server"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, []PreferredToolStatus{
		{Name: "srv", Kind: "mcp", When: "graph", Reason: preferredReasonMCPConfigured},
		{Name: "gone", Kind: "mcp", When: "none", Reason: preferredReasonMCPNotConfigured},
	}, got)
}

// TestListedPreferredToolNames: the environment section (and the turn_context
// event) list the available declarations only, in configuration order; a
// command additionally needs the session's shell_exec, so a read-only session
// leaves it out while a connected MCP server stays listed.
func TestListedPreferredToolNames(t *testing.T) {
	statuses := []PreferredToolStatus{
		{Name: "rg", Kind: "command", Present: true},
		{Name: "missing", Kind: "command"},
		{Name: "graph", Kind: "mcp", Present: true},
	}
	assert.Eq(t, []string{"rg", "graph"}, listedPreferredToolNames(statuses, true))
	assert.Eq(t, 0, len(listedPreferredToolNames(nil, true)))

	t.Run("without shell_exec only MCP declarations are listed", func(t *testing.T) {
		assert.Eq(t, []string{"graph"}, listedPreferredToolNames(statuses, false))
		assert.Eq(t, 0, len(listedPreferredToolNames([]PreferredToolStatus{
			{Name: "rg", Kind: "command", Present: true},
		}, false)))
	})
}

// TestPreferredEnvironmentSource: the runtime environment source renders the
// available declarations and records the same names for turn_context, so the
// prompt and the trace cannot drift apart; in a read-only session it records
// the MCP declarations only, exactly like the text it renders.
func TestPreferredEnvironmentSource(t *testing.T) {
	resetConfig(t)
	statuses := []PreferredToolStatus{
		{Name: "rg", Kind: "command", When: "search text", Present: true, Reason: preferredReasonFoundPath},
		{Name: "gone", Kind: "command", When: "never", Reason: preferredReasonMissingPath},
	}
	listed := &listedPreferredNames{}
	env := newPreferredEnvironmentSource(Options{Workspace: "/w", AllowWrite: true}, listed)(statuses)
	assert.StrContains(t, env, "Preferred tool: rg (command) — search text")
	assert.NotContains(t, env, "gone")
	assert.Eq(t, []string{"rg"}, listed.get())

	t.Run("a refreshed snapshot replaces the recorded names", func(t *testing.T) {
		refreshed := refreshPreferredCommands([]PreferredToolStatus{
			{Name: "go", Kind: "command", When: "build", Reason: preferredReasonMissingPath},
		})
		env := newPreferredEnvironmentSource(Options{Workspace: "/w", AllowWrite: true}, listed)(refreshed)
		assert.StrContains(t, env, "Preferred tool: go (command) — build")
		assert.Eq(t, []string{"go"}, listed.get())
	})

	t.Run("a read-only session lists MCP declarations only", func(t *testing.T) {
		readOnly := &listedPreferredNames{}
		env := newPreferredEnvironmentSource(Options{Workspace: "/w"}, readOnly)([]PreferredToolStatus{
			{Name: "rg", Kind: "command", When: "search text", Present: true, Reason: preferredReasonFoundPath},
			{Name: "graph", Kind: "mcp", When: "code graph", Present: true, Reason: preferredReasonMCPConnected},
		})
		assert.NotContains(t, env, "Preferred tool: rg")
		assert.StrContains(t, env, "Preferred tool: graph (mcp) — code graph")
		assert.Eq(t, []string{"graph"}, readOnly.get())
	})
}

// TestListedPreferredNamesKeepsACopy: the shared holder is read by turn_context
// while the environment sources keep writing it, so a reader must not alias the
// stored slice.
func TestListedPreferredNamesKeepsACopy(t *testing.T) {
	var holder listedPreferredNames
	source := []string{"rg"}
	holder.set(source)
	source[0] = "changed"
	got := holder.get()
	assert.Eq(t, []string{"rg"}, got)
	got[0] = "again"
	assert.Eq(t, []string{"rg"}, holder.get())
}

// TestResolvePreferredToolsHTTPMCPServer: an HTTP MCP server (url configured
// when no id) is an ordinary MCP server for tools.preferred, so a successful
// registration pins it exactly like a stdio server.
func TestResolvePreferredToolsHTTPMCPServer(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, gkconfig.Set("tools.preferred", []map[string]any{
		{"name": "remote", "kind": "mcp", "when": "http tools"},
	}))

	registry := tools.NewRegistry()
	registry.Register(newServerStubTool("remote", "remote_search"))
	got, err := ResolvePreferredTools(registry, []toolsmcp.ServerSettings{
		{ID: "remote", URL: "https://mcp.example.test/mcp", Transport: toolsmcp.TransportStreamableHTTP},
	})
	assert.NoErr(t, err)
	assert.Eq(t, []PreferredToolStatus{
		{Name: "remote", Kind: "mcp", When: "http tools", Present: true, Reason: preferredReasonMCPConnected},
	}, got)
}
