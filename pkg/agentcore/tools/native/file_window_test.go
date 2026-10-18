package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

func execTool(t *testing.T, tool interface {
	Execute(context.Context, api.ToolCall) (*api.ToolResult, error)
}, args map[string]any) string {
	t.Helper()
	res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Arguments: args})
	assert.Require(t, assert.NoErr(t, err))
	return res.Content
}

var lineNoPrefix = regexp.MustCompile(`(?m)^\d+\t`)
var nextOffset = regexp.MustCompile(`continue with offset=(\d+)\]$`)

func TestFileReadWindow(t *testing.T) {
	ws := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 250; i++ {
		fmt.Fprintf(&sb, "第%d行 line %d\n", i, i)
	}
	original := sb.String()
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "big.txt"), []byte(original), 0o644))
	tool := &FileReadTool{workspace: ws, maxOut: 500}

	t.Run("reading on with offset rebuilds the file", func(t *testing.T) {
		var got strings.Builder
		offset, calls := 1, 0
		for {
			calls++
			assert.Require(t, assert.True(t, calls < 100))
			out := execTool(t, tool, map[string]any{"path": "big.txt", "offset": offset, "limit": 40})
			m := nextOffset.FindStringSubmatch(out)
			body := out
			if m != nil {
				body = out[:strings.LastIndex(out, "[truncated")]
			}
			got.WriteString(strings.TrimRight(lineNoPrefix.ReplaceAllString(body, ""), "\n") + "\n")
			if m == nil {
				break
			}
			fmt.Sscan(m[1], &offset)
		}
		assert.Eq(t, original, got.String())
		assert.True(t, calls > 250/40+1, "calls %d", calls) // the byte cap cut windows short
	})
	t.Run("first line carries its number", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"path": "big.txt", "offset": "3", "limit": 1})
		assert.True(t, strings.HasPrefix(out, "3\t第3行 line 3\n[truncated at limit 1 lines; the file has 250 lines; "+
			"read a span as big.txt:start-end; continue with offset=4]"), out)
	})
	t.Run("offset past the end", func(t *testing.T) {
		assert.StrContains(t, execTool(t, tool, map[string]any{"path": "big.txt", "offset": 999}), "has 250 lines")
	})
	t.Run("binary file is reported", func(t *testing.T) {
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, "a.bin"), []byte{0x89, 'P', 'N', 'G', 0, 1, 2}, 0o644))
		out := execTool(t, tool, map[string]any{"path": "a.bin"})
		assert.StrContains(t, out, "[binary file: a.bin, 7 bytes")
	})
	t.Run("CRLF lines are shown without CR", func(t *testing.T) {
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, "crlf.txt"), []byte("a\r\nb\r\n"), 0o644))
		assert.Eq(t, "1\ta\n2\tb", execTool(t, tool, map[string]any{"path": "crlf.txt"}))
	})
	t.Run("one huge line returns a part", func(t *testing.T) {
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, "long.txt"), []byte(strings.Repeat("中", 1000)+"\nnext\n"), 0o644))
		out := execTool(t, tool, map[string]any{"path": "long.txt"})
		assert.StrContains(t, out, "continue with offset=2]")
	})
}

func TestFileListDepth(t *testing.T) {
	ws := t.TempDir()
	for _, p := range []string{"a/b/c/d.txt", "a/x.go", "node_modules/m/i.js", "top.md"} {
		assert.NoErr(t, os.MkdirAll(filepath.Join(ws, filepath.Dir(p)), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, p), []byte("x"), 0o644))
	}
	tool := &FileListTool{workspace: ws}

	t.Run("default is one level", func(t *testing.T) {
		assert.Eq(t, "a/\nnode_modules/\ntop.md", execTool(t, tool, map[string]any{}))
	})
	t.Run("depth recurses and skips ignored dirs", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"depth": 5})
		assert.Eq(t, "a/\na/b/\na/b/c/\na/b/c/d.txt\na/x.go\nnode_modules/\ntop.md\n"+
			"[skipped 1 ignored dir(s): node_modules/ (.gitignore and node_modules/vendor/dist); pass no_ignore: true to include them]", out)
	})
	t.Run("no_ignore enters ignored dirs", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"depth": 5, "no_ignore": true})
		assert.StrContains(t, out, "node_modules/m/i.js")
		assert.NotContains(t, out, "[skipped")
	})
	t.Run(".gitignore'd dirs are listed but not entered", func(t *testing.T) {
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("/a/b/\n"), 0o644))
		t.Cleanup(func() { _ = os.Remove(filepath.Join(ws, ".gitignore")) })
		out := execTool(t, tool, map[string]any{"depth": 5})
		assert.StrContains(t, out, "a/b/\n")
		assert.NotContains(t, out, "a/b/c/")
		assert.StrContains(t, out, "2 ignored dir(s): a/b/, node_modules/")
	})
	t.Run("max_entries truncates", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"depth": 5, "max_entries": 2})
		assert.Eq(t, "a/\na/b/\n[truncated at 2 entries; narrow dir or depth]", out)
	})
	t.Run("symlinks are marked and not followed", func(t *testing.T) {
		outside := t.TempDir()
		assert.NoErr(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o644))
		if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
			t.Skip("symlink not supported:", err)
		}
		out := execTool(t, tool, map[string]any{"depth": 3})
		assert.StrContains(t, out, "link@")
		assert.NotContains(t, out, "secret.txt")
	})
}

