package personal

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/adddir"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

type undoTestGate struct {
	id       string
	acquired int
}

func (g *undoTestGate) Acquire(ctx context.Context) (func(), error) {
	g.id = undoSessionID(ctx)
	g.acquired++
	return func() {}, nil
}

func (g *undoTestGate) AcquireUnlessBusy(ctx context.Context) (func(), error) {
	return g.Acquire(ctx)
}

func TestUndoCoreGateLockAndRootTrust(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	cwd := t.TempDir()
	added := t.TempDir()
	parent := filepath.Dir(cwd)
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "undo-core", Cwd: cwd, Meta: map[string]any{
		adddir.MetaKey: adddir.ToMeta([]adddir.Dir{{Path: added, Writable: true}}),
	}}))
	snapshots := NewSessionSnapshots(t.TempDir())
	create := func(t *testing.T, id, root string) {
		t.Helper()
		assert.NoErr(t, repo.CreateTurn(ctx, store.Turn{ID: id, SessionID: "undo-core", Status: store.TurnCompleted,
			StartedAt: time.Now().UTC()}))
		scope := withObserveScope(ctx, "undo-core", id)
		before, err := snapshots.BeforeCall(scope, native.SnapshotRequest{CallID: id, Tool: "file_write", Root: root,
			Rel: "a.txt", RealRel: "a.txt", Kind: native.SnapshotKindFile, Before: []byte("before")})
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, before.Unrecoverable)
		assert.NoErr(t, snapshots.AfterCall(scope, native.SnapshotResult{CallID: id, Status: native.SnapshotStatusComplete,
			AfterSHA256: "after"}))
	}
	create(t, "trusted-cwd", cwd)
	create(t, "trusted-added", added)
	create(t, "changed", parent)

	gate := &undoTestGate{}
	core := NewUndoCore(repo, snapshots, t.TempDir(), filepath.Join(t.TempDir(), "locks"))
	core.Gate = gate
	rows, err := core.Checkpoints(ctx, "undo-", 0)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "undo-core", gate.id)
	assert.Eq(t, 1, gate.acquired)
	assert.Require(t, assert.Len(t, rows, 3))
	byTurn := make(map[string]Checkpoint, len(rows))
	for _, row := range rows {
		byTurn[row.TurnID] = row
	}
	assert.Eq(t, SnapshotRootCwdChanged, byTurn["changed"].Items[0].RootStatus)
	assert.Eq(t, CheckpointActionSkip, byTurn["changed"].Items[0].Action)
	assert.Eq(t, "会话目录已变更，无法恢复", byTurn["changed"].Items[0].Reason)
	assert.Eq(t, SnapshotRootTrusted, byTurn["trusted-added"].Items[0].RootStatus)
	assert.Eq(t, SnapshotRootTrusted, byTurn["trusted-cwd"].Items[0].RootStatus)

	// A held chat lock is reused; no second OS lock is attempted.
	core.HeldLock = func(string) bool { return true }
	_, err = core.Checkpoints(ctx, "undo-core", 1)
	assert.NoErr(t, err)

	// The Runner adapter is the chat-facing path and does not construct a Provider.
	runner := &Runner{cfg: Config{Repository: repo}, heldLocks: map[string]*personalprocess.SessionLock{}}
	_, err = runner.Checkpoints(ctx, snapshots, t.TempDir(), "undo-core", 1)
	assert.NoErr(t, err)
}

func TestUndoCoreReportsRemovedAdditionalDirectory(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	cwd, added := t.TempDir(), t.TempDir()
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "undo-removed", Cwd: cwd}))
	_, err := repo.AppendEvent(ctx, store.Event{SessionID: "undo-removed", Kind: store.EventAddDir, Name: "remove",
		Status: "ok", Attrs: map[string]any{"path": added}})
	assert.NoErr(t, err)
	snapshots := NewSessionSnapshots(t.TempDir())
	assert.NoErr(t, repo.CreateTurn(ctx, store.Turn{ID: "turn", SessionID: "undo-removed", Status: store.TurnCompleted}))
	scope := withObserveScope(ctx, "undo-removed", "turn")
	_, err = snapshots.BeforeCall(scope, native.SnapshotRequest{CallID: "call", Tool: "file_write", Root: added,
		Rel: "a", RealRel: "a", Kind: native.SnapshotKindFile, Before: []byte("x")})
	assert.NoErr(t, err)
	assert.NoErr(t, snapshots.AfterCall(scope, native.SnapshotResult{CallID: "call", Status: native.SnapshotStatusComplete}))

	core := NewUndoCore(repo, snapshots, t.TempDir(), "")
	rows, err := core.Checkpoints(ctx, "undo-removed", 0)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, SnapshotRootAddedDirRemoved, rows[0].Items[0].RootStatus)
	assert.Eq(t, "附加目录已移除，无法恢复", rows[0].Items[0].Reason)
}

func TestUndoCoreReturnsBusyWithoutProvider(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	cwd := t.TempDir()
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "busy", Cwd: cwd}))
	assert.NoErr(t, repo.CreateTurn(ctx, store.Turn{ID: "turn", SessionID: "busy", Status: store.TurnCompleted}))
	snapshots := NewSessionSnapshots(t.TempDir())
	lockRoot := filepath.Join(t.TempDir(), "locks")
	lock, err := personalprocess.AcquireSessionLock(lockRoot, "busy", 0)
	assert.Require(t, assert.NoErr(t, err))
	defer lock.Close()

	core := NewUndoCore(repo, snapshots, t.TempDir(), lockRoot)
	_, err = core.Checkpoints(ctx, "busy", 0)
	assert.True(t, errors.Is(err, personalprocess.ErrSessionLocked))
	assert.True(t, errors.Is(err, ErrSessionBusy))
}
