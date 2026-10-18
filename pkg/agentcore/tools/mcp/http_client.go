package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

const (
	// mcpSessionHeader carries the Streamable HTTP session id the server assigns
	// during initialize; every later request must send it back.
	mcpSessionHeader = "Mcp-Session-Id"
	// streamableHTTPVersion is the MCP protocol revision this client speaks.
	streamableHTTPVersion = "2025-03-26"
	// maxHTTPResponseBytes bounds one HTTP response body so a runaway or broken
	// server cannot exhaust memory; it matches the stdio line cap.
	maxHTTPResponseBytes = maxMessageSize
	// httpStatusSnippetBytes bounds the error body kept for diagnostics.
	httpStatusSnippetBytes = 512
	// httpStopTimeout bounds the session-terminating DELETE in Stop.
	httpStopTimeout = 3 * time.Second
)

// httpClient talks to one MCP server over Streamable HTTP: every JSON-RPC
// request is a POST to the configured URL, and the answer is either one
// application/json body or a text/event-stream whose events carry the JSON-RPC
// messages. It implements toolClient exactly like the stdio Client, so the
// Provider, the registry and every permission layer stay transport-agnostic.
type httpClient struct {
	cfg  ServerConfig
	http *http.Client
	seq  atomic.Int64

	mu      sync.Mutex
	session string // Mcp-Session-Id assigned by the server ("" before initialize)
	stopped bool
}

// NewHTTPClient creates a Streamable HTTP MCP client from cfg but does not
// contact the server yet (Start performs initialize).
func NewHTTPClient(cfg ServerConfig) *httpClient {
	return newHTTPClient(cfg)
}

func newHTTPClient(cfg ServerConfig) *httpClient {
	return &httpClient{cfg: cfg, http: &http.Client{}}
}

// Start performs the JSON-RPC initialize handshake. ctx only bounds the
// handshake; the HTTP session lives until Stop.
func (c *httpClient) Start(ctx context.Context) error {
	if _, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": streamableHTTPVersion,
		"clientInfo":      map[string]any{"name": "suga", "version": "0.1.0"},
		"capabilities":    map[string]any{},
	}); err != nil {
		c.Stop() // release a session the server may already have created
		return fmt.Errorf("mcp http server %q: %w", c.cfg.Name, err)
	}
	// The initialized notification has no reply. Some servers create their
	// session only after it, so a failure is reported but does not fail the
	// handshake that already succeeded.
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		slog.Warn("mcp: initialized notification failed", "server", c.cfg.Name, "err", err)
	}
	return nil
}

// ListTools queries the MCP server for its available tools.
func (c *httpClient) ListTools(ctx context.Context) ([]mcpTool, error) {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	return resp.Tools, nil
}

// AsTool wraps an MCP tool definition as a api.Tool for the registry.
func (c *httpClient) AsTool(t mcpTool) api.Tool {
	return &mcpToolAdapter{client: c, def: t}
}

// Stop ends the HTTP session (DELETE with the session id when the server gave
// one) and makes later calls fail. It is idempotent and bounded.
func (c *httpClient) Stop() {
	c.mu.Lock()
	session, stopped := c.session, c.stopped
	c.stopped = true
	c.mu.Unlock()
	if stopped || session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpStopTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.cfg.URL, nil)
	if err != nil {
		return
	}
	req.Header.Set(mcpSessionHeader, session)
	resp, err := c.http.Do(req)
	if err != nil {
		slog.Debug("mcp: session delete failed", "server", c.cfg.Name, "err", err)
		return
	}
	_ = resp.Body.Close()
}

func (c *httpClient) serverName() string { return c.cfg.Name }
func (c *httpClient) callTimeout() time.Duration {
	return c.cfg.CallTimeout
}

// capRaw cuts a tool result to the configured raw-output cap.
func (c *httpClient) capRaw(s string) string {
	limit := c.cfg.RawBytes
	if limit <= 0 {
		limit = tools.DefaultRawOutputBytes
	}
	return tools.TruncateHeadTail(s, limit)
}

// ─── HTTP round trips ─────────────────────────────────────────────────────────

