package axconfig

import (
	"fmt"
	"time"

	"github.com/inhere/suga/pkg/agentcore/llm"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
)

// BuildProvider constructs the llm provider for one resolved model. The
// provider Name is the configured provider id so trace/stats record it.
func BuildProvider(spec ModelSpec) (llm.Provider, error) {
	return BuildProviderWithProxy(spec, nil)
}

// BuildProviderWithProxy additionally pins the global network.proxy selector on
// the provider's HTTP clients: a matched host goes through the proxy, every
// other host connects directly. A nil selector keeps the default transport,
// which follows the environment proxies.
func BuildProviderWithProxy(spec ModelSpec, proxy netproxy.ProxyFunc) (llm.Provider, error) {
	p := spec.Provider
	switch p.Type {
	case ProviderTypeAnthropic:
		return llm.NewClaudeProvider(llm.ClaudeConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   spec.APIModel(),
			Headers: p.Headers,
			Name:    p.ID,
			// Claude cache breakpoints, see ProviderConfig.PromptCache.
			PromptCache: p.PromptCache,
			// Extended thinking: adaptive + effort, or a token budget on
			// older models (see llm.applyClaudeThinking).
			Thinking:       spec.ThinkingLevel(),
			ThinkingStyle:  ThinkingStyleFor(spec),
			StreamRequired: StreamRequiredFor(spec),
			// A model without vision gets text notes instead of image parts (a
			// fallback model served by another provider keeps its own value).
			NoImages: !VisionFor(spec),
			Retry:    p.Retry,
			// Request bounds: timeouts.*, providers.<id>.timeouts.
			RequestTimeout:        llmRequestTimeout(p.RequestTimeout),
			ResponseHeaderTimeout: llmRequestTimeout(p.ResponseHeaderTimeout),
			// StreamIdleTimeout keeps its own convention: 0 = the provider's
			// default, negative ("off") = no bound.
			StreamIdleTimeout: p.StreamIdleTimeout,
			Proxy:             proxy,
		}), nil
	case ProviderTypeOpenAIResponses:
		return llm.NewResponsesProvider(llm.ResponsesConfig{
			OpenAIConfig: llm.OpenAIConfig{
				APIKey:  p.APIKey,
				BaseURL: p.BaseURL,
				Model:   spec.APIModel(),
				Name:    p.ID,
				Headers: p.Headers,
				// Thinking level as reasoning.effort ("off" is "none").
				ReasoningEffort: reasoningEffort(spec.ThinkingLevel()),
				Retry:           p.Retry,
				StreamRequired:  StreamRequiredFor(spec),
				NoImages:        !VisionFor(spec),
				// Request bounds: timeouts.*, providers.<id>.timeouts.
				RequestTimeout:        llmRequestTimeout(p.RequestTimeout),
				ResponseHeaderTimeout: llmRequestTimeout(p.ResponseHeaderTimeout),
				// StreamIdleTimeout keeps its own convention: 0 = the provider's
				// default, negative ("off") = no bound.
				StreamIdleTimeout: p.StreamIdleTimeout,
				Proxy:             proxy,
			},
			Compat: p.Compat,
		}), nil
	case ProviderTypeOpenAI:
		return llm.NewOpenAIProvider(llm.OpenAIConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   spec.APIModel(),
			Name:    p.ID,
			Headers: p.Headers,
			// Thinking level as reasoning_effort, see reasoningEffort.
			ReasoningEffort: reasoningEffort(spec.ThinkingLevel()),
			Retry:           p.Retry,
			StreamRequired:  StreamRequiredFor(spec),
			NoImages:        !VisionFor(spec),
			// Request bounds: timeouts.*, providers.<id>.timeouts.
			RequestTimeout:        llmRequestTimeout(p.RequestTimeout),
			ResponseHeaderTimeout: llmRequestTimeout(p.ResponseHeaderTimeout),
			// StreamIdleTimeout keeps its own convention: 0 = the provider's
			// default, negative ("off") = no bound.
			StreamIdleTimeout: p.StreamIdleTimeout,
			Proxy:             proxy,
		}), nil
	default:
		return nil, fmt.Errorf("providers.%s: unsupported type %q (want openai|openai-responses|anthropic)", p.ID, p.Type)
	}
}

// llmRequestTimeout maps a resolved config timeout onto the llm package's
// convention: a non-positive config value means "no limit" and becomes
// llm.NoTimeout there, while 0 in the llm config asks for the package default.
func llmRequestTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return llm.NoTimeout
	}
	return d
}
