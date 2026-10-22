package personal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

type undoRestoreFixture struct {
	ctx       context.Context
	repo      *store.Store
	snapshots *SessionSnapshots
	core      *UndoCore
	root      string
	path      string
}

func newUndoRestoreFixture(t *testing.T, initial string) undoRestoreFixture {
	t.Helper()
	ctx := context.Background()
	repo := openObserveRepo(t)
	root := t.TempDir()
	sid := "undo-restore"
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: sid, Cwd: root}))
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte(initial), 0o640))
	assert.NoErr(t, repo.CreateTurn(ctx, store.Turn{ID: "turn-restore", SessionID: sid, Status: store.TurnCompleted, StartedAt: time.Now().UTC()}))
	snapshots := NewSessionSnapshots(t.TempDir())
	return undoRestoreFixture{ctx: ctx, repo: repo, snapshots: snapshots,
		core: NewUndoCore(repo, snapshots, t.TempDir(), ""), root: root, path: filepath.Join(root, "a.txt")}
}

func (f undoRestoreFixture) recordFileWrite(t *testing.T, callID, content string) {
	t.Helper()
	before, err := os.ReadFile(f.path)
	assert.Require(t, assert.NoErr(t, err))
	scope := withObserveScope(f.ctx, "undo-restore", "turn-restore")
	_, err = f.snapshots.BeforeCall(scope, native.SnapshotRequest{
		CallID: callID, Tool: "file_write", Root: f.root, Rel: "a.txt", RealRel: "a.txt",
		Kind: native.SnapshotKindFile, Before: before,
	})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, os.WriteFile(f.path, []byte(content), 0o640))
	assert.NoErr(t, f.snapshots.AfterCall(scope, native.SnapshotResult{
		CallID: callID, Tool: "file_write", Root: f.root, Rel: "a.txt", RealRel: "a.txt",
		Kind: native.SnapshotKindFile, Status: native.SnapshotStatusComplete,
		Size: int64(len(before)), AfterSHA256: nativeSHA256([]byte(content)),
	}))
}

func TestUndoContentRecovery(t *testing.T) {
	t.Run("same path is restored in reverse call order and records pre-undo", func(t *testing.T) {
		f := newUndoRestoreFixture(t, "zero")
		f.recordFileWrite(t, "call-1", "one")
		f.recordFileWrite(t, "call-2", "two")

		result, err := f.core.Undo(f.ctx, "undo-rest", 1, false)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, result.Restored)
		assert.Eq(t, 0, result.Skipped)
		content, err := os.ReadFile(f.path)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "zero", string(content))
		state, err := f.snapshots.LoadCheckpointState("undo-restore", "turn-restore")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, SnapshotStateUndone, state.State)

		pre, err := loadTurn(filepath.Join(f.snapshots.dir, "undo-restore", "snapshots", "turn-restore", snapshotPreUndoDir))
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, pre.Metas, 2))
		assert.Eq(t, snapshotSourcePreUndo, pre.Metas[0].Source)
		before, err := os.ReadFile(filepath.Join(f.snapshots.dir, "undo-restore", "snapshots", "turn-restore", snapshotPreUndoDir, "001-"+snapshotCallHash(pre.Metas[0].CallID), "before"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "two", string(before))
	})

	t.Run("content conflict is skipped unless force is requested", func(t *testing.T) {
		f := newUndoRestoreFixture(t, "before")
		f.recordFileWrite(t, "call", "after")
		assert.NoErr(t, os.WriteFile(f.path, []byte("manual"), 0o640))

		result, err := f.core.Undo(f.ctx, "undo-restore", 1, false)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, result.Restored)
		assert.Eq(t, 1, result.Skipped)
		content, err := os.ReadFile(f.path)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "manual", string(content))
		state, err := f.snapshots.LoadCheckpointState("undo-restore", "turn-restore")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, SnapshotStatePartial, state.State)

		result, err = f.core.Undo(f.ctx, "undo-restore", 1, true)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, result.Forced)
		assert.Eq(t, 1, result.Restored)
		content, err = os.ReadFile(f.path)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "before", string(content))
	})

	t.Run("already restored content is idempotent", func(t *testing.T) {
		f := newUndoRestoreFixture(t, "before")
		f.recordFileWrite(t, "call", "after")
		assert.NoErr(t, os.WriteFile(f.path, []byte("before"), 0o640))

		result, err := f.core.Undo(f.ctx, "undo-restore", 1, false)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, result.Restored)
		assert.Eq(t, 1, result.Skipped)
		state, err := f.snapshots.LoadCheckpointState("undo-restore", "turn-restore")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, SnapshotStateUndone, state.State)
	})
}

func TestUndoDeletesNewFileAndPreUndoProtectsIt(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	root := t.TempDir()
	sid, turnID := "undo-new", "turn-new"
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: sid, Cwd: root}))
	assert.NoErr(t, repo.CreateTurn(ctx, store.Turn{ID: turnID, SessionID: sid, Status: store.TurnCompleted}))
	snapshots := NewSessionSnapshots(t.TempDir())
	scope := withObserveScope(ctx, sid, turnID)
	_, err := snapshots.BeforeCall(scope, native.SnapshotRequest{CallID: "new-call", Tool: "file_write", Root: root, Rel: "new.txt", RealRel: "new.txt", Kind: native.SnapshotKindAbsent})
	assert.Require(t, assert.NoErr(t, err))
	path := filepath.Join(root, "new.txt")
	assert.NoErr(t, os.WriteFile(path, []byte("created"), 0o640))
	assert.NoErr(t, snapshots.AfterCall(scope, native.SnapshotResult{CallID: "new-call", Tool: "file_write", Status: native.SnapshotStatusComplete}))

	core := NewUndoCore(repo, snapshots, t.TempDir(), "")
	result, err := core.Undo(ctx, sid, 1, false)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, result.Deleted)
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
	pre, err := loadTurn(filepath.Join(snapshots.dir, sid, "snapshots", turnID, snapshotPreUndoDir))
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, pre.Metas, 1))
}
