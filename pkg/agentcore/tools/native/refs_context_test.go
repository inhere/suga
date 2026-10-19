package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// refsContextGoSource is the Go fixture of the context tests: the name is
// defined once, used twice in one function and once in each of two later
// functions, so the blocks merge and split in a known way.
const refsContextGoSource = `package p

func Target() {}

func caller() {
	Target()
	_ = Target
}

func other() {
	Target()
}

func third() {
	Target()
}
`

// refsContextTSFile has the name in a comment line of its own: a context line
// must show the source as written, not the line with the comment masked.
const refsContextTSFile = `export function load() {
  // ReadOnly is a flag
  return opts.ReadOnly;
}
`

// refsContextRustFile is a language without an extractor: its matches come from
// the bare text fallback, which does not mask anything, and the context lines
// are the raw source as well.
const refsContextRustFile = `pub fn file_type() {
    // file_type() below
    let x = file_type();
    x
}
`

// refsContextWorkspace writes the tree of the context tests.
func refsContextWorkspace(t *testing.T) (string, *FileRefsTool) {
	t.Helper()
	ws := t.TempDir()
	files := map[string]string{
		"pkg/ctx.go":      refsContextGoSource,
		"web/loader.ts":   refsContextTSFile,
		"rust/src/lib.rs": refsContextRustFile,
		"rust/tests/t.rs": refsContextRustFile, // under tests/: skipped without include_tests
		"docs/notes.md":   "# notes\n\nTarget is mentioned here.\n",
		"pkg/ctx_test.go": "package p\n\nfunc TestTarget(t *testing.T) { _ = Target }\n",
		"config/app.yaml": "server:\n  port: 8080\n  name: app\n",
	}
	for name, content := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}
	return ws, &FileRefsTool{workspace: ws}
}

// TestFileRefsContextDefaultUnchanged is the compatibility contract: without the
// argument, and with context: 0, the output is the one-line-per-match result the
// tool always produced.
func TestFileRefsContextDefaultUnchanged(t *testing.T) {
	_, tool := refsContextWorkspace(t)

	plain := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go"})
	zero := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go", "context": 0})

	t.Run("no context line and no block separator without context", func(t *testing.T) {
		assert.Eq(t, plain, zero)
		assert.NotContains(t, plain, "pkg/ctx.go-")
		assert.NotContains(t, plain, "--")
		assert.StrContains(t, plain, "pkg/ctx.go:3 def func Target() {}")
		assert.StrContains(t, plain, "pkg/ctx.go:6 call Target()")
	})

	t.Run("a negative context is a plain 0", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go", "context": -2})
		assert.Eq(t, plain, got)
	})

	t.Run("the description tells the model what context buys", func(t *testing.T) {
		assert.StrContains(t, tool.Description(), "Pass context: 3")
		props := tool.Schema().InputSchema["properties"].(map[string]any)
		context, ok := props["context"].(map[string]any)
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, "integer", context["type"])
		assert.Eq(t, 0, context["minimum"])
		assert.Eq(t, maxRefContext, context["maximum"])
	})
}

