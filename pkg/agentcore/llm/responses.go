package llm

import (
	"context"
	"encoding/json"
	"strings"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// ResponsesReasoningMetaKey is the GenerateResult / Message metadata key
// holding a reply's reasoning items as the Responses API returned them
// ([]any of maps, without id and status). They are sent back first in that
// reply's turn on later requests.
const ResponsesReasoningMetaKey = "suga.responses_reasoning"

// reasoningPlaceholder fills a reasoning item a turn must carry but whose
// text is unknown (ReasoningEveryTurn).
const reasoningPlaceholder = "reasoning unavailable"

// ResponsesProvider implements Provider for the OpenAI Responses API
// (POST {base_url}/responses). It always sends the whole history with
// store false (third-party gateways often ignore previous_response_id, and
// segments and folding rewrite history anyway). HTTP, retries and headers
// are the OpenAI provider's; gateway differences come from Compat.
type ResponsesProvider struct {
	*OpenAIProvider
	compat Compat
}

// ResponsesConfig configures a ResponsesProvider.
type ResponsesConfig struct {
	OpenAIConfig
	// Compat names extra adapters (providers.<id>.compat) on top of the ones
	// matched by base URL and model.
	Compat []string
}

// NewResponsesProvider creates a ResponsesProvider from cfg.
func NewResponsesProvider(cfg ResponsesConfig) *ResponsesProvider {
	base := NewOpenAIProvider(cfg.OpenAIConfig)
	return &ResponsesProvider{OpenAIProvider: base, compat: ResolveCompat(base.baseURL, base.model, cfg.Compat...)}
}

// Compat returns the adapters this provider applies.
func (p *ResponsesProvider) Compat() Compat { return p.compat }

// SupportsToolChoice implements api.ToolChooser; a gateway whose compat says
// it has no usable tool_choice (Compat.NoToolChoice) never gets one, and the
// runtime skips its tool-less wrap-up retry then.
func (p *ResponsesProvider) SupportsToolChoice() bool { return !p.compat.NoToolChoice }

const responsesPath = "/responses"

// Generate implements Provider.
func (p *ResponsesProvider) Generate(ctx context.Context, req *agentcoreapi.GenerateRequest) (*agentcoreapi.GenerateResult, error) {
	if p.streamRequired {
		// The endpoint answers only streamed requests (providers.<id>
		// [.models.<model>].stream_required): even a caller of Generate - a
		// summary, for example - gets stream=true.
		return p.Stream(ctx, req, nil)
	}
	ctx = ensureRetryReport(ctx)
	body := p.buildResponsesBody(ctx, req, false)
	logProviderRequest(p.Name(), responsesPath, body)
	data, status, err := p.doRequest(ctx, responsesPath, body)
	if err != nil {
		return nil, err
	}
	res, err := p.parseResponsesBody(data)
	if err != nil {
		// A rejected request (4xx) keeps the API's own message but is classified by
		// its status (ErrorKind, ShouldFallback).
		return nil, statusErr(status, withRequestID(ctx, err))
	}
	res.OutputLimit = p.responsesMaxTokens(req)
	return res, nil
}

// effort maps the configured thinking level to reasoning.effort: "off" is
// the API's "none", other levels pass through ("" sends nothing).
func (p *ResponsesProvider) effort() string {
	if p.reasoningEffort == "off" {
		return "none"
	}
	return p.reasoningEffort
}

// buildResponsesBody constructs the /responses request payload.
func (p *ResponsesProvider) buildResponsesBody(ctx context.Context, req *agentcoreapi.GenerateRequest, stream bool) map[string]any {
	model := req.Model
	if model == "" {
		model = p.model
	}
	effort := p.effort()
	thinking := (effort != "" && effort != "none") || hasReasoning(req.Messages)
	body := map[string]any{
		"model":  model,
		"input":  p.buildInput(req.Messages, thinking),
		"stream": stream,
		"store":  false,
	}
	if req.System != "" {
		body["instructions"] = req.System
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.InputSchema}
		}
		body["tools"] = tools
	}
	// Only meaningful with declared tools; sent when the caller asked for it
	// (the runtime's tool-less wrap-up retry) and the gateway takes the field.
	if req.ToolChoice != "" && len(req.Tools) > 0 && !p.compat.NoToolChoice {
		body["tool_choice"] = req.ToolChoice
	}
	if effort != "" {
		reasoning := map[string]any{"effort": effort}
		if p.compat.ReasoningSummary && effort != "none" {
			reasoning["summary"] = "auto"
		}
		body["reasoning"] = reasoning
	}
	if p.compat.EncryptedReasoning && thinking && effort != "none" {
		body["include"] = []string{"reasoning.encrypted_content"}
	}
	// No max_output_tokens when nothing is configured: the API's own default
	// applies then (p.responsesMaxTokens).
	if limit := p.responsesMaxTokens(req); limit > 0 {
		body["max_output_tokens"] = limit
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	// The session id keeps one conversation on one cache (the API caps the
	// key at 64 characters).
	if key := agentcoreapi.SessionIDFrom(ctx); key != "" && len(key) <= 64 {
		body["prompt_cache_key"] = key
	}
	return body
}

