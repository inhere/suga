package axconfig

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/envutil"

	"github.com/inhere/suga/pkg/agentcore/llm"
)

// Provider protocol types accepted in providers.<id>.type.
const (
	ProviderTypeOpenAI    = "openai"
	ProviderTypeAnthropic = "anthropic"
	// ProviderTypeOpenAIResponses is the OpenAI Responses API
	// (POST {base_url}/responses), see llm.ResponsesProvider.
	ProviderTypeOpenAIResponses = "openai-responses"
)

// ModelConfig is the per-model override under providers.<id>.models.<model>.
type ModelConfig struct {
	// ModelID (models.<key>.model_id) is the model id sent to the provider when
	// it differs from the configured key, so a long upstream id such as
	// "deepseek/deepseek-v4.1-flash" can be referenced by a short key.
	ModelID string
	// Type and BaseURL (models.<key>.type / base_url) override the provider's
	// for this model: a gateway such as command-code serves Claude models over
	// the Anthropic protocol and the others over OpenAI's, often at another
	// base URL. "" keeps the provider's.
	Type, BaseURL string
	ContextWindow int
	// MaxOutputTokens (models.<key>.max_output_tokens) caps how many tokens
	// this model may answer with; 0 falls back to the provider's value, see
	// ProviderConfig.MaxOutputTokens.
	MaxOutputTokens int
	// Thinking (models.<key>.thinking) is the model's thinking level, see
	// ThinkingLevels; "" falls back to the provider's.
	Thinking string
	// ThinkingStyle (models.<key>.thinking_style) pins the protocol shape of
	// extended thinking (budget|adaptive|none, see ThinkingStyles) instead of
	// guessing it from the model name; "" falls back to the provider's, and
	// then to the model-name heuristic.
	ThinkingStyle string
	// StreamRequired (models.<key>.stream_required) marks a model whose endpoint
	// answers only streamed requests: the provider sends every request with
	// stream=true. StreamRequiredSet records that the key was written, so an
	// explicit false overrides the provider level.
	StreamRequired    bool
	StreamRequiredSet bool
	// Vision (models.<key>.vision) declares whether the model accepts images.
	// VisionSet records that the key was written, so an explicit false overrides
	// the provider level and an unset key falls back to the built-in list (see
	// VisionFor).
	Vision    bool
	VisionSet bool
	// Price is the optional per-million-token price used to compute cost when
	// the provider reports none; the zero value means "no price configured".
	Price Price
}

// Price is a model price in US dollars per million tokens
// (providers.<id>.models.<model>.price: input, output, cache_read, cache_write).
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
}

// IsZero reports whether no price is configured.
func (p Price) IsZero() bool { return p == Price{} }

