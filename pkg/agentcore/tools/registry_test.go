package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// mockTool is a simple test double.
type mockTool struct {
	name   string
	result string
	err    error
}

func (m *mockTool) Name() string           { return m.name }
func (m *mockTool) Description() string    { return "mock tool" }
func (m *mockTool) Kind() Kind             { return KindNative }
func (m *mockTool) Schema() api.ToolSchema { return api.ToolSchema{Name: m.name} }
func (m *mockTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    m.name,
		Content: m.result,
	}, m.err
}

var _ api.ToolRegistry = (*Registry)(nil)

func TestRegistry_RegisterAndExecute(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "echo", result: "hello"})

	result, err := reg.ExecuteTool(context.Background(), api.ToolCall{Name: "echo"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Content != "hello" {
		t.Errorf("result = %q, want hello", result.Content)
	}
}

func TestRegistry_Execute_PassesArgumentsAndErrors(t *testing.T) {
	t.Run("passes arguments to registered tool", func(t *testing.T) {
		reg := NewRegistry()
		tool := &captureTool{name: "capture", result: "ok"}
		reg.Register(tool)

		result, err := reg.ExecuteTool(context.Background(), api.ToolCall{
			CallID:    "call-1",
			Name:      "capture",
			Arguments: map[string]any{"msg": "hello", "count": 2},
		})
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if result.CallID != "call-1" || result.Name != "capture" || result.Content != "ok" {
			t.Fatalf("result = %#v, want preserved call metadata plus content", result)
		}
		if tool.lastArgs["msg"] != "hello" || tool.lastArgs["count"] != 2 {
			t.Fatalf("tool args = %#v, want msg/count to be preserved", tool.lastArgs)
		}
	})

	t.Run("returns underlying tool error", func(t *testing.T) {
		reg := NewRegistry()
		expectedErr := errors.New("boom")
		reg.Register(&captureTool{name: "broken", err: expectedErr})

		_, err := reg.ExecuteTool(context.Background(), api.ToolCall{
			Name:      "broken",
			Arguments: map[string]any{"msg": "fail"},
		})
		if !errors.Is(err, expectedErr) {
			t.Fatalf("err = %v, want %v", err, expectedErr)
		}
	})
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "tool"})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for duplicate tool name")
		}
	}()
	reg.Register(&mockTool{name: "tool"})
}

func TestRegistry_NotFound(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.ExecuteTool(context.Background(), api.ToolCall{Name: "missing"})
	if err == nil {
		t.Error("expected error for missing tool")
	}
}

func TestRegistry_Schemas(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "a"})
	reg.Register(&mockTool{name: "b"})

	schemas, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(schemas) != 2 {
		t.Errorf("got %d schemas, want 2", len(schemas))
	}
}

func TestRegistry_Schemas_PreserveMetadataAndListStaysComplete(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&schemaTool{name: "search"})
	reg.Register(&schemaTool{name: "write_file"})

	schemas, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(schemas) != 2 {
		t.Fatalf("schemas = %d, want 2", len(schemas))
	}

	byName := map[string]api.ToolSchema{}
	for _, schema := range schemas {
		byName[schema.Name] = schema
	}

	searchSchema, ok := byName["search"]
	if !ok {
		t.Fatalf("schemas missing search: %#v", schemas)
	}
	if searchSchema.Description != "schema search" {
		t.Fatalf("search description = %q, want schema search", searchSchema.Description)
	}
	if searchSchema.InputSchema["type"] != "object" {
		t.Fatalf("search parameters type = %#v, want object", searchSchema.InputSchema["type"])
	}
	required, ok := searchSchema.InputSchema["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Fatalf("search required = %#v, want query", searchSchema.InputSchema["required"])
	}
	properties, ok := searchSchema.InputSchema["properties"].(map[string]any)
	if !ok || properties["query"] == nil {
		t.Fatalf("search schema properties = %#v, want query property", searchSchema.InputSchema["properties"])
	}

	names := reg.List()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "search" || names[1] != "write_file" {
		t.Fatalf("list = %#v, want [search write_file]", names)
	}
}

func TestRegistry_ListTools_IsStableAndSortedByName(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&schemaTool{name: "z-last"})
	reg.Register(&schemaTool{name: "a-first"})
	reg.Register(&schemaTool{name: "m-middle"})

	for i := 0; i < 50; i++ {
		schemas, err := reg.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools error: %v", err)
		}
		if len(schemas) != 3 {
			t.Fatalf("schemas = %d, want 3", len(schemas))
		}

		got := []string{schemas[0].Name, schemas[1].Name, schemas[2].Name}
		want := []string{"a-first", "m-middle", "z-last"}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("schemas order = %#v, want %#v", got, want)
			}
		}
	}
}

func TestRegistry_ExecuteTool_PreservesRichResults(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&richTool{name: "rich_echo"})

	result, err := reg.ExecuteTool(context.Background(), api.ToolCall{
		CallID: "call-rich",
		Name:   "rich_echo",
	})
	if err != nil {
		t.Fatalf("ExecuteTool error: %v", err)
	}
	if result.CallID != "call-rich" || result.Name != "rich_echo" || result.Content != "rich:rich_echo" {
		t.Fatalf("result = %#v, want original registry result metadata", result)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Content != "artifact body" {
		t.Fatalf("artifacts = %#v, want preserved artifacts", result.Artifacts)
	}
	if result.Metadata["origin"] != "platform" {
		t.Fatalf("metadata = %#v, want preserved metadata", result.Metadata)
	}
}

