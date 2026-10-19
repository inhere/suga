package axconfig

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestInferVision pins the built-in vision list: the model id sent to the
// provider decides when no vision key is written.
func TestInferVision(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-3-5-sonnet-20241022", true},
		{"claude-3-haiku-20240307", true},
		{"claude-sonnet-4-20250514", true},
		{"claude-opus-4-1", true},
		{"claude-2.1", false},
		{"claude-instant-1.2", false},
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-4.1", true},
		{"gpt-5-codex", true},
		{"gpt-6-sol", true},
		{"gemini-2.5-pro", true},
		{"deepseek-v4.1-flash", false},
		{"glm-5.3-flash", false},
		{"qwen3-coder-plus", false},
		{"some-unknown-model", false},
		{"", false},
		// A gateway prefixes the family with its vendor: the family still wins.
		{"openai/gpt-4o", true},
		{"anthropic/claude-3-7-sonnet", true},
		// A text-only family stays text-only even with a vision name in it.
		{"deepseek-gpt-4o-lookalike", false},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			assert.Eq(t, tc.want, InferVision(tc.model))
		})
	}
}

// TestVisionFor pins the three states: a written model value, a written
// provider value, then the inferred one.
func TestVisionFor(t *testing.T) {
	provider := ProviderConfig{ID: "gw", Models: map[string]ModelConfig{
		"declared-on":  {Vision: true, VisionSet: true},
		"declared-off": {Vision: false, VisionSet: true},
		"inferred":     {},
	}}
	spec := func(model string) ModelSpec { return ModelSpec{Provider: provider, Model: model} }

	t.Run("inferred from the model id", func(t *testing.T) {
		assert.Eq(t, true, VisionFor(ModelSpec{Provider: ProviderConfig{ID: "gw"}, Model: "gpt-4o"}))
		assert.Eq(t, false, VisionFor(ModelSpec{Provider: ProviderConfig{ID: "gw"}, Model: "glm-5.3-flash"}))
	})
	t.Run("the model key overrides the inference", func(t *testing.T) {
		assert.Eq(t, true, VisionFor(spec("declared-on")))
		assert.Eq(t, false, VisionFor(spec("declared-off")))
		assert.Eq(t, false, VisionFor(spec("inferred")))
	})
	t.Run("the provider key applies to its models", func(t *testing.T) {
		// A gateway that rejects image parts sets vision: false once for all of
		// its models, including gpt-4o.
		off := ProviderConfig{ID: "gw", Vision: false, VisionSet: true,
			Models: map[string]ModelConfig{"gpt-4o": {}}}
		assert.Eq(t, false, VisionFor(ModelSpec{Provider: off, Model: "gpt-4o"}))
		on := ProviderConfig{ID: "gw", Vision: true, VisionSet: true,
			Models: map[string]ModelConfig{"glm-5.3-flash": {}, "declared-off": {Vision: false, VisionSet: true}}}
		assert.Eq(t, true, VisionFor(ModelSpec{Provider: on, Model: "glm-5.3-flash"}))
		// The model key still wins over the provider key.
		assert.Eq(t, false, VisionFor(ModelSpec{Provider: on, Model: "declared-off"}))
	})
	t.Run("model_id decides the inference", func(t *testing.T) {
		// The configured key is short, the upstream id is what the provider sees.
		gw := ProviderConfig{ID: "gw", Models: map[string]ModelConfig{
			"sol": {ModelID: "gpt-6-sol"}, "flash": {ModelID: "deepseek-v4.1-flash"},
		}}
		assert.Eq(t, true, VisionFor(ModelSpec{Provider: gw, Model: "sol"}))
		assert.Eq(t, false, VisionFor(ModelSpec{Provider: gw, Model: "flash"}))
	})
}

// TestReadProvidersVision pins the config read: providers.<id>.vision and
// providers.<id>.models.<m>.vision are three-state, so an explicit false is
// distinguishable from "not written".
func TestReadProvidersVision(t *testing.T) {
	t.Run("written values are kept", func(t *testing.T) {
		loadYAML(t, `
providers:
  gw:
    type: openai
    base_url: https://gw.example/v1
    vision: false
    models:
      gpt-4o: {}
      sol:
        vision: true
`)
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, true, ps["gw"].VisionSet)
		assert.Eq(t, false, ps["gw"].Vision)
		assert.Eq(t, true, ps["gw"].Models["sol"].VisionSet)
		assert.Eq(t, true, ps["gw"].Models["sol"].Vision)
		assert.Eq(t, false, ps["gw"].Models["gpt-4o"].VisionSet)
		// An unset key keeps the inferred value.
		assert.Eq(t, false, VisionFor(ModelSpec{Provider: ps["gw"], Model: "gpt-4o"}))
		assert.Eq(t, true, VisionFor(ModelSpec{Provider: ps["gw"], Model: "sol"}))
	})

	t.Run("an unset provider key infers per model", func(t *testing.T) {
		loadYAML(t, `
providers:
  gw:
    type: openai
    base_url: https://gw.example/v1
`)
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, false, ps["gw"].VisionSet)
		assert.Eq(t, true, VisionFor(ModelSpec{Provider: ps["gw"], Model: "gpt-4o"}))
	})
}

// TestModelCatalogShowsVision pins the models command projection: the
// capability is shown for every model, however it was decided.
func TestModelCatalogShowsVision(t *testing.T) {
	loadYAML(t, `
model: gw/gpt-4o
providers:
  gw:
    type: openai
    base_url: https://gw.example/v1
    models:
      gpt-4o: {}
      deepseek-v4.1-flash: {}
      pinned:
        model_id: glm-5.3-flash
        vision: true
`)
	providers, err := ReadProviders()
	assert.NoErr(t, err)
	settings, err := ReadModelSettings("")
	assert.NoErr(t, err)
	catalog := BuildModelCatalog(providers, settings)
	vision := map[string]bool{}
	for _, model := range catalog.Models {
		vision[model.Ref] = model.Vision
	}
	assert.Eq(t, true, vision["gw/gpt-4o"])
	assert.Eq(t, false, vision["gw/deepseek-v4.1-flash"])
	assert.Eq(t, true, vision["gw/pinned"])
}
