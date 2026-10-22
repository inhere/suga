package personal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

func TestSessionSnapshots(t *testing.T) {
	t.Run("stores binary before image and completes metadata", func(t *testing.T) {
		sessions := t.TempDir()
		store := NewSessionSnapshots(sessions)
		ctx := withObserveScope(context.Background(), "sess_1", "turn_1")
		root := t.TempDir()
		before := []byte{0x00, 0xff, 0x0a, 0x80}
		req := native.SnapshotRequest{
			CallID: "call/one", Tool: "file_edit", Root: root,
			Rel: "src/a.bin", RealRel: "src/a.bin", Kind: native.SnapshotKindFile, Before: before,
		}
		result, err := store.BeforeCall(ctx, req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, result.Unrecoverable)

		turnDir := filepath.Join(sessions, "sess_1", "snapshots", "turn_1")
		callDir := snapshotCallDir(t, turnDir)
		got, err := os.ReadFile(filepath.Join(callDir, "before"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, before, got)

		err = store.AfterCall(ctx, native.SnapshotResult{
			CallID: "call/one", Tool: "file_edit", Root: root,
			Rel: "src/a.bin", RealRel: "src/a.bin", Kind: native.SnapshotKindFile,
			AfterSHA256: "after-hash", Size: int64(len(before)), Status: native.SnapshotStatusComplete,
		})
		assert.NoErr(t, err)
		metas, err := store.LoadTurn("sess_1", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, metas, 1))
		meta := metas[0]
		assert.Eq(t, "sess_1", meta.SessionID)
		assert.Eq(t, "turn_1", meta.TurnID)
		assert.Eq(t, "call/one", meta.CallID)
		assert.Eq(t, 1, meta.Seq)
		assert.Eq(t, "src/a.bin", meta.Rel)
		assert.Eq(t, native.SnapshotStatusComplete, meta.Status)
		assert.Eq(t, nativeSHA256(before), meta.SHA256Before)
		assert.Eq(t, "after-hash", meta.SHA256After)
		assert.Eq(t, "before", meta.File)
		assert.Eq(t, "call", meta.Source)

		if runtime.GOOS != "windows" {
			st, err := os.Stat(filepath.Join(sessions, "sess_1", "snapshots"))
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, os.FileMode(0o700), st.Mode().Perm())
			st, err = os.Stat(filepath.Join(callDir, "before"))
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, os.FileMode(0o600), st.Mode().Perm())
		}
	})

	t.Run("keeps absent and repeated calls separate", func(t *testing.T) {
		store := NewSessionSnapshots(t.TempDir())
		ctx := withObserveScope(context.Background(), "sess_2", "turn_2")
		for _, content := range [][]byte{nil, []byte("old")} {
			kind := native.SnapshotKindAbsent
			if content != nil {
				kind = native.SnapshotKindFile
			}
			_, err := store.BeforeCall(ctx, native.SnapshotRequest{CallID: "same", Tool: "file_write", Root: t.TempDir(), Rel: "a", RealRel: "a", Kind: kind, Before: content})
			assert.NoErr(t, err)
			assert.NoErr(t, store.AfterCall(ctx, native.SnapshotResult{CallID: "same", Status: native.SnapshotStatusComplete, Size: int64(len(content))}))
		}
		metas, err := store.LoadTurn("sess_2", "turn_2")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, metas, 2))
		assert.Eq(t, 1, metas[0].Seq)
		assert.Eq(t, native.SnapshotKindAbsent, metas[0].Kind)
		assert.Eq(t, 2, metas[1].Seq)
		assert.Eq(t, native.SnapshotKindFile, metas[1].Kind)
		assert.True(t, strings.Contains(metas[0].CallID, "same"))
	})

	t.Run("hashes call ids instead of using them as path names", func(t *testing.T) {
		sessions := t.TempDir()
		store := NewSessionSnapshots(sessions)
		ctx := withObserveScope(context.Background(), "sess_3", "turn_3")
		_, err := store.BeforeCall(ctx, native.SnapshotRequest{CallID: "../../escape:/absolute", Tool: "file_write", Root: t.TempDir(), Rel: "a", RealRel: "a", Kind: native.SnapshotKindAbsent})
		assert.Require(t, assert.NoErr(t, err))
		callDir := snapshotCallDir(t, filepath.Join(sessions, "sess_3", "snapshots", "turn_3"))
		assert.Eq(t, "001-"+snapshotCallHash("../../escape:/absolute"), filepath.Base(callDir))
		assert.NoErr(t, store.AfterCall(ctx, native.SnapshotResult{CallID: "../../escape:/absolute", Status: native.SnapshotStatusIncomplete}))
	})

	t.Run("records oversize without writing before bytes", func(t *testing.T) {
		sessions := t.TempDir()
		store := NewSessionSnapshotsWithLimits(sessions, SnapshotLimits{MaxFileBytes: 3, MaxSessionBytes: 10, MaxGlobalBytes: 10})
		ctx := withObserveScope(context.Background(), "sess_4", "turn_4")
		before := []byte("four")
		got, err := store.BeforeCall(ctx, native.SnapshotRequest{CallID: "large", Tool: "file_write", Root: t.TempDir(), Rel: "large", RealRel: "large", Kind: native.SnapshotKindFile, Before: before})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, native.SnapshotUnrecoverableOversize, got.Unrecoverable)
		assert.StrContains(t, got.Notice, "不可撤销")
		turnDir := filepath.Join(sessions, "sess_4", "snapshots", "turn_4")
		entries, err := os.ReadDir(turnDir)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, fileExists(filepath.Join(turnDir, entries[0].Name(), "before")))
		assert.NoErr(t, store.AfterCall(ctx, native.SnapshotResult{CallID: "large", Status: native.SnapshotStatusComplete, Unrecoverable: native.SnapshotUnrecoverableOversize, Size: int64(len(before))}))
		metas, err := store.LoadTurn("sess_4", "turn_4")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, native.SnapshotUnrecoverableOversize, metas[0].Unrecoverable)
		assert.Eq(t, int64(4), metas[0].Size)
	})

	t.Run("enforces session and global byte limits", func(t *testing.T) {
		store := NewSessionSnapshotsWithLimits(t.TempDir(), SnapshotLimits{MaxFileBytes: 10, MaxSessionBytes: 3, MaxGlobalBytes: 4})
		ctx := withObserveScope(context.Background(), "sess_5", "turn_5")
		_, err := store.BeforeCall(ctx, native.SnapshotRequest{CallID: "first", Tool: "file_write", Root: t.TempDir(), Rel: "a", RealRel: "a", Kind: native.SnapshotKindFile, Before: []byte("abc")})
		assert.Require(t, assert.NoErr(t, err))
		assert.NoErr(t, store.AfterCall(ctx, native.SnapshotResult{CallID: "first", Status: native.SnapshotStatusComplete}))
		_, err = store.BeforeCall(ctx, native.SnapshotRequest{CallID: "second", Tool: "file_write", Root: t.TempDir(), Rel: "b", RealRel: "b", Kind: native.SnapshotKindFile, Before: []byte("d")})
		assert.ErrMsgContains(t, err, "session limit")

		ctx2 := withObserveScope(context.Background(), "sess_6", "turn_6")
		_, err = store.BeforeCall(ctx2, native.SnapshotRequest{CallID: "third", Tool: "file_write", Root: t.TempDir(), Rel: "c", RealRel: "c", Kind: native.SnapshotKindFile, Before: []byte("de")})
		assert.ErrMsgContains(t, err, "global limit")
	})

	t.Run("missing old session is empty and corrupt metadata is skipped", func(t *testing.T) {
		store := NewSessionSnapshots(t.TempDir())
		metas, err := store.LoadTurn("old", "turn_old")
		assert.NoErr(t, err)
		assert.Empty(t, metas)
		turnDir := filepath.Join(t.TempDir(), "sess", "snapshots", "turn")
		assert.NoErr(t, os.MkdirAll(filepath.Join(turnDir, "001-bad"), 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(turnDir, "001-bad", "meta.json"), []byte("{"), 0o600))
		corrupt := NewSessionSnapshots(filepath.Dir(filepath.Dir(filepath.Dir(turnDir))))
		metas, err = corrupt.LoadTurn("sess", "turn")
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, metas)
		loaded, err := loadTurn(turnDir)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"001-bad"}, loaded.Skipped)
	})

	t.Run("native file_write uses the real snapshot path", func(t *testing.T) {
		workspace, sessions := t.TempDir(), t.TempDir()
		store := NewSessionSnapshots(sessions)
		var tool *native.FileWriteTool
		for _, candidate := range native.BuildTools(native.Env{Workspace: workspace, Snapshots: store}, true) {
			if typed, ok := candidate.(*native.FileWriteTool); ok {
				tool = typed
			}
		}
		assert.Require(t, assert.NotNil(t, tool))
		ctx := withObserveScope(context.Background(), "sess_7", "turn_7")
		_, err := tool.Execute(ctx, api.ToolCall{CallID: "write", Name: "file_write", Arguments: map[string]any{"path": "a.txt", "content": "new"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "new", string(readSnapshotTestFile(t, filepath.Join(workspace, "a.txt"))))
		metas, err := store.LoadTurn("sess_7", "turn_7")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, metas, 1))
		assert.Eq(t, native.SnapshotKindAbsent, metas[0].Kind)
		assert.Eq(t, native.SnapshotStatusComplete, metas[0].Status)
	})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readSnapshotTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	return data
}

