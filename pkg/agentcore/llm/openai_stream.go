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
	"time"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
)

// newStreamClient returns the client of streamed requests: the wait for the
// response headers is bounded by responseHeader, the body is not (see
// DefaultResponseHeaderTimeout and ResponseHeaderTimeout). proxy, when set,
// replaces the environment proxies of the cloned default transport.
func newStreamClient(responseHeader time.Duration, proxy netproxy.ProxyFunc) *http.Client {
	return newProviderClient(0, newProviderTransport(responseHeader, proxy))
}

// Stream implements the public streaming contract with Server-Sent Events:
// text deltas are emitted as they arrive; tool calls, reasoning_content and
// usage are accumulated into the returned result. The connection is retried
// by openStream; a stream that breaks while being read is requested again by
// retryStream (after a StreamEventRetry when text was already emitted).
func (p *OpenAIProvider) Stream(ctx context.Context, req *agentcoreapi.GenerateRequest, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	ctx = ensureRetryReport(ctx)
	model := req.Model
	if model == "" {
		model = p.model
	}
	body := p.buildRequestBody(req, model, true)
	body["stream_options"] = map[string]any{"include_usage": true}
	logProviderRequest(p.Name(), "/chat/completions", body)

	res, err := retryStream(ctx, p.retry, p.Name(), emit, func(ctx context.Context, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
		// The attempt's own context: the idle timeout of the body cancels it, which
		// ends this request only - the turn's ctx stays usable for the retry.
		rctx, cancel := context.WithCancel(ctx)
		defer cancel()
		resp, err := p.openStream(rctx, "/chat/completions", body)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			// Some gateways ignore stream=true and answer with a plain completion.
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
		// The limit this request asked for; 0 = the API's own default.
		res.OutputLimit = req.MaxTokens
	}
	return res, err
}

// readWhole parses a non-streamed completion and emits its text in one delta.
func (p *OpenAIProvider) readWhole(ctx context.Context, r io.Reader, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, &streamReadError{fmt.Errorf("read openai response: %w", err)}
	}
	logProviderResponse(p.Name(), "/chat/completions", http.StatusOK, data)
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
func (p *OpenAIProvider) openStream(ctx context.Context, path string, body map[string]any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := postWithRetry(ctx, p.streamClient, p.retry, p.Name(), path, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		applyHeaders(ctx, req, p.headers, headerVars{Model: p.model, Provider: p.name})
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
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
	logProviderResponse(p.Name(), path, resp.StatusCode, respData)
	// 4xx: report the API's own message, as parseResponse does, but keep the
	// status so the failure is classified as a rejection (ErrorKind).
	var apiErr struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(respData, &apiErr) == nil && apiErr.Error != nil {
		return nil, statusErr(resp.StatusCode, withRequestID(ctx, apiErrorf("openai api error", apiErr.Error)))
	}
	return nil, statusErr(resp.StatusCode, withRequestID(ctx, apiMessageErrorf(fmt.Sprintf("openai api error %d", resp.StatusCode), strings.TrimSpace(string(respData)))))
}

// streamChunk is one chat.completion.chunk event.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		PromptCacheHitTokens int     `json:"prompt_cache_hit_tokens"` // DeepSeek native
		CostUSD              float64 `json:"cost_usd"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

// streamToolCall accumulates one tool call's fragments by its stream index.
type streamToolCall struct {
	id, name string
	args     strings.Builder
}

// readStream consumes "data: {...}" lines until "data: [DONE]" or EOF.
func (p *OpenAIProvider) readStream(ctx context.Context, r io.Reader, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	var content, reasoning strings.Builder
	calls := map[int]*streamToolCall{}
	res := &agentcoreapi.GenerateResult{}
	emitted := false
	finishReason := "" // the last non-empty finish_reason ("length" = cut off)
	done := false      // the provider sent "data: [DONE]"

	lines := newStreamLines()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	scanner.Split(lines.split)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue // blank separators, ": keep-alive" comments, event: lines
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		logStreamChunk(p.Name(), payload)
		if payload == "[DONE]" {
			done = true
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			if lines.truncated() {
				return nil, streamTruncatedError(p.Name(), cutLine)
			}
			return nil, badResponsef("parse openai stream chunk: %w", err)
		}
		if chunk.Error != nil {
			return nil, apiErrorf("openai api error", chunk.Error)
		}
		if u := chunk.Usage; u != nil {
			res.Usage = agentcoreapi.Usage{
				InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, TotalTokens: u.TotalTokens,
				CacheReadTokens: cachedTokens(u.PromptTokensDetails.CachedTokens, u.PromptCacheHitTokens), CostUSD: u.CostUSD,
			}
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			d := choice.Delta
			reasoning.WriteString(d.ReasoningContent)
			if d.Content != "" {
				content.WriteString(d.Content)
				if emit != nil {
					if err := emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventTextDelta, Delta: d.Content}); err != nil {
						return nil, err
					}
				}
				emitted = true
			}
			for _, tc := range d.ToolCalls {
				call := calls[tc.Index]
				if call == nil {
					call = &streamToolCall{}
					calls[tc.Index] = call
				}
				if tc.ID != "" {
					call.id = tc.ID
				}
				if tc.Function.Name != "" {
					call.name = tc.Function.Name
				}
				call.args.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, streamBodyError(p.Name(), err)
	}
	// A stream that ends at a line boundary without [DONE] and without a
	// finish_reason is an answer that was cut off: a gateway that simply omits
	// the marker always sends the finish_reason, so a missing pair means the
	// connection died (the half-line case above is reported earlier).
	if !done && finishReason == "" {
		return nil, streamTruncatedError(p.Name(), "the stream ended without [DONE] and without a finish_reason")
	}

	res.Content, res.Reasoning = content.String(), reasoning.String()
	indexes := make([]int, 0, len(calls))
	for i := range calls {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		call := calls[i]
		raw := call.args.String()
		if strings.TrimSpace(raw) == "" {
			raw = "{}" // a no-argument call may stream no argument fragment at all
		}
		res.ToolCalls = append(res.ToolCalls, newToolCall(call.id, call.name, json.RawMessage(raw)))
	}
	// Reasoning-channel text inside the arguments: retryStream requests it again.
	if err := checkCorruptedToolCalls(res.ToolCalls); err != nil {
		return nil, err
	}
	if finishReason == "length" {
		res.Truncated = true
		markTruncated(res.ToolCalls)
	}
	if len(res.ToolCalls) > 0 {
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
