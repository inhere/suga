package mcp

import (
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestNoArgumentToolSchemaGetsAPlaceholder: a server tool without parameters
// gets a non-empty properties object (gateways reject an empty one for the
// whole request), and tools with parameters are left as they are.
func TestNoArgumentToolSchemaGetsAPlaceholder(t *testing.T) {
	schema := func(raw string) map[string]any {
		a := &mcpToolAdapter{def: mcpTool{Name: "t", InputSchema: json.RawMessage(raw)}}
		return a.Schema().InputSchema
	}
	t.Run("empty properties", func(t *testing.T) {
		props := schema(`{"type":"object","properties":{}}`)["properties"].(map[string]any)
		assert.Len(t, props, 1)
		_, ok := props[mcpPlaceholderArg]
		assert.True(t, ok)
	})
	t.Run("no schema at all", func(t *testing.T) {
		s := schema(`{}`)
		assert.Eq(t, "object", s["type"])
		assert.Len(t, s["properties"].(map[string]any), 1)
	})
	t.Run("real parameters stay", func(t *testing.T) {
		props := schema(`{"type":"object","properties":{"message":{"type":"string"}}}`)["properties"].(map[string]any)
		assert.Len(t, props, 1)
		_, ok := props["message"]
		assert.True(t, ok)
	})
}