func TestFileReadPaths(t *testing.T) {
	ws := t.TempDir()
	for name, text := range map[string]string{"a.txt": "one\ntwo\n", "b.txt": "three\n", "c.txt": "four\nfive\nsix\n"} {
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, name), []byte(text), 0o644))
	}
	tool := &FileReadTool{workspace: ws}

	t.Run("several files in one call", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"paths": []any{"a.txt", "b.txt"}})
		assert.Eq(t, "==> a.txt <==\n1\tone\n2\ttwo\n\n==> b.txt <==\n1\tthree", out)
	})
	t.Run("path comes first and duplicates are dropped", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"path": "b.txt", "paths": []any{"a.txt", "b.txt"}})
		assert.True(t, strings.HasPrefix(out, "==> b.txt <==\n"), out)
		assert.Eq(t, 2, strings.Count(out, "==> "))
	})
	t.Run("offset and limit apply to each file", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"paths": []any{"a.txt", "c.txt"}, "offset": 2, "limit": 1})
		assert.StrContains(t, out, "==> a.txt <==\n2\ttwo\n")
		assert.StrContains(t, out, "==> c.txt <==\n2\tfive\n[truncated at limit 1 lines; the file has 3 lines; "+
			"read a span as c.txt:start-end; continue with offset=3]")
	})
	t.Run("a failing path is reported in place", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"paths": []any{"missing.txt", "../x.txt", "b.txt"}})
		assert.StrContains(t, out, "==> missing.txt <==\n[error: file_read: ")
		assert.StrContains(t, out, "==> ../x.txt <==\n[error: path escape")
		assert.StrContains(t, out, "==> b.txt <==\n1\tthree")
	})
	t.Run("paths as a JSON string or a plain string", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"paths": `["a.txt","b.txt"]`})
		assert.Eq(t, 2, strings.Count(out, "==> "))
		out = execTool(t, tool, map[string]any{"paths": "b.txt"})
		assert.Eq(t, "1\tthree", out) // one path reads like path
	})
	t.Run("one path keeps the plain form and errors", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"paths": []any{"a.txt"}})
		assert.Eq(t, "1\tone\n2\ttwo", out)
		_, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Arguments: map[string]any{"path": "missing.txt"}})
		assert.Err(t, err)
	})
	t.Run("invalid arguments", func(t *testing.T) {
		many := make([]any, maxReadPaths+1)
		for i := range many {
			many[i] = fmt.Sprintf("f%d.txt", i)
		}
		for args, want := range map[*map[string]any]string{
			{}:                         `"path" arg is required`,
			{"paths": []any{}}:         `"path" arg is required`,
			{"paths": []any{"a", 1.0}}: "paths[1] must be a string",
			{"paths": `["a",`}:         "paths must be an array of strings",
			{"paths": 3.0}:             "paths must be an array of strings",
			{"paths": many}:            fmt.Sprintf("at most %d paths per call", maxReadPaths),
		} {
			_, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Arguments: *args})
			assert.ErrMsgContains(t, err, want)
		}
	})
}

func TestFileReadPageCap(t *testing.T) {
	ws := t.TempDir()
	line := strings.Repeat("x", 99) + "\n" // 100 bytes
	big := strings.Repeat(line, 300)       // 30000 bytes: one default page
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "f.txt"} {
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, name), []byte(big), 0o644))
	}
	t.Run("a 30KB file fits in one default page", func(t *testing.T) {
		out := execTool(t, &FileReadTool{workspace: ws}, map[string]any{"path": "a.txt"})
		assert.NotContains(t, out, "[truncated")
		assert.StrContains(t, out, "300\t")
	})
	t.Run("a smaller configured page pages the same file", func(t *testing.T) {
		out := execTool(t, &FileReadTool{workspace: ws, maxOut: 8000}, map[string]any{"path": "a.txt"})
		assert.StrContains(t, out, "[truncated at output cap 8000 bytes")
	})
	t.Run("paths stop after four pages in all", func(t *testing.T) {
		out := execTool(t, &FileReadTool{workspace: ws, maxOut: 10000}, map[string]any{
			"paths": []any{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "f.txt"}})
		assert.Eq(t, 4, strings.Count(out, "==> "))
		assert.StrContains(t, out, "[output cap reached; not read: e.txt, f.txt")
	})
}
