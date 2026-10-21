package wiring

import (
	"context"
	"errors"
	"slices"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// catalogNames returns the native catalog tool names, read tools only unless
// write is set.
func catalogNames(write bool) []string {
	var names []string
	// The env session_message needs: without a messenger the catalog leaves the
	// tool out (tools.session_message.enabled off).
	env := native.Env{SessionMessenger: stubSessionMessenger{}}
	for _, spec := range native.Catalog() {
		if spec.Name == "web_search" { // needs serpapi_key, unset here
			continue
		}
		if tool := spec.New(env); !write && !tools.MetaOf(tool).ReadOnly {
			continue
		}
		names = append(names, spec.Name)
	}
	return names
}

// stubSessionMessenger is a native.SessionMessenger that lists and sends nothing:
// the catalog only needs one to build session_message.
type stubSessionMessenger struct{}

func (stubSessionMessenger) Peers(context.Context) ([]native.SessionPeer, error) {
	return nil, nil
}

func (stubSessionMessenger) Send(context.Context, string, string, string) (native.SessionSendResult, error) {
	return native.SessionSendResult{}, nil
}

// TestRegisteredToolsFollowTheCatalog pins the run/chat/ACP tool sets to the
// one native catalog plus update_plan, session_search, recall, memory,
// suga_manifest, agent and skill, which Open registers in every mode (skill is
// always there: the built-in guide is released into SUGA_HOME/skills).
func TestRegisteredToolsFollowTheCatalog(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	names := func(t *testing.T, rt *Runtime, err error) []string {
		t.Helper()
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		got := rt.Registry.List()
		slices.Sort(got)
		return got
	}
	want := func(write bool) []string {
		w := append(catalogNames(write), "update_plan", "session_search", "recall", "memory", "suga_manifest", "agent", "skill")
		slices.Sort(w)
		return w
	}

	t.Run("run read-only", func(t *testing.T) {
		rt, err := OpenCLI(context.Background(), Options{})
		assert.Eq(t, want(false), names(t, rt, err))
	})
	t.Run("run --allow-write", func(t *testing.T) {
		rt, err := OpenCLI(context.Background(), Options{AllowWrite: true})
		assert.Eq(t, want(true), names(t, rt, err))
	})
	t.Run("ACP read-only", func(t *testing.T) {
		rt, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir()})
		assert.Eq(t, want(false), names(t, rt, err))
	})
	t.Run("ACP writable", func(t *testing.T) {
		rt, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir(), AllowWrite: true})
		assert.Eq(t, want(true), names(t, rt, err))
	})
	t.Run("agents.enabled false drops the agent tool", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("agents.enabled", false))
		t.Cleanup(func() { _ = gkconfig.Set("agents.enabled", true) })
		rt, err := OpenCLI(context.Background(), Options{})
		assert.NotContains(t, names(t, rt, err), "agent")
	})
}

// TestToolSchemasDeclareProperties guards against zero-argument schemas:
// some OpenAI-compatible gateways turn an empty "properties" object into null
// and reject the whole request (seen with suga_manifest on opencode-go).
func TestToolSchemasDeclareProperties(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	assert.NoErr(t, gkconfig.Set("serpapi_key", "k"))

	for _, write := range []bool{false, true} {
		rt, err := OpenCLI(context.Background(), Options{AllowWrite: write})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		schemas, err := rt.Registry.ListTools(context.Background())
		assert.Require(t, assert.NoErr(t, err))
		assert.NotEmpty(t, schemas)
		for _, s := range schemas {
			assert.Eq(t, "object", s.InputSchema["type"], s.Name)
			props, _ := s.InputSchema["properties"].(map[string]any)
			assert.True(t, len(props) > 0, "%s declares no properties", s.Name)
		}
	}
}

func TestWebSearchIsARegisteredReadTool(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	t.Run("read-only with a key", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("serpapi_key", "k"))
		rt, err := OpenCLI(context.Background(), Options{})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		assert.Contains(t, rt.Registry.List(), "web_search")
		assert.NotContains(t, rt.Registry.List(), "shell_exec")
	})
	t.Run("no key, no tool", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("serpapi_key", ""))
		rt, err := OpenCLI(context.Background(), Options{AllowWrite: true})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		assert.NotContains(t, rt.Registry.List(), "web_search")
	})
}

// TestDisabledToolHints checks the enable hint each entry gives for a write
// tool it did not register, and that web_search without a key is listed too.
func TestDisabledToolHints(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	call := func(t *testing.T, rt *Runtime, err error, name string) error {
		t.Helper()
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		_, execErr := rt.tools.ExecuteTool(context.Background(), agentcoreapi.ToolCall{CallID: "c", Name: name})
		return execErr
	}
	for _, tc := range []struct {
		name string
		open func() (*Runtime, error)
		want string
	}{
		{"run", func() (*Runtime, error) { return OpenCLI(context.Background(), Options{}) }, "加 --allow-write"},
		{"chat", func() (*Runtime, error) { return OpenCLI(context.Background(), Options{ChatMode: true}) }, "tools.allow_write: true"},
		{"read-only", func() (*Runtime, error) { return OpenCLI(context.Background(), Options{ReadOnly: true}) }, "--read-only 启动"},
		{"ACP", func() (*Runtime, error) {
			return Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir()})
		}, "read_only 模式"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := tc.open()
			execErr := call(t, rt, err, "shell_exec")
			assert.ErrIs(t, execErr, tools.ErrDenied)
			assert.StrContains(t, execErr.Error(), tc.want)
		})
	}
	t.Run("web_search without a key", func(t *testing.T) {
		rt, err := OpenCLI(context.Background(), Options{AllowWrite: true})
		execErr := call(t, rt, err, "web_search")
		assert.ErrIs(t, execErr, tools.ErrDenied)
		assert.StrContains(t, execErr.Error(), "serpapi_key")
	})
	t.Run("session_message off", func(t *testing.T) {
		t.Setenv("SUGA_SESSION_MESSAGE", "off")
		rt, err := OpenCLI(context.Background(), Options{})
		execErr := call(t, rt, err, "session_message")
		assert.ErrIs(t, execErr, tools.ErrDenied)
		assert.StrContains(t, execErr.Error(), "tools.session_message.enabled")
	})
	t.Run("unknown name is not a denial", func(t *testing.T) {
		rt, err := OpenCLI(context.Background(), Options{})
		execErr := call(t, rt, err, "bash")
		assert.Err(t, execErr)
		assert.False(t, errors.Is(execErr, tools.ErrDenied))
		assert.StrContains(t, execErr.Error(), "available tools:")
	})
}
