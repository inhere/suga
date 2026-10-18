// Package mcp implements a JSON-RPC 2.0 client for MCP (Model Context Protocol) servers.
// It communicates with MCP servers via stdio (subprocess) or SSE transport.
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
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/extproc"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// ServerConfig describes an MCP server to connect to. It is either a stdio
// subprocess (Command, Args, Env) or a Streamable HTTP endpoint (URL, Headers);
// a non-empty URL selects the HTTP transport.
type ServerConfig struct {
	Name    string   // logical server name
	Command string   // executable
	Args    []string // command arguments
	Env     []string // extra environment variables (KEY=VALUE)
	// URL is the Streamable HTTP endpoint of the server; when set, no
	// subprocess is started and Headers are sent with every request.
	URL string
	// Headers are extra HTTP headers (for example Authorization). Values come
	// from ${VAR} expansion in the config reader and must never be logged.
	Headers map[string]string
	// CallTimeout bounds one tools/call (mcp.call_timeout_sec); 0 means
	// DefaultCallTimeout. A server that never answers must not hang the turn.
	CallTimeout time.Duration
	// RawBytes caps the text of one tool result (head and tail kept); 0 means
	// tools.DefaultRawOutputBytes. MCP servers have no output limit of their
	// own, so this is what bounds the output layer's saved copy.
	RawBytes int
}

// DefaultCallTimeout is the default bound on one tools/call.
const DefaultCallTimeout = 120 * time.Second

const (
	// stderrTailLimit bounds how much subprocess stderr is kept for diagnostics.
	stderrTailLimit = 4096
	// stopWaitTimeout bounds how long Stop waits for the killed subprocess to be reaped.
	stopWaitTimeout = 3 * time.Second
	// exitDiagWait bounds how long an error path waits for stderr to be fully drained.
	exitDiagWait = 500 * time.Millisecond
	// maxMessageSize is the largest single JSON-RPC line the reader accepts.
	maxMessageSize = 8 << 20
)

// Client connects to an MCP server via stdio and exposes its tools.
type Client struct {
	cfg     ServerConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	stderr  *tailBuffer
	mu      sync.Mutex // guards pending and done
	wmu     sync.Mutex // serializes stdin writes (may block on a full pipe)
	seq     atomic.Int64
	pending map[int64]chan *jsonRPCResponse

	// done is closed once the stdout reader stops (EOF or read error); after that
	// no response can ever arrive, so every call must fail instead of waiting.
	done    chan struct{}
	readErr error // set before done is closed
	// exited is closed after the subprocess has been waited on (reaped).
	exited   chan struct{}
	stopOnce sync.Once
}

// NewClient creates an MCP client from cfg but does not start the server yet.
func NewClient(cfg ServerConfig) *Client {
	return &Client{
		cfg:     cfg,
		pending: make(map[int64]chan *jsonRPCResponse),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
		stderr:  &tailBuffer{limit: stderrTailLimit},
	}
}

// Start launches the MCP server subprocess and performs the JSON-RPC initialize handshake.
// ctx only bounds the handshake: the subprocess lifetime is owned by Stop, so a
// startup-scoped (timeout) ctx being released does not kill a healthy server.
// On any failure the subprocess is stopped and reaped before returning.
func (c *Client) Start(ctx context.Context) error {
	c.cmd = exec.Command(c.cfg.Command, c.cfg.Args...)
	// MCP server env extends the process env (PATH and the like survive) minus LLM
	// credentials; the server's own configured env is appended last and wins.
	c.cmd.Env = extproc.StripCredentials(os.Environ(), c.cfg.Env)
	c.cmd.Stderr = c.stderr

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp stdin pipe: %w", err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp stdout pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("mcp start %q: %w", c.cfg.Command, err)
	}

	c.stdin = stdin
	c.scanner = bufio.NewScanner(stdout)
	c.scanner.Buffer(make([]byte, 0, 64*1024), maxMessageSize)
	go func() {
		c.readLoop()
		// Wait only after stdout is drained: Wait closes the pipe.
		_ = c.cmd.Wait()
		close(c.exited)
	}()

	// Handshake: initialize.
	_, err = c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]any{"name": "suga", "version": "0.1.0"},
		"capabilities":    map[string]any{},
	})
	if err != nil {
		c.Stop() // reap the subprocess so stderr is complete and nothing leaks
		return c.diagnose(err)
	}
	return nil
}

// diagnose decorates err with the server name and the stderr tail (if any).
func (c *Client) diagnose(err error) error {
	tail := strings.TrimSpace(c.stderr.String())
	if tail == "" {
		return fmt.Errorf("mcp server %q: %w", c.cfg.Name, err)
	}
	return fmt.Errorf("mcp server %q: %w (stderr: %s)", c.cfg.Name, err, tail)
}

// ListTools queries the MCP server for its available tools.
func (c *Client) ListTools(ctx context.Context) ([]mcpTool, error) {
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
func (c *Client) AsTool(t mcpTool) api.Tool {
	return &mcpToolAdapter{client: c, def: t}
}

// Stop terminates the MCP server subprocess and reaps it. It is idempotent and
// never blocks longer than stopWaitTimeout, even if Wait is stuck (for example a
// grandchild keeps the stdout pipe open).
func (c *Client) Stop() {
	c.stopOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd == nil || c.cmd.Process == nil {
			return
		}
		_ = c.cmd.Process.Kill()
		select {
		case <-c.exited:
		case <-time.After(stopWaitTimeout):
			slog.Warn("mcp: timed out waiting for server to exit", "server", c.cfg.Name)
		}
	})
}

// serverName is the name used in diagnostics and tool results.
func (c *Client) serverName() string { return c.cfg.Name }

