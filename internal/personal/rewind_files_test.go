package personal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// rewindFilesFixture is a session whose turns write files with real snapshots.
type rewindFilesFixture struct {
	ctx       context.Context
	repo      *store.Store
	snapshots *SessionSnapshots
	core      *UndoCore
	root      string
	sid       string
	n         int
}

func newRewindFilesFixture(t *testing.T) *rewindFilesFixture {
	t.Helper()
	ctx := context.Background()
	repo := openObserveRepo(t)
	root := t.TempDir()
	f := &rewindFilesFixture{ctx: ctx, repo: repo, root: root, sid: "rw-files"}
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: f.sid, Cwd: root}))
	f.snapshots = NewSessionSnapshots(t.TempDir())
	f.core = NewUndoCore(repo, f.snapshots, t.TempDir(), "")
	return f
}

// turn creates the next turn (increasing StartedAt) and returns its ID.
func (f *rewindFilesFixture) turn(t *testing.T) string {
	t.Helper()
	f.n++
	id := fmt.Sprintf("t%d", f.n)
	assert.NoErr(t, f.repo.CreateTurn(f.ctx, store.Turn{ID: id, SessionID: f.sid, Status: store.TurnCompleted,
		StartedAt: time.Date(2026, 1, 1, 0, f.n, 0, 0, time.UTC)}))
	return id
}

// write snapshots then writes name (kind absent when the file is new).
func (f *rewindFilesFixture) write(t *testing.T, turn, name, content string) {
	t.Helper()
	f.writeAt(t, turn, f.root, name, content)
}

func (f *rewindFilesFixture) writeAt(t *testing.T, turn, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	req := native.SnapshotRequest{CallID: turn + name + content, Tool: "file_write", Root: root, Rel: name, RealRel: name}
	before, err := os.ReadFile(path)
	if err == nil {
		req.Kind, req.Before = native.SnapshotKindFile, before
	} else {
		req.Kind = native.SnapshotKindAbsent
	}
	scope := withObserveScope(f.ctx, f.sid, turn)
	_, err = f.snapshots.BeforeCall(scope, req)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, os.WriteFile(path, []byte(content), 0o640))
	assert.NoErr(t, f.snapshots.AfterCall(scope, native.SnapshotResult{
		CallID: req.CallID, Tool: "file_write", Root: root, Rel: name, RealRel: name, Kind: req.Kind,
		Status: native.SnapshotStatusComplete, Size: int64(len(before)), AfterSHA256: nativeSHA256([]byte(content)),
	}))
}

func (f *rewindFilesFixture) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, name))
	assert.Require(t, assert.NoErr(t, err))
	return string(b)
}

func (f *rewindFilesFixture) state(t *testing.T, turn string) SnapshotState {
	t.Helper()
	st, err := f.snapshots.LoadCheckpointState(f.sid, turn)
	assert.Require(t, assert.NoErr(t, err))
	return st.State
}

// toolEvent records a write-tool event of a turn.
func (f *rewindFilesFixture) toolEvent(t *testing.T, turn, name, status string, attrs map[string]any) {
	t.Helper()
	_, err := f.repo.AppendEvent(f.ctx, store.Event{SessionID: f.sid, TurnID: turn, Kind: store.EventTool,
		Name: name, Status: status, StartedAt: time.Now().UTC(), Attrs: attrs})
	assert.NoErr(t, err)
}

func actions(steps []rewindFileStep) []string {
	var out []string
	for _, s := range steps {
		out = append(out, fmt.Sprintf("%s:%s:%s", s.View.TurnID, filepath.Base(s.View.Path), s.View.Action))
	}
	return out
}

