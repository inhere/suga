package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// pinBuiltinExtractors keeps universal-ctags out of the outline tests: which
// declarations an outline shows must not depend on whether the machine running
// the tests has ctags installed.
func pinBuiltinExtractors(t *testing.T) {
	t.Helper()
	command := ctagsCommand
	t.Cleanup(func() { ctagsCommand = command })
	ctagsCommand = func() string { return "" }
}

// outlineFixture builds a source file line by line: add returns the line number
// a line got, so a test can name a declaration's line and the spans between two
// of them instead of hard-coding numbers.
type outlineFixture struct {
	sb   strings.Builder
	line int
}

// add writes one line and returns its 1-based number.
func (f *outlineFixture) add(text string) int {
	f.sb.WriteString(text + "\n")
	f.line++
	return f.line
}

// pad writes n comment lines, so a fixture can grow past the outline threshold.
func (f *outlineFixture) pad(n int) {
	for i := 0; i < n; i++ {
		f.add("// padding")
	}
}

func (f *outlineFixture) String() string { return f.sb.String() }

// goOutlineFixture is a Go file with four declarations, bodies between them and
// a tail past the outline threshold.
func goOutlineFixture() (string, map[string]int) {
	f, lines := &outlineFixture{}, map[string]int{}
	f.add("package fixture")
	f.add("")
	f.pad(8)
	f.add("")
	lines["First"] = f.add("func First(a int) int {")
	f.add("\treturn a * 2 // FirstBody")
	f.pad(6)
	f.add("}")
	f.add("")
	lines["Thing"] = f.add("type Thing struct {")
	f.add("\tName string")
	f.add("}")
	f.add("")
	lines["counter"] = f.add("var counter = 1")
	f.add("")
	f.pad(4)
	lines["Second"] = f.add("func Second() {}")
	f.pad(300)
	return f.String(), lines
}

// pythonOutlineFixture is a Python file with a function, a class and a method,
// padded past the outline threshold: a language without a parser gets an outline
// too, marked approx.
func pythonOutlineFixture() (string, map[string]int) {
	f, lines := &outlineFixture{}, map[string]int{}
	f.add(`"""Fixture module."""`)
	f.add("")
	f.pad(20)
	lines["helper"] = f.add("def helper(value):")
	f.add("    return value")
	f.add("")
	f.pad(200)
	lines["Loader"] = f.add("class Loader:")
	lines["read"] = f.add("    def read(self):")
	f.add("        return 1")
	f.pad(120)
	return f.String(), lines
}

// exactGoFile is a Go source of exactly lines lines: one small function, then
// comments. It drives the outline threshold boundary.
func exactGoFile(lines int) string {
	f := &outlineFixture{}
	f.add("package fixture")
	f.add("")
	f.add("func Only() {}")
	for f.line < lines {
		f.add("// padding")
	}
	return f.String()
}

// TestFileReadOutlineGo is the Go outline: declaration line numbers with their
// declaration line, and a marker for every span in between, so the model knows
// which ranges it has not read.
func TestFileReadOutlineGo(t *testing.T) {
	pinBuiltinExtractors(t)
	ws := t.TempDir()
	content, lines := goOutlineFixture()
	total := strings.Count(content, "\n")
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "out.go"), []byte(content), 0o644))
	tool := &FileReadTool{workspace: ws}

	t.Run("declarations and omitted spans", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"path": "out.go", "outline": true})
		assert.True(t, strings.HasPrefix(out, "[outline of out.go: "), out)
		assert.StrContains(t, out, fmt.Sprintf("%d lines, 4 declarations; this is a declaration outline, not the whole file - "+
			"read a span as out.go:start-end for the text]", total))
		for name, want := range map[string]string{
			"First":   "func First(a int) int {",
			"Thing":   "type Thing struct {",
			"counter": "var counter = 1",
			"Second":  "func Second() {}",
		} {
			assert.StrContains(t, out, fmt.Sprintf("%d: %s", lines[name], want))
		}
		// every line that is not a declaration is marked with its exact range
		assert.StrContains(t, out, fmt.Sprintf("[omitted 1-%d: %d lines]", lines["First"]-1, lines["First"]-1))
		assert.StrContains(t, out, fmt.Sprintf("[omitted %d-%d: ", lines["First"]+1, lines["Thing"]-1))
		assert.StrContains(t, out, fmt.Sprintf("[omitted %d-%d: ", lines["Thing"]+1, lines["counter"]-1))
		assert.StrContains(t, out, fmt.Sprintf("[omitted %d-%d: %d lines]", lines["Second"]+1, total, total-lines["Second"]))
		// a declaration's body is not part of the outline
		assert.NotContains(t, out, "FirstBody")
		assert.NotContains(t, out, "Name string")
		assert.NotContains(t, out, "[approx")
	})
	t.Run("the output cap cuts the outline and says how to read on", func(t *testing.T) {
		small := &FileReadTool{workspace: ws, maxOut: 256}
		out := execTool(t, small, map[string]any{"path": "out.go", "outline": true})
		assert.StrContains(t, out, "[truncated at output cap 256 bytes; read a span as out.go:start-end]")
	})
	t.Run("two files in one call get one outline each", func(t *testing.T) {
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, "second.go"), []byte(exactGoFile(320)), 0o644))
		out := execTool(t, tool, map[string]any{"paths": []any{"out.go", "second.go"}, "outline": true})
		assert.Eq(t, 2, strings.Count(out, "[outline of "))
		assert.StrContains(t, out, "==> out.go <==\n[outline of out.go: ")
		assert.StrContains(t, out, "==> second.go <==\n[outline of second.go: 320 lines, 1 declarations")
	})
}

