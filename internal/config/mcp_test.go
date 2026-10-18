package axconfig

import (
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

func TestReadMCPServerSettings(t *testing.T) {
	t.Run("returns empty when config missing", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)

		servers, err := ReadMCPServerSettings()
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(servers))
	})

	t.Run("returns normalized servers", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)

		_ = gkconfig.Set("mcp.servers", []map[string]any{
			{
				"id":      " filesystem ",
				"command": " npx ",
				"args":    []string{" -y ", " @modelcontextprotocol/server-filesystem ", " ./workspace "},
				"env": map[string]any{
					"NODE_ENV": " development ",
					"EMPTY":    " ",
				},
			},
			{
				"command": " uvx ",
				"args":    []any{" example-mcp "},
			},
		})

		servers, err := ReadMCPServerSettings()
		assert.NoErr(t, err)

		assert.Eq(t, 2, len(servers))
		assert.Eq(t, "filesystem", servers[0].ID)
		assert.Eq(t, "npx", servers[0].Command)
		assert.Eq(t, 3, len(servers[0].Args))
		assert.Eq(t, "-y", servers[0].Args[0])
		assert.Eq(t, "@modelcontextprotocol/server-filesystem", servers[0].Args[1])
		assert.Eq(t, "./workspace", servers[0].Args[2])
		assert.Eq(t, "development", servers[0].Env["NODE_ENV"])
		assert.Eq(t, "", servers[0].Env["EMPTY"])

		assert.Eq(t, "uvx", servers[1].ID)
		assert.Eq(t, "uvx", servers[1].Command)
		assert.Eq(t, 1, len(servers[1].Args))
		assert.Eq(t, "example-mcp", servers[1].Args[0])
	})

	t.Run("rejects an entry without command or url", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)

		_ = gkconfig.Set("mcp.servers", []map[string]any{{"id": "broken", "command": " "}})

		_, err := ReadMCPServerSettings()
		assert.ErrMsgContains(t, err, "needs a command or a url")
	})
}

func TestReadMCPServerSettingsHTTP(t *testing.T) {
	t.Run("accepts a streamable http server with expanded headers", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		t.Setenv("SUGA_TEST_MCP_TOKEN", "secret-token")

		_ = gkconfig.Set("mcp.servers", []map[string]any{
			{
				"id":      " remote ",
				"url":     " https://mcp.example.test/mcp ",
				"headers": map[string]any{"Authorization": "Bearer ${SUGA_TEST_MCP_TOKEN}", "X-Trace": "  "},
			},
			{
				"id":      "local",
				"command": "npx",
			},
		})

		servers, err := ReadMCPServerSettings()
		assert.NoErr(t, err)
		assert.Eq(t, 2, len(servers))
		assert.Eq(t, "remote", servers[0].ID)
		assert.Eq(t, mcp.TransportStreamableHTTP, servers[0].Transport)
		assert.Eq(t, "Bearer secret-token", servers[0].Headers["Authorization"])
		assert.Eq(t, mcp.TransportStdio, servers[1].Transport)
	})

	t.Run("rejects command and url together", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)

		_ = gkconfig.Set("mcp.servers", []map[string]any{{"id": "both", "command": "npx", "url": "https://mcp.example.test/mcp"}})

		_, err := ReadMCPServerSettings()
		assert.ErrMsgContains(t, err, "mutually exclusive")
	})

	t.Run("rejects legacy sse transport", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)

		_ = gkconfig.Set("mcp.servers", []map[string]any{{"id": "old", "url": "https://mcp.example.test/sse", "transport": "sse"}})

		_, err := ReadMCPServerSettings()
		assert.ErrMsgContains(t, err, "legacy sse transport is not supported")
	})

	t.Run("rejects a non http url and a duplicate id", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)

		_ = gkconfig.Set("mcp.servers", []map[string]any{{"id": "bad", "url": "ftp://mcp.example.test/mcp"}})
		_, err := ReadMCPServerSettings()
		assert.ErrMsgContains(t, err, "http or https scheme")

		gkconfig.Reset()
		_ = gkconfig.Set("mcp.servers", []map[string]any{
			{"id": "dup", "command": "npx"},
			{"id": "dup", "command": "uvx"},
		})
		_, err = ReadMCPServerSettings()
		assert.ErrMsgContains(t, err, "duplicate server id")
	})
}

func TestMCPStartupTimeout(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)

	assert.Eq(t, 30*time.Second, MCPStartupTimeout())
	assert.NoErr(t, gkconfig.Set("mcp.startup_timeout_sec", 5))
	assert.Eq(t, 5*time.Second, MCPStartupTimeout())
}
