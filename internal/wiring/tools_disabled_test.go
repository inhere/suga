package wiring

import (
	"slices"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// disabledToolsConfig sets tools.disabled for one subtest; resetConfig clears it
// again with the rest of the configuration.
func disabledToolsConfig(t *testing.T, names ...string) {
	t.Helper()
	assert.NoErr(t, gkconfig.Set("tools.disabled", names))
}

// TestDisabledToolsMainRegistry: a tool listed in tools.disabled is not in the
// main session's registry (so it is absent from the tool list and tool_search,
// not merely refused when called), an empty list changes nothing, and an unknown
// name fails the startup with the tools that do exist.
func TestDisabledToolsMainRegistry(t *testing.T) {
	workspace := t.TempDir()

	t.Run("a listed tool is missing", func(t *testing.T) {
		resetConfig(t)
		disabledToolsConfig(t, "web_fetch", "web_search")
		caps, err := buildPersonalCapabilities(workspace, nil, true)
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = caps.Close() })
		for _, name := range []string{"web_fetch", "web_search"} {
			_, err := caps.Registry.Get(name)
			assert.Err(t, err)
			assert.False(t, slices.Contains(caps.Registry.List(), name), name)
			assert.False(t, hasSchema(caps.Registry.Schemas(), name), name)
		}
		_, err = caps.Registry.Get("file_read")
		assert.NoErr(t, err)
	})

	t.Run("an empty list keeps every tool", func(t *testing.T) {
		resetConfig(t)
		caps, err := buildPersonalCapabilities(workspace, nil, true)
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = caps.Close() })
		before := caps.Registry.List()
		slices.Sort(before)
		disabledToolsConfig(t)
		caps2, err := buildPersonalCapabilities(workspace, nil, true)
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = caps2.Close() })
		after := caps2.Registry.List()
		slices.Sort(after)
		assert.Eq(t, before, after)
	})

	// The alias of file_read's picture branch is a legal name and disabling it only
	// turns off the alias: pictures stay readable through file_read (design Q7).
	t.Run("view_image is still a legal name and only turns off the alias", func(t *testing.T) {
		resetConfig(t)
		disabledToolsConfig(t, "view_image")
		caps, err := buildPersonalCapabilities(workspace, nil, true)
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = caps.Close() })
		_, err = caps.Registry.Get("view_image")
		assert.Err(t, err)
		_, err = caps.Registry.Get("file_read")
		assert.NoErr(t, err)
	})

	t.Run("an unknown name fails with the available names", func(t *testing.T) {
		resetConfig(t)
		disabledToolsConfig(t, "no_such_tool")
		_, err := buildPersonalCapabilities(workspace, nil, true)
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "unknown tool no_such_tool")
		assert.StrContains(t, err.Error(), "web_fetch")
		assert.StrContains(t, err.Error(), "file_read")
	})
}

// TestDisabledToolsSubAgentRegistry: the read-only sub-agent tools go through
// the same policy, so a disabled tool is missing from the child's registry.
func TestDisabledToolsSubAgentRegistry(t *testing.T) {
	workspace := t.TempDir()

	t.Run("web_fetch is missing from explore", func(t *testing.T) {
		resetConfig(t)
		disabledToolsConfig(t, "web_fetch")
		reg, err := (&subAgents{workspace: workspace}).toolRegistry(native.AgentExplore)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, slices.Contains(reg.List(), "web_fetch"))
		assert.True(t, slices.Contains(reg.List(), "file_read"))
	})

	t.Run("the list keeps them when empty", func(t *testing.T) {
		resetConfig(t)
		reg, err := (&subAgents{workspace: workspace}).toolRegistry(native.AgentResearch)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, slices.Contains(reg.List(), "web_fetch"))
	})
}

// TestDisabledToolsConfigValidation: a malformed tools.disabled value is an
// error, and the names are trimmed and de-duplicated in configured order.
func TestDisabledToolsConfigValidation(t *testing.T) {
	t.Run("names are trimmed and de-duplicated", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.disabled", []string{" web_fetch ", "", "web_fetch", "web_search"}))
		names, err := appconfig.DisabledToolNames()
		assert.NoErr(t, err)
		assert.Eq(t, []string{"web_fetch", "web_search"}, names)
	})

	t.Run("a non-list value is an error", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.disabled", 42))
		_, err := appconfig.DisabledToolNames()
		assert.Err(t, err)
	})

	t.Run("a single name is accepted", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.disabled", "web_fetch"))
		names, err := appconfig.DisabledToolNames()
		assert.NoErr(t, err)
		assert.Eq(t, []string{"web_fetch"}, names)
	})
}

// TestDisabledToolsManifest: `suga inspect manifest` must not list a disabled
// tool, because the model never sees it.
func TestDisabledToolsManifest(t *testing.T) {
	workspace := manifestConfig(t)
	disabledToolsConfig(t, "web_fetch", "web_search")

	m, err := LoadManifest(ManifestOptions{Workspace: workspace})
	assert.Require(t, assert.NoErr(t, err))
	for _, name := range []string{"web_fetch", "web_search"} {
		if _, ok := toolByName(m, name); ok {
			t.Fatalf("manifest still lists %s", name)
		}
	}
	if _, ok := toolByName(m, "file_read"); !ok {
		t.Fatal("manifest lost file_read")
	}
}

// hasSchema reports whether name is one of the tool schemas offered to the model.
func hasSchema(schemas []api.ToolSchema, name string) bool {
	for _, s := range schemas {
		if s.Name == name {
			return true
		}
	}
	return false
}
