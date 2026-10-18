package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

const (
	claudeDefaultBaseURL = "https://api.anthropic.com"
	claudeAPIVersion     = "2023-06-01"
	// claudeDefaultMaxOutputTokens is max_tokens when neither the request nor
	// the configuration sets a limit. /v1/messages requires the field, and the
	// old 4096 default cut long answers and the arguments of large tool calls
	// (file_write/file_edit) off.
	claudeDefaultMaxOutputTokens = 32000
)

// claudeMaxTokens returns the max_tokens of one request: the limit the
// caller resolved (providers.<id>.models.<m>.max_output_tokens,
// --max-output-tokens, SUGA_MAX_OUTPUT_TOKENS, ...), or the protocol default.
func claudeMaxTokens(req *agentcoreapi.GenerateRequest) int {
	if req != nil && req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return claudeDefaultMaxOutputTokens
}

// ClaudeProvider implements Provider for Anthropic Claude.
type ClaudeProvider struct {
	name         string
	apiKey       string
	model        string
	messagesURL  string
	client       *http.Client
	streamClient *http.Client
	headers      map[string]string
	promptCache  bool
	thinking     string
	// thinkingStyle pins the thinking request shape (ClaudeConfig.ThinkingStyle,
	// see applyClaudeThinking); "" guesses it from the model name.
	thinkingStyle string
	// streamRequired sends every request as a streamed one (ClaudeConfig).
	streamRequired bool
	// retry is the provider's shared retry state (ClaudeConfig.Retry).
	retry *backoff
}

// SegmentContextMetaKey is the Message.Metadata key (bool true) that marks the
// segment context message. With PromptCache on, the Claude provider puts a
// cache breakpoint at the end of that message so the stable prefix (system +
// tools + segment context) is cached across turns. The application layer
// must set the same key when it builds that message.
const SegmentContextMetaKey = "suga.segment_context"

// ReasoningFoldedMetaKey is the Message.Metadata key (bool true) that marks an
// assistant message whose reasoning was removed from the request (history
// folding). The OpenAI provider still treats the request as a thinking-mode
// session then, so reasoning_content keeps being echoed on every assistant
// message even when no remaining step carries reasoning.
const ReasoningFoldedMetaKey = "suga.reasoning_folded"

// ClaudeThinkingMetaKey is the GenerateResult / Message metadata key holding
// a reply's thinking blocks as Anthropic returned them ([]any of maps:
// {type: thinking, thinking, signature} or {type: redacted_thinking, data}).
// They are sent back unchanged before the tool_use blocks of that reply, as
// extended thinking with tools requires.
const ClaudeThinkingMetaKey = "suga.claude_thinking"

// ClaudeConfig configures a ClaudeProvider.
type ClaudeConfig struct {
	// Name is the provider name reported by Name(); default: anthropic.
	Name   string
	APIKey string
	// BaseURL defaults to https://api.anthropic.com. A URL ending in /v1 gets
	// /messages appended, any other gets /v1/messages.
	BaseURL string
	Model   string // default: claude-sonnet-4-6
	// Headers are extra request headers; see applyHeaders for SessionIDPlaceholder.
	Headers map[string]string
	// PromptCache adds cache_control breakpoints (at most 2 of Anthropic's 4):
	// the segment context message's last block and the request's last block.
	// Default off, since gateways may reject cache_control.
	PromptCache bool
	// Thinking is the thinking level (off, minimal, low, medium, high, xhigh,
	// max, auto; see applyClaudeThinking). "", "auto" and "off" send no
	// thinking parameters.
	Thinking string
	// ThinkingStyle pins the thinking request shape (budget, adaptive or none,
	// see ClaudeThinking* constants and applyClaudeThinking); "" keeps the
	// model-name heuristic. "none" never sends thinking parameters.
	ThinkingStyle string
	// StreamRequired sends every request as a streamed one: Generate is routed
	// through Stream. Some gateways answer only stream=true requests, the
	// internal summary requests included.
	StreamRequired bool
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
}

// NewClaudeProvider creates a ClaudeProvider.
func NewClaudeProvider(cfg ClaudeConfig) *ClaudeProvider {
	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-6"
	}
	if cfg.Name == "" {
		cfg.Name = "anthropic"
	}
	return &ClaudeProvider{
		name:           cfg.Name,
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		messagesURL:    claudeMessagesURL(cfg.BaseURL),
		client:         newProviderClient(requestTimeout(cfg.RequestTimeout), nil),
		streamClient:   newStreamClient(responseHeaderTimeout(cfg.ResponseHeaderTimeout)),
		headers:        cfg.Headers,
		promptCache:    cfg.PromptCache,
		thinking:       cfg.Thinking,
		thinkingStyle:  cfg.ThinkingStyle,
		streamRequired: cfg.StreamRequired,
		retry:          newBackoff(cfg.Retry),
	}
}

