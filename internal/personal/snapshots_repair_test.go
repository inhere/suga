package personal

import (
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// TestLoadTurnSkipsDamagedRecords covers a record directory whose meta.json is
// missing or corrupt: it must be skipped and reported, never returned as an
// error that hides the healthy records of the same turn.
func TestLoadTurnSkipsDamagedRecords(t *testing.T) {
	t.Run("missing meta.json", func(t *testing.T) {
		_, turnDir := snapshotFixture(t, "sess_skip", "turn_skip")
		assert.NoErr(t, os.MkdirAll(filepath.Join(turnDir, "002-empty"), 0o700))

		loaded, err := loadTurn(turnDir)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, loaded.Metas, 1))
		assert.Eq(t, "first", loaded.Metas[0].CallID)
		assert.Eq(t, []string{"002-empty"}, loaded.Skipped)
	})

	t.Run("corrupt meta.json", func(t *testing.T) {
		_, turnDir := snapshotFixture(t, "sess_bad", "turn_bad")
		assert.NoErr(t, os.MkdirAll(filepath.Join(turnDir, "002-bad"), 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(turnDir, "002-bad", "meta.json"), []byte("{"), 0o600))

		loaded, err := loadTurn(turnDir)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, loaded.Metas, 1))
		assert.Eq(t, []string{"002-bad"}, loaded.Skipped)
	})

	t.Run("cleanup reports skipped records and keeps the store usable", func(t *testing.T) {
		store, turnDir := snapshotFixture(t, "sess_report", "turn_report")
		assert.NoErr(t, os.MkdirAll(filepath.Join(turnDir, "002-empty"), 0o700))

		report, err := store.CleanupSession("sess_report")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, report.SkippedRecords)

		// The healthy record is still readable and a new call still snapshots.
		ctx := withObserveScope(context.Background(), "sess_report", "turn_report")
		_, err = store.BeforeCall(ctx, native.SnapshotRequest{CallID: "second", Tool: "file_write", Kind: native.SnapshotKindAbsent})
		assert.Require(t, assert.NoErr(t, err))
		assert.NoErr(t, store.AfterCall(ctx, native.SnapshotResult{CallID: "second", Status: native.SnapshotStatusComplete, Size: 3}))
		metas, err := store.LoadTurn("sess_report", "turn_report")
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, metas, 2)
	})
}

// TestSnapshotRecordPublishIsAtomic checks the write order: a call directory
// only becomes visible after its meta.json is on disk, so a concurrent reader
// never sees a half-written record.
func TestSnapshotRecordPublishIsAtomic(t *testing.T) {
	store, turnDir := snapshotFixture(t, "sess_atomic", "turn_atomic")
	ctx := withObserveScope(context.Background(), "sess_atomic", "turn_atomic")

	var writing atomic.Bool
	writing.Store(true)
	done := make(chan error, 1)
	go func() {
		defer writing.Store(false)
		for i := 0; i < 30; i++ {
			callID := fmt.Sprintf("call-%d", i)
			if _, err := store.BeforeCall(ctx, native.SnapshotRequest{CallID: callID, Tool: "file_write", Kind: native.SnapshotKindAbsent}); err != nil {
				done <- err
				return
			}
			if err := store.AfterCall(ctx, native.SnapshotResult{CallID: callID, Status: native.SnapshotStatusComplete}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	for writing.Load() {
		entries, err := os.ReadDir(turnDir)
		assert.Require(t, assert.NoErr(t, err))
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == snapshotPreUndoDir {
				continue
			}
			if strings.HasPrefix(entry.Name(), snapshotPartialPrefix) {
				continue // in progress, not published yet
			}
			_, statErr := os.Stat(filepath.Join(turnDir, entry.Name(), "meta.json"))
			assert.Require(t, assert.NoErr(t, statErr))
		}
	}
	assert.Require(t, assert.NoErr(t, <-done))

	entries, err := os.ReadDir(turnDir)
	assert.Require(t, assert.NoErr(t, err))
	published := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), snapshotPartialPrefix) {
			t.Fatalf("published call left a partial directory behind: %s", entry.Name())
		}
		published++
	}
	assert.Eq(t, 31, published) // the fixture record plus the 30 written above
}

