package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// searchWorkspace builds a small tree for the grep/find tests.
func searchWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	files := map[string]string{
		"main.go":                 "package main\n\nfunc main() { Hello() }\n",
		"internal/a/a.go":         "package a\n\n// Hello says hi\nfunc Hello() {}\n",
		"internal/a/a_test.go":    "package a\n\nfunc TestHello(t *testing.T) {}\n",
		"docs/中文.md":              "# 标题\n你好 Hello 世界\n",
		"node_modules/x/index.js": "Hello()\n",
		"build/out.go":            "func Hello() {}\n",
		"logs/app.log":            "Hello log\n",
		".gitignore":              "# comment\nbuild/\n*.log\n!keep.log\n",
	}
	for p, c := range files {
		full := filepath.Join(ws, filepath.FromSlash(p))
		assert.NoErr(t, os.MkdirAll(filepath.Dir(full), 0o755))
		assert.NoErr(t, os.WriteFile(full, []byte(c), 0o644))
	}
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "bin.dat"), []byte("Hello\x00\x01binary"), 0o644))
	return ws
}

// withoutIgnoreNote drops the trailing "[skipped ...]" line of a search result.
func withoutIgnoreNote(out string) string {
	if i := strings.LastIndex(out, "\n[skipped "); i >= 0 {
		return out[:i]
	}
	return out
}

const searchIgnoreNote = "[skipped 2 ignored dir(s): build/, node_modules/; 1 ignored file(s) " +
	"(.gitignore and node_modules/vendor/dist); pass no_ignore: true to include them]"

func TestGrepFiles(t *testing.T) {
	ws := searchWorkspace(t)
	tool := &FileGrepTool{workspace: ws}

	t.Run("content with ignores and binary skipped", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "Hello"})
		assert.StrContains(t, out, "internal/a/a.go:3: // Hello says hi")
		assert.StrContains(t, out, "main.go:3: func main() { Hello() }")
		assert.StrContains(t, out, "docs/中文.md:2: 你好 Hello 世界")
		for _, skipped := range []string{"node_modules", "build/", "app.log", "bin.dat"} {
			assert.NotContains(t, withoutIgnoreNote(out), skipped)
		}
		assert.True(t, strings.HasSuffix(out, "\n"+searchIgnoreNote), out)
	})
	t.Run("no_ignore searches ignored paths", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "Hello", "no_ignore": true})
		assert.StrContains(t, out, "node_modules/x/index.js:1: Hello()")
		assert.StrContains(t, out, "build/out.go:1:")
		assert.StrContains(t, out, "logs/app.log:1:")
		assert.NotContains(t, out, "[skipped")
	})
	t.Run("files output, glob and ignore_case", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "hello", "ignore_case": true, "glob": "internal/**/*.go", "output": "files"})
		assert.Eq(t, "internal/a/a.go\ninternal/a/a_test.go", withoutIgnoreNote(out))
	})
	t.Run("path narrows the search", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "Hello", "path": "docs"})
		assert.Eq(t, "docs/中文.md:2: 你好 Hello 世界", out)
	})
	t.Run("a single file path", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "func", "path": "main.go"})
		assert.Eq(t, "main.go:3: func main() { Hello() }", out)
	})
	t.Run("max_results stops early", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "Hello", "max_results": 1})
		assert.StrContains(t, out, "[stopped at 1 results")
		assert.Eq(t, 2, len(strings.Split(withoutIgnoreNote(out), "\n")))
	})
	t.Run("no matches", func(t *testing.T) {
		assert.Eq(t, "no matches\n"+searchIgnoreNote, execTool(t, tool, map[string]any{"pattern": "zzz_nothing"}))
	})
	t.Run("timeout returns a marked partial result", func(t *testing.T) {
		old := searchTimeout
		// Already expired: a 1ns timer may not fire before a fast walk ends
		// on Windows, where the clock is coarse.
		searchTimeout = -time.Second
		t.Cleanup(func() { searchTimeout = old })
		assert.StrContains(t, execTool(t, tool, map[string]any{"pattern": "Hello"}), "已超时")
	})
	t.Run("symlink out of the workspace is not read", func(t *testing.T) {
		outside := t.TempDir()
		assert.NoErr(t, os.WriteFile(filepath.Join(outside, "secret.go"), []byte("Hello secret\n"), 0o644))
		if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
			t.Skip("symlink not supported:", err)
		}
		assert.NoErr(t, os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(ws, "flink.go")))
		out := execTool(t, tool, map[string]any{"pattern": "secret"})
		assert.Eq(t, "no matches", withoutIgnoreNote(out))
		_, err := tool.Execute(t.Context(), callWith(map[string]any{"pattern": "secret", "path": "link"}))
		assert.Err(t, err)
	})
	t.Run("invalid regexp", func(t *testing.T) {
		_, err := tool.Execute(t.Context(), callWith(map[string]any{"pattern": "("}))
		assert.ErrMsgContains(t, err, "invalid pattern")
	})
}

