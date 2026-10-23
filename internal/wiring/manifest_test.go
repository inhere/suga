package wiring

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
	toolsmcp "github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// manifestConfig sets a fake provider and a workspace with its own Git boundary
// (so parent repository files cannot leak into the test environment).
func manifestConfig(t *testing.T) (workspace string) {
	t.Helper()
	resetConfig(t)
	workspace = t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(workspace, ".git"), 0o755))
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k", "base_url": "http://127.0.0.1:1/v1"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	assert.NoErr(t, gkconfig.Set("compact_model", "openai/gpt-4o-mini"))
	assert.NoErr(t, gkconfig.Set("fallback_models", []string{"openai/gpt-4.1"}))
	// models.idle / models.handoff land in the manifest's per-scenario slots.
	assert.NoErr(t, gkconfig.Set("models.idle", "deepseek/deepseek-chat"))
	assert.NoErr(t, gkconfig.Set("models.handoff", "deepseek/deepseek-reasoner"))
	assert.NoErr(t, gkconfig.Set("workspace", workspace))
	assert.NoErr(t, gkconfig.Set("memory.project.backend", "files"))
	assert.NoErr(t, gkconfig.Set("memory.taint_tools", []string{"web_fetch"}))
	assert.NoErr(t, gkconfig.Set("memory.write_tainted", "deny"))
	assert.NoErr(t, gkconfig.Set("tools.max_output_bytes", 1234))
	assert.NoErr(t, gkconfig.Set("tools.max_spill_bytes", 99999))
	assert.NoErr(t, gkconfig.Set("mcp.servers", []any{map[string]any{"id": "srv", "command": "/bin/srv", "args": []any{"--token", "secret"}}}))
	return workspace
}

func toolByName(m Manifest, name string) (ManifestTool, bool) {
	for _, tool := range m.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return ManifestTool{}, false
}

