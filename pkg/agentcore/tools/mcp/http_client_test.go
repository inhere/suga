package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// httpRecord is one request the fake Streamable HTTP server received.
type httpRecord struct {
	method    string
	session   string
	auth      string
	rpcMethod string
}

// httpFixture is a minimal MCP Streamable HTTP server: it answers initialize,
// tools/list and tools/call as JSON or as an event stream, optionally requires
// the session and authorization headers, and records what it saw.
type httpFixture struct {
	mu             sync.Mutex
	records        []httpRecord
	session        string
	deleted        bool
	jsonResponse   bool
	requireSession bool
	token          string
	slowCall       time.Duration
	callError      bool
}

func newHTTPFixture(t *testing.T) (*httpFixture, ServerConfig) {
	t.Helper()
	fixture := &httpFixture{session: "session-1", jsonResponse: true}
	server := httptest.NewServer(http.HandlerFunc(fixture.handle))
	t.Cleanup(server.Close)
	return fixture, ServerConfig{Name: "remote", URL: server.URL}
}

func (f *httpFixture) handle(w http.ResponseWriter, r *http.Request) {
	session := r.Header.Get(mcpSessionHeader)
	if r.Method == http.MethodDelete {
		f.mu.Lock()
		f.deleted = session != ""
		f.records = append(f.records, httpRecord{method: r.Method, session: session})
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}
	var req struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.records = append(f.records, httpRecord{
		method: r.Method, session: session, auth: r.Header.Get("Authorization"), rpcMethod: req.Method,
	})
	f.mu.Unlock()

	if f.requireSession && req.Method != "initialize" && session != f.session {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}
	if f.token != "" && r.Header.Get("Authorization") != f.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if req.Method == "initialize" {
		w.Header().Set(mcpSessionHeader, f.session)
	}
	if req.ID == 0 { // a notification is acknowledged without a JSON-RPC body
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var result any = map[string]any{}
	switch req.Method {
	case "tools/list":
		result = map[string]any{"tools": []map[string]any{{
			"name": "echo", "description": "echo text",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		}}}
	case "tools/call":
		if f.slowCall > 0 {
			time.Sleep(f.slowCall)
		}
		if f.callError {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32000,"message":"boom"}}`, req.ID)
			return
		}
		result = map[string]any{"content": []map[string]any{{"type": "text", "text": "echoed"}}}
	}

	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	if err != nil {
		http.Error(w, "marshal", http.StatusInternalServerError)
		return
	}
	if f.jsonResponse {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
		return
	}
	// Event stream: a server notification first (which the client must skip),
	// then the response for the request.
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
	_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
}

func (f *httpFixture) seen() []httpRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]httpRecord(nil), f.records...)
}

func (f *httpFixture) wasDeleted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleted
}

func TestHTTPClientStreamableHTTPServer(t *testing.T) {
	cases := []struct {
		name         string
		jsonResponse bool
	}{
		{name: "json response", jsonResponse: true},
		{name: "event stream response", jsonResponse: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, cfg := newHTTPFixture(t)
			fixture.jsonResponse = tc.jsonResponse
			fixture.requireSession = true // the session header must be replayed
			fixture.token = "Bearer test-token"
			cfg.Headers = map[string]string{"Authorization": fixture.token}

			// newClient is the dispatch the provider uses: a URL means HTTP.
			client := newClient(cfg)
			httpClientImpl, ok := client.(*httpClient)
			assert.Require(t, assert.True(t, ok))

			ctx := context.Background()
			assert.Require(t, assert.NoErr(t, client.Start(ctx)))
			defs, err := client.ListTools(ctx)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, 1, len(defs))
			assert.Eq(t, "echo", defs[0].Name)

			result, err := client.AsTool(defs[0]).Execute(ctx, api.ToolCall{CallID: "c1", Name: "echo"})
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, "echoed", result.Content)

			tool := client.AsTool(defs[0])
			assert.Eq(t, tools.KindMCP, tools.KindOf(tool))
			assert.Eq(t, "remote", tool.(*mcpToolAdapter).MCPServer())

			// Every request after initialize carries the session and the
			// configured authorization header.
			for _, method := range []string{"tools/list", "tools/call"} {
				var found *httpRecord
				for _, record := range fixture.seen() {
					if record.rpcMethod == method {
						found = &record
					}
				}
				assert.Require(t, assert.True(t, found != nil))
				assert.Eq(t, fixture.session, found.session)
				assert.Eq(t, fixture.token, found.auth)
			}

			client.Stop()
			assert.True(t, fixture.wasDeleted())
			client.Stop() // idempotent
			assert.True(t, httpClientImpl.isStopped())

			// A stopped client refuses further calls instead of hanging.
			_, err = client.ListTools(ctx)
			assert.ErrMsgContains(t, err, "connection closed")
		})
	}
}

func TestHTTPClientCallTimeout(t *testing.T) {
	t.Run("slow tool call fails within its timeout", func(t *testing.T) {
		fixture, cfg := newHTTPFixture(t)
		fixture.slowCall = 2 * time.Second
		cfg.CallTimeout = 200 * time.Millisecond

		client := newClient(cfg)
		ctx := context.Background()
		assert.Require(t, assert.NoErr(t, client.Start(ctx)))
		t.Cleanup(client.Stop)
		defs, err := client.ListTools(ctx)
		assert.Require(t, assert.NoErr(t, err))

		begin := time.Now()
		_, err = client.AsTool(defs[0]).Execute(ctx, api.ToolCall{CallID: "c1", Name: "echo"})
		assert.ErrMsgContains(t, err, "did not answer within 200ms")
		assert.True(t, time.Since(begin) < time.Second)
	})

	t.Run("http discovery timeout is reported", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second) // never answers in time
		}))
		t.Cleanup(server.Close)
		settings := ServerSettings{
			ID: "slow", URL: server.URL, Transport: TransportStreamableHTTP,
			StartupTimeout: 200 * time.Millisecond,
		}
		begin := time.Now()
		_, err := NewProvider(context.Background(), settings)
		assert.ErrMsgContains(t, err, "timed out")
		assert.ErrMsgContains(t, err, `"slow"`)
		assert.True(t, time.Since(begin) < 2*time.Second)
	})
}

func TestHTTPClientServerError(t *testing.T) {
	fixture, cfg := newHTTPFixture(t)
	fixture.callError = true

	client := newClient(cfg)
	ctx := context.Background()
	assert.Require(t, assert.NoErr(t, client.Start(ctx)))
	t.Cleanup(client.Stop)
	defs, err := client.ListTools(ctx)
	assert.Require(t, assert.NoErr(t, err))

	_, err = client.AsTool(defs[0]).Execute(ctx, api.ToolCall{CallID: "c1", Name: "echo"})
	assert.ErrMsgContains(t, err, "mcp error -32000: boom")
}

func TestNewClientSelectsTransport(t *testing.T) {
	t.Run("url selects the http client", func(t *testing.T) {
		client := newClient(ServerConfig{Name: "remote", URL: "https://mcp.example.test/mcp"})
		_, ok := client.(*httpClient)
		assert.True(t, ok)
	})

	t.Run("command selects the stdio client", func(t *testing.T) {
		client := newClient(ServerConfig{Name: "local", Command: "npx"})
		_, ok := client.(*Client)
		assert.True(t, ok)
	})
}

// TestParseTransport covers the configured transport names, including the
// explicit rejection of the legacy SSE transport.
func TestParseTransport(t *testing.T) {
	t.Run("derives the transport from the settings shape", func(t *testing.T) {
		transport, err := ParseTransport("", true)
		assert.NoErr(t, err)
		assert.Eq(t, TransportStreamableHTTP, transport)
		transport, err = ParseTransport("", false)
		assert.NoErr(t, err)
		assert.Eq(t, TransportStdio, transport)
	})

	t.Run("accepts the explicit names", func(t *testing.T) {
		transport, err := ParseTransport(" Streamable_HTTP ", false)
		assert.NoErr(t, err)
		assert.Eq(t, TransportStreamableHTTP, transport)
	})

	t.Run("rejects legacy sse with an actionable message", func(t *testing.T) {
		_, err := ParseTransport("sse", true)
		assert.ErrMsgContains(t, err, "legacy sse transport is not supported")
		assert.True(t, strings.Contains(err.Error(), "streamable_http"))
	})

	t.Run("rejects an unknown transport", func(t *testing.T) {
		_, err := ParseTransport("carrier-pigeon", true)
		assert.ErrMsgContains(t, err, "unknown transport")
	})
}
