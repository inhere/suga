package wiring

// Provider, capability and price/mode builders used by Open.

import (
	"context"
	"fmt"
	"os"
	"strings"

	gkconfig "github.com/gookit/config/v2"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/llm"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// buildMainProvider builds the main provider without budget estimation. It is
// kept as the default helper for tests and callers that intentionally assemble
// an unbudgeted provider.
func buildMainProvider(settings appconfig.ModelSettings) (agentcore.Provider, error) {
	return buildMainProviderSpecs(settings.Main, settings.Fallbacks)
}

// buildMainProviderWithEstimate wraps every real provider before it enters the
// fallback pool. This preserves member capabilities and lets pool attempts carry
// their own model's estimated usage into the observer.
func buildMainProviderWithEstimate(settings appconfig.ModelSettings, options personal.EstimateOptions) (agentcore.Provider, error) {
	return buildMainProviderSpecsWithEstimate(settings.Main, settings.Fallbacks, options)
}

func buildMainProviderSpecs(mainSpec appconfig.ModelSpec, fallbacks []appconfig.ModelSpec) (agentcore.Provider, error) {
	return buildMainProviderSpecsWithEstimate(mainSpec, fallbacks, personal.EstimateOptions{Mode: personal.EstimateOff})
}

func buildMainProviderSpecsWithEstimate(mainSpec appconfig.ModelSpec, fallbacks []appconfig.ModelSpec, options personal.EstimateOptions) (agentcore.Provider, error) {
	main, err := buildProviderWithEstimate(mainSpec, options)
	if err != nil {
		return nil, err
	}
	if len(fallbacks) == 0 {
		return main, nil
	}
	providers := []llm.Provider{main}
	for i, spec := range fallbacks {
		fb, err := buildProviderWithEstimate(spec, options)
		if err != nil {
			return nil, fmt.Errorf("fallback_models[%d]: %w", i, err)
		}
		providers = append(providers, fb)
	}
	return llm.NewPool(providers), nil
}

func buildProviderWithEstimate(spec appconfig.ModelSpec, options personal.EstimateOptions) (agentcore.Provider, error) {
	// The global network.proxy selector is resolved once per provider; a bad
	// value fails startup here (see validateNetworkProxy).
	selector, err := networkProxySelector()
	if err != nil {
		return nil, err
	}
	provider, err := appconfig.BuildProviderWithProxy(spec, networkProxyFunc(selector))
	if err != nil {
		return nil, err
	}
	return personal.NewEstimatingProvider(provider, options), nil
}

// estimateOptionsForBudget disables the estimator when no budget is configured;
// otherwise it applies the resolved budget policy to every provider path.
func estimateOptionsForBudget(tokens int, costUSD float64, mode string, factor float64) personal.EstimateOptions {
	if tokens <= 0 && costUSD <= 0 {
		return personal.EstimateOptions{Mode: personal.EstimateOff}
	}
	if mode == "" {
		mode = personal.EstimateAuto
	}
	if factor <= 0 {
		factor = 1
	}
	return personal.EstimateOptions{Mode: mode, Factor: factor}
}

// configuredModelOptions uses the same canonical catalog projection as the
// offline models command. Runtime-only details such as provider specs and
// output limits are attached after the projection has fixed ref order. The
// model flag is included so an explicit current model is also selectable and
// receives the same current marker in chat.
func configuredModelOptions(model string) ([]ModelOption, map[string]appconfig.ModelSpec, error) {
	providers, err := appconfig.ReadProviders()
	if err != nil {
		return nil, nil, err
	}
	settings, err := appconfig.ReadModelSettings(model)
	if err != nil {
		return nil, nil, err
	}
	switchable, err := appconfig.SwitchableModelSpecs(providers, settings)
	if err != nil {
		return nil, nil, err
	}
	options := make([]ModelOption, 0, len(switchable))
	specs := make(map[string]appconfig.ModelSpec, len(switchable))
	for _, spec := range switchable {
		ref := spec.Ref()
		specs[ref] = spec
		options = append(options, ModelOption{Provider: spec.Provider.ID, Model: spec.Model,
			Thinking: spec.ThinkingLevel(), ContextWindow: appconfig.ContextWindowFor(spec),
			MaxOutputTokens: appconfig.MaxOutputTokensFor(spec)})
	}
	return options, specs, nil
}

func appendSystemPrompt(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("SUGA_APPEND_SYSTEM_PROMPT")
}

func buildPersonalCapabilities(workspace string, dirs *native.AddDirs, allowWrite bool) (defaultCapabilities, error) {
	return buildPersonalCapabilitiesWithServers(context.Background(), workspace, dirs, allowWrite, nil)
}

func buildPersonalCapabilitiesWithServers(ctx context.Context, workspace string, dirs *native.AddDirs, allowWrite bool, servers []mcp.ServerSettings) (defaultCapabilities, error) {
	return buildPersonalCapabilitiesWithServersAndSnapshot(ctx, workspace, dirs, allowWrite, servers, nil)
}

func buildPersonalCapabilitiesWithServersAndSnapshot(ctx context.Context, workspace string, dirs *native.AddDirs, allowWrite bool, servers []mcp.ServerSettings, snapshotter native.Snapshotter) (defaultCapabilities, error) {
	if allowWrite {
		return buildDefaultCapabilitiesWithServersAndSnapshot(ctx, workspace, dirs, servers, snapshotter)
	}
	// Read-only: only the catalog's read tools, and no MCP (ACP read_only
	// sessions must not start MCP servers).
	registry := tools.NewRegistry()
	for _, tool := range native.BuildTools(nativeEnvWithSnapshot(workspace, dirs, snapshotter), false) {
		registry.Register(tool)
	}
	if err := applyDisabledTools(registry); err != nil {
		return defaultCapabilities{}, err
	}
	return defaultCapabilities{Registry: registry}, nil
}

func buildPersonalCLICapabilitiesWithSnapshot(ctx context.Context, workspace string, dirs *native.AddDirs, allowWrite bool, servers []mcp.ServerSettings, snapshotter native.Snapshotter) (defaultCapabilities, error) {
	if allowWrite {
		return buildDefaultCapabilitiesWithServersAndSnapshot(ctx, workspace, dirs, servers, snapshotter)
	}
	built, err := buildPersonalCapabilitiesWithServers(ctx, workspace, dirs, false, nil)
	if err != nil {
		return defaultCapabilities{}, err
	}
	configured, err := appconfig.ReadMCPServerSettings()
	if err != nil {
		_ = built.Close()
		return defaultCapabilities{}, err
	}
	for _, settings := range append(configured, servers...) {
		provider, err := mcpProviderFactory(ctx, settings)
		if err != nil {
			_ = built.Close()
			return defaultCapabilities{}, fmt.Errorf("build mcp provider %q: %w", settings.ID, err)
		}
		if err := provider.RegisterTools(built.Registry); err != nil {
			_ = provider.Close()
			_ = built.Close()
			return defaultCapabilities{}, fmt.Errorf("register mcp tools for %q: %w", settings.ID, err)
		}
		built.recordConflicts(provider)
		built.closers = append(built.closers, provider)
	}
	// The read-only builder already applied tools.disabled; the MCP tools were
	// registered after that, so drop the disabled ones among them here.
	removeDisabledTools(built.Registry)
	return built, nil
}

// priceLookup adapts the configured model prices for personal, which never
// reads global configuration itself.
func priceLookup(settings appconfig.ModelSettings) func(provider, model string) (personal.Price, bool) {
	lookup := settings.PriceLookup()
	return func(provider, model string) (personal.Price, bool) {
		p, ok := lookup(provider, model)
		return personal.Price{Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite}, ok
	}
}

// runMode names the run mode for turn_context events.
func runMode(allowWrite bool) string {
	if allowWrite {
		return personal.ModeAllowWrite
	}
	return personal.ModeReadOnly
}

// toolNameList reads a list of tool names; unset means def, an explicit empty
// list means none. Anything but a list of non-empty strings is a startup
// error.
func toolNameList(key string, def []string) ([]string, error) {
	raw := gkconfig.Get(key)
	if raw == nil {
		return def, nil
	}
	var items []any
	switch value := raw.(type) {
	case []string:
		for _, v := range value {
			items = append(items, v)
		}
	case []any:
		items = value
	default:
		return nil, fmt.Errorf("%s must be a list of tool names", key)
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s must contain non-empty tool names, got %v", key, item)
		}
		names = append(names, strings.TrimSpace(name))
	}
	return names, nil
}