type captureTool struct {
	name     string
	result   string
	err      error
	lastArgs map[string]any
}

func (m *captureTool) Name() string        { return m.name }
func (m *captureTool) Description() string { return "capture tool" }
func (m *captureTool) Kind() Kind          { return KindNative }
func (m *captureTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        m.name,
		Description: m.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"msg": map[string]any{
					"type":        "string",
					"description": "message",
				},
			},
		},
	}
}
func (m *captureTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	m.lastArgs = cloneArgs(call.Arguments)
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    call.Name,
		Content: m.result,
	}, m.err
}

type schemaTool struct {
	name string
}

func (m *schemaTool) Name() string        { return m.name }
func (m *schemaTool) Description() string { return "schema " + m.name }
func (m *schemaTool) Kind() Kind          { return KindNative }
func (m *schemaTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        m.name,
		Description: "schema " + m.name,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "query text",
				},
			},
			"required": []any{"query"},
		},
	}
}
func (m *schemaTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    call.Name,
		Content: fmt.Sprintf("schema:%s", m.name),
	}, nil
}

type richTool struct {
	name string
}

func (m *richTool) Name() string        { return m.name }
func (m *richTool) Description() string { return "rich " + m.name }
func (m *richTool) Kind() Kind          { return KindNative }
func (m *richTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        m.name,
		Description: "rich " + m.name,
		InputSchema: map[string]any{"type": "object"},
	}
}
func (m *richTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    call.Name,
		Content: "rich:" + m.name,
		Artifacts: []api.Artifact{{
			Kind:    "text",
			Content: "artifact body",
		}},
		Metadata: map[string]any{
			"origin": "platform",
		},
	}, nil
}

func cloneArgs(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// plainTool implements only api.Tool, without Kind.
type plainTool struct{}

func (plainTool) Name() string                                                   { return "plain" }
func (plainTool) Description() string                                            { return "" }
func (plainTool) Schema() api.ToolSchema                                         { return api.ToolSchema{Name: "plain"} }
func (plainTool) Execute(context.Context, api.ToolCall) (*api.ToolResult, error) { return nil, nil }

func TestKindOf(t *testing.T) {
	assert.Eq(t, KindNative, KindOf(plainTool{}))
	assert.Eq(t, KindNative, KindOf(nil))
	assert.Eq(t, KindMCP, KindOf(&fakeMCP{}))
	reg := NewRegistry()
	reg.Register(plainTool{}) // Kind is optional for registration
	got, err := reg.Get("plain")
	assert.NoErr(t, err)
	assert.Eq(t, KindNative, KindOf(got))
}

// describedTool declares metadata; mcp makes it an MCP tool.
type describedTool struct {
	plainTool
	meta Meta
	mcp  bool
}

func (d describedTool) ToolMeta() Meta { return d.meta }
func (d describedTool) Kind() Kind {
	if d.mcp {
		return KindMCP
	}
	return KindNative
}
func (describedTool) MCPServer() string { return "srv" }

func TestMetaOf(t *testing.T) {
	other := Meta{Category: CategoryOther}
	assert.Eq(t, other, MetaOf(nil))
	assert.Eq(t, other, MetaOf(plainTool{}))
	read := Meta{Category: CategoryRead, ReadOnly: true}
	assert.Eq(t, read, MetaOf(describedTool{meta: read}))
	assert.Eq(t, Meta{Category: CategoryOther, ReadOnly: true}, MetaOf(describedTool{meta: Meta{ReadOnly: true}}))
	// An MCP tool is never trusted by its own declaration.
	assert.Eq(t, Meta{Category: CategoryOther, External: true}, MetaOf(describedTool{meta: read, mcp: true}))
	assert.True(t, IsMCP(describedTool{mcp: true}))
	assert.False(t, IsMCP(nil))
	assert.Eq(t, "srv", MCPServerOf(describedTool{}))
	assert.Eq(t, "", MCPServerOf(plainTool{}))
}

func TestMetaOfExtensions(t *testing.T) {
	full := Meta{Category: CategoryRead, ReadOnly: true, External: true, Rereadable: true, AgentState: true}
	t.Run("native keeps declared flags", func(t *testing.T) {
		assert.Eq(t, full, MetaOf(describedTool{meta: full}))
	})
	t.Run("mcp is external and never agent-state or rereadable", func(t *testing.T) {
		got := MetaOf(describedTool{meta: full, mcp: true})
		assert.True(t, got.External)
		assert.False(t, got.AgentState)
		assert.False(t, got.Rereadable)
		assert.False(t, got.ReadOnly)
	})
	t.Run("nil and undeclared tools are not external", func(t *testing.T) {
		assert.False(t, MetaOf(nil).External)
		assert.False(t, MetaOf(plainTool{}).External)
	})
}

// TestRegistry_Remove: Remove takes a tool out of the list and the schemas (the
// host's tools.disabled policy) and ignores a name that was never registered.
func TestRegistry_Remove(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "web_fetch"})
	reg.Register(&mockTool{name: "file_read"})

	reg.Remove("web_fetch")
	_, err := reg.Get("web_fetch")
	assert.Err(t, err)
	assert.Eq(t, []string{"file_read"}, reg.List())
	assert.Eq(t, 1, len(reg.Schemas()))
	assert.Eq(t, "file_read", reg.Schemas()[0].Name)

	reg.Remove("never_registered") // no panic, no change
	assert.Eq(t, []string{"file_read"}, reg.List())
}
