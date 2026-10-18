package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// symbolsWorkspace writes the source tree the tool tests list and returns the
// workspace with the tool over it.
func symbolsWorkspace(t *testing.T) (string, *FileSymbolsTool) {
	t.Helper()
	ws := t.TempDir()
	files := map[string]string{
		"internal/config/config.go":       configGoFixture,
		"internal/config/config_test.go":  "package config\n\nfunc ReadFixture() string { return \"\" }\n",
		"internal/config/loader.py":       "class PyLoader:\n    def fetch(self):\n        return 1\n",
		"internal/config/notes.md":        "# notes\n\nRead this by hand.\n",
		"internal/broken/broken.go":       "package broken\n\nfunc Broken( {\n",
		"internal/testonly/only_test.go":  "package testonly\n\nfunc helper() {}\n",
		"docs/readme.md":                  "# docs\n\nNo source here.\n",
		"pkg/sub/deep.go":                 "package sub\n\nfunc Deep() {}\n",
		"pkg/sub/deep_test.go":            "package sub\n\nfunc DeepFixture() {}\n",
		"internal/broken/also_broken.txt": "not source\n",
	}
	for name, content := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}
	return ws, &FileSymbolsTool{workspace: ws}
}

// configGoFixture is the directory listing's main fixture: two Read* functions
// (what the T2 task asks for), one other function, a type and a method.
const configGoFixture = `package config

// ReadConfig reads the config.
func ReadConfig(path string) (string, error) { return "", nil }

// ReadEnv reads one environment variable.
func ReadEnv(name string) string { return "" }

// WriteConfig writes the config.
func WriteConfig(path string) error { return nil }

type Loader struct{}

// Read loads through the loader.
func (l *Loader) Read() string { return "" }
`

// runFileSymbols executes the tool and returns the result content.
func runFileSymbols(t *testing.T, tool *FileSymbolsTool, args map[string]any) string {
	t.Helper()
	res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tool.Name(), Arguments: args})
	assert.Require(t, assert.NoErr(t, err))
	return res.Content
}

// runFileSymbolsErr executes the tool and returns the error of a failed call.
func runFileSymbolsErr(t *testing.T, tool *FileSymbolsTool, args map[string]any) error {
	t.Helper()
	_, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tool.Name(), Arguments: args})
	assert.Require(t, assert.Err(t, err))
	return err
}

// TestFileSymbolsToolDirectory is the T2 task itself: list the declarations of
// a package without guessing file_grep patterns.
func TestFileSymbolsToolDirectory(t *testing.T) {
	_, tool := symbolsWorkspace(t)

	t.Run("a directory lists every supported file, tests and other types left out", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "internal/config"})
		for _, want := range []string{
			"internal/config/config.go:4 func ReadConfig",
			"internal/config/config.go:7 func ReadEnv",
			"internal/config/config.go:10 func WriteConfig",
			"internal/config/config.go:12 type Loader",
			"internal/config/config.go:15 method Read",
			"internal/config/loader.py:1 class PyLoader",
			"internal/config/loader.py:2 method fetch",
		} {
			assert.StrContains(t, got, want)
		}
		assert.NotContains(t, got, "ReadFixture")
		// The declarations come out by path and line, the skipped files are counted.
		assert.True(t, strings.Index(got, "config.go:4") < strings.Index(got, "loader.py:1"), got)
		assert.StrContains(t, got, "[skipped 1 test file(s); pass include_tests: true to include them]")
		assert.StrContains(t, got, "[skipped 1 file(s) of a type no extractor covers")
		assert.StrContains(t, got, "[approx:")
	})

	t.Run("name filters by substring", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "internal/config", "name": "Read"})
		assert.StrContains(t, got, "internal/config/config.go:4 func ReadConfig")
		assert.StrContains(t, got, "internal/config/config.go:7 func ReadEnv")
		assert.StrContains(t, got, "internal/config/config.go:15 method Read")
		assert.NotContains(t, got, "WriteConfig")
		assert.NotContains(t, got, "PyLoader")
	})

	t.Run("kind filters", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "internal/config", "kind": "func"})
		assert.StrContains(t, got, "func ReadConfig")
		assert.NotContains(t, got, "method Read")
		assert.NotContains(t, got, "PyLoader")
		got = runFileSymbols(t, tool, map[string]any{"path": "internal/config", "kind": "class,method"})
		assert.StrContains(t, got, "class PyLoader")
		assert.StrContains(t, got, "method fetch")
		assert.NotContains(t, got, "func ReadConfig")
	})

	t.Run("include_tests lists the test files", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "internal/config", "include_tests": true})
		assert.StrContains(t, got, "internal/config/config_test.go:3 func ReadFixture")
		assert.NotContains(t, got, "skipped 1 test file(s)")
	})

	t.Run("a directory is walked recursively", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "pkg"})
		assert.StrContains(t, got, "pkg/sub/deep.go:3 func Deep")
		assert.NotContains(t, got, "DeepFixture")
	})

	t.Run("a single file is listed without the directory notes", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "internal/config/config.go"})
		assert.StrContains(t, got, "internal/config/config.go:4 func ReadConfig")
		assert.NotContains(t, got, "loader.py")
		assert.NotContains(t, got, "skipped")
		assert.NotContains(t, got, "approx", "Go is parsed, so no approx note")
	})

	t.Run("limit truncates and says more matched", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "internal/config/config.go", "limit": 2})
		assert.StrContains(t, got, "internal/config/config.go:4 func ReadConfig")
		assert.StrContains(t, got, "internal/config/config.go:7 func ReadEnv")
		assert.NotContains(t, got, "WriteConfig")
		assert.StrContains(t, got, "[showing 2 declarations; more matched - narrow name or kind, or raise limit]")
	})

	t.Run("a filter that matches nothing says so", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "internal/config", "name": "NothingHere"})
		assert.StrContains(t, got, `no symbols matched (2 source file(s) scanned; name="NothingHere")`)
	})
}

