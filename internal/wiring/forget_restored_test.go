package wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

func TestForgetRestoredFiles(t *testing.T) {
	ws := t.TempDir()
	reg := tools.NewRegistry()
	for _, tl := range native.BuildTools(native.Env{Workspace: ws, ProtectChangedFiles: true}, true) {
		reg.Register(tl)
	}
	exec := func(name string, args map[string]any) error {
		tl, err := reg.Get(name)
		assert.NoErr(t, err)
		_, err = tl.Execute(context.Background(), agentcore.ToolCall{CallID: "c", Name: name, Arguments: args})
		return err
	}
	path := filepath.Join(ws, "t.txt")
	assert.NoErr(t, os.WriteFile(path, []byte("one"), 0o644))
	assert.NoErr(t, exec("file_read", map[string]any{"path": "t.txt"}))
	assert.NoErr(t, exec("file_edit", map[string]any{"path": "t.txt", "old_string": "one", "new_string": "two"}))
	// the :undo restore: suga's own write behind the tools' back
	assert.NoErr(t, os.WriteFile(path, []byte("one"), 0o644))
	later := time.Now().Add(time.Hour)
	assert.NoErr(t, os.Chtimes(path, later, later))
	assert.ErrMsgContains(t, exec("file_edit", map[string]any{"path": "t.txt", "old_string": "one", "new_string": "x"}), "file_read")
	forgetRestoredFiles(reg)([]string{path})
	assert.NoErr(t, exec("file_edit", map[string]any{"path": "t.txt", "old_string": "one", "new_string": "three"}))
	forgetRestoredFiles(nil)([]string{path}) // no registry: nothing happens
}
