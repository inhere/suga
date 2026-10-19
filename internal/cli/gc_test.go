package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/fsutil"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
)

func TestGCCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_HOOKS", "off")
	ctx := context.Background()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	old := time.Now().Add(-40 * 24 * time.Hour)
	for _, id := range []string{"gc-idle", "gc-busy"} {
		assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: id, Title: id, CreatedAt: old, UpdatedAt: old}))
	}
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "gc-new", Title: "new"}))
	assert.NoErr(t, repo.Close())
	transcripts := filepath.Join(home, "transcripts")
	assert.NoErr(t, os.MkdirAll(transcripts, 0o700))
	sessionsDir := filepath.Join(home, "sessions")
	for _, id := range []string{"gc-idle", "gc-busy"} {
		assert.NoErr(t, os.WriteFile(filepath.Join(transcripts, id+".jsonl"), []byte("{}\n"), 0o600))
		outputs := filepath.Join(sessionsDir, id, "outputs")
		assert.NoErr(t, os.MkdirAll(outputs, 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(outputs, "0123456789abcdef.txt"), []byte("out"), 0o600))
	}

	exists := func(id string) bool {
		repo, err := store.Open(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		defer repo.Close()
		_, err = repo.GetSession(ctx, id)
		return err == nil
	}

	t.Run("usage", func(t *testing.T) {
		_, err := runCLI(t, ctx, "data", "gc")
		assert.ErrMsgContains(t, err, "--older-than")
	})

	t.Run("dry run by default", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "gc", "--older-than", "30d")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "gc-idle")
		assert.StrContains(t, out, "将删除 2 个会话")
		assert.StrContains(t, out, "--apply")
		assert.True(t, exists("gc-idle"))
	})

	t.Run("apply keeps a session another process holds", func(t *testing.T) {
		// A running turn holds run/locks/<id>.lock for the whole turn.
		lock, err := personalprocess.AcquireSessionLock(filepath.Join(home, "run", "locks"), "gc-busy", 0)
		assert.Require(t, assert.NoErr(t, err))
		defer lock.Close()
		out, err := runCLI(t, ctx, "data", "gc", "--older-than", "30d", "--apply")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "已删除 1 个会话")
		assert.False(t, exists("gc-idle"))
		assert.True(t, exists("gc-busy"))
		assert.True(t, exists("gc-new"))
		assert.False(t, fsutil.IsFile(filepath.Join(transcripts, "gc-idle.jsonl")))
		assert.True(t, fsutil.IsFile(filepath.Join(transcripts, "gc-busy.jsonl")))
		// The session directory (externalised outputs) goes with its session.
		assert.False(t, fsutil.PathExists(filepath.Join(sessionsDir, "gc-idle")))
		assert.True(t, fsutil.IsDir(filepath.Join(sessionsDir, "gc-busy", "outputs")))
	})
}

func TestSessionDirRejectsNonLocalIDs(t *testing.T) {
	root := filepath.Join("x", "sessions")
	dir, ok := sessionDir(root, "sess_1")
	assert.True(t, ok)
	assert.Eq(t, filepath.Join(root, "sess_1"), dir)
	for _, id := range []string{"", ".", "..", "../x", "a/b", `a\b`, "/abs", "C:x"} {
		_, ok := sessionDir(root, id)
		assert.False(t, ok, id)
	}
}
