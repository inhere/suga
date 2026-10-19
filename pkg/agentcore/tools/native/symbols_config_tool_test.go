package native

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestFileSymbolsToolConfigFiles runs the two configuration formats through the
// tool: this is the listing the model reads, so the key path, the kind, the
// signature, the kind filter and the limit are checked here and not only in the
// extractors.
func TestFileSymbolsToolConfigFiles(t *testing.T) {
	ws := t.TempDir()
	writeSourceFixture(t, ws, "config.yaml", yamlConfigFixture)
	writeSourceFixture(t, ws, "config.toml", tomlConfigFixture)
	tool := &FileSymbolsTool{workspace: ws}

	t.Run("a yaml file lists its keys by path", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "config.yaml"})
		for _, want := range []string{
			"config.yaml:2 section server {…}",
			"config.yaml:4 key server.http.port 8080",
			"config.yaml:5 key server.http.hosts […]",
			"config.yaml:12 key services[].name api ×2",
			"config.yaml:21 key quoted-key 1",
		} {
			assert.StrContains(t, got, want)
		}
		// The file was parsed: no approx marker, and no cap note either.
		assert.NotContains(t, got, "[approx")
		assert.NotContains(t, got, "key levels are listed")
	})

	t.Run("a toml file lists its tables and keys", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "config.toml"})
		for _, want := range []string{
			"config.toml:4 section server {…}",
			"config.toml:5 key server.port 8080",
			"config.toml:26 key products[].name \"hammer\" ×2",
		} {
			assert.StrContains(t, got, want)
		}
		assert.StrContains(t, got, "[approx: ")
	})

	t.Run("the kind filter selects sections or keys", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "config.yaml", "kind": "section"})
		assert.StrContains(t, got, "section server.http")
		assert.NotContains(t, got, "key server.http.port")
		// The alias of a model that says "tables" for a TOML file.
		got = runFileSymbols(t, tool, map[string]any{"path": "config.toml", "kind": "tables"})
		assert.StrContains(t, got, "section server")
		assert.NotContains(t, got, "key title")
	})

	t.Run("the limit cuts the listing and says more matched", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 20; i++ {
			fmt.Fprintf(&sb, "key%02d: %d\n", i, i)
		}
		writeSourceFixture(t, ws, "many.yaml", sb.String())
		got := runFileSymbols(t, tool, map[string]any{"path": "many.yaml", "limit": 5})
		assert.Eq(t, 5, strings.Count(got, " key key"))
		assert.StrContains(t, got, "[showing 5 declarations; more matched - narrow name or kind, or raise limit]")
	})

	t.Run("a directory walk tells a configuration cap once", func(t *testing.T) {
		dir := t.TempDir()
		writeSourceFixture(t, dir, "one.yaml", "a:\n  b:\n    c:\n      d:\n        e: 1\n")
		writeSourceFixture(t, dir, "two.yml", "a:\n  b:\n    c:\n      d:\n        f: 2\n")
		got := runFileSymbols(t, &FileSymbolsTool{workspace: dir}, map[string]any{"path": "."})
		assert.Eq(t, 1, strings.Count(got, "key levels are listed"))
		assert.StrContains(t, got, "one.yaml:4 section a.b.c.d")
		assert.StrContains(t, got, "two.yml:4 section a.b.c.d")
	})
}