func TestRewindFilesPlanAndApply(t *testing.T) {
	t.Run("restores turns newest first down to the target", func(t *testing.T) {
		f := newRewindFilesFixture(t)
		assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("zero"), 0o640))
		t1, t2, t3 := f.turn(t), f.turn(t), f.turn(t)
		f.write(t, t1, "a.txt", "one")
		f.write(t, t2, "a.txt", "two")
		f.write(t, t3, "a.txt", "three")
		f.write(t, t3, "b.txt", "new")

		steps, err := f.core.planRewindFiles(f.ctx, f.sid, t2)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"t3:b.txt:delete", "t3:a.txt:restore", "t2:a.txt:restore"}, actions(steps))

		got, err := f.core.applyRewindFiles(f.ctx, f.sid, steps)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 3, len(got))
		assert.Eq(t, "one", f.read(t, "a.txt"))
		_, err = os.Stat(filepath.Join(f.root, "b.txt"))
		assert.True(t, os.IsNotExist(err))
		assert.Eq(t, SnapshotStateUndone, f.state(t, t3))
		assert.Eq(t, SnapshotStateUndone, f.state(t, t2))
		assert.Eq(t, SnapshotStateOpen, f.state(t, t1)) // outside the range
	})

	t.Run("content conflict and its cascade", func(t *testing.T) {
		f := newRewindFilesFixture(t)
		assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("zero"), 0o640))
		t1, t2, t3 := f.turn(t), f.turn(t), f.turn(t)
		f.write(t, t1, "a.txt", "one")
		f.write(t, t2, "a.txt", "two")
		f.write(t, t3, "a.txt", "three")
		f.write(t, t3, "other.txt", "x")
		assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("manual"), 0o640))

		steps, err := f.core.planRewindFiles(f.ctx, f.sid, t1)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"t3:other.txt:delete", "t3:a.txt:conflict", "t2:a.txt:conflict", "t1:a.txt:conflict"}, actions(steps))
		assert.Eq(t, rewindReasonContent, steps[1].View.Reason)
		assert.Eq(t, rewindReasonCascade, steps[2].View.Reason)
		assert.Eq(t, rewindReasonCascade, steps[3].View.Reason)

		got, err := f.core.applyRewindFiles(f.ctx, f.sid, steps)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RewindFileDelete, got[0].Action)
		for _, g := range got[1:] {
			assert.Eq(t, RewindFileConflict, g.Action)
		}
		assert.Eq(t, "manual", f.read(t, "a.txt"))
		assert.Eq(t, SnapshotStatePartial, f.state(t, t3))
	})

	t.Run("undone turns are skipped and partial ones rerun", func(t *testing.T) {
		f := newRewindFilesFixture(t)
		assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("zero"), 0o640))
		t1, t2 := f.turn(t), f.turn(t)
		f.write(t, t1, "a.txt", "one")
		f.write(t, t2, "a.txt", "two")
		assert.NoErr(t, f.snapshots.SetCheckpointState(f.sid, t2, SnapshotStateUndone))
		steps, err := f.core.planRewindFiles(f.ctx, f.sid, t1)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"t1:a.txt:conflict"}, actions(steps)) // file still holds "two"

		assert.NoErr(t, f.snapshots.SetCheckpointState(f.sid, t2, SnapshotStatePartial))
		steps, err = f.core.planRewindFiles(f.ctx, f.sid, t1)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"t2:a.txt:restore", "t1:a.txt:restore"}, actions(steps))
	})

	t.Run("untrusted root is unrecoverable and never restored", func(t *testing.T) {
		f := newRewindFilesFixture(t)
		other := t.TempDir() // neither cwd nor an added dir
		t1 := f.turn(t)
		f.writeAt(t, t1, other, "a.txt", "one")
		f.write(t, t1, "ok.txt", "x")
		steps, err := f.core.planRewindFiles(f.ctx, f.sid, t1)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"t1:ok.txt:delete", "t1:a.txt:unrecoverable"}, actions(steps))
		assert.Eq(t, rewindReasonRootUntrusted, steps[1].View.Reason)

		got, err := f.core.applyRewindFiles(f.ctx, f.sid, steps)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, RewindFileUnrecoverable, got[1].Action)
		b, _ := os.ReadFile(filepath.Join(other, "a.txt"))
		assert.Eq(t, "one", string(b))
		assert.Eq(t, SnapshotStatePartial, f.state(t, t1)) // unresolved item
	})

	t.Run("unknown target turn is an error", func(t *testing.T) {
		f := newRewindFilesFixture(t)
		_, err := f.core.planRewindFiles(f.ctx, f.sid, "nope")
		assert.Err(t, err)
	})
}