// TestFileSymbolsToolReadRoot covers Q3 of the design: an absolute path under a
// read-only root (the Go module cache, the standard library sources) is listed
// too, and the results name the files absolutely so the model can pass them
// straight back to another tool.
func TestFileSymbolsToolReadRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "lib.go")
	assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte("package lib\n\nfunc Listed() {}\n"), 0o644)))
	tool := &FileSymbolsTool{workspace: t.TempDir(), readRoots: []string{root}}
	got := runFileSymbols(t, tool, map[string]any{"path": path})
	assert.StrContains(t, got, path+":3 func Listed")

	t.Run("a read root is walked like the workspace", func(t *testing.T) {
		inner := filepath.Join(root, "pkg")
		assert.Require(t, assert.NoErr(t, os.MkdirAll(inner, 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(inner, "deep.go"),
			[]byte("package pkg\n\nfunc DeepListing() {}\n"), 0o644)))
		got := runFileSymbols(t, tool, map[string]any{"path": root, "name": "DeepListing"})
		assert.StrContains(t, got, filepath.Join(inner, "deep.go")+":3 func DeepListing")
	})
}

// TestFileSymbolsToolErrors: every way of asking for something the extractor
// cannot answer is an error the model can act on, never an empty result.
func TestFileSymbolsToolErrors(t *testing.T) {
	_, tool := symbolsWorkspace(t)

	t.Run("an unsupported file type is refused with the supported list", func(t *testing.T) {
		err := runFileSymbolsErr(t, tool, map[string]any{"path": "internal/config/notes.md"})
		assert.StrContains(t, err.Error(), `unsupported file type ".md"; supported: Go (.go)`)
	})

	t.Run("a path that does not exist", func(t *testing.T) {
		err := runFileSymbolsErr(t, tool, map[string]any{"path": "internal/nope"})
		assert.StrContains(t, err.Error(), "does not exist")
	})

	t.Run("a directory of other file types", func(t *testing.T) {
		err := runFileSymbolsErr(t, tool, map[string]any{"path": "docs"})
		assert.StrContains(t, err.Error(), "no supported source files under docs")
	})

	t.Run("a directory of test files only", func(t *testing.T) {
		err := runFileSymbolsErr(t, tool, map[string]any{"path": "internal/testonly"})
		assert.StrContains(t, err.Error(), "only test files under internal/testonly; pass include_tests: true")
	})

	t.Run("an unknown kind", func(t *testing.T) {
		err := runFileSymbolsErr(t, tool, map[string]any{"path": "internal/config", "kind": "fnord"})
		assert.StrContains(t, err.Error(), `unknown kind "fnord"; kinds: func, method, type, const, var, class`)
	})

	t.Run("path is required", func(t *testing.T) {
		err := runFileSymbolsErr(t, tool, map[string]any{"name": "Read"})
		assert.StrContains(t, err.Error(), `"path" arg is required`)
	})

	t.Run("the path argument cannot leave the workspace", func(t *testing.T) {
		err := runFileSymbolsErr(t, tool, map[string]any{"path": filepath.Join(string(filepath.Separator), "etc")})
		assert.Err(t, err)
	})

	t.Run("a file that does not parse is one line, the others still come", func(t *testing.T) {
		got := runFileSymbols(t, tool, map[string]any{"path": "internal"})
		assert.StrContains(t, got, "[1 file(s) could not be parsed: internal/broken/broken.go:3:")
		assert.StrContains(t, got, "internal/config/config.go:4 func ReadConfig")
	})
}

// TestFileSymbolsToolMetadata pins the tool to the read-only classification: it
// reads sources, so it runs in a read-only session and needs no approval.
func TestFileSymbolsToolMetadata(t *testing.T) {
	tool := &FileSymbolsTool{workspace: t.TempDir()}
	meta := tools.MetaOf(tool)
	assert.Eq(t, tools.CategoryRead, meta.Category)
	assert.True(t, meta.ReadOnly)
	assert.Eq(t, "file_symbols", tool.Name())

	schema := tool.Schema()
	assert.Eq(t, "object", schema.InputSchema["type"])
	props, _ := schema.InputSchema["properties"].(map[string]any)
	assert.Len(t, props, 5)
	assert.Contains(t, schema.InputSchema["required"], "path")
	assert.StrContains(t, schema.Description, "file_grep")
}