// ProviderConfig is one resolved providers.<id> entry (preset merged with user
// config). Headers values may contain llm.SessionIDPlaceholder, kept verbatim.
type ProviderConfig struct {
	ID      string
	Type    string // openai | anthropic
	BaseURL string
	APIKey  string
	Headers map[string]string
	Models  map[string]ModelConfig
	// ContextWindow (providers.<id>.context_window) is the window of every
	// model of this provider without its own models.<model>.context_window;
	// 0 = infer from the model name.
	ContextWindow int
	// MaxOutputTokens (providers.<id>.max_output_tokens) is the output limit of
	// every model of this provider without its own models.<model> value; 0
	// leaves the limit to the protocol (see MaxOutputTokensFor).
	MaxOutputTokens int
	// Thinking (providers.<id>.thinking) is the default thinking level of the
	// provider's models; "" leaves the provider's own default.
	Thinking string
	// ThinkingStyle (providers.<id>.thinking_style) is the default thinking
	// style of the provider's models; "" leaves the model-name heuristic.
	ThinkingStyle string
	// StreamRequired (providers.<id>.stream_required) sends every request of
	// this provider's models as a streamed request, summaries included: some
	// gateways answer only stream=true.
	StreamRequired bool
	// Vision (providers.<id>.vision) declares whether the provider's models
	// accept images; VisionSet records that the key was written. A gateway that
	// rejects image parts sets it to false.
	Vision    bool
	VisionSet bool
	// PromptCache (providers.<id>.prompt_cache) enables Claude cache_control
	// breakpoints; only meaningful for type anthropic.
	PromptCache bool
	// Compat (providers.<id>.compat) names gateway adapters applied on top
	// of the ones matched by base URL and model (llm.ResolveCompat).
	Compat []string
	// Retry is the retry policy: the global retry: section (ReadRetryPolicy),
	// overridden by providers.<id>.retry.
	Retry llm.RetryPolicy
	// RequestTimeout and ResponseHeaderTimeout bound one model request: they
	// come from `timeouts:` (ReadRequestTimeouts), overridden by
	// providers.<id>.timeouts. A zero value means no limit; unset keys keep the
	// default (see config/timeouts.go).
	RequestTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	// StreamIdleTimeout bounds the silence between two reads of a streamed body
	// (timeouts.stream_idle, overridden by providers.<id>.timeouts.stream_idle).
	// Zero keeps the provider default, a negative value ("off") disables it.
	StreamIdleTimeout time.Duration
}

// ModelSpec is a model bound to the provider that serves it.
type ModelSpec struct {
	Provider ProviderConfig
	Model    string
	// Thinking overrides the configured thinking level (--thinking for the
	// main model); "" uses the configuration, see ThinkingLevel.
	Thinking string
}

// Ref returns the canonical "provider/model" form.
func (s ModelSpec) Ref() string { return s.Provider.ID + "/" + s.Model }

// APIModel is the model id sent to the provider: models.<key>.model_id when
// set, otherwise the key itself. Everything else (refs, trace, stats, price
// and context window lookups) uses the key.
func (s ModelSpec) APIModel() string {
	if cfg, ok := s.Provider.Models[s.Model]; ok && cfg.ModelID != "" {
		return cfg.ModelID
	}
	return s.Model
}

// ModelSettings is the effective model selection: main, optional compact
// (summary) model and ordered fallbacks.
type ModelSettings struct {
	Main      ModelSpec
	Compact   *ModelSpec
	Fallbacks []ModelSpec
}

// presetProviders are the built-in provider ids. api_key comes from KeyEnv.
var presetProviders = map[string]struct {
	Type, BaseURL, KeyEnv string
}{
	"openai":    {ProviderTypeOpenAI, "https://api.openai.com/v1", "OPENAI_API_KEY"},
	"anthropic": {ProviderTypeAnthropic, "https://api.anthropic.com", "ANTHROPIC_API_KEY"},
	"deepseek":  {ProviderTypeOpenAI, "https://api.deepseek.com/v1", "DEEPSEEK_API_KEY"},
}