// claudeMessagesURL builds the messages endpoint from a user-supplied base URL.
func claudeMessagesURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = claudeDefaultBaseURL
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

// Name implements Provider.
func (p *ClaudeProvider) Name() string  { return p.name }
func (p *ClaudeProvider) Model() string { return p.model }

// SupportsToolChoice implements api.ToolChooser: /v1/messages takes
// tool_choice as an object ({"type": "none"}), which is also allowed with
// extended thinking.
func (p *ClaudeProvider) SupportsToolChoice() bool { return true }

// Generate implements the public agentcore provider contract.
func (p *ClaudeProvider) Generate(ctx context.Context, req *agentcoreapi.GenerateRequest) (*agentcoreapi.GenerateResult, error) {
	if p.streamRequired {
		// The endpoint answers only streamed requests (providers.<id>
		// [.models.<model>].stream_required): even a caller of Generate - a
		// summary, for example - gets stream=true.
		return p.Stream(ctx, req, nil)
	}
	ctx = ensureRetryReport(ctx)
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := p.buildRequestBody(req, model)
	logProviderRequest(p.Name(), "/messages", body)
	respData, status, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	res, err := p.parseResponse(respData)
	if err != nil {
		// A rejected request (4xx) keeps the API's own message but is classified by
		// its status (ErrorKind, ShouldFallback).
		return nil, statusErr(status, withRequestID(ctx, err))
	}
	res.OutputLimit = claudeMaxTokens(req)
	return res, nil
}

// appendClaudeBlock adds a content block as a message of the given role. When
// the previous message has the same role and already holds content blocks
// (parallel tool_use / consecutive tool_result), the block is merged into it so
// roles keep alternating as Anthropic requires.
func appendClaudeBlock(msgs []map[string]any, role string, block map[string]any) []map[string]any {
	if n := len(msgs); n > 0 && msgs[n-1]["role"] == role {
		if blocks, ok := msgs[n-1]["content"].([]map[string]any); ok {
			msgs[n-1]["content"] = append(blocks, block)
			return msgs
		}
	}
	return append(msgs, map[string]any{"role": role, "content": []map[string]any{block}})
}

func (p *ClaudeProvider) buildRequestBody(req *agentcoreapi.GenerateRequest, model string) map[string]any {
	msgs := make([]map[string]any, 0, len(req.Messages))
	// Anthropic messages only accept user/assistant roles; mid-conversation
	// system messages (rule updates, hook context) go to the top-level system.
	system := req.System
	segmentIdx := -1 // index in msgs of the segment context message, if any
	for _, m := range groupToolBatches(req.Messages) {
		switch {
		case m.Role == "system" && m.ToolCall == nil && m.ToolResult == nil:
			if m.Content != "" {
				if system != "" {
					system += "\n\n"
				}
				system += m.Content
			}
		// agentcore persists a tool result as role=tool carrying BOTH ToolCall and
		// ToolResult, so the result check must come first; otherwise the result
		// is rendered as a duplicate tool_use and tool_result is never sent.
		case m.ToolResult != nil || m.Role == "tool":
			block := map[string]any{"type": "tool_result", "content": m.Content}
			if m.ToolResult != nil {
				block["tool_use_id"] = m.ToolResult.CallID
				block["content"] = m.ToolResult.Content
				if m.ToolResult.IsError {
					block["is_error"] = true
				}
			} else if m.ToolCall != nil {
				block["tool_use_id"] = m.ToolCall.CallID
			}
			msgs = appendClaudeBlock(msgs, "user", block)
		case m.ToolCall != nil:
			// The reply's thinking blocks go back first and unchanged, as
			// extended thinking with tools requires.
			for _, block := range claudeThinkingBlocks(m) {
				msgs = appendClaudeBlock(msgs, "assistant", block)
			}
			// Text the model emitted alongside the call precedes tool_use in the same turn.
			if m.Content != "" {
				msgs = appendClaudeBlock(msgs, "assistant", map[string]any{"type": "text", "text": m.Content})
			}
			msgs = appendClaudeBlock(msgs, "assistant", map[string]any{
				"type":  "tool_use",
				"id":    m.ToolCall.CallID,
				"name":  m.ToolCall.Name,
				"input": m.ToolCall.Arguments,
			})
		default:
			if v, _ := m.Metadata[SegmentContextMetaKey].(bool); v {
				segmentIdx = len(msgs)
			}
			msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
		}
	}

	body := map[string]any{
		"model":      model,
		"messages":   msgs,
		"max_tokens": claudeMaxTokens(req),
	}
	applyClaudeThinking(body, model, p.thinking, p.thinkingStyle)
	if system != "" {
		// A block array only when prompt caching is on: Anthropic accepts both
		// shapes, but some /v1/messages-compatible gateways only take a string.
		if p.promptCache {
			body["system"] = []map[string]any{{"type": "text", "text": system}}
		} else {
			body["system"] = system
		}
	}
	if p.promptCache {
		markClaudeCacheBreakpoints(msgs, segmentIdx)
	}

	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.InputSchema,
			}
		}
		body["tools"] = tools
	}
	// Anthropic takes tool_choice as an object; only sent with declared tools
	// and when the caller asked for it (the runtime's tool-less wrap-up retry).
	if req.ToolChoice != "" && len(req.Tools) > 0 {
		body["tool_choice"] = map[string]any{"type": req.ToolChoice}
	}

	return body
}

