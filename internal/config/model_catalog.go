package axconfig

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// ModelCatalog is the offline projection consumed by the models command and
// later by chat's model switcher. It contains configuration facts only; it
// never builds a provider or contacts an endpoint.
type ModelCatalog struct {
	Offline    bool                   `json:"offline"`
	Providers  []ModelCatalogProvider `json:"providers"`
	Models     []ModelCatalogModel    `json:"models"`
	Switchable []string               `json:"switchable"`
	Source     string                 `json:"source"`
}

// ModelCatalogProvider describes a provider namespace without exposing its
// endpoint or credentials.
type ModelCatalogProvider struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	ModelIDs []string `json:"model_ids"`
}

// ModelCatalogModel is one canonical provider/model reference. ModelID is the
// effective upstream id, while Ref remains the stable local configuration key.
type ModelCatalogModel struct {
	Ref           string `json:"ref"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ModelID       string `json:"model_id,omitempty"`
	Type          string `json:"type,omitempty"`
	ContextWindow int    `json:"context_window"`
	Thinking      string `json:"thinking,omitempty"`
	// ThinkingStyle is the declared thinking request shape ("" = the model-name
	// heuristic), see ThinkingStyleFor.
	ThinkingStyle string `json:"thinking_style,omitempty"`
	// StreamRequired marks a model whose endpoint answers only streamed requests.
	StreamRequired bool `json:"stream_required,omitempty"`
	// Vision reports whether the model accepts images (providers.<id>.models.<model>.vision,
	// else providers.<id>.vision, else the built-in list; see VisionFor).
	Vision    bool     `json:"vision"`
	Roles     []string `json:"roles"`
	CanSwitch bool     `json:"can_switch"`
}

// BuildModelCatalog projects resolved providers and model roles. The caller is
// responsible for obtaining the inputs through ReadProviders and
// ReadModelSettings, which keeps this projection independent from runtime
// assembly and network-capable provider code.
func BuildModelCatalog(providers map[string]ProviderConfig, settings ModelSettings) ModelCatalog {
	catalog := ModelCatalog{Offline: true, Providers: buildCatalogProviders(providers)}
	models := make(map[string]*ModelCatalogModel)

	providerIDs := make([]string, 0, len(providers))
	for id := range providers {
		providerIDs = append(providerIDs, id)
	}
	slices.Sort(providerIDs)
	for _, providerID := range providerIDs {
		provider := providers[providerID]
		modelKeys := make([]string, 0, len(provider.Models))
		for key := range provider.Models {
			modelKeys = append(modelKeys, key)
		}
		slices.Sort(modelKeys)
		for _, key := range modelKeys {
			spec, err := ResolveModel(providerID+"/"+key, providers, "")
			if err != nil {
				// ReadProviders has already validated this provider. Keeping the
				// projection defensive means a catalog never invents a partial
				// model entry if a future reader adds stricter resolution rules.
				continue
			}
			addCatalogModel(models, spec, true)
		}
	}

	addCatalogRole(models, settings.Main, "current")
	if settings.Compact != nil {
		addCatalogRole(models, *settings.Compact, "compact")
	}
	for i, spec := range settings.Fallbacks {
		addCatalogRole(models, spec, "fallback#"+strconv.Itoa(i+1))
	}

	refs := make([]string, 0, len(models))
	for ref, model := range models {
		if model.CanSwitch {
			refs = append(refs, ref)
		}
	}
	slices.Sort(refs)
	catalog.Switchable = refs
	catalog.Models = make([]ModelCatalogModel, 0, len(models))
	for _, ref := range sortedModelRefs(models) {
		model := *models[ref]
		model.Roles = append([]string(nil), model.Roles...)
		catalog.Models = append(catalog.Models, model)
	}
	return catalog
}

func buildCatalogProviders(providers map[string]ProviderConfig) []ModelCatalogProvider {
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]ModelCatalogProvider, 0, len(ids))
	for _, id := range ids {
		provider := providers[id]
		modelIDs := make([]string, 0, len(provider.Models))
		seen := make(map[string]struct{}, len(provider.Models))
		for key, model := range provider.Models {
			modelID := model.ModelID
			if modelID == "" {
				modelID = key
			}
			if _, ok := seen[modelID]; ok {
				continue
			}
			seen[modelID] = struct{}{}
			modelIDs = append(modelIDs, modelID)
		}
		slices.Sort(modelIDs)
		kind := "custom"
		if _, ok := presetProviders[id]; ok {
			kind = "preset"
		}
		out = append(out, ModelCatalogProvider{ID: id, Kind: kind, ModelIDs: modelIDs})
	}
	return out
}

func addCatalogRole(models map[string]*ModelCatalogModel, spec ModelSpec, role string) {
	if spec.Provider.ID == "" || spec.Model == "" {
		return
	}
	model := addCatalogModel(models, spec, false)
	if !slices.Contains(model.Roles, role) {
		model.Roles = append(model.Roles, role)
	}
	// A referenced model is a deliberate local candidate even when it has no
	// providers.<id>.models entry; arbitrary unreferenced preset names are not.
	model.CanSwitch = true
}

func addCatalogModel(models map[string]*ModelCatalogModel, spec ModelSpec, configured bool) *ModelCatalogModel {
	ref := spec.Ref()
	model, ok := models[ref]
	if !ok {
		model = &ModelCatalogModel{
			Ref: ref, Provider: spec.Provider.ID, Model: spec.Model,
			ModelID: spec.APIModel(), Type: spec.Provider.Type,
			ContextWindow: ContextWindowFor(spec), Thinking: spec.ThinkingLevel(),
			ThinkingStyle: ThinkingStyleFor(spec), StreamRequired: StreamRequiredFor(spec),
			Vision: VisionFor(spec),
			Roles:  []string{},
		}
		models[ref] = model
	}
	model.CanSwitch = model.CanSwitch || configured
	return model
}

func sortedModelRefs(models map[string]*ModelCatalogModel) []string {
	refs := make([]string, 0, len(models))
	for ref := range models {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	return refs
}

// SwitchableModelSpecs resolves the canonical switchable references from the
// catalog back to the specs used by runtime model switching. Keeping the
// catalog's ordered refs as the source ensures CLI output and chat candidates
// cannot drift or produce duplicate aliases.
func SwitchableModelSpecs(providers map[string]ProviderConfig, settings ModelSettings) ([]ModelSpec, error) {
	catalog := BuildModelCatalog(providers, settings)
	specs := make([]ModelSpec, 0, len(catalog.Switchable))
	for _, ref := range catalog.Switchable {
		spec, err := ResolveModel(ref, providers, "")
		if err != nil {
			return nil, fmt.Errorf("resolve switchable model %q: %w", ref, err)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// ModelCatalogSource reports the only environment override that changes the
// model selection shown by this command. It intentionally does not expose
// values from .env or any credential-bearing environment variable.
func ModelCatalogSource() string {
	if strings.TrimSpace(os.Getenv("SUGA_MODEL")) != "" {
		return "config+environment"
	}
	return "config"
}

// Ordering is owned by this projection; CLI renderers only consume its stable
// slices and must not iterate provider maps themselves.