// ReadProviders returns every defined provider: the built-in presets, each
// overridden field by field by the same-named providers.<id> entry (empty
// fields never override), plus user-defined providers, which must set type.
func ReadProviders() (map[string]ProviderConfig, error) {
	providers := make(map[string]ProviderConfig, len(presetProviders))
	for id, p := range presetProviders {
		providers[id] = ProviderConfig{
			ID:      id,
			Type:    p.Type,
			BaseURL: p.BaseURL,
			APIKey:  strings.TrimSpace(os.Getenv(p.KeyEnv)),
		}
	}
	// Request timeouts are `timeouts:` for every provider, overridden per
	// provider below (`providers.<id>.timeouts`).
	timeouts, err := ReadRequestTimeouts()
	if err != nil {
		return nil, err
	}
	for id, p := range providers {
		p.RequestTimeout, p.ResponseHeaderTimeout, p.StreamIdleTimeout = timeouts.Request, timeouts.ResponseHeader, timeouts.StreamIdle
		providers[id] = p
	}
	// Every provider retries with the global policy until providers.<id>.retry
	// overrides it (read below, together with the other provider fields).
	retry, err := ReadRetryPolicy()
	if err != nil {
		return nil, err
	}
	for id, p := range providers {
		p.Retry = retry
		providers[id] = p
	}

	raw, _ := gkconfig.Get("providers").(map[string]any)
	for id, item := range raw {
		id = strings.TrimSpace(id)
		entry, _ := item.(map[string]any)
		user := ProviderConfig{
			ID:      id,
			Type:    normalizeConfigToken(stringValue(entry["type"])),
			BaseURL: expandEnv(stringValue(entry["base_url"])),
			APIKey:  expandEnv(stringValue(entry["api_key"])),
			Headers: expandEnvMap(normalizeStringMap(entry["headers"])),
			Models:  readModelConfigs(entry["models"]),
			// Negative or non-numeric counts as unset, like the per-model value.
			ContextWindow:   intValue(entry["context_window"]),
			MaxOutputTokens: intValue(entry["max_output_tokens"]),
			Thinking:        stringValue(entry["thinking"]),
			ThinkingStyle:   stringValue(entry["thinking_style"]),
		}
		promptCache, promptCacheSet := entry["prompt_cache"].(bool)
		streamRequired, streamRequiredSet := entry["stream_required"].(bool)
		vision, visionSet := entry["vision"].(bool)
		user.Compat = normalizeStringList(entry["compat"])

		if err := normalizeThinking(id, &user); err != nil {
			return nil, err
		}
		merged, isPreset := providers[id]
		if !isPreset {
			merged = ProviderConfig{ID: id}
		}
		merged.Type = firstNonEmpty(user.Type, merged.Type)
		merged.BaseURL = firstNonEmpty(user.BaseURL, merged.BaseURL)
		merged.APIKey = firstNonEmpty(user.APIKey, merged.APIKey)
		if user.Headers != nil {
			merged.Headers = user.Headers
		}
		if user.Models != nil {
			merged.Models = user.Models
		}
		if user.ContextWindow > 0 {
			merged.ContextWindow = user.ContextWindow
		}
		if user.MaxOutputTokens > 0 {
			merged.MaxOutputTokens = user.MaxOutputTokens
		}
		merged.Thinking = firstNonEmpty(user.Thinking, merged.Thinking)
		merged.ThinkingStyle = firstNonEmpty(user.ThinkingStyle, merged.ThinkingStyle)
		if promptCacheSet {
			merged.PromptCache = promptCache
		}
		if streamRequiredSet {
			merged.StreamRequired = streamRequired
		}
		if visionSet {
			merged.Vision, merged.VisionSet = vision, true
		}
		if len(user.Compat) > 0 {
			merged.Compat = user.Compat
		}
		// The global retry: policy is the default; providers.<id>.retry changes
		// only the fields it names.
		merged.Retry = retry
		if section := mapValue(entry["retry"]); section != nil {
			if merged.Retry, err = applyRetryPolicy(retry, section, "providers."+id+".retry"); err != nil {
				return nil, err
			}
		}
		mergedTimeouts, err := applyTimeouts(timeouts, mapValue(entry["timeouts"]), "providers."+id+".timeouts")
		if err != nil {
			return nil, err
		}
		merged.RequestTimeout, merged.ResponseHeaderTimeout, merged.StreamIdleTimeout = mergedTimeouts.Request, mergedTimeouts.ResponseHeader, mergedTimeouts.StreamIdle
		if merged.Type == "" {
			return nil, fmt.Errorf("providers.%s: type is required for a non-preset provider (openai|openai-responses|anthropic)", id)
		}
		if !validProviderType(merged.Type) {
			return nil, fmt.Errorf("providers.%s: unsupported type %q (want openai|openai-responses|anthropic)", id, merged.Type)
		}
		for key, cfg := range merged.Models {
			if cfg.Type != "" && !validProviderType(cfg.Type) {
				return nil, fmt.Errorf("providers.%s.models.%s: unsupported type %q (want openai|openai-responses|anthropic)", id, key, cfg.Type)
			}
		}
		providers[id] = merged
	}
	return providers, nil
}

