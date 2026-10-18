package native

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestNoIgnoreArg pins that the "pass no_ignore: true" hint file_refs and
// file_symbols print for a skipped ignored directory names an argument the
// tool really takes: without it the hint sends the model into a call that
// changes nothing.
func TestNoIgnoreArg(t *testing.T) {
	ws := t.TempDir()
	files := map[string]string{
		".gitignore": "gen/\n",
		"main.go":    "package main\n\nfunc main() { Generated() }\n",
		"gen/gen.go": "package main\n\nfunc Generated() {}\n",
	}
	for name, content := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}

	t.Run("file_refs searches ignored directories only with no_ignore", func(t *testing.T) {
		tool := &FileRefsTool{workspace: ws}
		got := runFileRefs(t, tool, map[string]any{"name": "Generated"})
		assert.StrContains(t, got, "main.go:3 call")
		assert.NotContains(t, got, "gen/gen.go")
		assert.StrContains(t, got, "pass no_ignore: true")

		got = runFileRefs(t, tool, map[string]any{"name": "Generated", "no_ignore": true})
		assert.StrContains(t, got, "gen/gen.go:3 def")
		assert.NotContains(t, got, "pass no_ignore: true")
		props := tool.Schema().InputSchema["properties"].(map[string]any)
		assert.ContainsKey(t, props, "no_ignore")
	})

	t.Run("file_symbols lists ignored directories only with no_ignore", func(t *testing.T) {
		tool := &FileSymbolsTool{workspace: ws}
		got := runFileSymbols(t, tool, map[string]any{"path": "."})
		assert.NotContains(t, got, "func Generated")
		assert.StrContains(t, got, "pass no_ignore: true")

		got = runFileSymbols(t, tool, map[string]any{"path": ".", "no_ignore": true})
		assert.StrContains(t, got, "gen/gen.go:3 func Generated")
		props := tool.Schema().InputSchema["properties"].(map[string]any)
		assert.ContainsKey(t, props, "no_ignore")
	})
}
