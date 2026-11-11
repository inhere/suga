package native

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// denyTestPatterns is the shape of the built-in list without the "~" entries
// (those need an expanded home): secrets by name, with the usual examples
// excepted, and one picture to cover the image tools.
var denyTestPatterns = []string{"**/.env", "**/.env.*", "!**/.env.example", "**/secret.png"}

func mustDenyList(t *testing.T, patterns ...string) ReadDenyList {
	t.Helper()
	list, err := NewReadDenyList(patterns)
	assert.Require(t, assert.NoErr(t, err))
	return list
}

// denyTool returns the named catalog tool built over env.
func denyTool(t *testing.T, env Env, name string) api.Tool {
	t.Helper()
	for _, tool := range BuildTools(env, true) {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %s is not in the catalog", name)
	return nil
}

func runDeny(tool api.Tool, args map[string]any) (*api.ToolResult, error) {
	return tool.Execute(context.Background(), api.ToolCall{CallID: "c1", Name: tool.Name(), Arguments: args})
}

func TestReadDenyListMatching(t *testing.T) {
	sep := string(filepath.Separator)
	abs := func(parts ...string) string {
		root := sep
		if runtime.GOOS == "windows" {
			root = `C:\`
		}
		return filepath.Join(append([]string{root}, parts...)...)
	}
	t.Run("**/ prefix matches a file name in any directory", func(t *testing.T) {
		list := mustDenyList(t, "**/.env", "**/.env.*", "!**/.env.example")
		assert.True(t, list.Denied(abs("work", "app", ".env")))
		assert.True(t, list.Denied(abs(".env")))
		assert.True(t, list.Denied(abs("work", ".env.local")))
		assert.False(t, list.Denied(abs("work", ".envrc")))
		assert.False(t, list.Denied(abs("work", "env.txt")))
	})
	t.Run("a later exception overrides, a later deny overrides the exception", func(t *testing.T) {
		list := mustDenyList(t, "**/.env.*", "!**/.env.example")
		assert.False(t, list.Denied(abs("a", ".env.example")))
		assert.True(t, list.Denied(abs("a", ".env.prod")))
		list = mustDenyList(t, "!**/.env.example", "**/.env.*")
		assert.True(t, list.Denied(abs("a", ".env.example")))
	})
	t.Run("absolute directory glob", func(t *testing.T) {
		list := mustDenyList(t, filepath.ToSlash(abs("home", "u", ".ssh"))+"/**")
		assert.True(t, list.Denied(abs("home", "u", ".ssh", "id_rsa")))
		assert.True(t, list.Denied(abs("home", "u", ".ssh", "keys", "k1")))
		assert.False(t, list.Denied(abs("home", "u", ".sshx", "id_rsa")))
		assert.False(t, list.Denied(abs("home", "other", ".ssh", "id_rsa")))
	})
	t.Run("zero value denies nothing", func(t *testing.T) {
		assert.False(t, ReadDenyList{}.Denied(abs(".env")))
	})
	t.Run("bad glob and empty pattern are errors", func(t *testing.T) {
		_, err := NewReadDenyList([]string{"**/[x"})
		assert.Err(t, err)
		_, err = NewReadDenyList([]string{"!"})
		assert.Err(t, err)
	})
	t.Run("symlink: either end hits", func(t *testing.T) {
		dir := t.TempDir()
		secret := filepath.Join(dir, ".env")
		assert.Require(t, assert.NoErr(t, os.WriteFile(secret, []byte("TOKEN=x"), 0o600)))
		link := filepath.Join(dir, "innocent.txt")
		if err := os.Symlink(secret, link); err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}
		list := mustDenyList(t, "**/.env")
		assert.True(t, list.Denied(link), "link name is fine, its target is not")
		// The other way round: the link is named like a secret, its target is not.
		harmless := filepath.Join(dir, "notes.txt")
		assert.Require(t, assert.NoErr(t, os.WriteFile(harmless, []byte("hi"), 0o644)))
		envLink := filepath.Join(dir, "sub.env")
		assert.Require(t, assert.NoErr(t, os.Symlink(harmless, envLink)))
		assert.True(t, mustDenyList(t, "**/sub.env").Denied(envLink))
	})
	t.Run("windows folds case", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("case folding applies to Windows only")
		}
		list := mustDenyList(t, "**/.env", `C:/Users/Me/.ssh/**`)
		assert.True(t, list.Denied(`c:\users\me\PROJECT\.ENV`))
		assert.True(t, list.Denied(`C:\USERS\ME\.SSH\id_rsa`))
	})
}

func TestReadDenyTools(t *testing.T) {
	ws := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(ws, filepath.FromSlash(rel))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}
	write(".env", "API_TOKEN=sekret-value\n")
	write(".env.example", "API_TOKEN=\n")
	write("cfg/.env", "SEKRET=zzz\n")
	write("app/main.go", "package app\n\n// SekretUse mentions sekret-value\nfunc SekretUse() {}\n")
	write("app/.env.go", "package app\n\nfunc SekretHidden() {}\n") // ".env.*" denies it
	var pic bytes.Buffer
	assert.Require(t, assert.NoErr(t, png.Encode(&pic, solidImage(8, 8, false))))
	write("secret.png", pic.String())
	write("plain.png", pic.String())

	snaps := &recordingSnapshotter{}
	env := Env{Workspace: ws, ReadDeny: mustDenyList(t, denyTestPatterns...), Snapshots: snaps}
	const denied = "被 tools.read_deny 禁止"

	t.Run("file_read refuses, an exception and other files read", func(t *testing.T) {
		tool := denyTool(t, env, "file_read")
		_, err := runDeny(tool, map[string]any{"path": ".env"})
		assert.ErrMsgContains(t, err, denied)
		_, err = runDeny(tool, map[string]any{"path": "cfg/.env"})
		assert.ErrMsgContains(t, err, denied)
		res, err := runDeny(tool, map[string]any{"path": ".env.example"})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "API_TOKEN")
	})
	t.Run("file_read refuses the other argument forms", func(t *testing.T) {
		tool := denyTool(t, env, "file_read")
		for _, args := range []map[string]any{
			{"path": ".env", "outline": true},
			{"paths": []any{".env"}},
			{"path": ".env:1-2"},
		} {
			res, err := runDeny(tool, args)
			if err == nil {
				assert.NotContains(t, res.Content, "sekret-value")
				continue
			}
			assert.NotContains(t, err.Error(), "sekret-value")
		}
	})
	t.Run("file_read picture branch and view_image", func(t *testing.T) {
		for _, name := range []string{"file_read", "view_image"} {
			_, err := runDeny(denyTool(t, env, name), map[string]any{"path": "secret.png"})
			assert.ErrMsgContains(t, err, denied, name)
		}
		res, err := runDeny(denyTool(t, env, "view_image"), map[string]any{"path": "plain.png"})
		assert.Require(t, assert.NoErr(t, err))
		assert.NotEmpty(t, res.Content)
	})
	t.Run("file_symbols single file and directory", func(t *testing.T) {
		tool := denyTool(t, env, "file_symbols")
		_, err := runDeny(tool, map[string]any{"path": "app/.env.go"})
		assert.ErrMsgContains(t, err, denied)
		res, err := runDeny(tool, map[string]any{"path": "app"})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "SekretUse")
		assert.NotContains(t, res.Content, "SekretHidden")
		assert.StrContains(t, res.Content, "tools.read_deny")
	})
	t.Run("file_refs single file and across files with context", func(t *testing.T) {
		tool := denyTool(t, env, "file_refs")
		_, err := runDeny(tool, map[string]any{"name": "SekretHidden", "path": "app/.env.go"})
		assert.ErrMsgContains(t, err, denied)
		res, err := runDeny(tool, map[string]any{"name": "SekretUse", "path": "app", "context": 2})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "SekretUse")
		assert.StrContains(t, res.Content, "tools.read_deny")
		res, err = runDeny(tool, map[string]any{"name": "SekretHidden", "path": "app"})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "no references", "the only match is in a denied file")
	})
	t.Run("refs context never reads a denied file", func(t *testing.T) {
		target := readTarget{base: ws, abs: ws}
		cache := newRefSourceCache(target, env.ReadDeny)
		assert.Empty(t, cache.linesOf(".env"))
		assert.NotEmpty(t, newRefSourceCache(target, ReadDenyList{}).linesOf(".env"))
	})
	t.Run("file_grep skips and counts", func(t *testing.T) {
		res, err := runDeny(denyTool(t, env, "file_grep"), map[string]any{"pattern": "sekret|SEKRET", "ignore_case": true})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "app/main.go")
		assert.NotContains(t, res.Content, ".env:")
		assert.NotContains(t, res.Content, "zzz")
		assert.StrContains(t, res.Content, "已跳过")
		_, err = runDeny(denyTool(t, env, "file_grep"), map[string]any{"pattern": "TOKEN", "path": ".env"})
		assert.ErrMsgContains(t, err, denied)
	})
	t.Run("file_list marks and file_find marks", func(t *testing.T) {
		res, err := runDeny(denyTool(t, env, "file_list"), map[string]any{"depth": 2})
		assert.Require(t, assert.NoErr(t, err))
		lines := strings.Split(res.Content, "\n")
		assert.Contains(t, lines, ".env"+readDenyMark)
		assert.Contains(t, lines, "cfg/.env"+readDenyMark)
		assert.Contains(t, lines, ".env.example", "an exception is listed unmarked")
		assert.Contains(t, lines, "app/")

		res, err = runDeny(denyTool(t, env, "file_find"), map[string]any{"pattern": "**/.env*"})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, ".env"+readDenyMark)
		assert.StrContains(t, res.Content, ".env.example")
		assert.NotContains(t, res.Content, ".env.example"+readDenyMark)
	})
	t.Run("a directory argument still resolves", func(t *testing.T) {
		res, err := runDeny(denyTool(t, env, "file_list"), map[string]any{"dir": "cfg"})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, ".env"+readDenyMark)
	})
	t.Run("file_edit refuses before reading", func(t *testing.T) {
		before, _ := os.ReadFile(filepath.Join(ws, ".env"))
		_, err := runDeny(denyTool(t, env, "file_edit"), map[string]any{"path": ".env", "old_string": "sekret-value", "new_string": "x"})
		assert.ErrMsgContains(t, err, denied)
		assert.NotContains(t, err.Error(), "sekret-value")
		after, _ := os.ReadFile(filepath.Join(ws, ".env"))
		assert.Eq(t, string(before), string(after))
		// A file that is not denied still edits.
		_, err = runDeny(denyTool(t, env, "file_edit"), map[string]any{"path": "app/main.go", "old_string": "SekretUse()", "new_string": "SekretUse2()"})
		assert.NoErr(t, err)
	})
	t.Run("file_write overwrites without echoing the old content, snapshot kept", func(t *testing.T) {
		res, err := runDeny(denyTool(t, env, "file_write"), map[string]any{"path": "cfg/.env", "content": "NEW=1\n"})
		assert.Require(t, assert.NoErr(t, err))
		assert.NotContains(t, res.Content, "zzz")
		assert.NotEmpty(t, snaps.before)
		assert.Eq(t, []byte("SEKRET=zzz\n"), snaps.before[len(snaps.before)-1].Before)
	})
}
