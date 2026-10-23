package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// countingTool is a native test tool counting its executions.
type countingTool struct {
	name  string
	calls *[]string
}

func (c *countingTool) Name() string        { return c.name }
func (c *countingTool) Description() string { return "test tool " + c.name }
func (c *countingTool) Schema() api.ToolSchema {
	return api.ToolSchema{Name: c.name, Description: c.Description(), InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{
			"action": map[string]any{"type": "string"},
			"text":   map[string]any{"type": "string"},
		},
	}}
}
func (c *countingTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	if c.calls != nil {
		*c.calls = append(*c.calls, call.Name+"|"+strings.TrimSpace(toString(call.Arguments["text"])))
	}
	return &api.ToolResult{CallID: call.CallID, Name: c.name, Content: "ok"}, nil
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

// nativeReg builds a registry of two direct native tools and the given deferred
// candidates.
func nativeReg(executed *[]string, candidates ...string) *Registry {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "file_read", result: "r"})
	reg.Register(&mockTool{name: "shell_exec", result: "s"})
	for _, n := range candidates {
		reg.Register(&countingTool{name: n, calls: executed})
	}
	return reg
}

func TestVisibilityNativeSelection(t *testing.T) {
	ctx := context.Background()
	t.Run("configured native names are deferred, other native tools are not", func(t *testing.T) {
		reg := nativeReg(nil, "memory", "skill", "web_fetch")
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAuto,
			NativeTools: []string{"memory", "skill"}, Apply: DeferredApplySegment})
		assert.True(t, v.Active())
		got, err := v.ListTools(ctx)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"file_read", "shell_exec", "web_fetch"}, names(got))
	})
	t.Run("tools.deferred never defers no native tool, bytes unchanged", func(t *testing.T) {
		reg := nativeReg(nil, "memory", "skill")
		want, err := reg.ListTools(ctx)
		assert.NoErr(t, err)
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredNever,
			NativeTools: []string{"memory", "skill"}, Apply: DeferredApplySegment})
		assert.False(t, v.Active())
		got, err := v.ListTools(ctx)
		assert.NoErr(t, err)
		assert.Eq(t, string(mustJSON(want)), string(mustJSON(got)))
		assert.Eq(t, "", v.PromptSection())
	})
	t.Run("no native list keeps only MCP deferral", func(t *testing.T) {
		reg := nativeReg(nil, "memory")
		reg.Register(&fakeMCP{name: "srv_tool", server: "srv", desc: "server tool"})
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAlways})
		assert.True(t, v.Active())
		got, _ := v.ListTools(ctx)
		assert.Eq(t, []string{"file_read", "memory", "shell_exec"}, names(got))
	})
	t.Run("reserved and unregistered names are ignored without a panic", func(t *testing.T) {
		reg := nativeReg(nil, "memory")
		reg.Register(&mockTool{name: "tool_search", result: "s"})
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAuto,
			NativeTools: []string{"tool_search", ToolCallName, "not_registered", "memory"}})
		assert.True(t, v.Active())
		got, _ := v.ListTools(ctx)
		assert.Eq(t, []string{"file_read", "shell_exec", "tool_search"}, names(got))
		assert.True(t, strings.Contains(v.PromptSection(), "- memory —"))
	})
}