// TestFileReadOutlineFallback covers everything that answers outline=true with
// the plain window plus a first line saying why: the outline is a convenience,
// never an error.
func TestFileReadOutlineFallback(t *testing.T) {
	pinBuiltinExtractors(t)
	ws := t.TempDir()
	tool := &FileReadTool{workspace: ws}
	write := func(name, content string) {
		t.Helper()
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, name), []byte(content), 0o644))
	}
	write("small.go", exactGoFile(outlineLines))
	write("boundary.go", exactGoFile(301))

	t.Run("a file under the threshold returns the window", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"path": "small.go", "outline": true})
		assert.True(t, strings.HasPrefix(out,
			fmt.Sprintf("[no outline: %d lines is under the %d line outline threshold]\n1\tpackage fixture\n",
				outlineLines, outlineLines)), out)
		assert.StrContains(t, out, "3\tfunc Only() {}")
	})
	t.Run("the first line over the threshold outlines", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"path": "boundary.go", "outline": true})
		assert.True(t, strings.HasPrefix(out, "[outline of boundary.go: 301 lines, 1 declarations"), out)
		assert.StrContains(t, out, "3: func Only() {}")
	})
	t.Run("a file type no extractor covers returns the window", func(t *testing.T) {
		var sb strings.Builder
		for i := 1; i <= 400; i++ {
			fmt.Fprintf(&sb, "line %d\n", i)
		}
		write("notes.md", sb.String())
		out := execTool(t, tool, map[string]any{"path": "notes.md", "outline": true})
		assert.True(t, strings.HasPrefix(out, `[no outline: no declaration extractor for ".md"; supported: `), out)
		assert.StrContains(t, out, "\n1\tline 1")
	})
	t.Run("a binary source file returns the window's own report", func(t *testing.T) {
		write("blob.go", strings.Repeat("\x00\x01\x02", 4000))
		out := execTool(t, tool, map[string]any{"path": "blob.go", "outline": true})
		assert.True(t, strings.HasPrefix(out, "[binary file: blob.go, 12000 bytes"), out)
	})
	t.Run("a source file that does not parse returns the window", func(t *testing.T) {
		write("broken.go", "package broken\n\nfunc Broken( {\n"+strings.Repeat("// padding\n", outlineLines))
		out := execTool(t, tool, map[string]any{"path": "broken.go", "outline": true})
		assert.True(t, strings.HasPrefix(out, "[no outline: "), out)
		assert.StrContains(t, out, "1\tpackage broken")
	})
	t.Run("a file over the outline cap returns the window", func(t *testing.T) {
		write("huge.go", exactGoFile(outlineLines)+strings.Repeat("// padding that grows the file\n", 40000))
		info, err := os.Stat(filepath.Join(ws, "huge.go"))
		assert.Require(t, assert.NoErr(t, err))
		out := execTool(t, tool, map[string]any{"path": "huge.go", "outline": true})
		assert.True(t, strings.HasPrefix(out,
			fmt.Sprintf("[no outline: %d bytes is over the %d byte outline cap]\n", info.Size(), maxOutlineBytes)), out)
	})
	t.Run("a line span wins over the outline", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"paths": []any{"boundary.go:1-2"}, "outline": true})
		assert.Eq(t, "1\tpackage fixture\n2\t", out)
	})
}