// TestFileRefsContextBlocks is the format contract: match lines keep their shape,
// context lines are "path-line- text", close blocks merge and distant ones are
// separated by "--".
func TestFileRefsContextBlocks(t *testing.T) {
	_, tool := refsContextWorkspace(t)

	t.Run("blocks merge, separate and show the raw source around each match", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go", "context": 1})
		assert.StrContains(t, got, "pkg/ctx.go-2- \n"+
			"pkg/ctx.go:3 def func Target() {}\n"+
			"pkg/ctx.go-4- \n"+
			"pkg/ctx.go-5- func caller() {\n"+
			"pkg/ctx.go:6 call Target()\n"+
			"pkg/ctx.go:7 ref _ = Target\n"+
			"pkg/ctx.go-8- }\n"+
			"--\n"+
			"pkg/ctx.go-10- func other() {\n"+
			"pkg/ctx.go:11 call Target()\n"+
			"pkg/ctx.go-12- }\n"+
			"--\n"+
			"pkg/ctx.go-14- func third() {\n"+
			"pkg/ctx.go:15 call Target()\n"+
			"pkg/ctx.go-16- }")
	})

	t.Run("a block stops at the first and the last line of the file", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go", "context": 5})
		assert.StrContains(t, got, "pkg/ctx.go-1- package p\npkg/ctx.go-2- \npkg/ctx.go:3 def")
		assert.NotContains(t, got, "pkg/ctx.go-0-")
		assert.NotContains(t, got, "pkg/ctx.go-17-")
		assert.StrContains(t, got, "pkg/ctx.go-16- }\n")
		assert.NotContains(t, got, "\n--")
	})

	t.Run("context lines do not consume the match limit", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go", "context": 1, "limit": 1})
		assert.Eq(t, 1, strings.Count(got, "pkg/ctx.go:"), got)
		assert.StrContains(t, got, "pkg/ctx.go-2- \npkg/ctx.go:3 def func Target() {}\npkg/ctx.go-4- ")
		assert.StrContains(t, got, "[showing 1 matches; more matched")
	})

	t.Run("a radius over the cap is clamped, not rejected", func(t *testing.T) {
		wide := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go", "context": 99})
		capped := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go", "context": maxRefContext})
		assert.Eq(t, capped, wide)
	})

	t.Run("a single file path argument keeps its context lines", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "Target", "path": "pkg/ctx.go", "context": 1, "include_tests": false})
		assert.StrContains(t, got, "pkg/ctx.go:3 def")
		// The file's own path is not a directory: the test file next to it is not searched.
		assert.NotContains(t, got, "ctx_test.go")
	})
}

// TestFileRefsContextApproxLanguages covers the approximate matcher and the bare
// fallback: their context lines come from the raw source, so a comment holding
// the name is shown as written even where the match itself was masked.
func TestFileRefsContextApproxLanguages(t *testing.T) {
	_, tool := refsContextWorkspace(t)

	t.Run("a masked comment line still shows as a context line", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "ReadOnly", "path": "web/loader.ts", "context": 1})
		assert.StrContains(t, got, "web/loader.ts-2- // ReadOnly is a flag\n"+
			"web/loader.ts:3 ref return opts.ReadOnly;\n"+
			"web/loader.ts-4- }")
		// The masked comment is no match of its own.
		assert.Eq(t, 1, strings.Count(got, "web/loader.ts:"), got)
	})

	t.Run("the bare text fallback shows the source around its matches", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "file_type", "path": "rust/src", "context": 1})
		// The bare fallback shows the line as it is written, indentation included,
		// and its text match does not mask the comment line.
		assert.StrContains(t, got, "rust/src/lib.rs:1 def pub fn file_type() {\n"+
			"rust/src/lib.rs:2 call     // file_type() below\n"+
			"rust/src/lib.rs:3 call     let x = file_type();\n"+
			"rust/src/lib.rs-4- x")
	})
}

// TestFileRefsContextMaybeGroup is the qualified search: the maybe-also group of
// dropped variable uses carries the same context around its lines.
func TestFileRefsContextMaybeGroup(t *testing.T) {
	_, tool := refsWorkspace(t)

	got := runFileRefs(t, tool, map[string]any{"name": "Options.ReadOnly", "path": "internal/config", "context": 1})
	assert.StrContains(t, got, "check each line]\ninternal/config/config.go-9- func (o *Options) Read() bool {\n"+
		"internal/config/config.go:10 sel(o) if o.ReadOnly {")
	assert.NotContains(t, got, "internal/config/config.go-0-")
}

// TestFileRefsContextReadRoot covers the read-only dependency roots: the shown
// path is absolute there, and the context lines must read the same file back.
func TestFileRefsContextReadRoot(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(dep, 0o755)))
	file := filepath.Join(dep, "lib.go")
	src := "package dep\n\n// Helper is called below\nfunc Use() { Helper() }\n"
	assert.Require(t, assert.NoErr(t, os.WriteFile(file, []byte(src), 0o644)))

	tool := &FileRefsTool{workspace: t.TempDir(), readRoots: []string{root}}
	got := runFileRefs(t, tool, map[string]any{"name": "Helper", "path": dep, "context": 1})
	assert.StrContains(t, got, file+"-3- // Helper is called below\n"+file+":4 call func Use() { Helper() }")
}
