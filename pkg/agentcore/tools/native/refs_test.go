package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// refsGoSource is the Go fixture of the directory query: one definition, one
// selector use and a call through an imported package.
const refsGoSource = `package config

import "strings"

type Options struct {
	ReadOnly bool
}

func (o *Options) Read() bool {
	if o.ReadOnly {
		return strings.Contains("x", "y")
	}
	return false
}
`

// refsWorkspace writes the tree the tool tests search and returns the workspace
// with the tool over it.
func refsWorkspace(t *testing.T) (string, *FileRefsTool) {
	t.Helper()
	ws := t.TempDir()
	files := map[string]string{
		"internal/config/config.go":      refsGoSource,
		"internal/config/config_test.go": "package config\n\nfunc TestReadOnly(t *testing.T) { _ = Options{ReadOnly: true} }\n",
		"internal/config/loader.ts":      "export function load() {\n  return opts.ReadOnly; // ReadOnly is a flag\n}\n",
		"internal/config/loader.py":      "def load():\n    return opts.ReadOnly\n",
		"internal/broken/broken.go":      "package broken\n\nfunc Broken( {\n",
		"internal/testonly/only_test.go": "package testonly\n\nfunc TestOnly(t *testing.T) {}\n",
		"docs/notes.md":                  "# notes\n\nReadOnly is mentioned here.\n",
	}
	for name, content := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}
	return ws, &FileRefsTool{workspace: ws}
}

// runFileRefs executes the tool and returns the result content.
func runFileRefs(t *testing.T, tool *FileRefsTool, args map[string]any) string {
	t.Helper()
	res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tool.Name(), Arguments: args})
	assert.Require(t, assert.NoErr(t, err))
	return res.Content
}

// runFileRefsErr executes the tool and returns the error of a failed call.
func runFileRefsErr(t *testing.T, tool *FileRefsTool, args map[string]any) error {
	t.Helper()
	_, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tool.Name(), Arguments: args})
	assert.Require(t, assert.Err(t, err))
	return err
}

// TestFileRefsToolMeta is the schema contract: a read-only search tool whose
// properties are not empty (an empty properties object breaks OpenAI-compatible
// gateways).
func TestFileRefsToolMeta(t *testing.T) {
	tool := &FileRefsTool{}
	assert.Eq(t, "file_refs", tool.Name())
	meta := tool.ToolMeta()
	assert.Eq(t, tools.CategorySearch, meta.Category)
	assert.True(t, meta.ReadOnly)
	assert.True(t, meta.Prefetchable)

	schema := tool.Schema()
	props, ok := schema.InputSchema["properties"].(map[string]any)
	assert.Require(t, assert.True(t, ok))
	assert.True(t, len(props) > 0)
	required, ok := schema.InputSchema["required"].([]any)
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, []any{"name"}, required)
	// The description answers the question the tool exists for, and says how a
	// qualified name narrows the result.
	assert.StrContains(t, tool.Description(), "who calls X")
	assert.StrContains(t, tool.Description(), `name: "Runner.Run"`)
}