func TestGrepOptions(t *testing.T) {
	ws := searchWorkspace(t)
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "context.txt"), []byte(
		"one\nhit two\nthree\nhit four\nfive\nsix\nhit seven\neight\n"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "literal.txt"), []byte("a+b\naab\n"), 0o644))
	tool := &FileGrepTool{workspace: ws}

	t.Run("exclude filters content and files output", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "Hello", "exclude": "func"})
		assert.NotContains(t, out, "main.go")
		assert.StrContains(t, out, "internal/a/a.go:3: // Hello says hi")
		files := execTool(t, tool, map[string]any{"pattern": "Hello", "exclude": "func", "output": "files", "no_ignore": true})
		assert.StrContains(t, files, "internal/a/a.go")
		assert.NotContains(t, files, "main.go")
	})
	t.Run("glob list includes and excludes, including only exclusions", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "Hello", "glob": []any{"*.go", "!*_test.go"}, "output": "files", "no_ignore": true})
		assert.StrContains(t, out, "main.go")
		assert.StrContains(t, out, "build/out.go")
		assert.NotContains(t, out, "a_test.go")
		allButTests := execTool(t, tool, map[string]any{"pattern": "Hello", "glob": []any{"!*_test.go"}, "output": "files", "no_ignore": true})
		assert.StrContains(t, allButTests, "docs/中文.md")
		assert.NotContains(t, allButTests, "a_test.go")
	})
	t.Run("context merges adjacent blocks and does not consume max results", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "hit", "path": "context.txt", "context": 1, "max_results": 1})
		assert.StrContains(t, out, "context.txt-1- one")
		assert.StrContains(t, out, "context.txt:2: hit two")
		assert.StrContains(t, out, "context.txt-3- three")
		assert.StrContains(t, out, "[stopped at 1 results")
		assert.NotContains(t, out, "context.txt:4:")
		out = execTool(t, tool, map[string]any{"pattern": "seven", "path": "context.txt", "context": 2})
		assert.StrContains(t, out, "context.txt-5- five")
		assert.StrContains(t, out, "context.txt:7: hit seven")
	})
	t.Run("literal and exclude combine", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "a+b", "literal": true, "path": "literal.txt", "exclude": "aab"})
		assert.Eq(t, "literal.txt:1: a+b", out)
		out = execTool(t, tool, map[string]any{"pattern": "a+b", "literal": true, "path": "literal.txt", "output": "files"})
		assert.Eq(t, "literal.txt", out)
	})
	t.Run("regexp2 lookbehind and backreference fallback", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": `(?<=hit )two`, "path": "context.txt"})
		assert.StrContains(t, out, "context.txt:2: hit two")
		assert.StrContains(t, out, "used regexp2")
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, "repeat.txt"), []byte("go go\nno\n"), 0o644))
		out = execTool(t, tool, map[string]any{"pattern": `(\w+) \1`, "path": "repeat.txt"})
		assert.StrContains(t, out, "repeat.txt:1: go go")
		assert.StrContains(t, out, "used regexp2")
	})
	t.Run("regexp2 timeout is reported", func(t *testing.T) {
		long := strings.Repeat("a", 10000) + "x\n"
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, "catastrophic.txt"), []byte(long), 0o644))
		started := time.Now()
		out := execTool(t, tool, map[string]any{"pattern": `(?=(a+)+$)a`, "path": "catastrophic.txt"})
		assert.True(t, time.Since(started) < time.Second, time.Since(started).String())
		assert.StrContains(t, out, "regexp2 match timed out")
	})
	t.Run("invalid syntax gives executable advice without fallback", func(t *testing.T) {
		_, err := tool.Execute(t.Context(), callWith(map[string]any{"pattern": "("}))
		assert.ErrMsgContains(t, err, "literal: true")
		_, err = tool.Execute(t.Context(), callWith(map[string]any{"pattern": `(?<bad`}))
		assert.ErrMsgContains(t, err, "RE2 syntax")
	})
}

func TestFindFiles(t *testing.T) {
	ws := searchWorkspace(t)
	tool := &FileFindTool{workspace: ws}
	old := time.Now().Add(-time.Hour)
	assert.NoErr(t, os.Chtimes(filepath.Join(ws, "main.go"), old, old))

	t.Run("base-name pattern at any depth, newest first", func(t *testing.T) {
		out := execTool(t, tool, map[string]any{"pattern": "*.go"})
		lines := strings.Split(withoutIgnoreNote(out), "\n")
		assert.Eq(t, 3, len(lines))
		assert.Eq(t, "main.go", lines[2]) // oldest last; build/ is ignored
	})
	t.Run("double star", func(t *testing.T) {
		assert.Eq(t, "internal/a/a_test.go", withoutIgnoreNote(execTool(t, tool, map[string]any{"pattern": "**/*_test.go"})))
	})
	t.Run("limit", func(t *testing.T) {
		assert.StrContains(t, execTool(t, tool, map[string]any{"pattern": "*.go", "max_results": 1}), "[showing 1 of 3")
	})
	t.Run("nothing found", func(t *testing.T) {
		assert.Eq(t, "no files found\n"+searchIgnoreNote, execTool(t, tool, map[string]any{"pattern": "*.rs"}))
	})
}

func TestWalkGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, rel string
		want         bool
	}{
		{"*.go", "a/b/c.go", true},
		{"**/*.go", "c.go", true},
		{"internal/**/*.go", "internal/x/y/z.go", true},
		{"internal/**/*.go", "cmd/z.go", false},
		{"cmd/*/main.go", "cmd/suga/main.go", true},
		{"cmd/*/main.go", "cmd/a/b/main.go", false},
	} {
		assert.Eq(t, tc.want, globMatch(tc.pattern, tc.rel), tc.pattern+" "+tc.rel)
	}
}

func callWith(args map[string]any) api.ToolCall {
	return api.ToolCall{CallID: "c", Arguments: args}
}
