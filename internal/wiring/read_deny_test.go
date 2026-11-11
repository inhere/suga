package wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// openDenyRuntime opens a runtime whose approver allows everything (so the ban,
// not the approval, is what stops a read) over a workspace under a fake home,
// holding the secrets the tests try to read.
func openDenyRuntime(t *testing.T, extra []string) (*Runtime, string) {
	t.Helper()
	resetConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", t.TempDir())
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	ws := filepath.Join(home, "proj")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(ws, 0o755)))
	for name, content := range map[string]string{".env": "TOKEN=sekret", ".env.example": "TOKEN=", "notes.txt": "hello", "vault.txt": "private"} {
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, name), []byte(content), 0o600)))
	}
	allowAll := tools.ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return nil })
	rt, err := Open(context.Background(), Options{Workspace: ws, RuleCWD: ws, Approver: allowAll, AllowWrite: true, ReadDeny: extra})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	return rt, ws
}

func readViaRuntime(rt *Runtime, path string) (string, error) {
	res, err := rt.tools.ExecuteTool(context.Background(), api.ToolCall{CallID: "c1", Name: "file_read", Arguments: map[string]any{"path": path}})
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// TestReadDenyThroughOpen: the main session built by Open refuses the secret,
// whatever the approver says, and a host's additions (with "~" expanded) join
// the built-in list instead of replacing it.
func TestReadDenyThroughOpen(t *testing.T) {
	rt, _ := openDenyRuntime(t, []string{"~/proj/vault.txt"})
	t.Run("built-in .env is refused", func(t *testing.T) {
		_, err := readViaRuntime(rt, ".env")
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "被 tools.read_deny 禁止")
	})
	t.Run("the example file and a plain file read", func(t *testing.T) {
		out, err := readViaRuntime(rt, ".env.example")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "TOKEN")
		out, err = readViaRuntime(rt, "notes.txt")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "hello")
	})
	t.Run("host addition with ~ is refused", func(t *testing.T) {
		_, err := readViaRuntime(rt, "vault.txt")
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "tools.read_deny")
	})
	t.Run("file_edit is refused too", func(t *testing.T) {
		_, err := rt.tools.ExecuteTool(context.Background(), api.ToolCall{CallID: "c2", Name: "file_edit",
			Arguments: map[string]any{"path": ".env", "old_string": "TOKEN", "new_string": "X"}})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "tools.read_deny")
	})
}

func TestReadDenyStartupValidation(t *testing.T) {
	resetConfig(t)
	t.Setenv("SUGA_HOME", t.TempDir())
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	t.Run("bad glob in the configuration", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.read_deny", []string{"**/[x"}))
		_, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir()})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "tools.read_deny")
	})
	t.Run("bad glob from the host", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.read_deny", []string{}))
		_, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir(), ReadDeny: []string{"**/[x"}})
		assert.Require(t, assert.Err(t, err))
	})
}

// TestReadDenyInSubAgent: a sub-agent's tool set carries the parent's ban, and
// the read-only build (ACP read_only, the default CLI one) does as well.
func TestReadDenyInSubAgent(t *testing.T) {
	resetConfig(t)
	t.Setenv("SUGA_HOME", t.TempDir())
	ws := t.TempDir()
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, ".env"), []byte("TOKEN=sekret"), 0o600)))
	deny, err := appconfig.LoadReadDeny(nil)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("sub-agent registry", func(t *testing.T) {
		reg, err := (&subAgents{workspace: ws, readDeny: deny}).toolRegistry("explore")
		assert.Require(t, assert.NoErr(t, err))
		_, err = reg.ExecuteTool(context.Background(), api.ToolCall{CallID: "c", Name: "file_read", Arguments: map[string]any{"path": ".env"}})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "tools.read_deny")
	})
	t.Run("read-only capabilities", func(t *testing.T) {
		caps, err := buildPersonalCapabilitiesWithExtras(context.Background(), ws, nil, false, nil, nil, nil, deny)
		assert.Require(t, assert.NoErr(t, err))
		_, err = caps.Registry.ExecuteTool(context.Background(), api.ToolCall{CallID: "c", Name: "file_read", Arguments: map[string]any{"path": ".env"}})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "tools.read_deny")
	})
}

// TestReadDenyUnderTrust: the read ban is a hard limit, so the trust tier (which
// lets confirmation checks through) still refuses a banned file (design C).
func TestReadDenyUnderTrust(t *testing.T) {
	resetConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", t.TempDir())
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	ws := filepath.Join(home, "proj")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(ws, 0o755)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, ".env"), []byte("TOKEN=sekret"), 0o600)))
	allowAll := tools.ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return nil })
	rt, err := Open(context.Background(), Options{Workspace: ws, RuleCWD: ws, Approver: allowAll, AllowWrite: true, ApprovalMode: tools.ApprovalModeTrust})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	_, err = readViaRuntime(rt, ".env")
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), "被 tools.read_deny 禁止")
}
