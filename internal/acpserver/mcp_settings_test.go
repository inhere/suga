package acpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

// TestMcpSettingsAcceptsBothTransports covers the session setup contract: the
// stdio shape and the HTTP shape both map onto one ServerSettings list, and a
// malformed server fails with invalid params instead of being dropped.
func TestMcpSettingsAcceptsBothTransports(t *testing.T) {
	t.Run("stdio server", func(t *testing.T) {
		settings, err := mcpSettings([]acp.McpServer{{Stdio: &acp.McpServerStdio{
			Name: "local", Command: "npx", Args: []string{"-y", "server"},
			Env: []acp.EnvVariable{{Name: "NODE_ENV", Value: "test"}},
		}}})
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(settings))
		assert.Eq(t, "local", settings[0].ID)
		assert.Eq(t, "npx", settings[0].Command)
		assert.Eq(t, mcp.TransportStdio, settings[0].Transport)
		assert.Eq(t, "test", settings[0].Env["NODE_ENV"])
	})

	t.Run("http server keeps the client headers unchanged", func(t *testing.T) {
		t.Setenv("SUGA_TEST_ACP_TOKEN", "host-secret")
		settings, err := mcpSettings([]acp.McpServer{{Http: &acp.McpServerHttpInline{
			Name: "remote", Type: "http", Url: "https://mcp.example.test/mcp",
			Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer ${SUGA_TEST_ACP_TOKEN}"}},
		}}})
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(settings))
		assert.Eq(t, "remote", settings[0].ID)
		assert.Eq(t, "https://mcp.example.test/mcp", settings[0].URL)
		assert.Eq(t, mcp.TransportStreamableHTTP, settings[0].Transport)
		// The ACP payload is taken literally: suga resolves ${VAR} only for its
		// own configuration file, never for a client-supplied header.
		assert.Eq(t, "Bearer ${SUGA_TEST_ACP_TOKEN}", settings[0].Headers["Authorization"])
	})

	t.Run("stdio and http server share the list", func(t *testing.T) {
		settings, err := mcpSettings([]acp.McpServer{
			{Stdio: &acp.McpServerStdio{Name: "local", Command: "npx"}},
			{Http: &acp.McpServerHttpInline{Name: "remote", Url: "http://127.0.0.1:8931/mcp"}},
		})
		assert.NoErr(t, err)
		assert.Eq(t, 2, len(settings))
		assert.Eq(t, mcp.TransportStdio, settings[0].Transport)
		assert.Eq(t, mcp.TransportStreamableHTTP, settings[1].Transport)
	})

	t.Run("malformed servers are rejected", func(t *testing.T) {
		cases := []struct {
			name    string
			servers []acp.McpServer
		}{
			{"no transport", []acp.McpServer{{}}},
			{"both transports", []acp.McpServer{{
				Stdio: &acp.McpServerStdio{Name: "both", Command: "npx"},
				Http:  &acp.McpServerHttpInline{Name: "both", Url: "https://mcp.example.test/mcp"},
			}}},
			{"http without url", []acp.McpServer{{Http: &acp.McpServerHttpInline{Name: "remote"}}}},
			{"http without name", []acp.McpServer{{Http: &acp.McpServerHttpInline{Url: "https://mcp.example.test/mcp"}}}},
			{"ftp url", []acp.McpServer{{Http: &acp.McpServerHttpInline{Name: "remote", Url: "ftp://mcp.example.test/mcp"}}}},
			{"duplicate name", []acp.McpServer{
				{Stdio: &acp.McpServerStdio{Name: "dup", Command: "npx"}},
				{Http: &acp.McpServerHttpInline{Name: "dup", Url: "https://mcp.example.test/mcp"}},
			}},
			{"header without name", []acp.McpServer{{Http: &acp.McpServerHttpInline{
				Name: "remote", Url: "https://mcp.example.test/mcp",
				Headers: []acp.HttpHeader{{Value: "Bearer x"}},
			}}}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := mcpSettings(tc.servers)
				assert.Err(t, err)
				var requestErr *acp.RequestError
				assert.True(t, errors.As(err, &requestErr))
				assert.Eq(t, -32602, requestErr.Code)
			})
		}
	})
}

// TestSessionInputsAcceptsHTTPMCPServer runs the session-setup path that
// session/new and session/load share, so an HTTP server reaches the runtime.
func TestSessionInputsAcceptsHTTPMCPServer(t *testing.T) {
	cwd, servers, err := sessionInputs(t.TempDir(), nil, []acp.McpServer{{Http: &acp.McpServerHttpInline{
		Name: "remote", Url: "https://mcp.example.test/mcp",
		Headers: []acp.HttpHeader{{Name: "X-API-Key", Value: "token"}},
	}}})
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(servers))
	assert.Eq(t, "remote", servers[0].ID)
	assert.Eq(t, "https://mcp.example.test/mcp", servers[0].URL)
	assert.Eq(t, "token", servers[0].Headers["X-API-Key"])
	assert.True(t, cwd != "")

	t.Run("malformed server still fails the session", func(t *testing.T) {
		_, _, err := sessionInputs(t.TempDir(), nil, []acp.McpServer{{}})
		assert.Err(t, err)
	})
}

// TestInitializeAdvertisesHTTPMCP checks the capability clients negotiate
// against: session setup may send HTTP MCP servers.
func TestInitializeAdvertisesHTTPMCP(t *testing.T) {
	agent := New(nil)
	resp, err := agent.Initialize(context.Background(), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	assert.NoErr(t, err)
	assert.True(t, resp.AgentCapabilities.McpCapabilities.Http)
}
