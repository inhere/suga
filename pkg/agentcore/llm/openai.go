package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
)

const defaultOpenAIBase = "https://api.openai.com/v1"

// OpenAIProvider implements Provider for OpenAI-compatible APIs
// (OpenAI, DeepSeek, Ollama, etc.).
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
	// streamClient has no overall timeout: a streamed body may last minutes. Its
	// response header wait is bounded (ResponseHeaderTimeout though).
	streamClient *http.Client
	name         string
	headers      map[string]string
	// reasoningEffort is sent as reasoning_effort when set (OpenAIConfig).
	reasoningEffort string
	// retry is the provider's shared retry state (OpenAIConfig.Retry).
	retry *backoff
}

// OpenAIConfig configures an OpenAIProvider.
type OpenAIConfig struct {
	APIKey  string
	BaseURL string // default: https://api.openai.com/v1
	Model   string // default: gpt-4o
	Name    string // default: "openai"
	// Headers are extra request headers; see applyHeaders for SessionIDPlaceholder.
	Headers map[string]string
	// ReasoningEffort is sent as the reasoning_effort request field (e.g.
	// "low", "high", "none"); empty leaves the provider's default.
	ReasoningEffort string
	// Retry is the retry policy; zero fields take DefaultRetryPolicy's.
	Retry RetryPolicy
	// RequestTimeout bounds one non-streamed request as a whole
	// (http.Client.Timeout); zero takes DefaultRequestTimeout and NoTimeout
	// disables the bound. See timeouts.go.
	RequestTimeout time.Duration
	// ResponseHeaderTimeout bounds the wait for a streamed request's response
	// headers; zero takes DefaultResponseHeaderTimeout and NoTimeout disables
	// the bound.
	ResponseHeaderTimeout time.Duration
	// Proxy routes requests through the global network.proxy selector; nil keeps
	// the default transport, which follows the environment proxies.
	Proxy netproxy.ProxyFunc
}

// NewOpenAIProvider creates an OpenAIProvider from cfg.
func NewOpenAIProvider(cfg OpenAIConfig) *OpenAIProvider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultOpenAIBase
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o"
	}
	if cfg.Name == "" {
		cfg.Name = "openai"
	}
	return &OpenAIProvider{
		apiKey:       cfg.APIKey,
		baseURL:      cfg.BaseURL,
		model:        cfg.Model,
		client:       newProviderClient(requestTimeout(cfg.RequestTimeout), proxyTransport(cfg.Proxy)),
		streamClient: newStreamClient(responseHeaderTimeout(cfg.ResponseHeaderTimeout), cfg.Proxy),
		name:         cfg.Name,
		headers:      cfg.Headers,

		reasoningEffort: cfg.ReasoningEffort,
		retry:           newBackoff(cfg.Retry),
	}
}

// Name implements Provider.
func (p *OpenAIProvider) Name() string  { return p.name }
func (p *OpenAIProvider) Model() string { return p.model }

// SupportsToolChoice implements api.ToolChooser: chat/completions takes the
// tool_choice field ("none" included).
func (p *OpenAIProvider) SupportsToolChoice() bool { return true }

// Generate implements the public agentcore provider contract.
func (p *OpenAIProvider) Generate(ctx context.Context, req *agentcoreapi.GenerateRequest) (*agentcoreapi.GenerateResult, error) {
	ctx = ensureRetryReport(ctx)
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := p.buildRequestBody(req, model, false)
	logProviderRequest(p.Name(), "/chat/completions", body)
	respBody, status, err := p.doRequest(ctx, "/chat/completions", body)
	if err != nil {
		return nil, err
	}

	res, err := p.parseResponse(respBody)
	if err != nil {
		// A rejected request (4xx) keeps the API's own message but is classified by
		// its status (ErrorKind, ShouldFallback).
		return nil, statusErr(status, withRequestID(ctx, err))
	}
	// The limit this request asked for; 0 = the API's own default.
	res.OutputLimit = req.MaxTokens
	return res, nil
}