func validProviderType(t string) bool {
	return t == ProviderTypeOpenAI || t == ProviderTypeOpenAIResponses || t == ProviderTypeAnthropic
}

// normalizeThinking validates the provider's and its models' thinking levels
// and thinking styles (both accept the same flexible case).
func normalizeThinking(id string, p *ProviderConfig) error {
	level, err := ParseThinking(p.Thinking)
	if err != nil {
		return fmt.Errorf("providers.%s.thinking: %w", id, err)
	}
	p.Thinking = level
	style, err := ParseThinkingStyle(p.ThinkingStyle)
	if err != nil {
		return fmt.Errorf("providers.%s.thinking_style: %w", id, err)
	}
	p.ThinkingStyle = style
	for key, cfg := range p.Models {
		if cfg.Thinking, err = ParseThinking(cfg.Thinking); err != nil {
			return fmt.Errorf("providers.%s.models.%s.thinking: %w", id, key, err)
		}
		if cfg.ThinkingStyle, err = ParseThinkingStyle(cfg.ThinkingStyle); err != nil {
			return fmt.Errorf("providers.%s.models.%s.thinking_style: %w", id, key, err)
		}
		p.Models[key] = cfg
	}
	return nil
}

// expandEnv resolves ${VAR} references. The raw map read of providers bypasses
// gkconfig's ParseEnv (which only applies to typed getters), so do it here.
func expandEnv(v string) string { return strings.TrimSpace(envutil.ParseEnvValue(v)) }

func expandEnvMap(m map[string]string) map[string]string {
	for k, v := range m {
		m[k] = expandEnv(v)
	}
	return m
}

// readModelConfigs parses providers.<id>.models. It reads the raw map rather
// than a dotted key path because model ids may contain dots (e.g. gpt-4.1).
func readModelConfigs(raw any) map[string]ModelConfig {
	items, ok := raw.(map[string]any)
	if !ok || len(items) == 0 {
		return nil
	}
	models := make(map[string]ModelConfig, len(items))
	for name, item := range items {
		var cfg ModelConfig
		if entry, ok := item.(map[string]any); ok {
			if id, ok := entry["model_id"].(string); ok {
				cfg.ModelID = strings.TrimSpace(id)
			}
			cfg.Type = normalizeConfigToken(stringValue(entry["type"]))
			cfg.BaseURL = expandEnv(stringValue(entry["base_url"]))
			cfg.ContextWindow = intValue(entry["context_window"])
			cfg.MaxOutputTokens = intValue(entry["max_output_tokens"])
			cfg.Thinking = stringValue(entry["thinking"])
			cfg.ThinkingStyle = stringValue(entry["thinking_style"])
			if v, ok := entry["stream_required"].(bool); ok {
				cfg.StreamRequired, cfg.StreamRequiredSet = v, true
			}
			if v, ok := entry["vision"].(bool); ok {
				cfg.Vision, cfg.VisionSet = v, true
			}
			cfg.Price = readPrice(entry["price"])
		}
		models[strings.TrimSpace(name)] = cfg
	}
	return models
}

// readPrice parses a models.<model>.price map; negative or non-numeric values
// count as 0 (unset).
func readPrice(raw any) Price {
	entry, ok := raw.(map[string]any)
	if !ok {
		return Price{}
	}
	return Price{
		Input: floatValue(entry["input"]), Output: floatValue(entry["output"]),
		CacheRead: floatValue(entry["cache_read"]), CacheWrite: floatValue(entry["cache_write"]),
	}
}