// callTimeout is the per-call bound (0 = DefaultCallTimeout).
func (c *Client) callTimeout() time.Duration { return c.cfg.CallTimeout }

// ─── JSON-RPC internals ───────────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.seq.Add(1)
	ch := make(chan *jsonRPCResponse, 1)

	req := jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	// Register under the lock and re-check done, so a call racing with reader
	// shutdown either fails here or is failed by failPending; never hangs.
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, c.exitError()
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	// The write may block on a full pipe if the server stops reading, so it runs
	// off-thread and the select below keeps honoring ctx and subprocess exit.
	writeErr := make(chan error, 1)
	go func() {
		c.wmu.Lock()
		_, err := c.stdin.Write(data)
		c.wmu.Unlock()
		writeErr <- err
	}()

	for {
		select {
		case <-ctx.Done():
			forget()
			return nil, ctx.Err()
		case err := <-writeErr:
			if err != nil {
				forget()
				select {
				case <-c.done:
					return nil, c.exitError()
				default:
				}
				return nil, fmt.Errorf("mcp write: %w", err)
			}
			writeErr = nil // keep waiting for the response
		case resp := <-ch:
			return decodeResponse(resp)
		case <-c.done:
			// A response may have been delivered just before the reader stopped.
			select {
			case resp := <-ch:
				return decodeResponse(resp)
			default:
			}
			forget()
			return nil, c.exitError()
		}
	}
}

func decodeResponse(resp *jsonRPCResponse) (json.RawMessage, error) {
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// exitError describes why the server connection is gone, including the stderr tail.
func (c *Client) exitError() error {
	// Give the subprocess a moment to be reaped so stderr is fully captured.
	select {
	case <-c.exited:
	case <-time.After(exitDiagWait):
	}
	reason := "connection closed"
	if c.readErr != nil {
		reason = c.readErr.Error()
	}
	return c.diagnose(fmt.Errorf("server exited: %s", reason))
}

func (c *Client) readLoop() {
	defer func() {
		// Fail every in-flight call and make later calls fail immediately.
		c.mu.Lock()
		close(c.done)
		c.pending = make(map[int64]chan *jsonRPCResponse)
		c.mu.Unlock()
	}()
	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			slog.Warn("mcp: failed to parse response", "line", string(line), "err", err)
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- &resp
		}
	}
	if err := c.scanner.Err(); err != nil {
		c.readErr = err
	} else {
		c.readErr = io.EOF
	}
}

// tailBuffer keeps only the last limit bytes written to it (subprocess stderr).
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.limit:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// ─── mcpTool / adapter ────────────────────────────────────────────────────────

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// mcpToolAdapter exposes one MCP tool as an api.Tool. Its client is the
// transport-neutral toolCaller, so stdio and HTTP tools behave identically.
type mcpToolAdapter struct {
	client toolCaller
	def    mcpTool
}

// toolCaller is what the tool adapter needs from a transport-specific client:
// one JSON-RPC call, its server name and timeout, and the result cap.
type toolCaller interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	serverName() string
	callTimeout() time.Duration
	capRaw(s string) string
}

// MCPServer returns the configured name of the server that exposes this tool.
func (a *mcpToolAdapter) MCPServer() string   { return a.client.serverName() }
func (a *mcpToolAdapter) Name() string        { return a.def.Name }
func (a *mcpToolAdapter) Description() string { return a.def.Description }
func (a *mcpToolAdapter) Kind() tools.Kind    { return tools.KindMCP }

// mcpPlaceholderArg is the optional argument a tool without parameters gets in
// its schema: some OpenAI-compatible gateways turn an empty properties object
// into null and reject the whole request (every tool of the turn with it), so
// a server's no-argument tool (get-env, toggle-..., ...) broke every request.
// Execute drops it before the call reaches the server.
const mcpPlaceholderArg = "_unused"

func (a *mcpToolAdapter) Schema() api.ToolSchema {
	params := map[string]any{}
	_ = json.Unmarshal(a.def.InputSchema, &params)
	if params["type"] == nil {
		params["type"] = "object"
	}
	if props, _ := params["properties"].(map[string]any); len(props) == 0 {
		params["properties"] = map[string]any{mcpPlaceholderArg: map[string]any{
			"type": "string", "description": "Unused: this tool takes no arguments; leave it out.",
		}}
	}
	return api.ToolSchema{
		Name:        a.def.Name,
		Description: a.def.Description,
		InputSchema: params,
	}
}

func (a *mcpToolAdapter) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	timeout := a.client.callTimeout()
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := call.Arguments
	if _, ok := args[mcpPlaceholderArg]; ok {
		args = make(map[string]any, len(call.Arguments))
		for k, v := range call.Arguments {
			if k != mcpPlaceholderArg {
				args[k] = v
			}
		}
	}
	result, err := a.client.call(callCtx, "tools/call", map[string]any{
		"name":      a.def.Name,
		"arguments": args,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, fmt.Errorf("mcp tool %s (server %s) did not answer within %s", a.def.Name, a.client.serverName(), timeout)
		}
		return nil, err
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return &api.ToolResult{
			CallID:  call.CallID,
			Name:    a.def.Name,
			Content: a.client.capRaw(string(result)),
		}, nil
	}
	var buf bytes.Buffer
	for _, c := range resp.Content {
		buf.WriteString(c.Text)
	}
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    a.def.Name,
		Content: a.client.capRaw(buf.String()),
	}, nil
}

// capRaw cuts a tool result to the configured raw-output cap.
func (c *Client) capRaw(s string) string {
	limit := c.cfg.RawBytes
	if limit <= 0 {
		limit = tools.DefaultRawOutputBytes
	}
	return tools.TruncateHeadTail(s, limit)
}
