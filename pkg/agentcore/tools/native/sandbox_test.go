package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

func call(args map[string]any) api.ToolCall { return api.ToolCall{CallID: "c", Arguments: args} }

func TestFileTools_WorkspaceSymlinkToReadRoot(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "workspace")
	root := filepath.Join(base, "skills")
	assert.NoErr(t, os.MkdirAll(ws, 0o755))
	assert.NoErr(t, os.MkdirAll(root, 0o755))
	file := filepath.Join(root, "skills", "plc-hmi-develop", "SKILL.md")
	assert.NoErr(t, os.MkdirAll(filepath.Dir(file), 0o755))
	assert.NoErr(t, os.WriteFile(file, []byte("plaintext hmicli cscli\n"), 0o644))
	if err := os.Symlink(root, filepath.Join(ws, ".agents")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	ctx := context.Background()
	read := &FileReadTool{workspace: ws, readRoots: []string{root}}
	grep := &FileGrepTool{workspace: ws, readRoots: []string{root}}
	res, err := read.Execute(ctx, call(map[string]any{"path": ".agents/skills/plc-hmi-develop/SKILL.md"}))
	assert.NoErr(t, err)
	assert.StrContains(t, res.Content, "plaintext hmicli cscli")
	res, err = grep.Execute(ctx, call(map[string]any{
		"path":    ".agents/skills/plc-hmi-develop/SKILL.md",
		"pattern": "plaintext|hmicli|cscli",
	}))
	assert.NoErr(t, err)
	assert.StrContains(t, res.Content, "SKILL.md:1:")
}
func TestResolveSandboxedPath(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ws")

	t.Run("relative and in-workspace absolute args resolve under workspace", func(t *testing.T) {
		got, err := ResolveSandboxedPath(ws, "a/b.txt")
		assert.NoErr(t, err)
		assert.Eq(t, filepath.Join(ws, "a", "b.txt"), got)

		got, err = ResolveSandboxedPath(ws, filepath.Join(ws, "a", "..", "a", "b.txt"))
		assert.NoErr(t, err)
		assert.Eq(t, filepath.Join(ws, "a", "b.txt"), got)

		got, err = ResolveSandboxedPath(ws, ws)
		assert.NoErr(t, err)
		assert.Eq(t, ws, got)

		got, err = ResolveSandboxedPath(ws, ".")
		assert.NoErr(t, err)
		assert.Eq(t, ws, got)
	})

	t.Run("absolute outside workspace is rejected", func(t *testing.T) {
		for _, p := range []string{ws + "2", filepath.Join(ws+"2", "x"), filepath.Dir(ws), filepath.Join(ws, "..", "x")} {
			_, err := ResolveSandboxedPath(ws, p)
			assert.Err(t, err)
			assert.StrContains(t, err.Error(), "path escape")
		}
	})

	t.Run("sibling dir sharing prefix is rejected", func(t *testing.T) {
		_, err := ResolveSandboxedPath(ws, "../ws2/x")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "path escape")
		_, err = ResolveSandboxedPath(ws, "../x")
		assert.Err(t, err)
	})
}

func TestFileTools_SymlinkSandbox(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	outside := filepath.Join(base, "outside")
	assert.NoErr(t, os.MkdirAll(filepath.Join(ws, "real"), 0o755))
	assert.NoErr(t, os.MkdirAll(outside, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "real", "ok.txt"), []byte("ok"), 0o644))

	if err := os.Symlink(outside, filepath.Join(ws, "esc")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	assert.NoErr(t, os.Symlink("real", filepath.Join(ws, "inner")))

	rd := &FileReadTool{workspace: ws}
	wr := &FileWriteTool{workspace: ws}
	ls := &FileListTool{workspace: ws}
	ctx := context.Background()

	t.Run("symlink to outside is denied", func(t *testing.T) {
		_, err := rd.Execute(ctx, call(map[string]any{"path": "esc/secret.txt"}))
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "path escape")

		_, err = wr.Execute(ctx, call(map[string]any{"path": "esc/new.txt", "content": "x"}))
		assert.Err(t, err)
		_, statErr := os.Stat(filepath.Join(outside, "new.txt"))
		assert.True(t, os.IsNotExist(statErr))

		_, err = wr.Execute(ctx, call(map[string]any{"path": "esc/sub/new.txt", "content": "x"}))
		assert.Err(t, err)

		_, err = ls.Execute(ctx, call(map[string]any{"dir": "esc"}))
		assert.Err(t, err)
	})

	t.Run("symlink staying inside works", func(t *testing.T) {
		res, err := rd.Execute(ctx, call(map[string]any{"path": "inner/ok.txt"}))
		assert.NoErr(t, err)
		assert.Eq(t, "1\tok", res.Content)

		_, err = wr.Execute(ctx, call(map[string]any{"path": "inner/w.txt", "content": "w"}))
		assert.NoErr(t, err)
		b, err := os.ReadFile(filepath.Join(ws, "real", "w.txt"))
		assert.NoErr(t, err)
		assert.Eq(t, "w", string(b))

		res, err = ls.Execute(ctx, call(map[string]any{"dir": "inner"}))
		assert.NoErr(t, err)
		assert.StrContains(t, res.Content, "ok.txt")
	})

	t.Run("list workspace root and sibling prefix", func(t *testing.T) {
		res, err := ls.Execute(ctx, call(nil))
		assert.NoErr(t, err)
		assert.StrContains(t, res.Content, "real/")
		_, err = ls.Execute(ctx, call(map[string]any{"dir": "../ws2"}))
		assert.Err(t, err)
	})
}