// TestFileRefsToolDirectory is the tool's main workflow: one call over a
// package reports who uses the name, classified.
func TestFileRefsToolDirectory(t *testing.T) {
	_, tool := refsWorkspace(t)

	t.Run("a directory reports every use, sorted by path and line", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"path": "internal/config", "name": "ReadOnly"})
		assert.StrContains(t, got, "internal/config/config.go:6 def ReadOnly bool")
		assert.StrContains(t, got, "internal/config/config.go:10 sel(o) if o.ReadOnly {")
		assert.StrContainsAll(t, got, []string{"internal/config/loader.py:2", "return opts.ReadOnly"})
		assert.True(t, strings.Index(got, "config.go:6") < strings.Index(got, "config.go:10"), got)
		assert.NotContains(t, got, "docs/notes.md")
		assert.StrContains(t, got, "[approx: ") // the TS/Python matches are approximate
		assert.StrContains(t, got, "[no type resolution: ")
	})

	t.Run("a call through an imported package is marked as a package qualifier", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "Contains", "path": "internal/config/config.go"})
		assert.StrContains(t, got, "internal/config/config.go:11 call(pkg:strings) return strings.Contains(\"x\", \"y\")")
		assert.NotContains(t, got, "[approx: ") // a single Go file is parsed exactly
	})

	t.Run("comments and strings are no use in the approximate languages either", func(t *testing.T) {
		// loader.ts has one real use and one mention in a // comment; loader.py
		// one real use.
		got := runFileRefs(t, tool, map[string]any{"name": "ReadOnly", "path": "internal/config/loader.ts"})
		assert.Eq(t, 1, strings.Count(got, "loader.ts:"), got)

		got = runFileRefs(t, tool, map[string]any{"name": "ReadOnly", "path": "internal/config/loader.py"})
		assert.StrContains(t, got, "internal/config/loader.py:2 ref return opts.ReadOnly")
	})

	t.Run("test files are skipped unless include_tests is set", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "ReadOnly", "path": "internal/config", "include_tests": true})
		assert.StrContains(t, got, "config_test.go")
		assert.NotContains(t, got, "[skipped 1 test file(s)")

		got = runFileRefs(t, tool, map[string]any{"name": "ReadOnly", "path": "internal/config"})
		assert.NotContains(t, got, "config_test.go")
		assert.StrContains(t, got, "[skipped 1 test file(s); pass include_tests: true to search them]")
	})

	t.Run("limit truncates and says how to see the rest", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "ReadOnly", "path": ".", "limit": 1})
		assert.Eq(t, 1, strings.Count(got, "internal/config/config.go:6"), got)
		assert.StrContains(t, got, "[showing 1 matches; more matched - narrow path, or use file_grep with a word-boundary pattern for the full list]")
	})

	t.Run("a name nobody uses points at the rest of the workspace", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "NoSuchName", "path": "internal/config"})
		assert.StrContains(t, got, `no references to "NoSuchName" in internal/config`)
		assert.StrContains(t, got, "[no references in this path; the name may be used from another package/directory")
	})
}

// TestFileRefsToolQualified is the qualified name argument end to end: the result
// keeps only the uses of that type or package and says how many others it left
// out.
func TestFileRefsToolQualified(t *testing.T) {
	_, tool := refsWorkspace(t)

	t.Run("a type-qualified name keeps its field and drops the uses of other qualifiers", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "Options.ReadOnly", "path": "internal/config"})
		assert.StrContains(t, got, "internal/config/config.go:6 def ReadOnly bool")
		assert.NotContains(t, got, "sel(o)") // the receiver variable is not named after the type
		assert.NotContains(t, got, "loader.")
		assert.StrContains(t, got, `[qualified by "Options": kept the definitions of type/package Options and the `+
			`sel/call uses whose qualifier text is Options or ends with Options (case-insensitive); dropped 3 use(s) of `+
			`"ReadOnly" with another qualifier - search without the qualifier for all of them]`)
	})

	t.Run("a qualifier that only names the receiver keeps just that use", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "o.ReadOnly", "path": "internal/config/config.go"})
		assert.StrContains(t, got, "internal/config/config.go:10 sel(o) if o.ReadOnly {")
		assert.NotContains(t, got, "config.go:6 def")
		assert.StrContains(t, got, "dropped 1 use(s) of \"ReadOnly\" with another qualifier")
	})

	t.Run("the approximate languages filter on the qualified text as well", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "opts.ReadOnly", "path": "internal/config/loader.py"})
		assert.StrContains(t, got, "internal/config/loader.py:2 ref return opts.ReadOnly")
		assert.StrContains(t, got, "dropped 0 use(s)")

		got = runFileRefs(t, tool, map[string]any{"name": "Options.ReadOnly", "path": "internal/config/loader.py"})
		assert.StrContains(t, got, `no references to "Options.ReadOnly" in internal/config/loader.py`)
		assert.StrContains(t, got, "dropped 1 use(s)")
		// Only other qualifiers were dropped, so the cross-package hint would mislead.
		assert.NotContains(t, got, "[no references in this path")
	})

	t.Run("the Go result says the list is complete, the approx one does not", func(t *testing.T) {
		goOut := runFileRefs(t, tool, map[string]any{"name": "ReadOnly", "path": "internal/config/config.go"})
		assert.StrContains(t, goOut, "[Go results come from a syntax parse and list every occurrence; "+
			"only confirm same-named other objects - no file_grep needed to fill in]")

		approxOut := runFileRefs(t, tool, map[string]any{"name": "ReadOnly", "path": "internal/config/loader.py"})
		assert.NotContains(t, approxOut, "[Go results come from a syntax parse")
		assert.StrContains(t, approxOut, "[approx: ")
	})
}