// TestFileReadOutlineConfig is a configuration file's outline: file_read's
// outline=true and file_symbols share the extraction layer, so a long YAML file
// answers with one line per key, and the cap note tells the model where to read
// on.
func TestFileReadOutlineConfig(t *testing.T) {
	pinBuiltinExtractors(t)
	ws := t.TempDir()
	var sb strings.Builder
	for i := 0; i < maxConfigKeys+20; i++ {
		fmt.Fprintf(&sb, "key%03d: %d\n", i, i)
	}
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "big.yaml"), []byte(sb.String()), 0o644))
	tool := &FileReadTool{workspace: ws}

	out := execTool(t, tool, map[string]any{"path": "big.yaml", "outline": true})
	assert.StrContains(t, out, fmt.Sprintf("[outline of big.yaml: %d lines, %d declarations",
		maxConfigKeys+20, maxConfigKeys))
	assert.StrContains(t, out, "1: key000: 0")
	assert.StrContains(t, out, fmt.Sprintf("%d: key%03d: %d", maxConfigKeys, maxConfigKeys-1, maxConfigKeys-1))
	// The keys past the cap are the omitted span, and the note says where to look.
	assert.StrContains(t, out, fmt.Sprintf("[omitted %d-%d: 20 lines]", maxConfigKeys+1, maxConfigKeys+20))
	assert.StrContains(t, out, fmt.Sprintf("[yaml: showing the first %d keys; for the rest use file_grep]", maxConfigKeys))
	assert.NotContains(t, out, "[approx") // the parser read the file
}

// TestFileReadOutlineNonGo is the outline of a language that has only the
// declaration-line recogniser: it must be marked approx.
func TestFileReadOutlineNonGo(t *testing.T) {
	pinBuiltinExtractors(t)
	ws := t.TempDir()
	content, lines := pythonOutlineFixture()
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "loader.py"), []byte(content), 0o644))
	tool := &FileReadTool{workspace: ws}

	out := execTool(t, tool, map[string]any{"path": "loader.py", "outline": true})
	assert.StrContains(t, out, fmt.Sprintf("[outline of loader.py: %d lines, 3 declarations",
		strings.Count(content, "\n")))
	for name, want := range map[string]string{
		"helper": "def helper(value):",
		"Loader": "class Loader:",
		"read":   "def read(self):",
	} {
		assert.StrContains(t, out, fmt.Sprintf("%d: %s", lines[name], want))
	}
	assert.StrContains(t, out, "[approx: the declarations come from declaration-line detection, not a parser; "+
		"confirm a name with file_grep]")
}

