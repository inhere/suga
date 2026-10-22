package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// Stream implements the public streaming contract with Anthropic SSE: text
// deltas are emitted as they arrive; tool_use input, thinking and usage are
// accumulated into the returned result. The connection is retried by
// openStream; a stream that breaks while being read is requested again by
// retryStream (after a StreamEventRetry when text was already emitted).
func (p *ClaudeProvider) Stream(ctx context.Context, req *agentcoreapi.GenerateRequest, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	ctx = ensureRetryReport(ctx)
	model := req.Model
	if model == "" {
		model = p.model
	}
	body := p.buildRequestBody(req, model)
	body["stream"] = true
	logProviderRequest(p.Name(), "/messages", body)

	res, err := retryStream(ctx, p.retry, p.Name(), emit, func(ctx context.Context, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
		// The attempt's own context: the idle timeout of the body cancels it, which
		// ends this request only - the turn's ctx stays usable for the retry.
		rctx, cancel := context.WithCancel(ctx)
		defer cancel()
		resp, err := p.openStream(rctx, body)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			// Some gateways ignore stream=true and answer with a plain message.
			return p.readWhole(ctx, resp.Body, emit)
		}
		stream := resp.Body
		if idle := streamIdleTimeout(p.streamIdleTimeout, body); idle > 0 {
			idleBody := withIdleTimeout(resp.Body, idle, cancel)
			defer idleBody.Close()
			stream = idleBody
		}
		return p.readStream(ctx, stream, emit)
	})
	if res != nil {
		// The limit this request asked for, reported with a cut-off reply.
		res.OutputLimit = claudeMaxTokens(req)
	}
	return res, err
}

// readWhole parses a non-streamed message and emits its text in one delta.
func (p *ClaudeProvider) readWhole(ctx context.Context, r io.Reader, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, &streamReadError{fmt.Errorf("read claude response: %w", err)}
	}
	logProviderResponse(p.Name(), "/messages", http.StatusOK, data)
	res, err := p.parseResponse(data)
	if err != nil || emit == nil || res.Content == "" {
		return res, err
	}
	if err := emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventTextDelta, Delta: res.Content}); err != nil {
		return nil, err
	}
	return res, emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventMessageStop})
}

// openStream sends the request and returns a 2xx response whose body is the
// event stream; transient failures are retried by postWithRetry.
func (p *ClaudeProvider) openStream(ctx context.Context, body map[string]any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal claude request: %w", err)
	}
	resp, err := postWithRetry(ctx, p.streamClient, p.retry, p.Name(), "/messages", func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.messagesURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		applyHeaders(ctx, req, p.headers)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("x-api-key", p.apiKey)
		req.Header.Set("anthropic-version", claudeAPIVersion)
		setThinkingBeta(req, body)
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	respData, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	logProviderResponse(p.Name(), "/messages", resp.StatusCode, respData)
	// The status with the API's own message, so the failure is classified as a
	// rejection (ErrorKind).
	var apiErr struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(respData, &apiErr) == nil && apiErr.Error != nil {
		return nil, statusErr(resp.StatusCode, withRequestID(ctx, apiErrorf("claude api error", apiErr.Error)))
	}
	return nil, statusErr(resp.StatusCode, withRequestID(ctx, apiMessageErrorf(fmt.Sprintf("claude api error %d", resp.StatusCode), strings.TrimSpace(string(respData)))))
}

// claudeUsage is the usage object shared by message_start and message_delta.
type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// claudeStreamEvent is the union of the SSE data payloads we care about.
type claudeStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage claudeUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Data string `json:"data"` // redacted_thinking
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *claudeUsage `json:"usage"`
	Error *apiError    `json:"error"`
}

// claudeStreamTool accumulates one tool_use block by its content index.
type claudeStreamTool struct {
	id, name string
	args     strings.Builder
}