func TestSnapshotMetaJSONIsSelfDescribing(t *testing.T) {
	store := NewSessionSnapshots(t.TempDir())
	ctx := withObserveScope(context.Background(), "sess_json", "turn_json")
	_, err := store.BeforeCall(ctx, native.SnapshotRequest{CallID: "c", Tool: "file_write", Root: t.TempDir(), Rel: "a", RealRel: "a", Kind: native.SnapshotKindAbsent})
	assert.Require(t, assert.NoErr(t, err))
	path := filepath.Join(SessionSnapshotsDir(store.dir, "sess_json"), "turn_json", "001-"+snapshotCallHash("c"), "meta.json")
	data, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	var raw map[string]any
	assert.NoErr(t, json.Unmarshal(data, &raw))
	assert.Eq(t, "sess_json", raw["session_id"])
	assert.Eq(t, "absent", raw["kind"])
	assert.Eq(t, "pending", raw["status"])
}

func snapshotCallDir(t *testing.T, turnDir string) string {
	t.Helper()
	entries, err := os.ReadDir(turnDir)
	assert.Require(t, assert.NoErr(t, err))
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != snapshotPreUndoDir {
			return filepath.Join(turnDir, entry.Name())
		}
	}
	t.Fatal("snapshot call directory not found")
	return ""
}