// TestFileReadRanges is the line-span suffix of a paths entry: several spans in
// one call, mixed with plain entries, and the errors for a span the model got
// wrong.
func TestFileReadRanges(t *testing.T) {
	ws := t.TempDir()
	files := map[string]string{"a.txt": "one\ntwo\nthree\nfour\nfive\n", "b.txt": "bee\n"}
	for name, text := range files {
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, name), []byte(text), 0o644))
	}
	tool := &FileReadTool{workspace: ws}
	run := func(args map[string]any) (string, error) {
		t.Helper()
		res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Arguments: args})
		if err != nil {
			return "", err
		}
		return res.Content, nil
	}

	t.Run("several spans of one file in one call", func(t *testing.T) {
		assert.Eq(t, "2\ttwo\n3\tthree\n[skipped 4-4: not in the requested ranges]\n5\tfive",
			execTool(t, tool, map[string]any{"path": "a.txt:2-3,5-5"}))
	})
	t.Run("a span entry keeps the plain form and mixes with whole files", func(t *testing.T) {
		assert.Eq(t, "2\ttwo", execTool(t, tool, map[string]any{"paths": []any{"a.txt:2-2"}}))
		assert.Eq(t, "==> a.txt:4-5 <==\n4\tfour\n5\tfive\n\n"+
			"==> b.txt <==\n[offset 2 is past the end: the file has 1 lines]",
			execTool(t, tool, map[string]any{"paths": []any{"a.txt:4-5", "b.txt"}, "offset": 2, "limit": 1}))
	})
	t.Run("a span past the end is reported in place", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"path": "a.txt:1-2,9-12"})
		assert.Eq(t, "1\tone\n2\ttwo\n[span 9-12: offset 9 is past the end: the file has 5 lines]", out)
	})
	t.Run("an entry with no line at all is an error", func(t *testing.T) {
		_, err := run(map[string]any{"path": "a.txt:9-12"})
		assert.ErrMsgContains(t, err, "file_read: a.txt:9-12: no lines read: span 9-12: offset 9 is past the end: "+
			"the file has 5 lines")
	})
	t.Run("a malformed span is an error, not a short read", func(t *testing.T) {
		for arg := range map[string]bool{
			"a.txt:5-3": true, "a.txt:0-2": true, "a.txt:3-": true, "a.txt:1-2,": true,
			"a.txt:1-2,x": true, "a.txt:1-2-3": true, "a.txt:1-2;3-4": true,
		} {
			_, err := run(map[string]any{"paths": []any{arg}})
			assert.ErrMsgContains(t, err, "invalid line span")
			assert.ErrMsgContains(t, err, fmt.Sprintf("in %q", arg))
		}
	})
	t.Run("a colon that is not a span stays part of the path", func(t *testing.T) {
		_, err := run(map[string]any{"path": "a.txt:v1"})
		assert.ErrMsgContains(t, err, "a.txt:v1")
		assert.NotContains(t, err.Error(), "invalid line span")
	})
	t.Run("ReadPathList hands the real file to its other users", func(t *testing.T) {
		list, err := ReadPathList(map[string]any{"paths": []any{"a.txt:2-3", "b.txt"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"a.txt", "b.txt"}, list)
	})
	t.Run("the old calls keep their exact output", func(t *testing.T) {
		assert.Eq(t, "1\tone\n2\ttwo\n3\tthree\n4\tfour\n5\tfive", execTool(t, tool, map[string]any{"path": "a.txt"}))
		assert.Eq(t, "2\ttwo\n[truncated at limit 1 lines; the file has 5 lines; "+
			"read a span as a.txt:start-end; continue with offset=3]",
			execTool(t, tool, map[string]any{"path": "a.txt", "offset": 2, "limit": 1}))
		assert.Eq(t, "==> a.txt <==\n1\tone\n2\ttwo\n3\tthree\n4\tfour\n5\tfive\n\n==> b.txt <==\n1\tbee",
			execTool(t, tool, map[string]any{"paths": []any{"a.txt", "b.txt"}}))
	})
}

// TestFileReadRangesAbsolutePath is the Windows drive case of the suffix: an
// absolute path under a read-only root holds a colon of its own, so only a
// trailing ":start-end" may be read as a span.
func TestFileReadRangesAbsolutePath(t *testing.T) {
	ws, root := t.TempDir(), t.TempDir()
	abs := filepath.Join(root, "dep.txt")
	assert.NoErr(t, os.WriteFile(abs, []byte("one\ntwo\nthree\n"), 0o644))
	tool := &FileReadTool{workspace: ws, readRoots: []string{root}}

	t.Run("an absolute path with a span", func(t *testing.T) {
		assert.Eq(t, "2\ttwo\n3\tthree", execTool(t, tool, map[string]any{"path": abs + ":2-3"}))
	})
	t.Run("an absolute path without a span is unchanged", func(t *testing.T) {
		assert.Eq(t, "1\tone\n2\ttwo\n3\tthree", execTool(t, tool, map[string]any{"path": abs}))
	})
}

// TestSplitReadItem covers the suffix rule itself, including the paths whose
// colon is not a span.
func TestSplitReadItem(t *testing.T) {
	t.Run("no suffix", func(t *testing.T) {
		for _, arg := range []string{"a.go", "dir/a.go", `D:\src\a.go`, "C:/src/a.go", "a.go:", "a.go:v1"} {
			item, err := splitReadItem(arg)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, arg, item.path)
			assert.Eq(t, 0, len(item.ranges))
		}
	})
	t.Run("spans are split off", func(t *testing.T) {
		item, err := splitReadItem(`D:\src\a.go:10-20,30-40`)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, `D:\src\a.go`, item.path)
		assert.Eq(t, "10-20, 30-40", rangesText(item.ranges))
		assert.Eq(t, `D:\src\a.go:10-20,30-40`, item.arg)
	})
	t.Run("malformed spans", func(t *testing.T) {
		for _, arg := range []string{"a.go:2-1", "a.go:1", "a.go:1--2"} {
			_, err := splitReadItem(arg)
			assert.ErrMsgContains(t, err, "invalid line span")
			assert.ErrMsgContains(t, err, fmt.Sprintf("in %q", arg))
		}
	})
}