// call sends one request and returns the JSON-RPC result for it.
func (c *httpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.seq.Add(1)
	return c.roundTrip(ctx, method, jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}, id)
}

// notify sends one JSON-RPC notification: no id, so no JSON-RPC answer.
func (c *httpClient) notify(ctx context.Context, method string, params any) error {
	_, err := c.roundTrip(ctx, method, jsonRPCNotification{JSONRPC: "2.0", Method: method, Params: params}, 0)
	return err
}

// jsonRPCNotification is a JSON-RPC 2.0 notification (a request without an id).
type jsonRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// roundTrip POSTs one payload and decodes the answer for id (id 0 = a
// notification, which is only acknowledged). The request context is the only
// deadline, so a canceled call releases its response body at once.
func (c *httpClient) roundTrip(ctx context.Context, method string, payload any, id int64) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("mcp http %s: %w", method, err)
	}
	if c.isStopped() {
		return nil, fmt.Errorf("mcp http server %q: connection closed", c.cfg.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp http %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Streamable HTTP servers answer either shape; the client must accept both.
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Configured headers (Authorization and the like) come from ${VAR}
	// expansion in the config reader; they are never logged.
	for name, value := range c.cfg.Headers {
		req.Header.Set(name, value)
	}
	if session := c.sessionID(); session != "" {
		req.Header.Set(mcpSessionHeader, session)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp http %s: %w", method, err)
	}
	defer resp.Body.Close() // every path must close the body of a live connection
	if session := strings.TrimSpace(resp.Header.Get(mcpSessionHeader)); session != "" {
		c.setSessionID(session)
	}
	reader := io.LimitReader(resp.Body, maxHTTPResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(reader, httpStatusSnippetBytes))
		return nil, fmt.Errorf("mcp http %s: status %d: %s", method, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if id == 0 {
		return nil, nil // notification accepted (normally 202 with an empty body)
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return readSSEResponse(reader, id)
	}
	return readJSONResponse(reader, id)
}

// readJSONResponse decodes one application/json body: a single JSON-RPC
// response or a batch, and returns the result for id.
func readJSONResponse(r io.Reader, id int64) (json.RawMessage, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("mcp http read response: %w", err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("mcp http: empty response body")
	}
	if trimmed[0] == '[' {
		var batch []jsonRPCResponse
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return nil, fmt.Errorf("mcp http parse response: %w", err)
		}
		for i := range batch {
			if batch[i].ID == id {
				return decodeResponse(&batch[i])
			}
		}
		return nil, fmt.Errorf("mcp http: no response for id %d", id)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return nil, fmt.Errorf("mcp http parse response: %w", err)
	}
	if resp.ID != id {
		return nil, fmt.Errorf("mcp http: response id %d, want %d", resp.ID, id)
	}
	return decodeResponse(&resp)
}

// readSSEResponse reads one text/event-stream body until the event carrying the
// response for id arrives; other events (notifications, keep-alives) are
// skipped. A stream that ends first is an error, so a call can never succeed
// without an answer.
func readSSEResponse(r io.Reader, id int64) (json.RawMessage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageSize)
	var data []string
	flush := func() (json.RawMessage, bool, error) {
		if len(data) == 0 {
			return nil, false, nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		return matchSSEEvent(payload, id)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" { // an empty line ends one event
			result, ok, err := flush()
			if err != nil {
				return nil, err
			}
			if ok {
				return result, nil
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mcp http read event stream: %w", err)
	}
	if result, ok, err := flush(); ok || err != nil {
		return result, err
	}
	return nil, fmt.Errorf("mcp http: event stream ended without a response for id %d", id)
}

// matchSSEEvent parses one SSE event payload and reports whether it is the
// response for id. Unknown or non-JSON events are ignored rather than failing
// the call, because servers may interleave their own notifications.
func matchSSEEvent(payload string, id int64) (json.RawMessage, bool, error) {
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return nil, false, nil
	}
	if resp.ID != id {
		return nil, false, nil
	}
	result, err := decodeResponse(&resp)
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
}

func (c *httpClient) sessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

func (c *httpClient) setSessionID(session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = session
}

func (c *httpClient) isStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}