// responsesMaxTokens returns the max_output_tokens of one request: the limit
// the caller resolved, else the compat adapter's own default (0 = the API's,
// as when nothing is configured).
func (p *ResponsesProvider) responsesMaxTokens(req *agentcoreapi.GenerateRequest) int {
	if req != nil && req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return p.compat.MaxOutputTokens
}

// hasReasoning reports whether an earlier assistant message carries
// reasoning (or had it folded away): the conversation is in thinking mode.
func hasReasoning(msgs []agentcoreapi.Message) bool {
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		folded, _ := m.Metadata[ReasoningFoldedMetaKey].(bool)
		if m.Reasoning != "" || folded || m.Metadata[ResponsesReasoningMetaKey] != nil {
			return true
		}
	}
	return false
}

// buildInput converts the history into Responses input items. A turn - the
// assistant items of one reply, its parallel calls regrouped - starts with
// that reply's reasoning items.
func (p *ResponsesProvider) buildInput(msgs []agentcoreapi.Message, thinking bool) []map[string]any {
	items := make([]map[string]any, 0, len(msgs))
	inTurn := false // the last item came from the assistant
	for _, m := range groupToolBatches(msgs) {
		switch {
		case m.ToolResult != nil || m.Role == "tool":
			callID, out := "", m.Content
			if m.ToolResult != nil {
				callID, out = m.ToolResult.CallID, m.ToolResult.Content
			} else if m.ToolCall != nil {
				callID = m.ToolCall.CallID
			}
			items = append(items, map[string]any{"type": "function_call_output", "call_id": callID, "output": out})
			inTurn = false
		case m.Role == "assistant":
			if !inTurn {
				items = append(items, p.turnReasoning(m, thinking)...)
				inTurn = true
			}
			if m.Content != "" {
				items = append(items, map[string]any{"type": "message", "role": "assistant",
					"content": []map[string]any{{"type": "output_text", "text": m.Content, "annotations": []any{}}}})
			}
			if m.ToolCall != nil {
				items = append(items, map[string]any{"type": "function_call", "call_id": m.ToolCall.CallID,
					"name": m.ToolCall.Name, "arguments": mustJSON(m.ToolCall.Arguments)})
			}
		default:
			// user, and system messages mid-conversation: gateways such as
			// command-code reject the developer role; the system prompt
			// itself is the instructions field.
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			items = append(items, map[string]any{"role": "user",
				"content": []map[string]any{{"type": "input_text", "text": m.Content}}})
			inTurn = false
		}
	}
	return items
}

// turnReasoning returns the reasoning items opening m's turn: the stored
// ones (not when folding removed the reasoning), each with its text when the
// gateway needs it (ReasoningEveryTurn), or a filled-in one when the turn
// has none and the gateway needs one.
func (p *ResponsesProvider) turnReasoning(m agentcoreapi.Message, thinking bool) []map[string]any {
	folded, _ := m.Metadata[ReasoningFoldedMetaKey].(bool)
	var out []map[string]any
	if !folded {
		for _, item := range metaItems(m.Metadata[ResponsesReasoningMetaKey]) {
			item = replayItem(item)
			if p.compat.ReasoningEveryTurn {
				ensureReasoningText(item, m.Reasoning)
			}
			out = append(out, item)
		}
	}
	if len(out) == 0 && thinking && p.compat.ReasoningEveryTurn {
		text := m.Reasoning
		if folded || strings.TrimSpace(text) == "" {
			text = reasoningPlaceholder
		}
		out = append(out, map[string]any{"type": "reasoning", "summary": []any{},
			"content": []map[string]any{{"type": "reasoning_text", "text": text}}})
	}
	return out
}

// metaItems reads stored items: []any of maps after a JSON round trip, or
// []map[string]any as produced in this process.
func metaItems(v any) []map[string]any {
	switch list := v.(type) {
	case []map[string]any:
		return list
	case []any:
		out := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// replayItem copies an output item without the fields a stateless replay
// must not send (id, status).
func replayItem(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))
	for k, v := range item {
		if k != "id" && k != "status" {
			out[k] = v
		}
	}
	return out
}