func floatValue(raw any) float64 {
	var v float64
	switch n := raw.(type) {
	case int:
		v = float64(n)
	case int64:
		v = float64(n)
	case uint64:
		v = float64(n)
	case float64:
		v = n
	case float32:
		v = float64(n)
	}
	return max(v, 0)
}

func intValue(raw any) int {
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case uint64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// ResolveModel parses "provider/model" or a bare model id. It splits only at
// the first "/" and only when the prefix is a defined provider; otherwise the
// whole string is the model id (e.g. "meta/llama-3") served by defaultProvider.
//
// A name that is no configured model of a provider listing its models (and a
// bare name without a default provider) is then looked up loosely, see
// matchModels: a single match is used, several are an error listing them,
// none keeps the name as it is.
func ResolveModel(ref string, providers map[string]ProviderConfig, defaultProvider string) (ModelSpec, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ModelSpec{}, errors.New("model reference is empty")
	}
	providerID, model, explicit := defaultProvider, ref, false
	if prefix, rest, found := strings.Cut(ref, "/"); found {
		if _, defined := providers[prefix]; defined {
			providerID, model, explicit = prefix, rest, true
		}
	}
	if model == "" {
		return ModelSpec{}, fmt.Errorf("model reference %q has no model id", ref)
	}
	if providerID == "" {
		if spec, ok, err := looseModel(ref, model, providers, ""); ok || err != nil {
			return spec, err
		}
		return ModelSpec{}, fmt.Errorf("model %q has no provider; use <provider>/<model>", ref)
	}
	provider, ok := providers[providerID]
	if !ok {
		return ModelSpec{}, fmt.Errorf("provider %q is not defined (presets: openai, anthropic, deepseek; add it under providers:)", providerID)
	}
	key := modelKey(provider, model)
	if _, listed := provider.Models[key]; !listed && len(provider.Models) > 0 {
		scope := ""
		if explicit {
			scope = providerID
		}
		if spec, ok, err := looseModel(ref, model, providers, scope); ok || err != nil {
			return spec, err
		}
	}
	return bindModel(provider, key), nil
}

// bindModel returns the spec of a model key: a model's own type and base_url
// win over the provider's; the spec gets its own copy of the provider, so
// everything downstream (BuildProvider, manifest, thinking checks) sees the
// protocol actually used.
func bindModel(provider ProviderConfig, key string) ModelSpec {
	if cfg, ok := provider.Models[key]; ok {
		provider.Type = firstNonEmpty(cfg.Type, provider.Type)
		provider.BaseURL = firstNonEmpty(cfg.BaseURL, provider.BaseURL)
	}
	return ModelSpec{Provider: provider, Model: key}
}

// maxListedMatches bounds the candidates an ambiguous model name lists.
const maxListedMatches = 20

// looseModel resolves name with matchModels in the provider scope ("" = all
// providers): ok with the single match, an error naming the candidates when
// there are several, neither when nothing matches.
func looseModel(ref, name string, providers map[string]ProviderConfig, scope string) (ModelSpec, bool, error) {
	matches := matchModels(name, providers, scope)
	switch len(matches) {
	case 0:
		return ModelSpec{}, false, nil
	case 1:
		return bindModel(providers[matches[0].provider], matches[0].key), true, nil
	}
	names := make([]string, 0, min(len(matches), maxListedMatches))
	for i, m := range matches {
		if i == maxListedMatches {
			names = append(names, fmt.Sprintf("... %d more", len(matches)-i))
			break
		}
		names = append(names, m.provider+"/"+m.key)
	}
	return ModelSpec{}, false, fmt.Errorf("model %q matches several configured models: %s; give one of them", ref, strings.Join(names, ", "))
}

type modelMatch struct{ provider, key string }