func TestRewindFilesClearedDetection(t *testing.T) {
	built := map[string]any{"kind": "file", "status": "complete"}
	t.Run("only ok write events without a snapshot directory count", func(t *testing.T) {
		f := newRewindFilesFixture(t)
		t1 := f.turn(t)
		f.toolEvent(t, t1, "file_write", "ok", map[string]any{"target": []string{"a/b.txt", "c.txt"}, "snapshot": built})
		f.toolEvent(t, t1, "file_edit", "error", map[string]any{"target": []string{"err.txt"}, "snapshot": built})
		f.toolEvent(t, t1, "file_remove", "denied", map[string]any{"target": []string{"den.txt"}})
		f.toolEvent(t, t1, "shell_exec", "ok", map[string]any{"command": "rm x"})
		f.toolEvent(t, t1, "file_read", "ok", map[string]any{"target": []string{"read.txt"}})

		steps, err := f.core.planRewindFiles(f.ctx, f.sid, t1)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"t1:c.txt:unrecoverable", "t1:b.txt:unrecoverable"}, actions(steps))
		assert.Eq(t, "a/b.txt", steps[1].View.Path) // raw target value
		assert.Eq(t, rewindReasonCleared, steps[0].View.Reason)
		assert.False(t, steps[0].hasSnapshot())

		got, err := f.core.applyRewindFiles(f.ctx, f.sid, steps)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, len(got))
		// No snapshot directory: no checkpoint state is created for it.
		_, statErr := os.Stat(filepath.Join(f.snapshots.dir, f.sid, "snapshots", t1))
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("snapshot attr tells cleared from never built and snapshots off", func(t *testing.T) {
		f := newRewindFilesFixture(t)
		t1 := f.turn(t)
		f.toolEvent(t, t1, "file_write", "ok", map[string]any{"target": []string{"off.txt"}})
		f.toolEvent(t, t1, "file_write", "ok", map[string]any{"target": []string{"none.txt"},
			"snapshot": map[string]any{"kind": "", "status": "incomplete"}})
		f.toolEvent(t, t1, "file_remove", "ok", map[string]any{"target": []string{"gone.txt"}, "snapshot": built})
		steps, err := f.core.planRewindFiles(f.ctx, f.sid, t1)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 3, len(steps))
		assert.Eq(t, rewindReasonCleared, steps[0].View.Reason)
		assert.Eq(t, rewindReasonNoSnapshot, steps[1].View.Reason)
		assert.Eq(t, rewindReasonSnapshotsOff, steps[2].View.Reason)
	})

	t.Run("a turn with a snapshot directory is not reported as cleared", func(t *testing.T) {
		f := newRewindFilesFixture(t)
		t1 := f.turn(t)
		f.write(t, t1, "a.txt", "x")
		f.toolEvent(t, t1, "file_write", "ok", map[string]any{"target": []string{"a.txt"}, "snapshot": built})
		steps, err := f.core.planRewindFiles(f.ctx, f.sid, t1)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"t1:a.txt:delete"}, actions(steps))
	})
}

func TestRewindFilesApplyKeepsGoingAfterFailure(t *testing.T) {
	f := newRewindFilesFixture(t)
	t1 := f.turn(t)
	f.write(t, t1, "a.txt", "x")
	f.write(t, t1, "b.txt", "y")
	steps, err := f.core.planRewindFiles(f.ctx, f.sid, t1)
	assert.Require(t, assert.NoErr(t, err))
	// Corrupt the b.txt before-image path identity: a real restore error.
	steps[0].Item.Meta.RealRel = "../escape"
	got, err := f.core.applyRewindFiles(f.ctx, f.sid, steps)
	assert.Err(t, err)
	assert.Eq(t, 2, len(got))
	assert.Eq(t, RewindFileDelete, got[1].Action) // the rest still ran
	assert.Eq(t, SnapshotStatePartial, f.state(t, t1))
	assert.False(t, errors.Is(err, ErrRewindBusy))
}
