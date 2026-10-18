package axconfig

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

// ReadMCPServerSettings reads mcp.servers: stdio servers (command) and
// Streamable HTTP servers (url) share the list. A malformed entry, a duplicate
// id or a command/url mix fails startup instead of silently dropping a server,
// so a typo cannot disable a configured server unnoticed.
func ReadMCPServerSettings() ([]mcp.ServerSettings, error) {
	raw := gkconfig.Get("mcp.servers")
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		typedItems, ok := raw.([]map[string]any)
		if !ok || len(typedItems) == 0 {
			return nil, nil
		}

		items = make([]any, 0, len(typedItems))
		for _, item := range typedItems {
			items = append(items, item)
		}
	}

	servers := make([]mcp.ServerSettings, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		server, err := normalizeMCPServerSettings(item)
		if err != nil {
			return nil, err
		}
		if seen[server.ID] {
			return nil, fmt.Errorf("mcp.servers: duplicate server id %q", server.ID)
		}
		seen[server.ID] = true
		servers = append(servers, server)
	}
	return servers, nil
}

func normalizeMCPServerSettings(raw any) (mcp.ServerSettings, error) {
	item, ok := raw.(map[string]any)
	if !ok {
		return mcp.ServerSettings{}, fmt.Errorf("mcp.servers entry must be a mapping")
	}

	command := strings.TrimSpace(stringValue(item["command"]))
	serverURL := strings.TrimSpace(stringValue(item["url"]))
	id := strings.TrimSpace(stringValue(item["id"]))
	if id == "" {
		id = command
	}
	if id == "" {
		id = serverURL
	}
	if id == "" {
		return mcp.ServerSettings{}, fmt.Errorf("mcp server needs a command or a url")
	}
	if command == "" && serverURL == "" {
		return mcp.ServerSettings{}, fmt.Errorf("mcp server %q needs a command or a url", id)
	}
	if command != "" && serverURL != "" {
		return mcp.ServerSettings{}, fmt.Errorf("mcp server %q: command and url are mutually exclusive", id)
	}

	transport, err := mcp.ParseTransport(stringValue(item["transport"]), serverURL != "")
	if err != nil {
		return mcp.ServerSettings{}, fmt.Errorf("mcp server %q: %w", id, err)
	}
	if transport == mcp.TransportStreamableHTTP && serverURL == "" {
		return mcp.ServerSettings{}, fmt.Errorf("mcp server %q: transport %s requires url", id, transport)
	}
	if transport == mcp.TransportStdio && command == "" {
		return mcp.ServerSettings{}, fmt.Errorf("mcp server %q: transport %s requires command", id, transport)
	}

	headers := expandEnvMap(normalizeStringMap(item["headers"]))
	if len(headers) > 0 && transport != mcp.TransportStreamableHTTP {
		return mcp.ServerSettings{}, fmt.Errorf("mcp server %q: headers need a streamable_http url", id)
	}
	if transport == mcp.TransportStreamableHTTP {
		if err := validateMCPServerURL(serverURL); err != nil {
			return mcp.ServerSettings{}, fmt.Errorf("mcp server %q: %w", id, err)
		}
	}

	return mcp.ServerSettings{
		ID:        id,
		Command:   command,
		Args:      normalizeStringList(item["args"]),
		Env:       normalizeStringMap(item["env"]),
		URL:       serverURL,
		Headers:   headers,
		Transport: transport,
	}, nil
}

// validateMCPServerURL accepts only explicit http/https endpoints: the MCP
// client has no other transport, and a bare host would hide the scheme.
func validateMCPServerURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url needs an http or https scheme")
	}
	if parsed.Host == "" {
		return fmt.Errorf("url needs a host")
	}
	return nil
}

func normalizeStringList(raw any) []string {
	switch value := raw.(type) {
	case []string:
		items := make([]string, 0, len(value))
		for _, item := range value {
			items = append(items, strings.TrimSpace(item))
		}
		return items
	case []any:
		items := make([]string, 0, len(value))
		for _, item := range value {
			items = append(items, strings.TrimSpace(stringValue(item)))
		}
		return items
	default:
		return nil
	}
}

func normalizeStringMap(raw any) map[string]string {
	value, ok := raw.(map[string]any)
	if !ok || len(value) == 0 {
		return nil
	}

	items := make(map[string]string, len(value))
	for key, val := range value {
		items[strings.TrimSpace(key)] = strings.TrimSpace(stringValue(val))
	}
	return items
}

func stringValue(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	default:
		return ""
	}
}

// MCPCallTimeout reads mcp.call_timeout_sec (default 120): the bound on one
// MCP tools/call.
func MCPCallTimeout() time.Duration {
	return time.Duration(gkconfig.Int("mcp.call_timeout_sec", 120)) * time.Second
}

// MCPStartupTimeout reads mcp.startup_timeout_sec (default 30): the bound on one
// server's initialize + tools/list discovery, so a slow HTTP server cannot
// extend the startup of the others.
func MCPStartupTimeout() time.Duration {
	return time.Duration(gkconfig.Int("mcp.startup_timeout_sec", 30)) * time.Second
}