// ensureReasoningText gives a reasoning item without encrypted content a
// reasoning_text: its summary text, else fallback, else the placeholder.
func ensureReasoningText(item map[string]any, fallback string) {
	if s, _ := item["encrypted_content"].(string); s != "" {
		return
	}
	for _, c := range metaItems(item["content"]) {
		if t, _ := c["text"].(string); strings.TrimSpace(t) != "" {
			return
		}
	}
	text := reasoningSummaryText(item)
	if text == "" {
		text = fallback
	}
	if strings.TrimSpace(text) == "" {
		text = reasoningPlaceholder
	}
	item["content"] = []map[string]any{{"type": "reasoning_text", "text": text}}
}

// reasoningSummaryText joins a reasoning item's summary texts.
func reasoningSummaryText(item map[string]any) string {
	var parts []string
	for _, s := range metaItems(item["summary"]) {
		if t, _ := s["text"].(string); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

// reasoningText is the readable reasoning of an item: the summary, else the
// reasoning_text content.
func reasoningText(item map[string]any) string {
	if s := reasoningSummaryText(item); s != "" {
		return s
	}
	var parts []string
	for _, c := range metaItems(item["content"]) {
		if t, _ := c["text"].(string); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

// responsesBody is the part of a response object the provider reads.
type responsesBody struct {
	Status            string                       `json:"status"`
	Output            []map[string]json.RawMessage `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *apiError      `json:"error"`
	Usage map[string]any `json:"usage"`
}

// parseResponsesBody parses a non-streamed response.
func (p *ResponsesProvider) parseResponsesBody(data []byte) (*agentcoreapi.GenerateResult, error) {
	var body responsesBody
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, badResponsef("parse responses result: %w", err)
	}
	return p.resultOf(body)
}

// resultOf turns a response object (non-streamed, or the one a terminal
// stream event carries) into a GenerateResult.
func (p *ResponsesProvider) resultOf(body responsesBody) (*agentcoreapi.GenerateResult, error) {
	// An error object is reported as it stands, even without a message (its code
	// or type still names the failure); a temporary one is retried by retryStream.
	if !body.Error.empty() {
		return nil, apiErrorf("responses api error", body.Error)
	}
	if body.Status == "failed" {
		return nil, badResponsef("responses api error: response failed")
	}
	res := &agentcoreapi.GenerateResult{}
	var reasoning []any
	var reasoningParts []string
	for _, raw := range body.Output {
		var kind string
		_ = json.Unmarshal(raw["type"], &kind)
		switch kind {
		case "reasoning":
			var item map[string]any
			full, _ := json.Marshal(raw)
			if json.Unmarshal(full, &item) != nil {
				continue
			}
			if t := reasoningText(item); t != "" {
				reasoningParts = append(reasoningParts, t)
			}
			reasoning = append(reasoning, replayItem(item))
		case "message":
			var content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(raw["content"], &content)
			for _, c := range content {
				if c.Type == "output_text" {
					res.Content += c.Text
				}
			}
		case "function_call":
			var callID, name, args string
			_ = json.Unmarshal(raw["call_id"], &callID)
			_ = json.Unmarshal(raw["name"], &name)
			_ = json.Unmarshal(raw["arguments"], &args)
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			res.ToolCalls = append(res.ToolCalls, newToolCall(callID, name, json.RawMessage(args)))
		}
	}
	res.Reasoning = strings.Join(reasoningParts, "\n\n")
	if len(reasoning) > 0 {
		res.Metadata = map[string]any{ResponsesReasoningMetaKey: reasoning}
	}
	res.Usage = p.usageOf(body.Usage)
	if body.Status == "incomplete" && body.IncompleteDetails != nil {
		switch body.IncompleteDetails.Reason {
		case "content_filter":
			return nil, badResponsef("responses api: the reply was blocked by the content filter")
		case "max_output_tokens":
			res.Truncated = true
			markTruncated(res.ToolCalls)
		}
	}
	res.FinishReason = agentcoreapi.FinishFinal
	if len(res.ToolCalls) > 0 {
		res.FinishReason = agentcoreapi.FinishToolCalls
	}
	return res, nil
}

// usageOf maps a Responses usage object: input_tokens already includes the
// cached tokens (input_tokens_details.cached_tokens, or the compat field).
func (p *ResponsesProvider) usageOf(u map[string]any) agentcoreapi.Usage {
	num := func(m map[string]any, key string) int {
		v, _ := m[key].(float64)
		return int(v)
	}
	if u == nil {
		return agentcoreapi.Usage{}
	}
	details, _ := u["input_tokens_details"].(map[string]any)
	in, out := num(u, "input_tokens"), num(u, "output_tokens")
	cached := p.compat.cacheHit(num(details, "cached_tokens"), u)
	return agentcoreapi.Usage{InputTokens: in, OutputTokens: out, TotalTokens: in + out, CacheReadTokens: cached}
}
