package wiring

import (
	"context"
	"slices"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
)

// runtimeToolNames lists the tools the runtime's registry stack offers right
// now (the runtime lists tools at every step, so this is what a model sees).
func runtimeToolNames(t *testing.T, rt *Runtime) []string {
	t.Helper()
	schemas, err := rt.Runner.ToolRegistry().ListTools(context.Background())
	assert.NoErr(t, err)
	names := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		names = append(names, schema.Name)
	}
	return names
}

// TestSwitchModelFiltersViewImageByVision pins the model-scoped tool table end
// to end: the runtime filters view_image by the current main model's vision
// capability, so a :model switch changes what the next step offers.
func TestSwitchModelFiltersViewImageByVision(t *testing.T) {
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("model", "fake/gpt-4o"))
	setProviders(t, map[string]any{"fake": map[string]any{"type": "openai", "api_key": "k",
		"models": map[string]any{
			"gpt-4o":              map[string]any{},
			"deepseek-v4.1-flash": map[string]any{},
			"pinned":              map[string]any{"model_id": "glm-5.3-flash", "vision": true},
		}}})
	rt := openTestRuntime(t, "")
	session, err := rt.Runner.NewSession(context.Background(), "vision-switch", "test")
	assert.Require(t, assert.NoErr(t, err))
	hasViewImage := func() bool {
		return slices.Contains(runtimeToolNames(t, rt), "view_image")
	}

	assert.True(t, hasViewImage())
	_, err = rt.SwitchModel(context.Background(), session.ID, "fake/deepseek-v4.1-flash")
	assert.Require(t, assert.NoErr(t, err))
	assert.False(t, hasViewImage())
	// The configured vision: true wins over the inferred "glm" answer.
	_, err = rt.SwitchModel(context.Background(), session.ID, "fake/pinned")
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, hasViewImage())
	// And back to the vision model.
	_, err = rt.SwitchModel(context.Background(), session.ID, "fake/gpt-4o")
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, hasViewImage())

	// The tool itself refuses a stale call of a text-only model.
	_, err = rt.SwitchModel(context.Background(), session.ID, "fake/deepseek-v4.1-flash")
	assert.Require(t, assert.NoErr(t, err))
	_, err = rt.Runner.ToolRegistry().ExecuteTool(context.Background(), agentcore.ToolCall{CallID: "c1", Name: "view_image",
		Arguments: map[string]any{"path": "x.png"}})
	assert.Require(t, assert.Err(t, err))
	assert.Contains(t, err.Error(), ":model")
}