func createCompletedSnapshot(t *testing.T, store *SessionSnapshots, sessionID, turnID, callID string) {
	t.Helper()
	ctx := withObserveScope(context.Background(), sessionID, turnID)
	_, err := store.BeforeCall(ctx, native.SnapshotRequest{
		CallID: callID, Tool: "file_write", Root: t.TempDir(), Rel: callID, RealRel: callID,
		Kind: native.SnapshotKindFile, Before: []byte(callID),
	})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, store.AfterCall(ctx, native.SnapshotResult{
		CallID: callID, Status: native.SnapshotStatusComplete, Size: int64(len(callID)),
	}))
}

func setSnapshotCreatedAt(t *testing.T, store *SessionSnapshots, sessionID, turnID string, at time.Time) {
	t.Helper()
	path := filepath.Join(store.dir, sessionID, "snapshots", turnID)
	callDir := snapshotCallDir(t, path)
	metaPath := filepath.Join(callDir, "meta.json")
	data, err := os.ReadFile(metaPath)
	assert.Require(t, assert.NoErr(t, err))
	var meta SnapshotMeta
	assert.Require(t, assert.NoErr(t, json.Unmarshal(data, &meta)))
	meta.CreatedAt = at
	data, err = json.MarshalIndent(meta, "", "  ")
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, os.WriteFile(metaPath, append(data, '\n'), 0o600))
}

