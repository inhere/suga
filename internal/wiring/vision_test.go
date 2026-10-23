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

// TestSwitchModelKeepsToolTableStable pins design 2026-10-23 section 8 end to
// end: the tool table no longer follows the current main model's vision
// capability, so a :model switch leaves it identical, and file_read explains a
// picture to a model that cannot see it instead of a picture tool appearing and
// disappearing.
func TestSwitchModelKeepsToolTableStable(t *testing.T) {
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("model", "fake/gpt-4o"))
	setProviders(t, map[string]any{"fake": map[string]any{"type": "openai", "api_key": "k",
		"models": map[string]any{
			"gpt-4o":              map[string]any{},
			"deepseek-v4.1-flash": map[string]any{},
			"deepseek-chat":       map[string]any{},
			"glm-4":               map[string]any{},
			"pinned":              map[string]any{"model_id": "glm-5.3-flash", "vision": true},
		}}})
	workspace := t.TempDir()
	rt, err := Open(context.Background(), Options{Workspace: workspace, RuleCWD: t.TempDir()})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	ctx := context.Background()
	session, err := rt.Runner.NewSession(ctx, "vision-switch", "test")
	assert.Require(t, assert.NoErr(t, err))
	writeTestPNG(t, workspace) // shot.png: the picture every model reads below

	before := runtimeToolNames(t, rt)
	assert.False(t, slices.Contains(before, "view_image"), "the alias is not listed")
	// Every switch model, vision or not, sees the same table.
	for _, model := range []string{"fake/deepseek-v4.1-flash", "fake/deepseek-chat", "fake/glm-4",
		"fake/pinned", "fake/gpt-4o"} {
		_, err := rt.SwitchModel(ctx, session.ID, model)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, before, runtimeToolNames(t, rt), "the table does not follow the model: "+model)
	}

	t.Run("a model without vision gets the note", func(t *testing.T) {
		_, err := rt.SwitchModel(ctx, session.ID, "fake/deepseek-chat")
		assert.Require(t, assert.NoErr(t, err))
		res, err := rt.capabilities.Registry.ExecuteTool(ctx, agentcore.ToolCall{CallID: "c1", Name: "file_read",
			Arguments: map[string]any{"path": "shot.png"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, len(res.Images))
		assert.Contains(t, res.Content, "cannot see images")
		assert.Contains(t, res.Content, ":model")
	})
	t.Run("a model with vision gets the picture", func(t *testing.T) {
		_, err := rt.SwitchModel(ctx, session.ID, "fake/gpt-4o")
		assert.Require(t, assert.NoErr(t, err))
		res, err := rt.capabilities.Registry.ExecuteTool(ctx, agentcore.ToolCall{CallID: "c2", Name: "file_read",
			Arguments: map[string]any{"path": "shot.png"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Eq(t, 8, res.Images[0].Width)
	})
	t.Run("the hidden alias still runs by name", func(t *testing.T) {
		res, err := rt.capabilities.Registry.ExecuteTool(ctx, agentcore.ToolCall{CallID: "c3", Name: "view_image",
			Arguments: map[string]any{"path": "shot.png"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
	})
}
