package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// `suga data tool-bodies`: move the historical bodies out, undo them byte for
// byte, check the ledger against the files (design 2026-10-19, 0.3).

// inlineFixture fills three sessions with tool bodies above the threshold while
// the store keeps them inline (threshold 0), then opens the same store with the
// default threshold: exactly what an upgraded ledger looks like.
func inlineFixture(t *testing.T) (*Store, map[string]map[int64]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db", "sessions.db")
	inline, err := Open(path, WithToolBodyInlineBytes(0))
	assert.Require(t, assert.NoErr(t, err))
	before := map[string]map[int64]string{}
	for _, id := range []string{"s1", "s2", "s3"} {
		createSession(t, inline, id, "/w")
		for n := 0; n < 3; n++ {
			msg := bigToolMessage("c" + id + string(rune('0'+n)))
			_, err := inline.AppendEntries(context.Background(), id,
				user(t, "run"), NewEntry{Type: EntryTool, JSON: msgJSON(t, msg)})
			assert.Require(t, assert.NoErr(t, err))
		}
		// A small tool result never moves.
		_, err := inline.AppendEntries(context.Background(), id, NewEntry{Type: EntryTool, JSON: msgJSON(t, toolMessage())})
		assert.Require(t, assert.NoErr(t, err))
		before[id] = storedEntries(t, inline, id)
	}
	assert.Require(t, assert.NoErr(t, inline.Close()))
	s, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = s.Close() })
	return s, before
}

func TestToolBodiesMoveUndoCheckStatus(t *testing.T) {
	ctx := context.Background()
	s, before := inlineFixture(t)

	status, err := s.ToolBodyStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), status.PendingRows)
	assert.Eq(t, int64(0), status.MovedRows)
	assert.True(t, status.MoveEnabled)

	// --move: two rows per transaction, so the pass takes several batches.
	report, err := s.MoveToolBodies(ctx, ToolBodyMaintOptions{Batch: 2})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), report.Rows)
	assert.True(t, report.Batches >= 4, "the batch size must be honored")
	assert.True(t, report.Complete)

	t.Run("reads are unchanged after the move", func(t *testing.T) {
		for id, want := range before {
			assert.Eq(t, want, storedEntries(t, s, id), id)
		}
	})

	t.Run("status reports the moved rows and files", func(t *testing.T) {
		status, err := s.ToolBodyStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(0), status.PendingRows)
		assert.Eq(t, int64(9), status.MovedRows)
		assert.Eq(t, 3, status.Files)
		assert.True(t, status.FileBytes > 0)
	})

	t.Run("check verifies every stub", func(t *testing.T) {
		check, err := s.ToolBodyCheck(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(9), check.Checked)
		assert.Eq(t, int64(0), check.Missing)
		assert.Eq(t, int64(0), check.OrphanRecords)
		assert.True(t, check.OK())
	})

	t.Run("an orphan record is reported and the stub still reads", func(t *testing.T) {
		// A crash before the commit leaves a record no stub points at.
		writer, err := s.openToolBodyWriter("s1")
		assert.Require(t, assert.NoErr(t, err))
		_, err = writer.WriteRecord(99, []byte(`{"Role":"tool","Content":"orphan"}`))
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.NoErr(t, writer.Close(true)))
		check, err := s.ToolBodyCheck(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(1), check.OrphanRecords)
		assert.True(t, check.OK(), "an orphan is not a missing body")
		assert.Eq(t, before["s1"], storedEntries(t, s, "s1"))
	})

	t.Run("--move is idempotent and keeps the small rows inline", func(t *testing.T) {
		again, err := s.MoveToolBodies(ctx, ToolBodyMaintOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(0), again.Rows)
	})

	t.Run("--undo restores the original bytes and stops new moves", func(t *testing.T) {
		report, err := s.UndoToolBodies(ctx, ToolBodyMaintOptions{Batch: 4})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(9), report.Rows)
		for id, want := range before {
			assert.Eq(t, want, storedEntries(t, s, id), id)
		}
		var stubs int
		assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE COALESCE(body_ref, '') <> ''`).Scan(&stubs))
		assert.Eq(t, 0, stubs)
		// The switch stays off: a new tool body is stored inline.
		moved := appendAll(t, s, "s1", NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("later"))})
		var ref string
		assert.NoErr(t, s.db.QueryRow(`SELECT COALESCE(body_ref, '') FROM entries WHERE session_id = 's1' AND seq = ?`, moved[0].Seq).Scan(&ref))
		assert.Eq(t, "", ref)
	})

	t.Run("--remove-files drops the files of sessions without stubs", func(t *testing.T) {
		removed, err := s.RemoveToolBodyFiles(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 3, removed)
		status, err := s.ToolBodyStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, status.Files)
	})
}

// TestToolBodiesMoveSkipsExistingStubs: a rerun from scratch (--abort then move)
// must not move a stub again, even when the threshold is smaller than a stub: the
// row would otherwise be replaced by a stub of a stub and lose its body.
func TestToolBodiesMoveSkipsExistingStubs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db", "sessions.db")
	inline, err := Open(path, WithToolBodyInlineBytes(0))
	assert.Require(t, assert.NoErr(t, err))
	createSession(t, inline, "s1", "/w")
	appendAll(t, inline, "s1", NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c1"))})
	before := storedEntries(t, inline, "s1")
	assert.NoErr(t, inline.Close())

	tiny, err := Open(path, WithToolBodyInlineBytes(1))
	assert.Require(t, assert.NoErr(t, err))
	defer tiny.Close()
	first, err := tiny.MoveToolBodies(ctx, ToolBodyMaintOptions{Batch: 10})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(1), first.Rows)
	file := filepath.Join(tiny.sessionsDir, "s1", LegacyBodyFileName)
	info, err := os.Stat(file)
	assert.Require(t, assert.NoErr(t, err))

	assert.NoErr(t, tiny.ResetToolBodyState(ctx))
	second, err := tiny.MoveToolBodies(ctx, ToolBodyMaintOptions{Batch: 10})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(0), second.Rows, "a stub is not a body to move")
	again, err := os.Stat(file)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, info.Size(), again.Size(), "the file got no second record")
	assert.Eq(t, before, storedEntries(t, tiny, "s1"))
}

// TestToolBodiesAreInvisibleToDedupe: a moved row is a stub, so the P2 passes
// must leave it alone and must not turn its preview into a deduped row.
func TestToolBodiesAreInvisibleToDedupe(t *testing.T) {
	ctx := context.Background()
	s, before := inlineFixture(t)
	report, err := s.MoveToolBodies(ctx, ToolBodyMaintOptions{Batch: 5})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), report.Rows)

	_, err = s.DedupeToolResults(ctx, ToolDedupeOptions{})
	assert.Require(t, assert.NoErr(t, err))
	_, err = s.UndoToolDedupe(ctx, ToolDedupeOptions{})
	assert.Require(t, assert.NoErr(t, err))
	for id, want := range before {
		assert.Eq(t, want, storedEntries(t, s, id), id)
	}
	// The stubs are still stubs and carry no dedupe marker.
	var marked int
	assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE COALESCE(body_ref, '') <> ''
		AND json LIKE '%ResultSameAsContent%'`).Scan(&marked))
	assert.Eq(t, 0, marked)
	check, err := s.ToolBodyCheck(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), check.Checked)
	assert.True(t, check.OK())
}

