package native

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestFileRefsQualifiedKeepsSamePackageCalls pins the gpt T5 miss
// (docs/research/2026-10-18-explore-efficiency.md section 11): asked for
// appconfig.ReadModelSettings, the qualified filter dropped config's own
// ReadModelSettings("") calls, which need no qualifier inside the package.
func TestFileRefsQualifiedKeepsSamePackageCalls(t *testing.T) {
	ws := t.TempDir()
	files := map[string]string{
		"internal/config/providers.go": "package config\n\nfunc ReadModelSettings(flag string) error { return nil }\n",
		"internal/config/registry.go":  "package config\n\nfunc load() { _ = ReadModelSettings(\"\") }\n",
		"internal/cli/models.go": "package cli\n\nimport appconfig \"example.com/internal/config\"\n\n" +
			"func run() { _ = appconfig.ReadModelSettings(\"\") }\n",
	}
	for name, content := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}
	tool := &FileRefsTool{workspace: ws}

	t.Run("the package alias keeps the qualified call and shows the bare one", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "appconfig.ReadModelSettings"})
		assert.StrContains(t, got, "internal/cli/models.go:5 call(pkg:appconfig)")
		assert.StrContains(t, got, "[maybe also appconfig: unqualified uses")
		assert.StrContains(t, got, "internal/config/registry.go:3 call func load() { _ = ReadModelSettings(\"\") }")
	})

	t.Run("a definition is not shown as a maybe use", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "x.ReadModelSettings"})
		assert.NotContains(t, got, "providers.go:3 def")
	})
}