// buildRequestBody constructs the OpenAI chat completion request payload.
func (p *OpenAIProvider) buildRequestBody(req *agentcoreapi.GenerateRequest, model string, stream bool) map[string]any {
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	// A thinking-mode session echoes reasoning_content on every assistant
	// message, even a step whose reasoning was empty: DeepSeek rejects a request
	// with tools when any earlier assistant message lacks the field. A step whose
	// reasoning was folded away still marks the session as thinking.
	thinking := false
	for _, m := range req.Messages {
		if folded, _ := m.Metadata[ReasoningFoldedMetaKey].(bool); m.Role == "assistant" && (m.Reasoning != "" || folded) {
			thinking = true
			break
		}
	}

	// System prompt injection.
	system := req.System
	if system != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": system})
	}

	for _, m := range groupToolBatches(req.Messages) {
		// Tool messages carry both ToolCall and ToolResult in agentcore; OpenAI
		// wants role=tool with only tool_call_id+content (no tool_calls).
		if m.ToolResult != nil || m.Role == "tool" {
			msg := map[string]any{"role": "tool", "content": m.Content}
			if m.ToolResult != nil {
				msg["tool_call_id"] = m.ToolResult.CallID
				msg["content"] = m.ToolResult.Content
			} else if m.ToolCall != nil {
				msg["tool_call_id"] = m.ToolCall.CallID
			}
			msgs = append(msgs, msg)
			continue
		}
		if m.ToolCall != nil {
			call := map[string]any{
				"id":   m.ToolCall.CallID,
				"type": "function",
				"function": map[string]any{
					"name":      m.ToolCall.Name,
					"arguments": mustJSON(m.ToolCall.Arguments),
				},
			}
			// Merge consecutive assistant tool calls into one message.
			if n := len(msgs); n > 0 && msgs[n-1]["role"] == "assistant" {
				if calls, ok := msgs[n-1]["tool_calls"].([]map[string]any); ok {
					msgs[n-1]["tool_calls"] = append(calls, call)
					continue
				}
			}
			var content any // nil -> JSON null when the model sent no text
			if m.Content != "" {
				content = m.Content
			}
			msg := map[string]any{"role": "assistant", "content": content, "tool_calls": []map[string]any{call}}
			withReasoning(msg, m, thinking)
			msgs = append(msgs, msg)
			continue
		}
		// A plain assistant answer carries no reasoning_content: thinking-mode
		// APIs need it only on tool-call messages (DeepSeek-Reasonix does the
		// same), and an earlier answer's reasoning would only cost input tokens.
		msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
	}

	body := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   stream,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if p.reasoningEffort != "" {
		body["reasoning_effort"] = p.reasoningEffort
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.InputSchema,
				},
			}
		}
		body["tools"] = tools
	}
	// Only meaningful with declared tools; sent when the caller asked for it
	// (the runtime's tool-less wrap-up retry).
	if req.ToolChoice != "" && len(req.Tools) > 0 {
		body["tool_choice"] = req.ToolChoice
	}
	return body
}

// withReasoning echoes an assistant message's reasoning as reasoning_content.
// Thinking-mode APIs (DeepSeek, Kimi, ...) reject a request with tools whose
// earlier assistant messages lack it. Without any reasoning in the request
// (thinking=false) the field is never sent, so other APIs never see it.
func withReasoning(msg map[string]any, m agentcoreapi.Message, thinking bool) {
	if m.Role == "assistant" && (thinking || m.Reasoning != "") {
		msg["reasoning_content"] = m.Reasoning
	}
}

// parseResponse decodes an OpenAI chat completion response.
func (p *OpenAIProvider) parseResponse(data []byte) (*agentcoreapi.GenerateResult, error) {
	var raw struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			TotalTokens         int `json:"total_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			// DeepSeek's native cache field (see cachedTokens).
			PromptCacheHitTokens int     `json:"prompt_cache_hit_tokens"`
			CostUSD              float64 `json:"cost_usd"`
		} `json:"usage"`
		Error *apiError `json:"error"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, badResponsef("parse openai response: %w", err)
	}
	if raw.Error != nil {
		return nil, apiErrorf("openai api error", raw.Error)
	}
	if len(raw.Choices) == 0 {
		return nil, badResponsef("openai: empty choices")
	}

	choice := raw.Choices[0]
	resp := &agentcoreapi.GenerateResult{
		Content:   choice.Message.Content,
		Reasoning: choice.Message.ReasoningContent,
		Usage: agentcoreapi.Usage{
			InputTokens:     raw.Usage.PromptTokens,
			OutputTokens:    raw.Usage.CompletionTokens,
			TotalTokens:     raw.Usage.TotalTokens,
			CacheReadTokens: cachedTokens(raw.Usage.PromptTokensDetails.CachedTokens, raw.Usage.PromptCacheHitTokens),
			CostUSD:         raw.Usage.CostUSD,
		},
	}

	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, newToolCall(tc.ID, tc.Function.Name, tc.Function.Arguments))
	}
	if choice.FinishReason == "length" {
		resp.Truncated = true
		markTruncated(resp.ToolCalls)
	}
	if len(resp.ToolCalls) > 0 {
		// Text emitted alongside the tool calls is kept in resp.Content; the runtime
		// persists it with the first tool-call message.
		resp.FinishReason = agentcoreapi.FinishToolCalls
	} else {
		resp.FinishReason = agentcoreapi.FinishFinal
	}

	return resp, nil
}

// doRequest sends a POST to the OpenAI API and returns the raw response body
// and its HTTP status (an API error's body too: the caller parses it and
// classifies a non-2xx status). Transient failures are retried by postWithRetry.
func (p *OpenAIProvider) doRequest(ctx context.Context, path string, body map[string]any) ([]byte, int, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := postWithRetry(ctx, p.client, p.retry, p.Name(), path, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		applyHeaders(ctx, req, p.headers)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		return req, nil
	})
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		// The body broke while being read: the connection failed, not the request.
		return nil, 0, withRequestID(ctx, &transientRequestError{fmt.Errorf("read %s response: %w", p.Name(), err)})
	}
	logProviderResponse(p.Name(), path, resp.StatusCode, respData)
	return respData, resp.StatusCode, nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
