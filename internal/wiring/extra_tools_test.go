package wiring

import (
	"context"
	"slices"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// hostStubTool is a host's in-process tool (Options.ExtraTools): it counts its
// calls, so a test can tell that the main session really ran it.
type hostStubTool struct {
	name string
	meta tools.Meta
	ran  int
}

func (t *hostStubTool) Name() string        { return t.name }
func (t *hostStubTool) Description() string { return "host tool " + t.name }
func (t *hostStubTool) ToolMeta() tools.Meta {
	return t.meta
}

func (t *hostStubTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.name,
		Description: t.Description(),
		// Host tools follow the same rule as the built-in ones: properties must
		// not be empty (some gateways reject an empty object).
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
	}
}

func (t *hostStubTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	t.ran++
	return &api.ToolResult{CallID: call.CallID, Name: t.name, Content: "host-ok:" + t.name}, nil
}

// openWithExtraTools opens a runtime with the given host tools the way a host
// (e.g. xhen) does: one provider from the configuration, a fresh workspace.
func openWithExtraTools(t *testing.T, extra ...api.Tool) (*Runtime, error) {
	t.Helper()
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	allowAll := tools.ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return nil })
	return Open(context.Background(), Options{
		Workspace: t.TempDir(), RuleCWD: t.TempDir(), Approver: allowAll, ExtraTools: extra,
	})
}

// TestExtraToolsMainSession: a host tool reaches the main session's registry,
// answers through the runtime's tool stack (approval and visibility layers
// included), and shows up in the manifest's tool list.
func TestExtraToolsMainSession(t *testing.T) {
	t.Run("registered, callable and in the manifest", func(t *testing.T) {
		resetConfig(t)
		host := &hostStubTool{name: "host_ping", meta: tools.Meta{Category: tools.CategoryRead, ReadOnly: true}}
		rt, err := openWithExtraTools(t, host)
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })

		// The base registry (manifest, tool_search) knows it...
		_, err = rt.Registry.Get("host_ping")
		assert.NoErr(t, err)
		// ...and the stack the runner calls really runs it.
		res, err := rt.tools.ExecuteTool(context.Background(), api.ToolCall{CallID: "c1", Name: "host_ping"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "host-ok:host_ping", res.Content)
		assert.Eq(t, 1, host.ran)

		m := BuildManifest(ManifestInput{Registry: rt.Registry})
		found := false
		for _, tool := range m.Tools {
			if tool.Name == "host_ping" {
				found = true
			}
		}
		assert.True(t, found)
	})

	t.Run("a name a built-in tool owns is an error", func(t *testing.T) {
		resetConfig(t)
		_, err := openWithExtraTools(t, &hostStubTool{name: "file_read"})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), `extra tool "file_read"`)
		assert.StrContains(t, err.Error(), "already has that name")
	})

	t.Run("a runtime tool name is refused before the build", func(t *testing.T) {
		resetConfig(t)
		_, err := openWithExtraTools(t, &hostStubTool{name: "update_plan"})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "reserved by the runtime")
	})

	t.Run("two host tools with one name are an error", func(t *testing.T) {
		resetConfig(t)
		_, err := openWithExtraTools(t, &hostStubTool{name: "host_dup"}, &hostStubTool{name: "host_dup"})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "duplicate name")
	})

	t.Run("an empty name is an error", func(t *testing.T) {
		resetConfig(t)
		_, err := openWithExtraTools(t, &hostStubTool{name: " "})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "empty name")
	})
}

// TestExtraToolsDisabled: tools.disabled removes a host tool like any other, so
// the model never sees (and cannot call) it.
func TestExtraToolsDisabled(t *testing.T) {
	resetConfig(t)
	disabledToolsConfig(t, "host_ping")
	host := &hostStubTool{name: "host_ping", meta: tools.Meta{Category: tools.CategoryRead, ReadOnly: true}}
	rt, err := openWithExtraTools(t, host)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	_, err = rt.Registry.Get("host_ping")
	assert.Err(t, err)
	assert.False(t, slices.Contains(rt.Registry.List(), "host_ping"))
}

// TestExtraToolsReadOnlyMode: host tools are part of the main session in a
// read-only runtime too (the host decides what it injects).
func TestExtraToolsReadOnlyMode(t *testing.T) {
	resetConfig(t)
	host := &hostStubTool{name: "host_ping", meta: tools.Meta{Category: tools.CategoryRead, ReadOnly: true}}
	caps, err := buildPersonalCapabilitiesWithExtras(context.Background(), t.TempDir(), nil, false, nil, nil, []api.Tool{host})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = caps.Close() })
	res, err := caps.Registry.ExecuteTool(context.Background(), api.ToolCall{CallID: "c", Name: "host_ping"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "host-ok:host_ping", res.Content)
}

// TestExtraToolsCLIBuilder: the CLI entry point registers host tools too (run
// and chat go through it).
func TestExtraToolsCLIBuilder(t *testing.T) {
	resetConfig(t)
	host := &hostStubTool{name: "host_ping", meta: tools.Meta{Category: tools.CategoryRead, ReadOnly: true}}
	caps, err := buildPersonalCLICapabilitiesWithExtras(context.Background(), t.TempDir(), nil, true, nil, nil, []api.Tool{host})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = caps.Close() })
	res, err := caps.Registry.ExecuteTool(context.Background(), api.ToolCall{CallID: "c", Name: "host_ping"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "host-ok:host_ping", res.Content)
}

// TestExtraToolsNotInSubAgent: a sub-agent gets its kind's catalog tools only,
// never the host's tools (the host injects them for its own session).
func TestExtraToolsNotInSubAgent(t *testing.T) {
	resetConfig(t)
	for _, kind := range []string{native.AgentExplore, native.AgentResearch} {
		t.Run(kind, func(t *testing.T) {
			reg, err := (&subAgents{workspace: t.TempDir()}).toolRegistry(kind)
			assert.Require(t, assert.NoErr(t, err))
			assert.False(t, slices.Contains(reg.List(), "host_ping"))
			// Only the kind's catalog tools are there (web_search is absent without
			// a search key, so the whitelist is an upper bound).
			assert.True(t, slices.Contains(reg.List(), "file_read"))
			for _, name := range reg.List() {
				assert.True(t, slices.Contains(subAgentTools[kind], name), name)
			}
		})
	}
}
