package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// Stream implements the streaming contract over the Responses API's
// Server-Sent Events: output_text deltas are emitted as they arrive; the
// result comes from the terminal event's response object (or, without one,
// the items of response.output_item.done). A stream that ends without a
// terminal event is a broken stream, requested again by retryStream.
func (p *ResponsesProvider) Stream(ctx context.Context, req *agentcoreapi.GenerateRequest, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	ctx = ensureRetryReport(ctx)
	body := p.buildResponsesBody(ctx, req, true)
	logProviderRequest(p.Name(), responsesPath, body)
	res, err := retryStream(ctx, p.retry, p.Name(), emit, func(ctx context.Context, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
		// The attempt's own context: the idle timeout of the body cancels it, which
		// ends this request only - the turn's ctx stays usable for the retry.
		rctx, cancel := context.WithCancel(ctx)
		defer cancel()
		resp, err := p.openStream(rctx, responsesPath, body)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			// A gateway ignoring stream=true answers with the response object.
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, &streamReadError{fmt.Errorf("read responses result: %w", err)}
			}
			logProviderResponse(p.Name(), responsesPath, http.StatusOK, data)
			res, err := p.parseResponsesBody(data)
			if err == nil {
				err = emitWhole(ctx, emit, res.Content)
			}
			return res, err
		}
		stream := resp.Body
		if idle := streamIdleTimeout(p.streamIdleTimeout, body); idle > 0 {
			idleBody := withIdleTimeout(resp.Body, idle, cancel)
			defer idleBody.Close()
			stream = idleBody
		}
		return p.readResponsesStream(ctx, stream, emit)
	})
	if res != nil {
		// The limit this request asked for; 0 = the API's own default.
		res.OutputLimit = p.responsesMaxTokens(req)
	}
	return res, err
}

// emitWhole emits a whole text as one delta and a stop.
func emitWhole(ctx context.Context, emit agentcoreapi.StreamFunc, text string) error {
	if emit == nil || text == "" {
		return nil
	}
	if err := emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventTextDelta, Delta: text}); err != nil {
		return err
	}
	return emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventMessageStop})
}

// responsesEvent is the part of a stream event the provider reads.
type responsesEvent struct {
	Type        string                     `json:"type"`
	Delta       string                     `json:"delta"`
	OutputIndex int                        `json:"output_index"`
	Item        map[string]json.RawMessage `json:"item"`
	Response    *responsesBody             `json:"response"`
	Message     string                     `json:"message"`
	Error       *apiError                  `json:"error"`
}

func (p *ResponsesProvider) readResponsesStream(ctx context.Context, r io.Reader, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error) {
	done := map[int]map[string]json.RawMessage{} // output items by index
	added := map[int]bool{}                      // items started (output_item.added)
	emitted := false
	// cut is set when the stream was cut off (a half line, or a body that ended
	// early); see the tail of this function for who may still rescue it.
	var cut error
	lines := newStreamLines()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	sc.Split(lines.split)
	for sc.Scan() {
		payload, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "data:")
		if !ok {
			continue // event: lines, blank separators, comments
		}
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev responsesEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			if lines.truncated() {
				cut = streamTruncatedError(p.Name(), cutLine)
				break
			}
			return nil, badResponsef("parse responses stream event: %w", err)
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta == "" || emit == nil {
				continue
			}
			if err := emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventTextDelta, Delta: ev.Delta}); err != nil {
				return nil, err
			}
			emitted = true
		case "response.output_item.added":
			added[ev.OutputIndex] = true
		case "response.output_item.done":
			if ev.Item != nil {
				done[ev.OutputIndex] = ev.Item
			}
		case "response.completed", "response.incomplete", "response.done", "response.failed":
			body := responsesBody{}
			if ev.Response != nil {
				body = *ev.Response
			}
			if len(body.Output) == 0 {
				body.Output = orderedItems(done)
			}
			if ev.Type == "response.failed" && body.Error.empty() {
				return nil, badResponsef("responses api error: response failed")
			}
			res, err := p.resultOf(body)
			if err != nil {
				return nil, err
			}
			if emitted && emit != nil {
				if err := emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventMessageStop}); err != nil {
					return nil, err
				}
			}
			return res, nil
		case "error":
			// The error object carries the code/type; an event with only a
			// top-level message has just that text to classify.
			if ev.Error != nil {
				return nil, apiErrorf("responses api error", ev.Error)
			}
			return nil, apiMessageErrorf("responses api error", ev.Message)
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		readErr := streamBodyError(p.Name(), err)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, readErr // a transport failure other than a cut stream
		}
		if cut == nil {
			cut = readErr
		}
	}
	// The stream ended before its terminal event: it was cut off (the half line
	// above included), or a gateway simply left the event out. When every item it
	// started was also finished, the reply is complete but for the summary event
	// (and its usage): use the finished items rather than fail a turn whose text
	// was already shown and cannot be requested again. Otherwise the cut stream is
	// reported to retryStream, which requests the whole answer again.
	if len(done) > 0 && allDone(added, done) {
		slog.Warn("responses stream ended without a terminal event; using the finished output items", "provider", p.Name(), "items", len(done))
		res, err := p.resultOf(responsesBody{Status: "completed", Output: orderedItems(done)})
		if err == nil && emitted && emit != nil {
			err = emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventMessageStop})
		}
		return res, err
	}
	if cut != nil {
		return nil, cut
	}
	return nil, &streamReadError{fmt.Errorf("responses stream ended without a terminal event")}
}

// allDone reports whether every started output item has finished.
func allDone(added map[int]bool, done map[int]map[string]json.RawMessage) bool {
	for i := range added {
		if done[i] == nil {
			return false
		}
	}
	return true
}

// orderedItems returns the collected output items by output_index.
func orderedItems(items map[int]map[string]json.RawMessage) []map[string]json.RawMessage {
	keys := make([]int, 0, len(items))
	for k := range items {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]map[string]json.RawMessage, 0, len(keys))
	for _, k := range keys {
		out = append(out, items[k])
	}
	return out
}