func TestSessionSnapshotsRetentionStateAndPreUndo(t *testing.T) {
	t.Run("retains only the newest completed turns", func(t *testing.T) {
		store := NewSessionSnapshotsWithLimits(t.TempDir(), SnapshotLimits{KeepTurns: 2, KeepDays: 365})
		createCompletedSnapshot(t, store, "keep", "turn_1", "one")
		setSnapshotCreatedAt(t, store, "keep", "turn_1", time.Now().Add(-3*time.Hour))
		createCompletedSnapshot(t, store, "keep", "turn_2", "two")
		setSnapshotCreatedAt(t, store, "keep", "turn_2", time.Now().Add(-2*time.Hour))
		createCompletedSnapshot(t, store, "keep", "turn_3", "three")
		metas, err := store.LoadTurn("keep", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, metas)
		metas, err = store.LoadTurn("keep", "turn_2")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, metas, 1))
		assert.Eq(t, 2, store.Limits().KeepTurns)
	})

	t.Run("age cleanup keeps pending and incomplete turns", func(t *testing.T) {
		store := NewSessionSnapshotsWithLimits(t.TempDir(), SnapshotLimits{KeepTurns: 20, KeepDays: 1})
		createCompletedSnapshot(t, store, "states", "old", "old")
		setSnapshotCreatedAt(t, store, "states", "old", time.Now().Add(-48*time.Hour))
		report, err := store.CleanupSession("states")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, report.RemovedTurns)
		ctx := withObserveScope(context.Background(), "states", "pending")
		_, err = store.BeforeCall(ctx, native.SnapshotRequest{CallID: "pending", Tool: "file_write", Root: t.TempDir(), Rel: "p", RealRel: "p", Kind: native.SnapshotKindFile, Before: []byte("p")})
		assert.Require(t, assert.NoErr(t, err))
		ctx = withObserveScope(context.Background(), "states", "incomplete")
		_, err = store.BeforeCall(ctx, native.SnapshotRequest{CallID: "incomplete", Tool: "file_write", Root: t.TempDir(), Rel: "i", RealRel: "i", Kind: native.SnapshotKindFile, Before: []byte("i")})
		assert.Require(t, assert.NoErr(t, err))
		assert.NoErr(t, store.AfterCall(ctx, native.SnapshotResult{CallID: "incomplete", Status: native.SnapshotStatusIncomplete}))
		old, err := store.LoadTurn("states", "old")
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, old)
		pending, err := store.LoadTurn("states", "pending")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, pending, 1))
		state, err := store.LoadCheckpointState("states", "incomplete")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, SnapshotStatePartial, state.State)
	})

	t.Run("size cleanup removes oldest completed checkpoints", func(t *testing.T) {
		store := NewSessionSnapshotsWithLimits(t.TempDir(), SnapshotLimits{MaxFileBytes: 10, MaxSessionBytes: 4, MaxGlobalBytes: 100, KeepDays: 365})
		createCompletedSnapshot(t, store, "size", "turn_1", "abc")
		createCompletedSnapshot(t, store, "size", "turn_2", "de")
		old, err := store.LoadTurn("size", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, old)

		global := NewSessionSnapshotsWithLimits(t.TempDir(), SnapshotLimits{MaxFileBytes: 10, MaxSessionBytes: 100, MaxGlobalBytes: 100, KeepDays: 365})
		createCompletedSnapshot(t, global, "global_a", "turn_1", "abc")
		createCompletedSnapshot(t, global, "global_b", "turn_1", "def")
		global.limits.MaxGlobalBytes = 4
		report, err := global.CleanupAll()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, report.RemovedTurns)
		remainingA, err := global.LoadTurn("global_a", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		remainingB, err := global.LoadTurn("global_b", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(remainingA)+len(remainingB))
	})

	t.Run("state transitions and pre-undo records are separate", func(t *testing.T) {
		store := NewSessionSnapshots(t.TempDir())
		createCompletedSnapshot(t, store, "undo", "turn_1", "call")
		state, err := store.LoadCheckpointState("undo", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, SnapshotStateOpen, state.State)
		assert.NoErr(t, store.SetCheckpointState("undo", "turn_1", SnapshotStateUndone))
		state, err = store.LoadCheckpointState("undo", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, SnapshotStateUndone, state.State)

		ctx := withObserveScope(context.Background(), "undo", "turn_1")
		_, err = store.BeforeUndo(ctx, native.SnapshotRequest{CallID: "restore", Tool: "undo", Root: t.TempDir(), Rel: "a", RealRel: "a", Kind: native.SnapshotKindFile, Before: []byte("after")})
		assert.Require(t, assert.NoErr(t, err))
		assert.NoErr(t, store.AfterUndo(ctx, native.SnapshotResult{CallID: "restore", Status: native.SnapshotStatusComplete}))
		metas, err := store.LoadTurn("undo", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, metas, 1))
		pre, err := loadTurn(filepath.Join(store.dir, "undo", "snapshots", "turn_1", snapshotPreUndoDir))
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, pre.Metas, 1))
		assert.Eq(t, snapshotSourcePreUndo, pre.Metas[0].Source)
		assert.Eq(t, native.SnapshotStatusComplete, pre.Metas[0].Status)
	})

	t.Run("empty sessions root fails closed", func(t *testing.T) {
		store := NewSessionSnapshots("")
		_, err := store.BeforeCall(withObserveScope(context.Background(), "empty", "turn_1"), native.SnapshotRequest{CallID: "call", Kind: native.SnapshotKindAbsent})
		assert.ErrMsgContains(t, err, "sessions directory is empty")
	})

	t.Run("old sessions without a snapshot directory are empty", func(t *testing.T) {
		store := NewSessionSnapshots(t.TempDir())
		metas, err := store.LoadTurn("legacy", "turn_1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, metas)
		report, err := store.CleanupSession("legacy")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, report.RemovedTurns)
	})
}
