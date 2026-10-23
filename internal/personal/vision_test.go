package personal

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// schemaRegistry is a ToolRegistry that only lists its schemas and counts the
// calls it handed on (enough for the observedTools layer).
type schemaRegistry struct {
	schemas  []agentcore.ToolSchema
	executed []string
}

func (r *schemaRegistry) ListTools(context.Context) ([]agentcore.ToolSchema, error) {
	return r.schemas, nil
}

func (r *schemaRegistry) ExecuteTool(_ context.Context, call agentcore.ToolCall) (*agentcore.ToolResult, error) {
	r.executed = append(r.executed, call.Name)
	return &api.ToolResult{CallID: call.CallID, Name: call.Name, Content: "ok"}, nil
}

var visionToolSchemas = []agentcore.ToolSchema{{Name: "file_read"}, {Name: "file_list"}}

// registryToolNames lists the tool names a registry offers.
func registryToolNames(t *testing.T, registry agentcore.ToolRegistry) []string {
	t.Helper()
	schemas, err := registry.ListTools(context.Background())
	assert.NoErr(t, err)
	names := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		names = append(names, schema.Name)
	}
	return names
}

// TestObservedToolsToolTableIsModelIndependent pins design 2026-10-23 section 8:
// the table the model is offered no longer follows its vision capability, and a
// picture call is no longer refused in front of the tool (file_read itself
// explains a picture the model cannot use).
func TestObservedToolsToolTableIsModelIndependent(t *testing.T) {
	t.Run("a model without vision gets the same table", func(t *testing.T) {
		gate := tools.NewVisionGate(false)
		inner := &schemaRegistry{schemas: visionToolSchemas}
		assert.Eq(t, []string{"file_read", "file_list"}, registryToolNames(t, (&recorder{vision: gate}).observeTools(inner)))
	})
	t.Run("a model with vision gets the same table", func(t *testing.T) {
		gate := tools.NewVisionGate(true)
		inner := &schemaRegistry{schemas: visionToolSchemas}
		assert.Eq(t, []string{"file_read", "file_list"}, registryToolNames(t, (&recorder{vision: gate}).observeTools(inner)))
	})
	t.Run("a model switch changes nothing", func(t *testing.T) {
		gate := tools.NewVisionGate(true)
		inner := &schemaRegistry{schemas: visionToolSchemas}
		observed := (&recorder{vision: gate}).observeTools(inner)
		before := registryToolNames(t, observed)
		gate.SetVision(false)
		assert.Eq(t, before, registryToolNames(t, observed))
	})
	t.Run("a picture call reaches the tool of a text-only model", func(t *testing.T) {
		gate := tools.NewVisionGate(false)
		inner := &schemaRegistry{schemas: visionToolSchemas}
		observed := (&recorder{vision: gate}).observeTools(inner)
		_, err := observed.ExecuteTool(context.Background(), agentcore.ToolCall{Name: "file_read",
			Arguments: map[string]any{"path": "a.png"}})
		assert.NoErr(t, err)
		assert.Eq(t, []string{"file_read"}, inner.executed)
	})
}
