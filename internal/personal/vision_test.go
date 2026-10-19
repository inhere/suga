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

var visionToolSchemas = []agentcore.ToolSchema{{Name: "file_read"}, {Name: "view_image"}, {Name: "file_list"}}

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

// TestObservedToolsFilterViewImageByVision pins the model-scoped tool table:
// view_image is offered only while the current model can see images, and the
// same gate follows a model switch (no tool rebuild).
func TestObservedToolsFilterViewImageByVision(t *testing.T) {
	t.Run("a model without vision does not get view_image", func(t *testing.T) {
		gate := tools.NewVisionGate(false)
		inner := &schemaRegistry{schemas: visionToolSchemas}
		assert.Eq(t, []string{"file_read", "file_list"}, registryToolNames(t, (&recorder{vision: gate}).observeTools(inner)))
	})
	t.Run("a model with vision gets it", func(t *testing.T) {
		gate := tools.NewVisionGate(true)
		inner := &schemaRegistry{schemas: visionToolSchemas}
		assert.Eq(t, []string{"file_read", "view_image", "file_list"}, registryToolNames(t, (&recorder{vision: gate}).observeTools(inner)))
	})
	t.Run("a model switch updates the table", func(t *testing.T) {
		gate := tools.NewVisionGate(true)
		inner := &schemaRegistry{schemas: visionToolSchemas}
		observed := (&recorder{vision: gate}).observeTools(inner)
		assert.Eq(t, 3, len(registryToolNames(t, observed)))
		// :model to a text-only model, then back.
		gate.SetVision(false)
		assert.Eq(t, []string{"file_read", "file_list"}, registryToolNames(t, observed))
		gate.SetVision(true)
		assert.Eq(t, 3, len(registryToolNames(t, observed)))
	})
	t.Run("a call of a text-only model is refused before the tool runs", func(t *testing.T) {
		gate := tools.NewVisionGate(false)
		inner := &schemaRegistry{schemas: visionToolSchemas}
		observed := (&recorder{vision: gate}).observeTools(inner)
		_, err := observed.ExecuteTool(context.Background(), agentcore.ToolCall{Name: "view_image"})
		assert.Require(t, assert.Err(t, err))
		assert.Contains(t, err.Error(), ":model")
		assert.Eq(t, 0, len(inner.executed))
		// Other tools still run.
		_, err = observed.ExecuteTool(context.Background(), agentcore.ToolCall{Name: "file_read"})
		assert.NoErr(t, err)
		assert.Eq(t, []string{"file_read"}, inner.executed)
	})
	t.Run("a nil gate keeps every tool", func(t *testing.T) {
		inner := &schemaRegistry{schemas: visionToolSchemas}
		assert.Eq(t, 3, len(registryToolNames(t, (&recorder{}).observeTools(inner))))
	})
}