func TestLoadManifest(t *testing.T) {
	workspace := manifestConfig(t)
	SetBuildVersion("9.9.9")
	t.Cleanup(func() { SetBuildVersion("dev") })

	m, err := LoadManifest(ManifestOptions{})
	assert.Require(t, assert.NoErr(t, err))

	t.Run("version and models", func(t *testing.T) {
		assert.Eq(t, "9.9.9", m.Version)
		assert.Eq(t, "openai", m.Models.Main.Provider)
		assert.Eq(t, "gpt-4o", m.Models.Main.Model)
		assert.True(t, m.Models.Main.ContextWindow > 0)
		assert.Require(t, assert.NotNil(t, m.Models.Compact))
		assert.Eq(t, "gpt-4o-mini", m.Models.Compact.Model)
		assert.Len(t, m.Models.Fallbacks, 1)
		assert.Eq(t, "gpt-4.1", m.Models.Fallbacks[0].Model)
		assert.Require(t, assert.NotNil(t, m.Models.Idle))
		assert.Eq(t, "deepseek-chat", m.Models.Idle.Model)
		assert.Eq(t, "deepseek", m.Models.Idle.Provider)
		assert.Require(t, assert.NotNil(t, m.Models.Handoff))
		assert.Eq(t, "deepseek-reasoner", m.Models.Handoff.Model)
	})
	t.Run("tools carry their metadata", func(t *testing.T) {
		fr, ok := toolByName(m, "file_read")
		assert.Require(t, assert.True(t, ok))
		assert.True(t, fr.ReadOnly)
		assert.True(t, fr.Rereadable)
		assert.False(t, fr.NeedsAllowWrite)
		wf, _ := toolByName(m, "web_fetch")
		assert.True(t, wf.External)
		sh, ok := toolByName(m, "shell_exec")
		assert.True(t, ok)
		assert.True(t, sh.NeedsAllowWrite)
		mem, ok := toolByName(m, "memory")
		assert.True(t, ok)
		assert.True(t, mem.AgentState)
		sm, ok := toolByName(m, "suga_manifest")
		assert.True(t, ok)
		assert.True(t, sm.ReadOnly)
		assert.False(t, sm.MCP)
		assert.True(t, slices.IsSortedFunc(m.Tools, func(a, b ManifestTool) int {
			if a.Name < b.Name {
				return -1
			}
			return 1
		}))
	})
	t.Run("MCP servers hide arguments", func(t *testing.T) {
		assert.Eq(t, []ManifestMCPServer{{ID: "srv", Command: "/bin/srv"}}, m.MCPServers)
		raw, _ := json.Marshal(m)
		assert.NotContains(t, string(raw), "secret")
		assert.NotContains(t, string(raw), "api_key")
	})
	t.Run("skills", func(t *testing.T) {
		assert.Contains(t, m.Skills.Roots, filepath.Join(workspace, ".agents", "skills"))
		sugaHome, err := appconfig.ResolveSugaHome()
		assert.Require(t, assert.NoErr(t, err))
		assert.Contains(t, m.Skills.Roots, filepath.Join(sugaHome, "skills"))
		assert.Eq(t, 1, m.Skills.Count) // the built-in guide released into SUGA_HOME/skills
	})
	t.Run("memory scopes", func(t *testing.T) {
		assert.Len(t, m.Memory, 2)
		assert.Eq(t, "user", m.Memory[0].Scope)
		assert.Eq(t, "files", m.Memory[0].Backend)
		assert.Eq(t, MemoryReasonDefault, m.Memory[0].Reason)
		assert.True(t, m.Memory[0].Replaceable)
		assert.Eq(t, "files", m.Memory[1].Backend)
		assert.Eq(t, MemoryReasonConfig, m.Memory[1].Reason)
		assert.NotEmpty(t, m.Memory[1].Location)
		assert.True(t, m.Memory[1].Replaceable)
	})
	t.Run("output, taint and write policy", func(t *testing.T) {
		assert.Eq(t, 1234, m.Output.InlineBytes)
		assert.Eq(t, 99999, m.Output.SpillBytes)
		assert.True(t, filepath.IsAbs(m.Output.SessionsDir))
		assert.Eq(t, []string{"web_fetch"}, m.Taint.TaintTools)
		assert.Eq(t, []string{"shell_exec"}, m.Taint.TaintOnSpill)
		assert.Eq(t, ManifestWritePolicy{MemoryWrite: "auto", MemoryWriteTainted: "deny"}, m.WritePolicy)
	})
	t.Run("exec backend reports the command", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("memory.user.backend", "exec"))
		assert.NoErr(t, gkconfig.Set("memory.user.command", "/opt/mem"))
		m, err := LoadManifest(ManifestOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "exec", m.Memory[0].Backend)
		assert.Eq(t, "/opt/mem", m.Memory[0].Location)
	})
	t.Run("thinking override is validated and reported", func(t *testing.T) {
		m, err := LoadManifest(ManifestOptions{Thinking: "High"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "high", m.Models.Main.Thinking)
		assert.Eq(t, "", m.Models.Compact.Thinking) // --thinking applies to the main model only
		_, err = LoadManifest(ManifestOptions{Thinking: "huge"})
		assert.ErrMsgContains(t, err, "--thinking: unknown thinking level")
	})
	t.Run("declared thinking style and stream requirement are reported", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("providers.openai.thinking_style", "budget"))
		assert.NoErr(t, gkconfig.Set("providers.openai.stream_required", true))
		t.Cleanup(func() {
			_ = gkconfig.Set("providers.openai.thinking_style", "")
			_ = gkconfig.Set("providers.openai.stream_required", false)
		})
		m, err := LoadManifest(ManifestOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "budget", m.Models.Main.ThinkingStyle)
		assert.True(t, m.Models.Main.StreamRequired)
	})
	t.Run("invalid fold settings fail", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("context.fold_keep", 0))
		t.Cleanup(func() { _ = gkconfig.Set("context.fold_keep", 5) })
		_, err := LoadManifest(ManifestOptions{})
		assert.ErrMsgContains(t, err, "context.fold_keep must be an integer >= 1")
	})
	t.Run("missing model fails", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("model", ""))
		_, err := LoadManifest(ManifestOptions{})
		assert.Err(t, err)
	})
}

