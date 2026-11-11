package rules

import (
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestProtectedPathAndClampAllow(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "repo")
	home := filepath.Join(t.TempDir(), ".suga")
	userRules := filepath.Join(home, "approval-rules.yaml")
	projectRules := filepath.Join(workspace, ".suga", "approval-rules.yaml")
	for _, tc := range []struct {
		name      string
		path      string
		mode      Mode
		protected bool
		clamped   Action
	}{
		{name: "workspace git file", path: ".git/config", mode: ModeRun, protected: true, clamped: Deny},
		{name: "workspace suga file", path: ".suga/output-rules.yaml", mode: ModeChat, protected: true, clamped: Ask},
		{name: "user rule file", path: userRules, mode: ModeACP, protected: true, clamped: Ask},
		{name: "project rule file", path: projectRules, mode: ModeRun, protected: true, clamped: Deny},
		{name: "ordinary file", path: "docs/note.md", mode: ModeRun, protected: false, clamped: Allow},
		{name: "prefix is not a root", path: filepath.Join(workspace+"-copy", ".git", "config"), mode: ModeRun, protected: false, clamped: Allow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := Target{Tool: "file_write", Root: workspace, Path: tc.path, Mode: tc.mode}
			assert.Eq(t, tc.protected, IsProtectedPath(target, workspace, home, userRules, projectRules))
			got, protected := ClampAllow(Allow, target, workspace, home, userRules, projectRules)
			assert.Eq(t, tc.clamped, got)
			// every protected-path case is downgraded, and only those are flagged.
			assert.Eq(t, tc.protected, protected)
		})
	}
}

func TestProtectedPathDoesNotApplyToReadToolsOrMCP(t *testing.T) {
	workspace := t.TempDir()
	t.Run("read tool", func(t *testing.T) {
		target := Target{Tool: "file_read", Root: workspace, Path: ".git/config"}
		assert.False(t, IsProtectedPath(target, workspace, t.TempDir()))
	})
	t.Run("MCP tool", func(t *testing.T) {
		target := Target{Tool: "file_write", MCP: true, Root: workspace, Path: ".git/config"}
		assert.False(t, IsProtectedPath(target, workspace, t.TempDir()))
	})
}

func TestPolicyDecideClampsProtectedAllow(t *testing.T) {
	workspace := t.TempDir()
	policy := Policy{Rules: []Rule{{Tool: "file_write", Path: []string{"**"}, Root: workspace, Action: Allow}}}
	target := Target{
		Tool:      "file_write",
		Root:      workspace,
		Path:      ".git/config",
		Mode:      ModeRun,
		Workspace: workspace,
	}
	action, hit, protected := policy.Decide(target)
	assert.Require(t, assert.NotNil(t, hit))
	assert.Eq(t, Deny, action)
	assert.True(t, protected)
}

// A broad allow rule that matches a protected path is downgraded and flagged;
// the same rule on an ordinary path stays allowed and unflagged.
func TestPolicyDecideReportsProtectedFlag(t *testing.T) {
	workspace := t.TempDir()
	policy := Policy{Rules: []Rule{{Tool: "file_write", Path: []string{"**"}, Root: workspace, Action: Allow}}}
	target := func(mode Mode, path string) Target {
		return Target{Tool: "file_write", Root: workspace, Path: path, Mode: mode, Workspace: workspace}
	}
	t.Run("protected path in chat asks", func(t *testing.T) {
		action, _, protected := policy.Decide(target(ModeChat, ".git/hooks/x"))
		assert.Eq(t, Ask, action)
		assert.True(t, protected)
	})
	t.Run("ordinary path", func(t *testing.T) {
		action, _, protected := policy.Decide(target(ModeChat, "docs/a.txt"))
		assert.Eq(t, Allow, action)
		assert.False(t, protected)
	})
	t.Run("default action is not clamped here", func(t *testing.T) {
		action, hit, protected := (Policy{Default: Allow}).Decide(target(ModeChat, ".git/hooks/x"))
		assert.Eq(t, Allow, action)
		assert.Nil(t, hit)
		assert.False(t, protected)
	})
}