// TestSnapshotConcurrentDamageAndWrite runs a writer against a tree that another
// goroutine keeps damaging (record directories without metadata) while a reader
// keeps reading. Reads and writes must stay free of errors, which is what the
// session lost while loadTurn failed as a whole.
func TestSnapshotConcurrentDamageAndWrite(t *testing.T) {
	store, turnDir := snapshotFixture(t, "sess_race", "turn_race")
	writerCtx := withObserveScope(context.Background(), "sess_race", "turn_race")
	// A second store on the same root stands in for the cleanup another process
	// runs: the two do not share s.mu.
	other := NewSessionSnapshots(store.dir)

	var damage sync.WaitGroup
	damage.Add(1)
	go func() {
		defer damage.Done()
		for i := 0; i < 20; i++ {
			// A half-written record of a concurrent writer: a directory whose
			// metadata has not been published yet.
			if err := os.MkdirAll(filepath.Join(turnDir, fmt.Sprintf("%03d-empty", 900+i)), 0o700); err != nil {
				return
			}
		}
	}()

	cleanupErr := make(chan error, 1)
	var clearing sync.WaitGroup
	clearing.Add(1)
	go func() {
		defer clearing.Done()
		for i := 0; i < 10; i++ {
			if _, err := other.CleanupSession("sess_race"); err != nil && !snapshotPathGone(err) {
				cleanupErr <- err
				return
			}
		}
	}()

	for i := 0; i < 20; i++ {
		callID := fmt.Sprintf("race-%d", i)
		_, err := store.BeforeCall(writerCtx, native.SnapshotRequest{CallID: callID, Tool: "file_write", Kind: native.SnapshotKindAbsent})
		assert.Require(t, assert.NoErr(t, err))
		assert.NoErr(t, store.AfterCall(writerCtx, native.SnapshotResult{CallID: callID, Status: native.SnapshotStatusComplete}))

		metas, err := store.LoadTurn("sess_race", "turn_race")
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, len(metas) >= 1)
	}
	damage.Wait()
	clearing.Wait()
	select {
	case err := <-cleanupErr:
		t.Fatalf("concurrent cleanup failed on damaged records: %v", err)
	default:
	}

	// The tree is damaged on purpose; one more call must still work, which is the
	// "a single read failure only degrades that call" half of the report.
	_, err := store.BeforeCall(writerCtx, native.SnapshotRequest{CallID: "after-damage", Tool: "file_write", Kind: native.SnapshotKindAbsent})
	assert.Require(t, assert.NoErr(t, err))
	report, err := store.CleanupSession("sess_race")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 20, report.SkippedRecords)
}

// TestCleanupPrunesStalePartialDirs covers the repair path for the directories a
// killed writer leaves behind: only the stale ones go, a live writer's partial
// directory survives.
func TestCleanupPrunesStalePartialDirs(t *testing.T) {
	store, turnDir := snapshotFixture(t, "sess_partial", "turn_partial")
	stale := filepath.Join(turnDir, snapshotPartialPrefix+"004-"+snapshotCallHash("old")+"-1-1")
	live := filepath.Join(turnDir, snapshotPartialPrefix+"005-"+snapshotCallHash("live")+"-2-2")
	for _, dir := range []string{stale, live} {
		assert.NoErr(t, os.MkdirAll(dir, 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "before"), []byte("x"), 0o600))
	}
	old := time.Now().Add(-2 * snapshotPartialStaleAfter)
	assert.NoErr(t, os.Chtimes(stale, old, old))

	report, err := store.CleanupSession("sess_partial")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, report.RepairedPartials)
	assert.False(t, fileExists(stale))
	assert.True(t, fileExists(live))
}

// TestCleanupRepairsPublishedRecordsWithoutMeta covers the leftover of the
// 2026-10-22 tree: a published call directory whose meta.json never reached the
// disk is useless for a restore, so cleanup removes it once it is old enough.
func TestCleanupRepairsPublishedRecordsWithoutMeta(t *testing.T) {
	store, turnDir := snapshotFixture(t, "sess_repair", "turn_repair")
	stale := filepath.Join(turnDir, "002-empty")
	fresh := filepath.Join(turnDir, "003-empty")
	for _, dir := range []string{stale, fresh} {
		assert.NoErr(t, os.MkdirAll(dir, 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "before"), []byte("x"), 0o600))
	}
	old := time.Now().Add(-2 * snapshotPartialStaleAfter)
	assert.NoErr(t, os.Chtimes(stale, old, old))

	report, err := store.CleanupSession("sess_repair")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 2, report.SkippedRecords)
	assert.Eq(t, 1, report.RepairedPartials)
	assert.False(t, fileExists(stale))
	assert.True(t, fileExists(fresh))
}

// snapshotPathGone reports whether err only says that a path a concurrent
// cleanup removed is gone. A metadata error must still fail a test.
func snapshotPathGone(err error) bool {
	if strings.Contains(err.Error(), "meta.json") {
		return false
	}
	return stderrors.Is(err, fs.ErrNotExist)
}

// snapshotFixture creates a store with one completed call record and returns the
// turn directory holding it.
func snapshotFixture(t *testing.T, sessionID, turnID string) (*SessionSnapshots, string) {
	t.Helper()
	sessions := t.TempDir()
	store := NewSessionSnapshots(sessions)
	ctx := withObserveScope(context.Background(), sessionID, turnID)
	_, err := store.BeforeCall(ctx, native.SnapshotRequest{
		CallID: "first", Tool: "file_write", Root: t.TempDir(), Rel: "a", RealRel: "a",
		Kind: native.SnapshotKindFile, Before: []byte("old"),
	})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, store.AfterCall(ctx, native.SnapshotResult{CallID: "first", Status: native.SnapshotStatusComplete, Size: 3}))
	return store, filepath.Join(sessions, sessionID, "snapshots", turnID)
}