func TestVisibilityNativeApplyModes(t *testing.T) {
	ctx := context.Background()
	t.Run("segment keeps the table bytes and folds at the boundary", func(t *testing.T) {
		reg := nativeReg(nil, "memory", "recall")
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAuto,
			NativeTools: []string{"memory", "recall"}, Apply: DeferredApplySegment})
		var events []SearchEvent
		v.Bind(&memStore{}, func(_ context.Context, ev SearchEvent) { events = append(events, ev) })

		before, _ := v.ListTools(ctx)
		res, err := v.Search(ctx, "select:memory", 5)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"memory"}, res.NewlyLoaded)
		inside, _ := v.ListTools(ctx)
		assert.Eq(t, string(mustJSON(before)), string(mustJSON(inside)))
		assert.Require(t, assert.Eq(t, 1, len(events)))
		assert.False(t, events[0].CacheInvalidated)

		v.FoldLoaded(ctx)
		after, _ := v.ListTools(ctx)
		assert.Eq(t, []string{"file_read", "shell_exec", "memory"}, names(after))
	})
	t.Run("immediate lists a loaded native tool at once", func(t *testing.T) {
		reg := nativeReg(nil, "memory")
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAuto,
			NativeTools: []string{"memory"}, Apply: DeferredApplyImmediate})
		var events []SearchEvent
		v.Bind(&memStore{}, func(_ context.Context, ev SearchEvent) { events = append(events, ev) })
		_, err := v.Search(ctx, "select:memory", 5)
		assert.NoErr(t, err)
		got, _ := v.ListTools(ctx)
		assert.Eq(t, []string{"file_read", "shell_exec", "memory"}, names(got))
		assert.True(t, events[0].CacheInvalidated)
	})
	t.Run("same loaded set lists the same bytes whatever the load order", func(t *testing.T) {
		build := func(order ...string) string {
			reg := nativeReg(nil, "memory", "recall", "skill")
			v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAuto,
				NativeTools: []string{"memory", "recall", "skill"}, Apply: DeferredApplySegment})
			v.Bind(&memStore{}, nil)
			for _, n := range order {
				_, err := v.Search(ctx, "select:"+n, 5)
				assert.NoErr(t, err)
			}
			v.FoldLoaded(ctx)
			got, _ := v.ListTools(ctx)
			return string(mustJSON(got))
		}
		assert.Eq(t, build("memory", "recall", "skill"), build("skill", "memory", "recall"))
	})
}

func TestVisibilityNativeProxyCall(t *testing.T) {
	ctx := context.Background()
	t.Run("tool_call expands to the real call after loading", func(t *testing.T) {
		var executed []string
		reg := nativeReg(&executed, "memory")
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAuto,
			NativeTools: []string{"memory"}, Apply: DeferredApplySegment})
		v.Bind(&memStore{}, nil)
		proxy := NewToolCallProxy(v)

		call := api.ToolCall{CallID: "c1", Name: ToolCallName, Arguments: map[string]any{
			"name": "memory", "arguments": map[string]any{"action": "add", "text": "note"},
		}}
		// Not loaded yet: the runtime does not expand and the proxy explains why.
		if _, ok := v.ExpandCall(ctx, call); ok {
			t.Fatal("an unloaded tool must not be expanded")
		}
		if _, err := proxy.Execute(ctx, call); err == nil {
			t.Fatal("an unloaded tool must be reported")
		} else if !strings.Contains(err.Error(), "memory") {
			t.Fatalf("error should name the deferred tool, got %v", err)
		}

		_, err := v.Search(ctx, "select:memory", 5)
		assert.NoErr(t, err)
		real, ok := v.ExpandCall(ctx, call)
		assert.True(t, ok)
		assert.Eq(t, "memory", real.Name)
		assert.Eq(t, "c1", real.CallID)

		// Executing the expanded call reaches the tool itself (approval and hook
		// layers sit between the proxy and the tool in the real stack).
		res, err := proxy.Execute(ctx, call)
		assert.NoErr(t, err)
		assert.Eq(t, "memory", res.Name)
		assert.Eq(t, []string{"memory|note"}, executed)
	})
	t.Run("a direct tool name is not a proxy call", func(t *testing.T) {
		v := NewVisibility(nativeReg(nil, "memory"), VisibilityOptions{Mode: DeferredAuto,
			NativeTools: []string{"memory"}})
		_, ok := v.ExpandCall(ctx, api.ToolCall{Name: "file_read", Arguments: map[string]any{"path": "a"}})
		assert.False(t, ok)
	})
}

func TestNativePromptSection(t *testing.T) {
	t.Run("default list renders one line per tool within the size guard", func(t *testing.T) {
		names := append([]string{}, DefaultNativeDeferredTools...)
		reg := nativeReg(nil, names...)
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAuto, NativeTools: names,
			Apply: DeferredApplySegment})
		section := v.PromptSection()
		for _, n := range names {
			assert.True(t, strings.Contains(section, "- "+n+" —"), "missing line for "+n)
		}
		assert.True(t, strings.Contains(section, "tool_call"))
		assert.True(t, len(section) <= 1200, "the listing must stay under 1200 characters")
		t.Logf("deferred native listing: %d characters", len(section))
	})
	t.Run("no tool_call hint without segment apply", func(t *testing.T) {
		reg := nativeReg(nil, "memory")
		v := NewVisibility(reg, VisibilityOptions{Mode: DeferredAuto, NativeTools: []string{"memory"},
			Apply: DeferredApplyImmediate})
		section := v.PromptSection()
		assert.False(t, strings.Contains(section, "tool_call 调用"))
	})
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}
