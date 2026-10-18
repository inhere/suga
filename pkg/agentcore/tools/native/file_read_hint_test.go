package native

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// hintPad returns n comment lines: the padding that grows a fixture past the
// outline threshold without adding declarations.
func hintPad(n int) string {
	return strings.Repeat("// padding line\n", n)
}

// TestFileReadTruncationHint covers the guidance line a truncated read adds
// (suag-nxwg): the file's length, its declaration count when the extraction
// layer covers the file, and how to read on without paging. The extraction is
// pinned to the built-in one, so the counts do not depend on ctags.
func TestFileReadTruncationHint(t *testing.T) {
	pinBuiltinExtractors(t)
	ws := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, name), []byte(content), 0o644))
		return content
	}
	// big.go: 402 lines, 4 declarations (one every 100 padding lines).
	var sb strings.Builder
	sb.WriteString("package fixture\n\n")
	decls := 0
	for i := 0; i < 400; i++ {
		if i%100 == 0 {
			fmt.Fprintf(&sb, "func F%d() {}\n", i)
			decls++
			continue
		}
		sb.WriteString("// padding line\n")
	}
	big := write("big.go", sb.String())
	total := fileLineCount(newSourceLines([]byte(big)))
	tool := &FileReadTool{workspace: ws, maxOut: 2000}

	t.Run("a truncated source file names the outline and a span", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"path": "big.go"})
		assert.StrContains(t, out, fmt.Sprintf("[truncated at output cap 2000 bytes; the file has %d lines and %d declarations; "+
			"read a span as big.go:start-end or ask for the declaration outline with outline=true; continue with offset=", total, decls))
	})
	t.Run("a file type without an extractor gets the line count only", func(t *testing.T) {
		write("notes.txt", strings.Repeat("plain line\n", 400))
		out := execTool(t, tool, map[string]any{"path": "notes.txt"})
		assert.StrContains(t, out, "the file has 400 lines; read a span as notes.txt:start-end; continue with offset=")
		assert.NotContains(t, out, "declarations")
		assert.NotContains(t, out, "outline=true")
	})
	t.Run("a source file that does not parse keeps the line count only", func(t *testing.T) {
		write("broken.go", "package broken\n\nfunc Broken( {\n"+hintPad(400))
		out := execTool(t, tool, map[string]any{"path": "broken.go"})
		assert.StrContains(t, out, "the file has 403 lines; read a span as broken.go:start-end; continue with offset=")
		assert.NotContains(t, out, "declarations")
	})
	t.Run("outline is not promised for a file under its threshold", func(t *testing.T) {
		write("short.go", "package fixture\n\nfunc Only() {}\n"+hintPad(247))
		out := execTool(t, tool, map[string]any{"path": "short.go"})
		assert.StrContains(t, out, "the file has 250 lines and 1 declarations; read a span as short.go:start-end; continue with offset=")
		assert.NotContains(t, out, "outline=true")
	})
	t.Run("a file over the outline cap keeps the plain note", func(t *testing.T) {
		write("huge.go", "package fixture\n\n"+hintPad(120000))
		out := execTool(t, tool, map[string]any{"path": "huge.go"})
		assert.StrContains(t, out, "[truncated at output cap 2000 bytes; continue with offset=")
		assert.NotContains(t, out, "the file has")
	})
	t.Run("an untruncated read is unchanged", func(t *testing.T) {
		write("small.txt", "one\ntwo\n")
		full := &FileReadTool{workspace: ws}
		assert.Eq(t, "1\tone\n2\ttwo", execTool(t, full, map[string]any{"path": "small.txt"}))
		out := execTool(t, full, map[string]any{"path": "big.go"})
		assert.StrNotContains(t, out, "[truncated")
		assert.True(t, strings.HasPrefix(out, "1\tpackage fixture\n2\t\n3\tfunc F0() {}"), out)
	})
	t.Run("a truncated span read gets the whole file's size", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"path": "big.go:1-400"})
		assert.StrContains(t, out, "[truncated at output cap 2000 bytes; ")
		assert.StrContains(t, out, fmt.Sprintf("the file has %d lines and %d declarations", total, decls))
	})
}
