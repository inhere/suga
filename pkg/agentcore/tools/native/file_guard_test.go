package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// guardedTools builds the file tools of one tool set over a fresh workspace.
func guardedTools(t *testing.T, protect bool) (dir string, read, write, edit api.Tool) {
	t.Helper()
	dir = t.TempDir()
	for _, tl := range BuildTools(Env{Workspace: dir, ProtectChangedFiles: protect}, true) {
		switch tl.Name() {
		case "file_read":
			read = tl
		case "file_write":
			write = tl
		case "file_edit":
			edit = tl
		}
	}
	return
}

// touchExternally rewrites path the way another process would, with an mtime
// that differs from the earlier one whatever the filesystem's granularity.
func touchExternally(t *testing.T, path, content string) {
	t.Helper()
	assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644))
	later := time.Now().Add(time.Hour)
	assert.NoErr(t, os.Chtimes(path, later, later))
}

func run(t *testing.T, tl api.Tool, args map[string]any) error {
	t.Helper()
	_, err := tl.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tl.Name(), Arguments: args})
	return err
}

func TestFileGuard(t *testing.T) {
	t.Run("read, changed outside, write: refused", func(t *testing.T) {
		dir, read, write, _ := guardedTools(t, true)
		path := filepath.Join(dir, "a.txt")
		assert.NoErr(t, os.WriteFile(path, []byte("one"), 0o644))
		assert.NoErr(t, run(t, read, map[string]any{"path": "a.txt"}))
		touchExternally(t, path, "two!")
		err := run(t, write, map[string]any{"path": "a.txt", "content": "mine"})
		assert.ErrMsgContains(t, err, "file_read")
		assert.ErrMsgContains(t, err, "changed on disk")
		data, _ := os.ReadFile(path)
		assert.Eq(t, "two!", string(data))
		// after reading again the write goes through
		assert.NoErr(t, run(t, read, map[string]any{"path": "a.txt"}))
		assert.NoErr(t, run(t, write, map[string]any{"path": "a.txt", "content": "mine"}))
	})
	t.Run("read, write: allowed", func(t *testing.T) {
		dir, read, write, _ := guardedTools(t, true)
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644))
		assert.NoErr(t, run(t, read, map[string]any{"path": "a.txt"}))
		assert.NoErr(t, run(t, write, map[string]any{"path": "a.txt", "content": "two"}))
		// the write refreshed the record: a second write needs no read
		assert.NoErr(t, run(t, write, map[string]any{"path": "a.txt", "content": "three"}))
	})
	t.Run("write, changed outside, edit: refused", func(t *testing.T) {
		dir, _, write, edit := guardedTools(t, true)
		path := filepath.Join(dir, "b.txt")
		assert.NoErr(t, run(t, write, map[string]any{"path": "b.txt", "content": "hello world"}))
		touchExternally(t, path, "hello brave world")
		err := run(t, edit, map[string]any{"path": "b.txt", "old_string": "hello", "new_string": "bye"})
		assert.ErrMsgContains(t, err, "file_read")
		data, _ := os.ReadFile(path)
		assert.Eq(t, "hello brave world", string(data))
	})
	t.Run("never read file and new file are not refused", func(t *testing.T) {
		dir, _, write, edit := guardedTools(t, true)
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "c.txt"), []byte("abc"), 0o644))
		assert.NoErr(t, run(t, edit, map[string]any{"path": "c.txt", "old_string": "abc", "new_string": "xyz"}))
		assert.NoErr(t, run(t, write, map[string]any{"path": "sub/new.txt", "content": "n"}))
	})
	t.Run("switched off: allowed", func(t *testing.T) {
		dir, read, write, _ := guardedTools(t, false)
		path := filepath.Join(dir, "a.txt")
		assert.NoErr(t, os.WriteFile(path, []byte("one"), 0o644))
		assert.NoErr(t, run(t, read, map[string]any{"path": "a.txt"}))
		touchExternally(t, path, "two!")
		assert.NoErr(t, run(t, write, map[string]any{"path": "a.txt", "content": "mine"}))
	})
	t.Run("tool sets do not share records", func(t *testing.T) {
		dir, read, _, _ := guardedTools(t, true)
		path := filepath.Join(dir, "a.txt")
		assert.NoErr(t, os.WriteFile(path, []byte("one"), 0o644))
		assert.NoErr(t, run(t, read, map[string]any{"path": "a.txt"}))
		touchExternally(t, path, "two!")
		// a second tool set (a sub-agent's) never read it: not guarded
		var other api.Tool
		for _, tl := range BuildTools(Env{Workspace: dir, ProtectChangedFiles: true}, true) {
			if tl.Name() == "file_write" {
				other = tl
			}
		}
		assert.NoErr(t, run(t, other, map[string]any{"path": "a.txt", "content": "x"}))
	})
}
