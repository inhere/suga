package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestRuleMatchesPath(t *testing.T) {
	root := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(root, "docs", "design"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "docs", "design", "note.md"), []byte("note"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "root.txt"), []byte("root"), 0o644))

	tests := []struct {
		name   string
		rule   Rule
		target Target
		want   bool
	}{
		{
			name:   "write directory glob matches one level",
			rule:   Rule{Tool: "file_write", Root: root, Path: []string{"docs/*"}, Action: Allow},
			target: Target{Tool: "file_write", Root: root, Path: "docs/new.txt"}, want: true,
		},
		{
			name:   "star does not cross directory",
			rule:   Rule{Tool: "file_write", Root: root, Path: []string{"docs/*"}, Action: Allow},
			target: Target{Tool: "file_write", Root: root, Path: "docs/design/new.txt"},
		},
		{
			name:   "double star crosses directories",
			rule:   Rule{Tool: "file_edit", Root: root, Path: []string{"docs/**"}, Action: Allow},
			target: Target{Tool: "file_edit", Root: root, Path: "docs/design/note.md"}, want: true,
		},
		{
			name:   "edit rule is directory scoped",
			rule:   Rule{Tool: "file_edit", Root: root, Path: []string{"docs/design/**"}, Action: Allow},
			target: Target{Tool: "file_edit", Root: root, Path: "docs/design/new.md"}, want: true,
		},
		{
			name:   "remove rule is exact path",
			rule:   Rule{Tool: "file_remove", Root: root, Path: []string{"root.txt"}, Action: Allow},
			target: Target{Tool: "file_remove", Root: root, Path: "root.txt"}, want: true,
		},
		{
			name:   "remove exact path does not broaden",
			rule:   Rule{Tool: "file_remove", Root: root, Path: []string{"docs/design/note.md"}, Action: Allow},
			target: Target{Tool: "file_remove", Root: root, Path: "docs/design/other.md"},
		},
		{
			name:   "path rule requires exact file tool identity",
			rule:   Rule{Tool: "file_*", Root: root, Path: []string{"docs/**"}, Action: Allow},
			target: Target{Tool: "file_write", Root: root, Path: "docs/new.txt"},
		},
		{
			name:   "mcp cannot impersonate file tool",
			rule:   Rule{Tool: "file_write", Root: root, Path: []string{"docs/**"}, Action: Allow},
			target: Target{Tool: "file_write", MCP: true, Root: root, Path: "docs/new.txt"},
		},
		{
			name:   "target outside root is never allowed",
			rule:   Rule{Tool: "file_write", Root: root, Path: []string{"**"}, Action: Allow},
			target: Target{Tool: "file_write", Root: root, Path: filepath.Join(filepath.Dir(root), "outside.txt")},
		},
		{
			name:   "different anchored root does not match",
			rule:   Rule{Tool: "file_write", Root: filepath.Dir(root), Path: []string{"**"}, Action: Allow},
			target: Target{Tool: "file_write", Root: root, Path: "docs/new.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Eq(t, tt.want, tt.rule.MatchesPath(tt.target))
		})
	}
}

func TestRuleMatchesPath_ResolutionAndRealTarget(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(root, "docs"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "docs", "safe.txt"), []byte("safe"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644))

	allow := Rule{Tool: "file_write", Root: root, Path: []string{"docs/**"}, Action: Allow}
	deny := Rule{Tool: "file_write", Root: root, Path: []string{"docs/**"}, Action: Deny}

	t.Run("missing target resolves deepest existing ancestor", func(t *testing.T) {
		target := Target{Tool: "file_write", Root: root, Path: "docs/new/deep/file.txt"}
		assert.True(t, allow.MatchesPath(target))
	})

	t.Run("directory target is not an allow target", func(t *testing.T) {
		assert.False(t, allow.MatchesPath(Target{Tool: "file_write", Root: root, Path: "docs"}))
	})

	t.Run("symlink target is not an allow target", func(t *testing.T) {
		link := filepath.Join(root, "docs", "link.txt")
		if err := os.Symlink(filepath.Join(root, "docs", "safe.txt"), link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		assert.False(t, allow.MatchesPath(Target{Tool: "file_write", Root: root, Path: "docs/link.txt"}))
		assert.True(t, deny.MatchesPath(Target{Tool: "file_write", Root: root, Path: "docs/link.txt"}))
	})

	t.Run("hard link is not an allow target", func(t *testing.T) {
		link := filepath.Join(root, "docs", "hard.txt")
		if err := os.Link(filepath.Join(root, "docs", "safe.txt"), link); err != nil {
			t.Skipf("hard links unsupported: %v", err)
		}
		assert.False(t, allow.MatchesPath(Target{Tool: "file_write", Root: root, Path: "docs/hard.txt"}))
		assert.True(t, deny.MatchesPath(Target{Tool: "file_write", Root: root, Path: "docs/hard.txt"}))
	})

	t.Run("resolution failure denies fail closed", func(t *testing.T) {
		bad := Target{Tool: "file_write", Root: root, Path: "docs/\x00bad.txt"}
		assert.False(t, allow.MatchesPath(bad))
		assert.True(t, deny.MatchesPath(bad))
		assert.True(t, (Rule{Tool: "file_write", Root: root, Path: []string{"unrelated/**"}, Action: Deny}).MatchesPath(bad))
	})

	t.Run("real path outside root does not become an allow", func(t *testing.T) {
		link := filepath.Join(root, "docs", "outside.txt")
		if err := os.Symlink(filepath.Join(outside, "secret.txt"), link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		assert.False(t, allow.MatchesPath(Target{Tool: "file_write", Root: root, Path: "docs/outside.txt"}))
	})
}

func TestPolicyDecidePathRule(t *testing.T) {
	root := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o644))
	policy := Policy{
		Rules: []Rule{
			{Source: SourceUser, Index: 1, Tool: "file_write", Root: root, Path: []string{"**"}, Action: Allow},
			{Source: SourceProject, Index: 2, Tool: "file_write", Root: root, Path: []string{"ok.txt"}, Action: Deny},
		},
		Default: Ask,
	}
	action, rule, _ := policy.Decide(Target{Tool: "file_write", Root: root, Path: "ok.txt"})
	assert.Eq(t, Deny, action)
	assert.Require(t, assert.NotNil(t, rule))
	assert.Eq(t, 2, rule.Index)
}
