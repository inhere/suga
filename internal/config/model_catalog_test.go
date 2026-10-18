package axconfig

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestBuildModelCatalogProjectsRolesAndLocalModelDetails(t *testing.T) {
	loadYAML(t, `
model: proxy/primary
compact_model: compact
fallback_models:
  - deepseek/reasoner
  - proxy/primary
providers:
  proxy:
    type: openai
    base_url: http://127.0.0.1:1/v1
    models:
      alias:
        model_id: upstream-primary
      compact:
        context_window: 64000
      primary:
        model_id: upstream-primary
        context_window: 128000
`)
	providers, err := ReadProviders()
	assert.Require(t, assert.NoErr(t, err))
	settings, err := ReadModelSettings("")
	assert.Require(t, assert.NoErr(t, err))
	catalog := BuildModelCatalog(providers, settings)
	assert.True(t, catalog.Offline)
	specs, err := SwitchableModelSpecs(providers, settings)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, []string{"deepseek/reasoner", "proxy/alias", "proxy/compact", "proxy/primary"}, specRefs(specs))

	assert.Eq(t, []string{"anthropic", "deepseek", "openai", "proxy"}, providerIDs(catalog))
	assert.Eq(t, []string{"deepseek/reasoner", "proxy/alias", "proxy/compact", "proxy/primary"}, catalog.Switchable)
	assert.Eq(t, []string{"deepseek/reasoner", "proxy/alias", "proxy/compact", "proxy/primary"}, modelRefs(catalog))

	primary := findCatalogModel(t, catalog, "proxy/primary")
	assert.Eq(t, "upstream-primary", primary.ModelID)
	assert.Eq(t, 128000, primary.ContextWindow)
	assert.Eq(t, []string{"current", "fallback#2"}, primary.Roles)
	assert.True(t, primary.CanSwitch)

	compact := findCatalogModel(t, catalog, "proxy/compact")
	assert.Eq(t, 64000, compact.ContextWindow)
	assert.Eq(t, []string{"compact"}, compact.Roles)

	unlisted := findCatalogModel(t, catalog, "deepseek/reasoner")
	assert.Eq(t, []string{"fallback#1"}, unlisted.Roles)
	assert.Eq(t, DefaultContextWindow, unlisted.ContextWindow)
	assert.True(t, unlisted.CanSwitch)

	for _, provider := range catalog.Providers {
		if provider.ID == "proxy" {
			assert.Eq(t, []string{"compact", "upstream-primary"}, provider.ModelIDs)
		}
	}
}

func TestBuildModelCatalogProjectsThinkingStyleAndStreamRequirement(t *testing.T) {
	loadYAML(t, `
model: gw/streaming
providers:
  gw:
    type: openai
    thinking_style: adaptive
    models:
      streaming:
        stream_required: true
      plain: {}
`)
	providers, err := ReadProviders()
	assert.Require(t, assert.NoErr(t, err))
	settings, err := ReadModelSettings("")
	assert.Require(t, assert.NoErr(t, err))
	catalog := BuildModelCatalog(providers, settings)

	streaming := findCatalogModel(t, catalog, "gw/streaming")
	assert.Eq(t, ThinkingStyleAdaptive, streaming.ThinkingStyle)
	assert.True(t, streaming.StreamRequired)

	plain := findCatalogModel(t, catalog, "gw/plain")
	assert.Eq(t, ThinkingStyleAdaptive, plain.ThinkingStyle) // provider value
	assert.False(t, plain.StreamRequired)
}

func TestBuildModelCatalogKeepsConfiguredAliasesButDeduplicatesProviderIDs(t *testing.T) {
	loadYAML(t, `
model: gateway/one
providers:
  gateway:
    type: openai
    models:
      one:
        model_id: same-upstream
      two:
        model_id: same-upstream
`)
	providers, err := ReadProviders()
	assert.Require(t, assert.NoErr(t, err))
	settings, err := ReadModelSettings("")
	assert.Require(t, assert.NoErr(t, err))
	catalog := BuildModelCatalog(providers, settings)

	assert.Eq(t, []string{"gateway/one", "gateway/two"}, modelRefs(catalog))
	for _, provider := range catalog.Providers {
		if provider.ID == "gateway" {
			assert.Eq(t, []string{"same-upstream"}, provider.ModelIDs)
		}
	}
}

func TestBuildModelCatalogEmptyListIsStableAndOffline(t *testing.T) {
	loadYAML(t, "")
	providers, err := ReadProviders()
	assert.Require(t, assert.NoErr(t, err))
	catalog := BuildModelCatalog(providers, ModelSettings{})
	assert.True(t, catalog.Offline)
	assert.Eq(t, 3, len(catalog.Providers))
	assert.Eq(t, 0, len(catalog.Models))
	assert.Eq(t, []string{}, catalog.Switchable)
}

func TestReadModelSettingsRejectsAmbiguousConfiguredModel(t *testing.T) {
	loadYAML(t, `
model: shared
providers:
  first:
    type: openai
    models:
      one: {model_id: shared}
  second:
    type: openai
    models:
      two: {model_id: shared}
`)
	_, err := ReadModelSettings("")
	assert.Require(t, assert.Err(t, err))
	assert.True(t, strings.Contains(err.Error(), "matches several configured models"))
}

func specRefs(specs []ModelSpec) []string {
	refs := make([]string, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, spec.Ref())
	}
	return refs
}

func providerIDs(catalog ModelCatalog) []string {
	ids := make([]string, 0, len(catalog.Providers))
	for _, provider := range catalog.Providers {
		ids = append(ids, provider.ID)
	}
	return ids
}

func modelRefs(catalog ModelCatalog) []string {
	refs := make([]string, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		refs = append(refs, model.Ref)
	}
	return refs
}

func findCatalogModel(t *testing.T, catalog ModelCatalog, ref string) ModelCatalogModel {
	t.Helper()
	for _, model := range catalog.Models {
		if model.Ref == ref {
			return model
		}
	}
	assert.Require(t, assert.True(t, false, "model %q was not found", ref))
	return ModelCatalogModel{}
}