// TestManifestMatchesRuntimeTools guards against drift: the tools the static
// manifest lists are the tools a writable runtime registers (minus
// tool_search, which exists only when tools are deferred).
func TestManifestMatchesRuntimeTools(t *testing.T) {
	manifestConfig(t)
	assert.NoErr(t, gkconfig.Set("mcp.servers", nil))
	m, err := LoadManifest(ManifestOptions{})
	assert.Require(t, assert.NoErr(t, err))
	var fromManifest []string
	for _, tool := range m.Tools {
		fromManifest = append(fromManifest, tool.Name)
	}

	// chat and run hand in a task manager: shell_exec gets background and
	// the task tool is registered.
	dir := t.TempDir()
	taskManager := tasks.NewManager(tasks.Config{Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, id), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(taskManager.Close)
	rt, err := OpenCLI(context.Background(), Options{AllowWrite: true, Tasks: taskManager})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	runtimeNames := rt.Registry.List()
	// The tool table is what the manifest lists: hidden aliases (view_image) and
	// the deferred-tool search stay out of both.
	runtimeNames = slices.DeleteFunc(runtimeNames, func(n string) bool {
		if n == "tool_search" {
			return true
		}
		t, err := rt.Registry.Get(n)
		return err == nil && tools.MetaOf(t).Hidden
	})
	slices.Sort(runtimeNames)
	assert.Eq(t, runtimeNames, fromManifest)

	t.Run("suga_manifest tool returns the live manifest", func(t *testing.T) {
		res, err := rt.Registry.ExecuteTool(context.Background(), api.ToolCall{CallID: "c", Name: "suga_manifest"})
		assert.Require(t, assert.NoErr(t, err))
		var got Manifest
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(res.Content), &got)))
		assert.Eq(t, "openai", got.Models.Main.Provider)
		assert.Eq(t, m.Memory, got.Memory)
		assert.Eq(t, m.Output, got.Output)
		_, ok := toolByName(got, "suga_manifest")
		assert.True(t, ok)
	})
}

func TestManifestToolInReadOnlySet(t *testing.T) {
	manifestConfig(t)
	assert.NoErr(t, gkconfig.Set("mcp.servers", nil))
	rt, err := OpenCLI(context.Background(), Options{}) // read-only run
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	assert.Contains(t, rt.Registry.List(), "suga_manifest")
	assert.NotContains(t, rt.Registry.List(), "shell_exec")
}

// TestBuildManifestLangRules: the manifest lists the effective declaration
// languages of file_symbols/file_refs and the rule files a load skipped, so a
// lang-rules.yaml with a typo is visible without reading the log (design
// docs/design/2026-10-22-lang-rules-design.md, section 8.1).
func TestBuildManifestLangRules(t *testing.T) {
	m := BuildManifest(ManifestInput{LangRules: codeintel.DefaultLangRules()})
	names := make([]string, 0, len(m.LangRules))
	for _, lang := range m.LangRules {
		names = append(names, lang.Name)
	}
	assert.True(t, slices.Contains(names, "Ruby"), "a rule language is listed: %v", names)
	assert.True(t, slices.Contains(names, "Go"), "a code language is listed: %v", names)
	assert.Eq(t, 0, len(m.LangRuleSkips))
	encoded, err := json.Marshal(m)
	assert.NoErr(t, err)
	assert.StrContains(t, string(encoded), `"lang_rule_skips":[]`)

	// A broken rule file is skipped by the loader and reported by the manifest.
	path := filepath.Join(t.TempDir(), "lang-rules.yaml")
	assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(`languages:
  - {name: Foo, exts: [.foo], blocks: braceless, decls: [{pattern: '(x)', kind: func, name: 1}]}
`), 0o644)))
	reg, errs := codeintel.LoadLangRules(path)
	assert.Eq(t, 1, len(errs))
	m = BuildManifest(ManifestInput{LangRules: reg})
	assert.Eq(t, 1, len(m.LangRuleSkips))
	assert.Eq(t, path, m.LangRuleSkips[0].File)
	assert.StrContains(t, m.LangRuleSkips[0].Err, "braceless")
}

// TestBuildManifestToolConflicts: the manifest reports the same-name tool
// collisions of the live registry (server/tool/kept_by) and keeps the empty
// list for a standalone manifest, which never starts an MCP server.
func TestBuildManifestToolConflicts(t *testing.T) {
	m := BuildManifest(ManifestInput{ToolConflicts: []toolsmcp.Conflict{
		{Server: "mcp.gofer", Tool: "file_read", KeptBy: "builtin"},
	}})
	assert.Eq(t, []ManifestToolConflict{
		{Server: "mcp.gofer", Tool: "file_read", KeptBy: "builtin"},
	}, m.ToolConflicts)

	standalone := BuildManifest(ManifestInput{})
	assert.Eq(t, 0, len(standalone.ToolConflicts))
	encoded, err := json.Marshal(standalone)
	assert.NoErr(t, err)
	assert.StrContains(t, string(encoded), `"tool_conflicts":[]`)
}