// readStream consumes SSE "data:" lines until message_stop or EOF. The event
// type is taken from the payload's "type" field, falling back to "event:".
func (p *ClaudeProvider) readStream(ctx context.Context, r io.Reader, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	var content, reasoning strings.Builder
	calls := map[int]*claudeStreamTool{}
	// thinking blocks by content index, kept to be sent back (ClaudeThinkingMetaKey)
	thinking := map[int]map[string]any{}
	res := &agentcoreapi.GenerateResult{}
	var usage claudeUsage
	stopReason, eventName, emitted := "", "", false
	stopped := false // the provider sent message_stop

	lines := newStreamLines()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	scanner.Split(lines.split)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if name, ok := strings.CutPrefix(line, "event:"); ok {
			eventName = strings.TrimSpace(name)
			continue
		}
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // blank separators and ": keep-alive" comments
		}
		var ev claudeStreamEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &ev); err != nil {
			if lines.truncated() {
				return nil, streamTruncatedError(p.Name(), cutLine)
			}
			return nil, badResponsef("parse claude stream event: %w", err)
		}
		if ev.Type == "" {
			ev.Type = eventName
		}
		eventName = ""

		switch ev.Type {
		case "error":
			if ev.Error != nil {
				return nil, apiErrorf("claude api error", ev.Error)
			}
			return nil, apiMessageErrorf("claude api error", "unknown error")
		case "message_start":
			usage = ev.Message.Usage
		case "content_block_start":
			switch ev.ContentBlock.Type {
			case "tool_use":
				calls[ev.Index] = &claudeStreamTool{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			case "thinking":
				thinking[ev.Index] = thinkingBlock("", "")
			case "redacted_thinking":
				thinking[ev.Index] = map[string]any{"type": "redacted_thinking", "data": ev.ContentBlock.Data}
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text == "" {
					continue
				}
				content.WriteString(ev.Delta.Text)
				if emit != nil {
					if err := emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventTextDelta, Delta: ev.Delta.Text}); err != nil {
						return nil, err
					}
				}
				emitted = true
			case "input_json_delta":
				if call := calls[ev.Index]; call != nil {
					call.args.WriteString(ev.Delta.PartialJSON)
				}
			case "thinking_delta":
				reasoning.WriteString(ev.Delta.Thinking)
				if b := thinking[ev.Index]; b != nil {
					b["thinking"] = b["thinking"].(string) + ev.Delta.Thinking
				}
			case "signature_delta":
				if b := thinking[ev.Index]; b != nil {
					b["signature"] = b["signature"].(string) + ev.Delta.Signature
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				usage.OutputTokens = ev.Usage.OutputTokens
			}
		}
		if ev.Type == "message_stop" {
			stopped = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, streamBodyError(p.Name(), err)
	}
	// Claude ends a message with message_stop, after a message_delta that
	// carries the stop_reason. A stream that ends without either was cut off at
	// a line boundary and must not pass as a complete answer.
	if !stopped && stopReason == "" {
		return nil, streamTruncatedError(p.Name(), "the stream ended without message_stop")
	}

	in := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	res.Usage = agentcoreapi.Usage{
		InputTokens:      in,
		OutputTokens:     usage.OutputTokens,
		TotalTokens:      in + usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadInputTokens,
		CacheWriteTokens: usage.CacheCreationInputTokens,
	}
	res.Content, res.Reasoning = content.String(), reasoning.String()
	if len(thinking) > 0 {
		order := make([]int, 0, len(thinking))
		for i := range thinking {
			order = append(order, i)
		}
		sort.Ints(order)
		blocks := make([]any, 0, len(order))
		for _, i := range order {
			blocks = append(blocks, thinking[i])
		}
		res.Metadata = map[string]any{ClaudeThinkingMetaKey: blocks}
	}

	indexes := make([]int, 0, len(calls))
	for i := range calls {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		call := calls[i]
		raw := call.args.String()
		if strings.TrimSpace(raw) == "" {
			raw = "{}" // a no-argument call streams no input_json_delta
		}
		res.ToolCalls = append(res.ToolCalls, newToolCall(call.id, call.name, json.RawMessage(raw)))
	}
	if stopReason == "max_tokens" {
		res.Truncated = true
		markTruncated(res.ToolCalls)
	}
	if len(res.ToolCalls) > 0 || stopReason == "tool_use" {
		res.FinishReason = agentcoreapi.FinishToolCalls
	} else {
		res.FinishReason = agentcoreapi.FinishFinal
	}
	if emitted && emit != nil {
		if err := emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventMessageStop}); err != nil {
			return nil, err
		}
	}
	return res, nil
}