// matchModels finds the configured models (providers.<id>.models keys and
// their model_id) containing name, case-insensitively; when some equal it,
// only those count. scope limits the search to one provider. The result is
// sorted by provider and key.
func matchModels(name string, providers map[string]ProviderConfig, scope string) []modelMatch {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return nil
	}
	var exact, partial []modelMatch
	for id, p := range providers {
		if scope != "" && id != scope {
			continue
		}
		for key, cfg := range p.Models {
			k, mid := strings.ToLower(key), strings.ToLower(cfg.ModelID)
			switch {
			case k == want || (mid != "" && mid == want):
				exact = append(exact, modelMatch{id, key})
			case strings.Contains(k, want) || (mid != "" && strings.Contains(mid, want)):
				partial = append(partial, modelMatch{id, key})
			}
		}
	}
	out := partial
	if len(exact) > 0 {
		out = exact
	}
	slices.SortFunc(out, func(a, b modelMatch) int {
		return strings.Compare(a.provider+"/"+a.key, b.provider+"/"+b.key)
	})
	return out
}

// modelKey maps a model reference to its models.<key>: the key itself, or the
// key whose model_id it names (so the full upstream id keeps working after a
// model is given a short key). Anything else is returned unchanged.
func modelKey(provider ProviderConfig, model string) string {
	if _, ok := provider.Models[model]; ok {
		return model
	}
	keys := make([]string, 0, len(provider.Models))
	for key, cfg := range provider.Models {
		if cfg.ModelID == model {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return model
	}
	slices.Sort(keys) // deterministic when two keys share a model_id
	return keys[0]
}

// unlistedModelWarning returns a warning when the provider lists models but
// spec's model is none of them: usually a typo the provider would reject with
// a less helpful message. Providers without a models list are not checked.
func unlistedModelWarning(what string, spec ModelSpec) string {
	if len(spec.Provider.Models) == 0 {
		return ""
	}
	if _, ok := spec.Provider.Models[spec.Model]; ok {
		return ""
	}
	keys := make([]string, 0, len(spec.Provider.Models))
	for key := range spec.Provider.Models {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return fmt.Sprintf("%s: %q is not under providers.%s.models (configured: %s); sending it to the provider as is",
		what, spec.Model, spec.Provider.ID, strings.Join(keys, ", "))
}

// CheckLegacyConfig rejects the removed llm: section and SUGA_PROVIDER.
func CheckLegacyConfig() error {
	if gkconfig.Get("llm") != nil {
		return errors.New("llm: section is no longer supported; use `model: <provider>/<model>` and `providers:` (see docs/usage/llm-config-v0.md)")
	}
	if strings.TrimSpace(os.Getenv("SUGA_PROVIDER")) != "" {
		return errors.New("SUGA_PROVIDER is no longer supported; use SUGA_MODEL=<provider>/<model> (or --model <provider>/<model>)")
	}
	return nil
}

// ReadModelSettings resolves the main model (flagModel > SUGA_MODEL > model),
// compact_model and fallback_models. The main ref's provider is the default
// provider for bare model ids in compact_model and fallback_models.
func ReadModelSettings(flagModel string) (ModelSettings, error) {
	if err := CheckLegacyConfig(); err != nil {
		return ModelSettings{}, err
	}
	providers, err := ReadProviders()
	if err != nil {
		return ModelSettings{}, err
	}

	// The configured model names the default provider; a bare --model or
	// SUGA_MODEL (no known provider prefix) then selects a model of that provider.
	configRef := strings.TrimSpace(gkconfig.String("model"))
	defaultProvider := ""
	if configRef != "" {
		spec, err := ResolveModel(configRef, providers, "")
		if err != nil {
			return ModelSettings{}, fmt.Errorf("model: %w", err)
		}
		defaultProvider = spec.Provider.ID
	}
	mainRef := firstNonEmpty(flagModel, os.Getenv("SUGA_MODEL"), configRef)
	if strings.TrimSpace(mainRef) == "" {
		return ModelSettings{}, errors.New("model is not configured; set `model: <provider>/<model>` (or SUGA_MODEL / --model)")
	}
	main, err := ResolveModel(mainRef, providers, defaultProvider)
	if err != nil {
		return ModelSettings{}, fmt.Errorf("model: %w", err)
	}
	settings := ModelSettings{Main: main}
	if w := unlistedModelWarning("model", main); w != "" {
		slog.Warn(w)
	}

	if ref := strings.TrimSpace(gkconfig.String("compact_model")); ref != "" {
		spec, err := ResolveModel(ref, providers, main.Provider.ID)
		if err != nil {
			return ModelSettings{}, fmt.Errorf("compact_model: %w", err)
		}
		settings.Compact = &spec
	}
	for i, ref := range normalizeStringList(gkconfig.Get("fallback_models")) {
		spec, err := ResolveModel(ref, providers, main.Provider.ID)
		if err != nil {
			return ModelSettings{}, fmt.Errorf("fallback_models[%d]: %w", i, err)
		}
		settings.Fallbacks = append(settings.Fallbacks, spec)
	}
	return settings, nil
}

// ContextWindowFor returns providers.<id>.models.<model>.context_window when
// set, then providers.<id>.context_window, otherwise the window inferred from
// the model id sent to the provider.
func ContextWindowFor(spec ModelSpec) int {
	if cfg, ok := spec.Provider.Models[spec.Model]; ok && cfg.ContextWindow > 0 {
		return cfg.ContextWindow
	}
	if spec.Provider.ContextWindow > 0 {
		return spec.Provider.ContextWindow
	}
	return ContextWindowForModel(spec.APIModel())
}

// ThinkingStyleFor returns the configured thinking style of spec's model
// (ThinkingStyles): providers.<id>.models.<model>.thinking_style, else
// providers.<id>.thinking_style, else "" for the model-name heuristic (see
// llm.applyClaudeThinking).
func ThinkingStyleFor(spec ModelSpec) string {
	if cfg, ok := spec.Provider.Models[spec.Model]; ok && cfg.ThinkingStyle != "" {
		return cfg.ThinkingStyle
	}
	return spec.Provider.ThinkingStyle
}

// StreamRequiredFor reports whether spec's model must be requested with
// stream=true: providers.<id>.models.<model>.stream_required when written, else
// providers.<id>.stream_required.
func StreamRequiredFor(spec ModelSpec) bool {
	if cfg, ok := spec.Provider.Models[spec.Model]; ok && cfg.StreamRequiredSet {
		return cfg.StreamRequired
	}
	return spec.Provider.StreamRequired
}

// MaxOutputTokensFor returns the configured output token limit of spec's
// model: providers.<id>.models.<model>.max_output_tokens, else
// providers.<id>.max_output_tokens, else 0 for "no limit configured", which
// leaves the limit to the provider protocol (see ResolveMaxOutputTokens).
func MaxOutputTokensFor(spec ModelSpec) int {
	if cfg, ok := spec.Provider.Models[spec.Model]; ok && cfg.MaxOutputTokens > 0 {
		return cfg.MaxOutputTokens
	}
	return spec.Provider.MaxOutputTokens
}

// PriceFor returns the configured price of spec's model.
func PriceFor(spec ModelSpec) (Price, bool) {
	cfg, ok := spec.Provider.Models[spec.Model]
	return cfg.Price, ok && !cfg.Price.IsZero()
}

// PriceLookup returns a (provider id, model) price lookup over the main,
// compact and fallback models of s. Models outside these are unknown.
func (s ModelSettings) PriceLookup() func(provider, model string) (Price, bool) {
	specs := append([]ModelSpec{s.Main}, s.Fallbacks...)
	if s.Compact != nil {
		specs = append(specs, *s.Compact)
	}
	return func(provider, model string) (Price, bool) {
		for _, spec := range specs {
			if spec.Provider.ID == provider && spec.Model == model {
				return PriceFor(spec)
			}
		}
		return Price{}, false
	}
}