// TestFileRefsToolErrors covers the refusals: no name, an unsupported type and
// a directory without supported files.
func TestFileRefsToolErrors(t *testing.T) {
	_, tool := refsWorkspace(t)

	t.Run("name is required", func(t *testing.T) {
		err := runFileRefsErr(t, tool, map[string]any{"path": "internal/config"})
		assert.StrContains(t, err.Error(), `"name" arg is required`)
		assert.StrContains(t, err.Error(), "file_refs")
	})

	t.Run("a file of an unsupported type is refused with the supported list", func(t *testing.T) {
		err := runFileRefsErr(t, tool, map[string]any{"name": "ReadOnly", "path": "docs/notes.md"})
		assert.StrContains(t, err.Error(), "unsupported file type")
		assert.StrContains(t, err.Error(), "Go (.go)")
	})

	t.Run("a directory without supported files reports the supported languages", func(t *testing.T) {
		err := runFileRefsErr(t, tool, map[string]any{"name": "ReadOnly", "path": "docs"})
		assert.StrContains(t, err.Error(), "no supported source files under docs")
		assert.StrContains(t, err.Error(), "Go (.go)")
	})

	t.Run("a directory of test files only suggests include_tests", func(t *testing.T) {
		err := runFileRefsErr(t, tool, map[string]any{"name": "ReadOnly", "path": "internal/testonly"})
		assert.StrContains(t, err.Error(), "only test files under internal/testonly")
	})

	t.Run("a path outside the workspace is refused", func(t *testing.T) {
		err := runFileRefsErr(t, tool, map[string]any{"name": "ReadOnly", "path": filepath.Join(t.TempDir(), "elsewhere")})
		assert.StrContains(t, err.Error(), "path escape")
	})

	t.Run("a file that does not parse is reported, not fatal", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "Broken", "path": "internal/broken"})
		assert.StrContains(t, got, "[1 file(s) could not be parsed: internal/broken/broken.go")
	})
}

// TestFileRefsToolReadRoot searches an absolute path under a read-only root,
// which is how a dependency's sources are reachable.
func TestFileRefsToolReadRoot(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(dep, 0o755)))
	file := filepath.Join(dep, "lib.go")
	src := "package dep\n\nfunc Use() { Helper() }\n"
	assert.Require(t, assert.NoErr(t, os.WriteFile(file, []byte(src), 0o644)))

	tool := &FileRefsTool{workspace: t.TempDir(), readRoots: []string{root}}
	got := runFileRefs(t, tool, map[string]any{"name": "Helper", "path": dep})
	assert.StrContains(t, got, filepath.Join(dep, "lib.go")+":3 call func Use() { Helper() }")
}

// TestFileRefsToolScanCost holds the cost promise of the design: a directory
// scan pre-filters on the bytes, so a few hundred files are still one quick
// call (an unstaged parse of every Go file would be much slower).
func TestFileRefsToolScanCost(t *testing.T) {
	ws := t.TempDir()
	const files = 200
	for i := 0; i < files; i++ {
		dir := filepath.Join(ws, "pkg", fmt.Sprintf("p%03d", i))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(dir, 0o755)))
		body := "package p\n\nfunc OnlyHere() {}\n"
		if i == files-1 {
			body = "package p\n\nfunc Target() {}\n\nfunc caller() { Target() }\n"
		}
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte(body), 0o644)))
	}
	tool := &FileRefsTool{workspace: ws}
	start := time.Now()
	got := runFileRefs(t, tool, map[string]any{"name": "Target"})
	elapsed := time.Since(start)
	assert.StrContains(t, got, "def func Target() {}")
	assert.StrContains(t, got, "call func caller() { Target() }")
	t.Logf("%d files in %s", files, elapsed)
	assert.True(t, elapsed < 2*time.Second, elapsed)
}
