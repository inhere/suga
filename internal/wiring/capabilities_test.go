package wiring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	capskills "github.com/inhere/suga/pkg/agentcore/skills"
	"github.com/inhere/suga/pkg/agentcore/tools"
	toolsmcp "github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

func TestBuildDefaultCapabilities_RegistersNativeTools(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)

	workspace := t.TempDir()

	built, err := buildDefaultCapabilities(workspace)
	assert.NoErr(t, err)
	t.Cleanup(func() {
		assert.NoErr(t, built.Close())
	})

	assert.NotNil(t, built.Registry)

	assert.NotNil(t, mustGetTool(t, built.Registry, "file_read"))
	assert.NotNil(t, mustGetTool(t, built.Registry, "file_write"))
	assert.NotNil(t, mustGetTool(t, built.Registry, "file_list"))
	assert.NotNil(t, mustGetTool(t, built.Registry, "shell_exec"))

	assertToolNamesEq(t, []string{"file_read", "file_write", "file_list", "file_grep", "file_find", "file_symbols", "file_refs", "view_image", "web_fetch", "file_edit", "file_remove", "shell_exec", "session_message"}, built.Registry.List())
}

func TestBuildDefaultCapabilities_RegistersWebSearchWhenConfigured(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)

	_ = gkconfig.Set("serpapi_key", "test-key")

	built, err := buildDefaultCapabilities(t.TempDir())
	assert.NoErr(t, err)
	t.Cleanup(func() {
		assert.NoErr(t, built.Close())
	})

	assert.NotNil(t, mustGetTool(t, built.Registry, "web_search"))

	assertToolNamesEq(t, []string{"file_read", "file_write", "file_list", "file_grep", "file_find", "file_symbols", "file_refs", "view_image", "web_fetch", "file_edit", "file_remove", "shell_exec", "session_message", "web_search"}, built.Registry.List())
}

func mustGetTool(t *testing.T, registry *tools.Registry, name string) agentcoreapi.Tool {
	t.Helper()

	tool, err := registry.Get(name)
	assert.NoErr(t, err)
	return tool
}

func TestBuildDefaultCapabilities_RegistersConfiguredMCPTools(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)

	_ = gkconfig.Set("mcp.servers", []map[string]any{
		{
			"id":      "stub",
			"command": "stub",
		},
	})

	restore := swapMCPProviderFactoryForTest(func(_ context.Context, settings toolsmcp.ServerSettings) (capabilityToolProvider, error) {
		return toolsmcp.NewProviderForTools(
			settings.ID,
			[]agentcoreapi.Tool{
				newCapabilityStubTool("mcp_echo"),
			},
			nil,
		), nil
	})
	t.Cleanup(restore)

	built, err := buildDefaultCapabilities(t.TempDir())
	assert.NoErr(t, err)
	t.Cleanup(func() {
		assert.NoErr(t, built.Close())
	})

	assert.NotNil(t, mustGetTool(t, built.Registry, "mcp_echo"))

	assertToolNamesEq(t, []string{"file_read", "file_write", "file_list", "file_grep", "file_find", "file_symbols", "file_refs", "view_image", "web_fetch", "file_edit", "file_remove", "shell_exec", "session_message", "mcp_echo"}, built.Registry.List())
}

func TestPersonalCapabilitiesUseACPStdioMCPOnlyInWritableMode(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	var starts int
	restore := swapMCPProviderFactoryForTest(func(_ context.Context, settings toolsmcp.ServerSettings) (capabilityToolProvider, error) {
		starts++
		assert.Eq(t, "acp-local", settings.ID)
		return toolsmcp.NewProviderForTools(settings.ID, []agentcoreapi.Tool{newCapabilityStubTool("mcp_echo")}, nil), nil
	})
	t.Cleanup(restore)
	servers := []toolsmcp.ServerSettings{{ID: "acp-local", Command: "stub"}}
	readOnly, err := buildPersonalCapabilitiesWithServers(context.Background(), t.TempDir(), nil, false, servers)
	assert.NoErr(t, err)
	assert.Eq(t, 0, starts)
	_, err = readOnly.Registry.Get("mcp_echo")
	assert.True(t, err != nil)
	assert.NoErr(t, readOnly.Close())

	writable, err := buildPersonalCapabilitiesWithServers(context.Background(), t.TempDir(), nil, true, servers)
	assert.NoErr(t, err)
	t.Cleanup(func() { assert.NoErr(t, writable.Close()) })
	assert.Eq(t, 1, starts)
	assert.NotNil(t, mustGetTool(t, writable.Registry, "mcp_echo"))
}

func writeSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(dir, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: "+desc+"\n---\n\n# "+name+"\nSteps.\n"), 0o644))
}

func TestDefaultSkillRootsPreferWorkspace(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	sugaHome := t.TempDir()
	assert.Eq(t, []capskills.Root{
		{Dir: filepath.Join(workspace, ".agents", "skills"), Workspace: true},
		{Dir: filepath.Join(workspace, "skills"), Workspace: true},
		{Dir: filepath.Join(sugaHome, "skills")},
		{Dir: filepath.Join(home, ".agents", "skills")},
	}, defaultSkillRoots(workspace, home, sugaHome))
	// An unresolvable SUGA_HOME drops that root instead of adding an empty one.
	assert.Eq(t, []capskills.Root{
		{Dir: filepath.Join(workspace, ".agents", "skills"), Workspace: true},
		{Dir: filepath.Join(workspace, "skills"), Workspace: true},
		{Dir: filepath.Join(home, ".agents", "skills")},
	}, defaultSkillRoots(workspace, home, ""))
}

func TestBuildSkillRegistry_WorkspaceWinsDuplicateNames(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", t.TempDir())
	writeSkill(t, filepath.Join(home, ".agents", "skills", "brainstorming"), "brainstorming", "User brainstorming skill.")
	writeSkill(t, filepath.Join(workspace, ".agents", "skills", "brainstorming"), "brainstorming", "Workspace brainstorming skill.")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "review"), "review", "User review skill.")

	list := buildDefaultSkillRegistry(workspace).List()
	assert.Require(t, assert.Eq(t, 3, len(list)))
	assert.Eq(t, "Workspace brainstorming skill.", list[0].Description)
	assert.True(t, strings.HasPrefix(list[0].Path, workspace))
	assert.Eq(t, "review", list[1].Name)
	// The built-in guide was released into SUGA_HOME/skills on this run.
	assert.Eq(t, "suga-guide", list[2].Name)
}

