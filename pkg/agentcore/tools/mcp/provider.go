package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Transport names the wire protocol of one MCP server.
type Transport string

const (
	// TransportStdio runs a local subprocess and speaks JSON-RPC on its stdio.
	TransportStdio Transport = "stdio"
	// TransportStreamableHTTP talks to an HTTP endpoint (MCP Streamable HTTP).
	TransportStreamableHTTP Transport = "streamable_http"
)

// ParseTransport normalizes a configured transport name. An empty value is
// derived from the settings shape (url -> streamable_http, command -> stdio).
// Legacy SSE is rejected explicitly so a typo cannot silently become stdio.
func ParseTransport(raw string, hasURL bool) (Transport, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		if hasURL {
			return TransportStreamableHTTP, nil
		}
		return TransportStdio, nil
	case string(TransportStdio):
		return TransportStdio, nil
	case string(TransportStreamableHTTP), "http":
		return TransportStreamableHTTP, nil
	case "sse":
		return "", errors.New("legacy sse transport is not supported; use streamable_http")
	default:
		return "", fmt.Errorf("unknown transport %q", strings.TrimSpace(raw))
	}
}

// ServerSettings describes one MCP server. A server is either stdio
// (Command/Args/Env) or Streamable HTTP (URL/Headers). The host fills it from
// its own configuration; this package never reads global config.
type ServerSettings struct {
	ID      string
	Command string
	Args    []string
	Env     map[string]string
	// URL, Headers and Transport configure the Streamable HTTP transport.
	URL       string
	Headers   map[string]string
	Transport Transport
	// StartupTimeout bounds initialize + tools/list for this server;
	// 0 means the package default (mcp.startup_timeout_sec).
	StartupTimeout time.Duration
	// CallTimeout bounds one tools/call; 0 means DefaultCallTimeout.
	CallTimeout time.Duration
	// RawBytes caps one tool result (tools.max_spill_bytes); 0 means
	// tools.DefaultRawOutputBytes.
	RawBytes int
}

// Conflict records one tool name a later MCP server could not register because
// an earlier tool already owns it (first-wins, design D8). KeptBy names that
// owner so a runtime diagnostics view can explain the missing tool; it never
// carries an endpoint, argument, environment variable or header value.
type Conflict struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	KeptBy string `json:"kept_by"`
}

type toolClient interface {
	Start(ctx context.Context) error
	ListTools(ctx context.Context) ([]mcpTool, error)
	AsTool(t mcpTool) api.Tool
	Stop()
}

// newClient picks the transport-specific client: a URL server speaks
// Streamable HTTP, everything else starts a stdio subprocess.
var newClient = func(cfg ServerConfig) toolClient {
	if strings.TrimSpace(cfg.URL) != "" {
		return newHTTPClient(cfg)
	}
	return NewClient(cfg)
}

// startupTimeout bounds MCP server spawn, initialize and tools/list when a
// server has no own mcp.startup_timeout_sec. It is a var so tests can shrink it.
var startupTimeout = 30 * time.Second

// startupBudget is the discovery bound of one server: its own configured
// timeout, or the package default.
func startupBudget(settings ServerSettings) time.Duration {
	if settings.StartupTimeout > 0 {
		return settings.StartupTimeout
	}
	return startupTimeout
}

// startErr turns a bare context error into an actionable message.
func startErr(ctx context.Context, budget time.Duration, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s waiting for server: %w", budget, err)
	}
	return err
}

// Provider exposes MCP tools as a runtime capability-governed tool source.
type Provider struct {
	id        string
	client    toolClient
	tools     []api.Tool
	conflicts []Conflict
}

// NewProviderForTools constructs a Provider from already-materialized MCP tools.
// It exists for higher-level wiring tests so app capability assembly can verify
// provider composition without starting a real MCP subprocess.
func NewProviderForTools(id string, toolsList []api.Tool, client toolClient) *Provider {
	return &Provider{
		id:     providerID(id),
		client: client,
		tools:  append([]api.Tool(nil), toolsList...),
	}
}

func NewProvider(ctx context.Context, settings ServerSettings) (*Provider, error) {
	serverCfg := ServerConfig{
		Name:    settings.ID,
		Command: settings.Command,
		Args:    append([]string(nil), settings.Args...),
		Env:     formatEnvMap(settings.Env),
		URL:     strings.TrimSpace(settings.URL),
		Headers: settings.Headers,

		CallTimeout: settings.CallTimeout,
		RawBytes:    settings.RawBytes,
	}

	// Startup (spawn/connect + initialize + tools/list) is bounded and honors the
	// caller's ctx, so a broken server can never hang `suga run`.
	budget := startupBudget(settings)
	startCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	client := newClient(serverCfg)
	if err := client.Start(startCtx); err != nil {
		client.Stop() // Start already cleans up; Stop is idempotent
		return nil, fmt.Errorf("start mcp provider %q: %w", settings.ID, startErr(startCtx, budget, err))
	}

	defs, err := client.ListTools(startCtx)
	if err != nil {
		client.Stop()
		return nil, fmt.Errorf("list mcp tools for %q: %w", settings.ID, startErr(startCtx, budget, err))
	}

	toolsList := make([]api.Tool, 0, len(defs))
	for _, def := range defs {
		toolsList = append(toolsList, client.AsTool(def))
	}

	return &Provider{
		id:     providerID(settings.ID),
		client: client,
		tools:  toolsList,
	}, nil
}

func (p *Provider) ID() string {
	if p == nil {
		return ""
	}
	return p.id
}

func (p *Provider) RegisterTools(registry *tools.Registry) error {
	if p == nil || registry == nil {
		return nil
	}

	// First registration wins: built-in tools and earlier servers keep their
	// names, so a later server cannot impersonate them. Callers register
	// providers in configuration order, which keeps the result reproducible.
	// A dropped tool is logged and recorded as a conflict, never renamed.
	for _, tool := range p.tools {
		if registry.TryRegister(tool) {
			continue
		}
		keptBy := ""
		if owner, err := registry.Get(tool.Name()); err == nil {
			keptBy = toolOwner(owner)
		}
		p.conflicts = append(p.conflicts, Conflict{Server: p.id, Tool: tool.Name(), KeptBy: keptBy})
		slog.Warn("mcp tool dropped: name already registered", "server", p.id, "tool", tool.Name(), "kept_by", keptBy)
	}
	return nil
}

// Conflicts reports the tool-name collisions this provider hit while
// registering; it is empty when every tool registered. The list is a copy, so
// callers can render it without locking the provider.
func (p *Provider) Conflicts() []Conflict {
	if p == nil {
		return nil
	}
	return append([]Conflict(nil), p.conflicts...)
}

// toolOwner names the current owner of a tool for conflict diagnostics: the MCP
// provider id, or "builtin" for any tool that is not served by MCP.
func toolOwner(t api.Tool) string {
	if server := strings.TrimSpace(tools.MCPServerOf(t)); server != "" {
		return providerID(server)
	}
	return "builtin"
}

func (p *Provider) Close() error {
	if p == nil || p.client == nil {
		return nil
	}
	p.client.Stop()
	return nil
}

func providerID(serverID string) string {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		serverID = "default"
	}
	return "mcp." + serverID
}

func formatEnvMap(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}

	items := make([]string, 0, len(env))
	for key, value := range env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		items = append(items, key+"="+strings.TrimSpace(value))
	}
	return items
}
