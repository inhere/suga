package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// plainTool is a non-MCP tool: it declares no annotations at all.
type plainTool struct{}

func (plainTool) Name() string           { return "plain" }
func (plainTool) Description() string    { return "plain tool" }
func (plainTool) Kind() tools.Kind       { return tools.KindNative }
func (plainTool) Schema() api.ToolSchema { return api.ToolSchema{Name: "plain"} }
func (plainTool) Execute(context.Context, api.ToolCall) (*api.ToolResult, error) {
	return &api.ToolResult{}, nil
}

// TestMCPToolAnnotationsParsing covers the annotations read from a tools/list
// result: the full spec object, no annotations at all, and a partial one.
func TestMCPToolAnnotationsParsing(t *testing.T) {
	// parse runs the unmarshal ListTools does on a tools/list result.
	parse := func(t *testing.T, payload string) (Annotations, bool) {
		t.Helper()
		var resp struct {
			Tools []mcpTool `json:"tools"`
		}
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(payload), &resp)))
		assert.Require(t, assert.Len(t, resp.Tools, 1))
		return AnnotationsOf(NewClient(ServerConfig{Name: "srv"}).AsTool(resp.Tools[0]))
	}

	t.Run("full annotations", func(t *testing.T) {
		annotations, ok := parse(t, `{"tools":[{"name":"gofer_work_list","annotations":{
			"title":"List work","readOnlyHint":true,"destructiveHint":false,
			"idempotentHint":true,"openWorldHint":false}}]}`)
		assert.True(t, ok)
		assert.Eq(t, "List work", annotations.Title)
		assert.True(t, annotations.ReadOnly())
		assert.False(t, annotations.Destructive())
		assert.True(t, annotations.SafeReadOnly())
		assert.NotNil(t, annotations.IdempotentHint)
		assert.Eq(t, false, *annotations.OpenWorldHint)
	})

	t.Run("no annotations", func(t *testing.T) {
		annotations, ok := parse(t, `{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`)
		assert.False(t, ok)
		assert.False(t, annotations.ReadOnly())
		assert.False(t, annotations.SafeReadOnly())
	})

	t.Run("only readOnlyHint", func(t *testing.T) {
		annotations, ok := parse(t, `{"tools":[{"name":"gofer_today_list","annotations":{"readOnlyHint":true}}]}`)
		assert.True(t, ok)
		assert.True(t, annotations.ReadOnly())
		// An absent destructiveHint is not a "not destructive" declaration.
		assert.Nil(t, annotations.DestructiveHint)
		assert.True(t, annotations.SafeReadOnly())
	})

	t.Run("readOnlyHint false", func(t *testing.T) {
		annotations, ok := parse(t, `{"tools":[{"name":"write","annotations":{"readOnlyHint":false}}]}`)
		assert.True(t, ok)
		assert.False(t, annotations.ReadOnly())
	})

	t.Run("destructive read-only claim is contradicted", func(t *testing.T) {
		annotations, ok := parse(t, `{"tools":[{"name":"purge","annotations":{"readOnlyHint":true,"destructiveHint":true}}]}`)
		assert.True(t, ok)
		assert.True(t, annotations.ReadOnly())
		assert.True(t, annotations.Destructive())
		assert.False(t, annotations.SafeReadOnly())
	})
}

// TestMCPListToolsAnnotationsOverHTTP walks the real discovery path: a server's
// tools/list annotations arrive on the adapter the registry registers.
func TestMCPListToolsAnnotationsOverHTTP(t *testing.T) {
	t.Run("read-only tool", func(t *testing.T) {
		fixture, cfg := newHTTPFixture(t)
		fixture.toolAnnotations = map[string]any{"readOnlyHint": true, "title": "Echo"}
		client := newClient(cfg)
		ctx := context.Background()
		assert.Require(t, assert.NoErr(t, client.Start(ctx)))
		t.Cleanup(client.Stop)
		defs, err := client.ListTools(ctx)
		assert.Require(t, assert.NoErr(t, err))
		annotations, ok := AnnotationsOf(client.AsTool(defs[0]))
		assert.True(t, ok)
		assert.True(t, annotations.SafeReadOnly())
	})

	t.Run("tool without annotations", func(t *testing.T) {
		fixture, cfg := newHTTPFixture(t)
		fixture.toolAnnotations = nil
		client := newClient(cfg)
		ctx := context.Background()
		assert.Require(t, assert.NoErr(t, client.Start(ctx)))
		t.Cleanup(client.Stop)
		defs, err := client.ListTools(ctx)
		assert.Require(t, assert.NoErr(t, err))
		_, ok := AnnotationsOf(client.AsTool(defs[0]))
		assert.False(t, ok)
	})
}

// TestAnnotationsOfNonMCPTool pins that only MCP adapters carry annotations.
func TestAnnotationsOfNonMCPTool(t *testing.T) {
	for name, tool := range map[string]api.Tool{"plain": plainTool{}, "nil": nil} {
		t.Run(name, func(t *testing.T) {
			_, ok := AnnotationsOf(tool)
			assert.False(t, ok)
		})
	}
}