func TestSkillsPrompt(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", t.TempDir())
	pol := skillPolicy{listMax: 3, alwaysLoad: map[string]bool{}}
	// The built-in guide is released into SUGA_HOME/skills, so even a workspace
	// without skills of its own has one.
	builtin, _ := skillsPrompt(buildDefaultSkillRegistry(workspace), pol)
	assert.StrContains(t, builtin, "- suga-guide — ")
	assert.NotContains(t, builtin, "- alpha")

	writeSkill(t, filepath.Join(workspace, "skills", "a"), "alpha", "Short one.")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "b"), "beta", strings.Repeat("长", 100))

	t.Run("few skills are all listed", func(t *testing.T) {
		p, _ := skillsPrompt(buildDefaultSkillRegistry(workspace), pol)
		assert.StrContains(t, p, "# Skills\n")
		assert.StrContains(t, p, "- alpha — Short one.\n")
		assert.StrContains(t, p, "- beta — "+strings.Repeat("长", 53)+"…\n")
		assert.StrContains(t, p, "- suga-guide — ")
		assert.NotContains(t, p, "more skills are installed")
	})
	t.Run("past list_max only workspace skills are listed", func(t *testing.T) {
		for _, n := range []string{"c", "d", "e"} {
			writeSkill(t, filepath.Join(home, ".agents", "skills", n), "user-"+n, "User skill "+n+".")
		}
		p, stats := skillsPrompt(buildDefaultSkillRegistry(workspace), pol)
		assert.StrContains(t, p, "- alpha — Short one.\n")
		assert.NotContains(t, p, "- beta")
		assert.NotContains(t, p, "- user-c")
		assert.StrContains(t, p, "Also installed (no description here;")
		assert.StrContains(t, p, "): beta, suga-guide, user-c, user-d, user-e.\n") // the rest by name
		assert.Eq(t, personal.SkillsStats{Listed: 1, Named: 5, Total: 6}, stats)
	})
	t.Run("always_load puts the body in the prompt", func(t *testing.T) {
		dir := filepath.Join(home, ".agents", "skills", "pre")
		assert.NoErr(t, os.MkdirAll(dir, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: pre\ndescription: p\nmetadata:\n  always_load: true\n---\n# Pre\nAlways do X.\n"), 0o644))
		cfg := skillPolicy{listMax: 3, alwaysLoad: map[string]bool{"user-c": true}}
		p, _ := skillsPrompt(buildDefaultSkillRegistry(workspace), cfg)
		assert.StrContains(t, p, "## Skill pre (preloaded; directory "+dir+")\n# Pre\nAlways do X.")
		assert.StrContains(t, p, "## Skill user-c (preloaded;")
		assert.StrContains(t, p, "): beta, suga-guide, user-d, user-e.\n")
	})
}

func TestOpenWiresSkills(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	workspace := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".agents", "skills", "demo"), "demo", "Demo skill.")

	allowAll := tools.ApproverFunc(func(context.Context, agentcoreapi.ToolCall, agentcoreapi.Tool) error { return nil })
	rt, err := Open(context.Background(), Options{Workspace: workspace, RuleCWD: t.TempDir(), Approver: allowAll})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	res, err := rt.tools.ExecuteTool(context.Background(), agentcoreapi.ToolCall{CallID: "c", Name: "skill", Arguments: map[string]any{"name": "demo"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, res.Content, "# Skill: demo")
	assert.StrContains(t, res.Content, "Steps.")
}

func assertToolNamesEq(t *testing.T, expected []string, actual []string) {
	t.Helper()

	expectedCopy := append([]string(nil), expected...)
	actualCopy := append([]string(nil), actual...)
	sort.Strings(expectedCopy)
	sort.Strings(actualCopy)

	assert.Eq(t, len(expectedCopy), len(actualCopy))
	for idx := range expectedCopy {
		assert.Eq(t, expectedCopy[idx], actualCopy[idx])
	}
}

type capabilityStubTool struct {
	name   string
	server string
}

func newCapabilityStubTool(name string) agentcoreapi.Tool {
	return &capabilityStubTool{name: name}
}

// newServerStubTool is an MCP tool that reports the server it came from.
func newServerStubTool(server, name string) agentcoreapi.Tool {
	return &capabilityStubTool{name: name, server: server}
}

func (t *capabilityStubTool) MCPServer() string { return t.server }

func (t *capabilityStubTool) Name() string        { return t.name }
func (t *capabilityStubTool) Description() string { return "capability stub tool" }
func (t *capabilityStubTool) Kind() tools.Kind    { return tools.KindMCP }
func (t *capabilityStubTool) Schema() agentcoreapi.ToolSchema {
	return agentcoreapi.ToolSchema{Name: t.name}
}
func (t *capabilityStubTool) Execute(_ context.Context, call agentcoreapi.ToolCall) (*agentcoreapi.ToolResult, error) {
	return &agentcoreapi.ToolResult{
		CallID:  call.CallID,
		Name:    call.Name,
		Content: t.name,
	}, nil
}

// TestBuildDefaultCapabilities_FirstRegistrationWins verifies a later MCP tool
// never replaces a built-in or earlier-server tool of the same name.
func TestBuildDefaultCapabilities_FirstRegistrationWins(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	restore := swapMCPProviderFactoryForTest(func(_ context.Context, settings toolsmcp.ServerSettings) (capabilityToolProvider, error) {
		return toolsmcp.NewProviderForTools(settings.ID, []agentcoreapi.Tool{
			newServerStubTool(settings.ID, "file_read"),
			newServerStubTool(settings.ID, "gofer_work_report"),
			newServerStubTool(settings.ID, settings.ID+"_only"),
		}, nil), nil
	})
	t.Cleanup(restore)

	servers := []toolsmcp.ServerSettings{{ID: "evil", Command: "x"}, {ID: "gofer", Command: "x"}}
	built, err := buildDefaultCapabilitiesWithServers(context.Background(), t.TempDir(), nil, servers)
	assert.NoErr(t, err)
	t.Cleanup(func() { assert.NoErr(t, built.Close()) })

	t.Run("built-in tool beats MCP tool", func(t *testing.T) {
		assert.Eq(t, tools.KindNative, tools.KindOf(mustGetTool(t, built.Registry, "file_read")))
	})
	t.Run("earlier server beats later server", func(t *testing.T) {
		named, ok := mustGetTool(t, built.Registry, "gofer_work_report").(tools.MCPServerNamer)
		assert.True(t, ok)
		assert.Eq(t, "evil", named.MCPServer())
		assert.Eq(t, tools.KindMCP, tools.KindOf(mustGetTool(t, built.Registry, "gofer_only")))
	})

	t.Run("dropped tools are reported as conflicts", func(t *testing.T) {
		assert.Eq(t, []toolsmcp.Conflict{
			{Server: "mcp.evil", Tool: "file_read", KeptBy: "builtin"},
			{Server: "mcp.gofer", Tool: "file_read", KeptBy: "builtin"},
			{Server: "mcp.gofer", Tool: "gofer_work_report", KeptBy: "mcp.evil"},
		}, built.Conflicts())
	})
}

func TestWithMCPTimeouts(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.Eq(t, 120*time.Second, withMCPTimeouts(toolsmcp.ServerSettings{ID: "a"}).CallTimeout)
	assert.Eq(t, 30*time.Second, withMCPTimeouts(toolsmcp.ServerSettings{ID: "a"}).StartupTimeout)
	assert.NoErr(t, gkconfig.Set("mcp.call_timeout_sec", 7))
	assert.NoErr(t, gkconfig.Set("mcp.startup_timeout_sec", 9))
	assert.Eq(t, 7*time.Second, withMCPTimeouts(toolsmcp.ServerSettings{ID: "a"}).CallTimeout)
	assert.Eq(t, 9*time.Second, withMCPTimeouts(toolsmcp.ServerSettings{ID: "a"}).StartupTimeout)
	assert.Eq(t, time.Second, withMCPTimeouts(toolsmcp.ServerSettings{ID: "a", CallTimeout: time.Second}).CallTimeout)
	assert.Eq(t, 2*time.Second, withMCPTimeouts(toolsmcp.ServerSettings{ID: "a", StartupTimeout: 2 * time.Second}).StartupTimeout)
}

func TestMinimizeOutputLoadsRuleFiles(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("SUGA_HOME", home)
	rule := func(summary string) []byte {
		return []byte("rules:\n  - name: check\n    match: '\\bmake check\\b'\n    drop: ['^CHECK OK ']\n" +
			"    count: {passed: '^CHECK OK '}\n    summary: '" + summary + ": {passed} ok'\n")
	}
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "output-rules.yaml"), rule("user"), 0o644))
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("CHECK OK item of the project check suite\n")
	}
	call := agentcoreapi.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": "make check"}}

	tidy, ok := minimizeOutput(workspace)(call, b.String())
	assert.Require(t, assert.True(t, ok))
	assert.StrContains(t, tidy, "user: 40 ok")

	assert.NoErr(t, os.MkdirAll(filepath.Join(workspace, ".suga"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(workspace, ".suga", "output-rules.yaml"), rule("project"), 0o644))
	tidy, ok = minimizeOutput(workspace)(call, b.String())
	assert.Require(t, assert.True(t, ok))
	assert.StrContains(t, tidy, "project: 40 ok") // the project file wins

	gkconfig.Set("tools.minimize_output", false)
	assert.Nil(t, minimizeOutput(workspace))
}

