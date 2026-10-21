package wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/adddir"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// addDirsSessionConfig isolates the config and points the workspace at ws.
func addDirsSessionConfig(t *testing.T, ws string) {
	t.Helper()
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	assert.NoErr(t, gkconfig.Set("workspace", ws))
	assert.NoErr(t, gkconfig.Set("tools.dependency_roots", false))
	assert.NoErr(t, gkconfig.Set("tools.standards_roots", false))
	assert.NoErr(t, gkconfig.Set("tools.skill_roots", false))
}

// openAddDirsRuntime opens a runtime with the given added directories.
func openAddDirsRuntime(t *testing.T, opts Options) *Runtime {
	t.Helper()
	rt, err := OpenCLI(context.Background(), opts)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// openAddDirsChatSession opens a runtime, a session and publishes its added
// directories, as chat does through chat.Options.SetupSession.
func openAddDirsChatSession(t *testing.T, opts Options) (*Runtime, string) {
	t.Helper()
	rt := openAddDirsRuntime(t, opts)
	model, err := rt.Runner.NewSession(context.Background(), "", "测试")
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, rt.ApplyAddDirs(context.Background(), model.ID)))
	return rt, model.ID
}

// storedAddDirs reads the session's recorded additional directories.
func storedAddDirs(t *testing.T, rt *Runtime, sessionID string) []adddir.Dir {
	t.Helper()
	model, err := rt.Runner.LoadSession(context.Background(), sessionID)
	assert.Require(t, assert.NoErr(t, err))
	dirs, err := adddir.FromMeta(model.Meta[adddir.MetaKey])
	assert.Require(t, assert.NoErr(t, err))
	return dirs
}

// TestAddDirsSession covers the session lifecycle of --add-dir: the runtime
// publishes the requested set before any session exists, ApplyAddDirs records
// it in the session metadata, a resume keeps the stored set (this invocation's
// mode winning on the same path) and a --read-only run only ever exposes the
// read-only view.
func TestAddDirsSession(t *testing.T) {
	ctx := context.Background()
	ws, ro, rw := t.TempDir(), t.TempDir(), t.TempDir()
	addDirsSessionConfig(t, ws)
	assert.NoErr(t, os.WriteFile(filepath.Join(ro, "seed.txt"), []byte("ro-seed\n"), 0o644))

	rt := openAddDirsRuntime(t, Options{AllowWrite: true,
		AddDirs: []adddir.Dir{{Path: ro}, {Path: rw, Writable: true}}})

	t.Run("the requested set is live before the session is known", func(t *testing.T) {
		assert.Eq(t, []string{ro + ":ro", rw + ":rw"}, rt.AddDirs.List())
		prompt := environmentPrompt(Options{Workspace: ws, sessionDirs: rt.AddDirs})
		assert.StrContains(t, prompt, "- Additional directories of this session (--add-dir")
		assert.StrContains(t, prompt, "  - "+ro+": read-only")
		assert.StrContains(t, prompt, "  - "+rw+": read-write")
	})
	t.Run("a read-only runtime says read-only", func(t *testing.T) {
		readOnly := openAddDirsRuntime(t, Options{ReadOnly: true, AddDirs: []adddir.Dir{{Path: rw, Writable: true}}})
		assert.Eq(t, []string{rw + ":ro"}, readOnly.AddDirs.List())
		assert.StrContains(t, environmentPrompt(Options{Workspace: ws, sessionDirs: readOnly.AddDirs}),
			"  - "+rw+": read-only")
	})

	model, err := rt.Runner.NewSession(ctx, "adddirs-1", "测试")
	assert.Require(t, assert.NoErr(t, err))

	t.Run("ApplyAddDirs records the set in the session", func(t *testing.T) {
		assert.NoErr(t, rt.ApplyAddDirs(ctx, model.ID))
		assert.Eq(t, []adddir.Dir{{Path: ro}, {Path: rw, Writable: true}}, storedAddDirs(t, rt, model.ID))
	})
	t.Run("the tools accept the added directories", func(t *testing.T) {
		write, err := rt.Registry.Get("file_write")
		assert.Require(t, assert.NoErr(t, err))
		_, err = write.Execute(ctx, api.ToolCall{CallID: "c", Arguments: map[string]any{
			"path": filepath.Join(rw, "a.txt"), "content": "x\n"}})
		assert.NoErr(t, err)
		// The :ro directory is readable, not writable: the same rule as ever.
		_, err = write.Execute(ctx, api.ToolCall{CallID: "c", Arguments: map[string]any{
			"path": filepath.Join(ro, "a.txt"), "content": "x\n"}})
		assert.ErrMsgContains(t, err, "outside the workspace and the added read-write directories")

		read, err := rt.Registry.Get("file_read")
		assert.Require(t, assert.NoErr(t, err))
		res, err := read.Execute(ctx, api.ToolCall{CallID: "c", Arguments: map[string]any{
			"path": filepath.Join(ro, "seed.txt")}})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "ro-seed")
	})
	t.Run("a resume keeps the stored set and takes the new mode", func(t *testing.T) {
		// A second process adds the :rw directory as read-only: the stored
		// read-only directory stays, the same path takes this invocation's mode.
		other := openAddDirsRuntime(t, Options{AllowWrite: true, AddDirs: []adddir.Dir{{Path: rw}}})
		assert.NoErr(t, other.ApplyAddDirs(ctx, model.ID))
		assert.Eq(t, []adddir.Dir{{Path: ro}, {Path: rw}}, storedAddDirs(t, other, model.ID))
	})
	t.Run("a run without --add-dir keeps the session's directories", func(t *testing.T) {
		plain := openAddDirsRuntime(t, Options{AllowWrite: true})
		assert.Empty(t, plain.AddDirs.List())
		assert.NoErr(t, plain.ApplyAddDirs(ctx, model.ID))
		assert.Eq(t, []adddir.Dir{{Path: ro}, {Path: rw}}, storedAddDirs(t, plain, model.ID))
		assert.Eq(t, []string{ro + ":ro", rw + ":ro"}, plain.AddDirs.List())
	})
	t.Run("a read-only run keeps the requested mode but exposes read-only", func(t *testing.T) {
		readOnly := openAddDirsRuntime(t, Options{ReadOnly: true, AddDirs: []adddir.Dir{{Path: rw, Writable: true}}})
		assert.NoErr(t, readOnly.ApplyAddDirs(ctx, model.ID))
		assert.Eq(t, []adddir.Dir{{Path: ro}, {Path: rw, Writable: true}}, storedAddDirs(t, readOnly, model.ID))
		assert.Eq(t, []string{ro + ":ro", rw + ":ro"}, readOnly.AddDirs.List())
	})
	t.Run("a malformed stored value is ignored", func(t *testing.T) {
		assert.NoErr(t, rt.Runner.SaveSessionMeta(ctx, model.ID, map[string]any{adddir.MetaKey: "junk"}))
		assert.NoErr(t, rt.ApplyAddDirs(ctx, model.ID))
		assert.Eq(t, []adddir.Dir{{Path: ro}, {Path: rw, Writable: true}}, storedAddDirs(t, rt, model.ID))
	})
}
