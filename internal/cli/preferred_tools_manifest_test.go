package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/wiring"
)

// TestManifestPreferredTools: `suga inspect manifest` reports every declared preferred
// tool (also the unavailable ones) with its reason in text and JSON, and never
// leaks the declared server's arguments.
func TestManifestPreferredTools(t *testing.T) {
	manifestCLIEnv(t)
	assert.NoErr(t, gkconfig.Set("mcp.servers", []any{map[string]any{
		"id": "srv", "command": "srv-bin", "args": []any{"--token", "secret"},
	}}))
	assert.NoErr(t, gkconfig.Set("tools.preferred", []map[string]any{
		{"name": "suga-missing-preferred-command", "kind": "command", "when": "never"},
		{"name": "srv", "kind": "mcp", "when": "graph"},
		{"name": "unconfigured", "kind": "mcp", "when": "none"},
	}))

	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"inspect", "manifest", "--json"})
	assert.Require(t, assert.NoErr(t, err))
	var m wiring.Manifest
	assert.Require(t, assert.NoErr(t, json.Unmarshal(stdout.Bytes(), &m)))
	assert.Eq(t, []wiring.ManifestPreferred{
		{Name: "suga-missing-preferred-command", Kind: "command", When: "never", Reason: "not found on PATH"},
		{Name: "srv", Kind: "mcp", When: "graph", Reason: "configured; connection confirmed at session start"},
		{Name: "unconfigured", Kind: "mcp", When: "none", Reason: "server not configured"},
	}, m.Preferred)
	assert.NotContains(t, stdout.String(), "secret")

	stdout.Reset()
	err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"inspect", "manifest"})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, stdout.String(), "Preferred tools")
	assert.StrContains(t, stdout.String(), "suga-missing-preferred-command (command) present=false: not found on PATH — never")
	assert.StrContains(t, stdout.String(), "srv (mcp) present=false: configured; connection confirmed at session start — graph")
	assert.NotContains(t, stdout.String(), "secret")
}