// markClaudeCacheBreakpoints adds cache_control to the last content block of the
// segment context message (segmentIdx, -1 when absent) and of the final
// message. Both pointing at one block yields a single breakpoint. String
// content is converted to a text block first.
func markClaudeCacheBreakpoints(msgs []map[string]any, segmentIdx int) {
	if len(msgs) == 0 {
		return
	}
	last := len(msgs) - 1
	if segmentIdx >= 0 && segmentIdx < last {
		markClaudeLastBlock(msgs[segmentIdx])
	}
	markClaudeLastBlock(msgs[last])
}

func markClaudeLastBlock(msg map[string]any) {
	var blocks []map[string]any
	switch c := msg["content"].(type) {
	case string:
		if c == "" {
			return // Anthropic rejects cache_control on empty text blocks
		}
		blocks = []map[string]any{{"type": "text", "text": c}}
	case []map[string]any:
		blocks = c
	}
	if len(blocks) == 0 {
		return
	}
	blocks[len(blocks)-1]["cache_control"] = map[string]any{"type": "ephemeral"}
	msg["content"] = blocks
}

func (p *ClaudeProvider) parseResponse(data []byte) (*agentcoreapi.GenerateResult, error) {
	var raw struct {
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			Thinking  string          `json:"thinking"`
			Signature string          `json:"signature"`
			Data      string          `json:"data"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Error *apiError `json:"error"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, badResponsef("parse claude response: %w", err)
	}
	if raw.Error != nil {
		return nil, apiErrorf("claude api error", raw.Error)
	}

	resp := &agentcoreapi.GenerateResult{
		Usage: agentcoreapi.Usage{
			InputTokens:      raw.Usage.InputTokens + raw.Usage.CacheReadInputTokens + raw.Usage.CacheCreationInputTokens,
			OutputTokens:     raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.InputTokens + raw.Usage.CacheReadInputTokens + raw.Usage.CacheCreationInputTokens + raw.Usage.OutputTokens,
			CacheReadTokens:  raw.Usage.CacheReadInputTokens,
			CacheWriteTokens: raw.Usage.CacheCreationInputTokens,
		},
	}

	var thinking []any
	for _, block := range raw.Content {
		switch block.Type {
		case "text":
			resp.Content += block.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, newToolCall(block.ID, block.Name, block.Input))
		case "thinking":
			resp.Reasoning += block.Thinking
			thinking = append(thinking, thinkingBlock(block.Thinking, block.Signature))
		case "redacted_thinking":
			thinking = append(thinking, map[string]any{"type": "redacted_thinking", "data": block.Data})
		}
	}
	if len(thinking) > 0 {
		resp.Metadata = map[string]any{ClaudeThinkingMetaKey: thinking}
	}
	if raw.StopReason == "max_tokens" {
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

// doRequest sends the request and returns the raw response body and its HTTP
// status (an API error's body too: parseResponse reports it and the status
// classifies it). Transient failures are retried by postWithRetry.
func (p *ClaudeProvider) doRequest(ctx context.Context, body map[string]any) ([]byte, int, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal claude request: %w", err)
	}
	resp, err := postWithRetry(ctx, p.client, p.retry, p.Name(), "/messages", func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.messagesURL, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		applyHeaders(ctx, req, p.headers)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", p.apiKey)
		req.Header.Set("anthropic-version", claudeAPIVersion)
		setThinkingBeta(req, body)
		return req, nil
	})
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		// The body broke while being read: the connection failed, not the request.
		return nil, 0, withRequestID(ctx, &transientRequestError{fmt.Errorf("read claude response: %w", err)})
	}
	logProviderResponse(p.Name(), "/messages", resp.StatusCode, respData)
	return respData, resp.StatusCode, nil
}

func thinkingBlock(text, signature string) map[string]any {
	return map[string]any{"type": "thinking", "thinking": text, "signature": signature}
}

// setThinkingBeta adds the interleaved-thinking beta when body uses
// budget-mode thinking, unless the configured headers set anthropic-beta.
func setThinkingBeta(req *http.Request, body map[string]any) {
	t, _ := body["thinking"].(map[string]any)
	if t["type"] == "enabled" && req.Header.Get("anthropic-beta") == "" {
		req.Header.Set("anthropic-beta", claudeInterleavedBeta)
	}
}