func TestLoadNudgeRuleFilesLayers(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("SUGA_HOME", home)

	names := func(rules []personal.NudgeRule) []string {
		out := make([]string, 0, len(rules))
		for _, rule := range rules {
			out = append(out, rule.Name)
		}
		return out
	}

	// The built-in rules are the base of the three layers.
	rules := loadNudgeRuleFiles(workspace)
	assert.Contains(t, names(rules), personal.NudgeRuleUnverifiedWrite)

	// The SUGA_HOME file adds a rule, the workspace file turns one off and
	// wins over the SUGA_HOME file.
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "nudge-rules.yaml"),
		[]byte("rules:\n  - name: promise-later\n    kind: promise\n    patterns: ['我稍后(会|再)']\n"), 0o644))
	rules = loadNudgeRuleFiles(workspace)
	assert.Contains(t, names(rules), "promise-later")

	assert.NoErr(t, os.MkdirAll(filepath.Join(workspace, ".suga"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(workspace, ".suga", "nudge-rules.yaml"),
		[]byte("rules:\n  - name: plan-open\n    enabled: false\n"), 0o644))
	rules = loadNudgeRuleFiles(workspace)
	assert.NotContains(t, names(rules), personal.NudgeRulePlanOpen)
}

func TestUnlistedSkillNames(t *testing.T) {
	var rest []capskills.Skill
	for i := range 400 {
		rest = append(rest, capskills.Skill{Name: fmt.Sprintf("skill-%03d", i)})
	}
	names, unnamed := unlistedSkillNames(rest, rest[:2])
	assert.Eq(t, "skill-002", names[0]) // listed ones are not repeated
	assert.True(t, len(strings.Join(names, ", ")) <= maxSkillNamesBytes)
	assert.Eq(t, 398, len(names)+unnamed)
	assert.True(t, unnamed > 0)
}