// TestDeleteSessionFilesRemovesTheSessionDirectory: a deleted session must not
// leave its tools.jsonl behind (the catalogs call this after DeleteSession, data
// gc removes the whole directory itself).
func TestDeleteSessionFilesRemovesTheSessionDirectory(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	appendAll(t, s, "s1", NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c1"))})
	dir := filepath.Join(s.sessionsDir, "s1")
	_, err := os.Stat(filepath.Join(dir, LegacyBodyFileName))
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, s.DeleteSessionFiles("s1"))
	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "the session directory is gone")
	// An id that cannot name a directory is skipped, not an error.
	assert.NoErr(t, s.DeleteSessionFiles("../escape"))
}

// TestToolBodiesResumeAfterInterrupt: a cancelled pass keeps its checkpoint, the
// rerun continues there and the ledger ends up complete.
func TestToolBodiesResumeAfterInterrupt(t *testing.T) {
	s, before := inlineFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := s.MoveToolBodies(ctx, ToolBodyMaintOptions{Batch: 2, OnBatch: func(p ToolBodyMaintProgress) {
		if p.Rows >= 4 {
			cancel()
		}
	}})
	assert.True(t, err != nil, "the cancelled pass reports the context error")
	status, err := s.ToolBodyStatus(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, status.MoveStarted, "the interrupted pass keeps its checkpoint")
	// The rerun starts from the checkpoint and finishes the remaining rows.
	report, err := s.MoveToolBodies(context.Background(), ToolBodyMaintOptions{Batch: 2})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, report.Complete)
	check, err := s.ToolBodyCheck(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), check.Checked)
	for id, want := range before {
		assert.Eq(t, want, storedEntries(t, s, id), id)
	}
}

// TestMoveToolBodiesRestartsAfterComplete: a move pass that completed sits on the
// last row; a rerun must scan from the start again so it sees the inline rows
// written since it finished (a resumed cursor would hide them, suga-rykn).
func TestMoveToolBodiesRestartsAfterComplete(t *testing.T) {
	s, _ := inlineFixture(t) // MoveToolBodies has not run yet in this fixture
	ctx := context.Background()
	first, err := s.MoveToolBodies(ctx, ToolBodyMaintOptions{Batch: 8})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, first.Complete)
	assert.Eq(t, int64(9), first.Rows)

	// A tool row written after the finish moves out at append (the switch is on);
	// one the finished pass never saw is restored back to the inline shape (full
	// json, no body_ref), at a key before the finished cursor ("a0" sorts before
	// "s1"): a pass that resumes at the cursor s3/6 would never see it.
	createSession(t, s, "a0", "/w")
	big := msgJSON(t, bigToolMessage("cNew"))
	appended := appendAll(t, s, "a0", NewEntry{Type: EntryTool, JSON: big})
	_, err = s.db.Exec(`UPDATE entries SET json = ?, body_ref = NULL WHERE session_id = ? AND seq = ?`,
		string(big), "a0", appended[0].Seq)
	assert.Require(t, assert.NoErr(t, err))

	again, err := s.MoveToolBodies(ctx, ToolBodyMaintOptions{Batch: 8})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, again.Complete)
	assert.Eq(t, int64(1), again.Rows, "the rerun scans from the start and moves the new row")
	_, ref := storedRow(t, s, "a0", appended[0].Seq)
	assert.True(t, ref != "", "the rerun moved the new row out")
}
