package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// recordApprover records the tool names it decides on and optionally denies all.
type recordApprover struct {
	calls []string
	deny  bool
}

func (a *recordApprover) Decide(_ context.Context, call agentcoreapi.ToolCall, _ agentcoreapi.Tool) error {
	a.calls = append(a.calls, call.Name)
	if a.deny {
		return tools.Denied("not allowed in this test")
	}
	return nil
}

// TestNewToolVisibilityNative covers the tools.deferred_native /
// tools.deferred_apply matrix on the real assembly function.
func TestNewToolVisibilityNative(t *testing.T) {
	build := func(t *testing.T, settings map[string]any, approver tools.Approver) (*tools.Registry, *tools.Visibility, error) {
		t.Helper()
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		for k, v := range settings {
			assert.NoErr(t, gkconfig.Set(k, v))
		}
		reg := tools.NewRegistry()
		reg.Register(&nativeStubTool{name: "file_read"})
		reg.Register(&nativeStubTool{name: "memory"})
		reg.Register(&nativeStubTool{name: "skill"})
		if approver == nil {
			approver = &recordApprover{}
		}
		vis, err := newToolVisibility(reg, tools.NewApproval(reg, approver), 128000, nil)
		return reg, vis, err
	}
	t.Run("unset defers nothing and registers no proxy", func(t *testing.T) {
		reg, vis, err := build(t, nil, nil)
		assert.NoErr(t, err)
		assert.False(t, vis.Active())
		want, _ := reg.ListTools(context.Background())
		got, _ := vis.ListTools(context.Background())
		assert.Eq(t, string(mustMarshalJSON(want)), string(mustMarshalJSON(got)))
		_, err = reg.Get(tools.ToolCallName)
		assert.Err(t, err)
		_, err = reg.Get("tool_search")
		assert.Err(t, err)
	})
	t.Run("auto defers the built-in list and registers tool_search and tool_call", func(t *testing.T) {
		reg, vis, err := build(t, map[string]any{"tools.deferred_native": "auto"}, nil)
		assert.NoErr(t, err)
		assert.True(t, vis.Active())
		_, err = reg.Get("tool_search")
		assert.NoErr(t, err)
		_, err = reg.Get(tools.ToolCallName)
		assert.NoErr(t, err)
		got, _ := vis.ListTools(context.Background())
		assert.Eq(t, []string{"file_read", "tool_call", "tool_search"}, schemaNames(got))
	})
	t.Run("tools.deferred never turns the native list off as well", func(t *testing.T) {
		reg, vis, err := build(t, map[string]any{"tools.deferred": "never", "tools.deferred_native": "auto"}, nil)
		assert.NoErr(t, err)
		assert.False(t, vis.Active())
		want, _ := reg.ListTools(context.Background())
		got, _ := vis.ListTools(context.Background())
		assert.Eq(t, string(mustMarshalJSON(want)), string(mustMarshalJSON(got)))
		_, err = reg.Get(tools.ToolCallName)
		assert.Err(t, err)
	})
	t.Run("immediate apply registers no tool_call", func(t *testing.T) {
		reg, vis, err := build(t, map[string]any{"tools.deferred_native": "auto", "tools.deferred_apply": "immediate"}, nil)
		assert.NoErr(t, err)
		assert.True(t, vis.Active())
		_, err = reg.Get(tools.ToolCallName)
		assert.Err(t, err)
		_, err = reg.Get("tool_search")
		assert.NoErr(t, err)
	})
	t.Run("invalid settings are startup errors", func(t *testing.T) {
		_, _, err := build(t, map[string]any{"tools.deferred_native": "no_such_tool"}, nil)
		assert.Err(t, err)
		_, _, err = build(t, map[string]any{"tools.deferred_apply": "sometimes"}, nil)
		assert.Err(t, err)
	})
	t.Run("a proxied call reaches approval with the real tool name", func(t *testing.T) {
		approver := &recordApprover{deny: true}
		reg, vis, err := build(t, map[string]any{"tools.deferred_native": "auto"}, approver)
		assert.NoErr(t, err)
		assert.True(t, vis.Active())
		_, err = reg.Get("tool_search")
		assert.NoErr(t, err)
		ctx := context.Background()
		before, _ := vis.ListTools(ctx)
		_, err = vis.Search(ctx, "select:memory", 5)
		assert.NoErr(t, err)
		inside, _ := vis.ListTools(ctx)
		assert.Eq(t, string(mustMarshalJSON(before)), string(mustMarshalJSON(inside)))

		proxyCall := agentcoreapi.ToolCall{CallID: "c1", Name: tools.ToolCallName, Arguments: map[string]any{
			"name": "memory", "arguments": map[string]any{"action": "search", "query": "x"},
		}}
		real, ok := vis.ExpandCall(ctx, proxyCall)
		assert.True(t, ok)
		_, err = vis.ExecuteTool(ctx, real)
		assert.True(t, errors.Is(err, tools.ErrDenied))
		assert.Eq(t, []string{"memory"}, approver.calls)
		// The proxy tool itself routes through the same layers, never around them.
		_, err = tools.NewToolCallProxy(vis).Execute(ctx, proxyCall)
		assert.True(t, errors.Is(err, tools.ErrDenied))
		assert.Eq(t, []string{"memory", "memory"}, approver.calls)
		// A denied call never ran, and the table of the running segment is unchanged:
		// the tool is only loaded, it joins the table at the next segment boundary.
		after, _ := vis.ListTools(ctx)
		assert.Eq(t, string(mustMarshalJSON(inside)), string(mustMarshalJSON(after)))
		assert.Eq(t, []string{"file_read", "tool_call", "tool_search"}, schemaNames(after))
	})
}

func schemaNames(schemas []agentcoreapi.ToolSchema) []string {
	out := make([]string, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, s.Name)
	}
	return out
}

func mustMarshalJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}
