package personal

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
)

// TestPathTallyLastEntryWins: the entry executed last decides a path's outcome
// and the counts and path lists count each path once (suga-y027).
func TestPathTallyLastEntryWins(t *testing.T) {
	tally := newPathTally()
	tally.add(RewindFileItem{Path: "a", Action: RewindFileRestore})
	tally.add(RewindFileItem{Path: "b", Action: RewindFileDelete})
	tally.add(RewindFileItem{Path: "a", Action: RewindFileRestore})
	tally.add(RewindFileItem{Path: "c", Action: RewindFileRestore})
	tally.add(RewindFileItem{Path: "c", Action: RewindFileConflict, Reason: rewindReasonUnchanged})
	assert.Eq(t, 1, tally.count(RewindFileRestore))
	assert.Eq(t, 1, tally.count(RewindFileDelete))
	assert.Eq(t, 1, tally.count(RewindFileConflict))
	assert.Eq(t, []string{"a"}, tally.paths(RewindFileRestore))
	assert.Eq(t, []string{"a", "b"}, tally.paths(RewindFileRestore, RewindFileDelete))

	rows := DedupeRewindFiles([]RewindFileItem{
		{Path: "x", Action: RewindFileConflict, Reason: rewindReasonContent},
		{Path: "y", Action: RewindFileRestore},
		{Path: "x", Action: RewindFileConflict, Reason: rewindReasonCascade},
	})
	assert.Require(t, assert.Len(t, rows, 2))
	assert.Eq(t, "x", rows[0].Path)
	assert.Eq(t, rewindReasonCascade, rows[0].Reason)
	assert.Eq(t, "y", rows[1].Path)
}

// TestUndoCountsOnePathOnce: two writes to one path in one turn give one
// restored file in the result, the model notice and the event, while the
// per-entry state is untouched.
func TestUndoCountsOnePathOnce(t *testing.T) {
	f := newUndoRestoreFixture(t, "zero")
	f.recordFileWrite(t, "call-1", "one")
	f.recordFileWrite(t, "call-2", "two")
	result, err := f.core.Undo(f.ctx, "undo-restore", 1, false)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, result.Restored)
	assert.Eq(t, []string{f.path}, result.Files)
	notices := pendingNotices(t, f)
	assert.Require(t, assert.Len(t, notices, 1))
	assert.StrContains(t, notices[0].Text, "已恢复 1 个文件")
	assert.Eq(t, 1, notices[0].Restored)
	assert.Eq(t, []string{f.path}, notices[0].Files)
	events := eventsOf(t, f.repo, "undo-restore", store.EventUndo)
	assert.Require(t, assert.Len(t, events, 1))
	assert.Eq(t, float64(1), events[0].Attrs["restored"])
	assert.Len(t, events[0].Attrs["files"], 1)
}

func pendingNotices(t *testing.T, f undoRestoreFixture) []pendingUndoNotice {
	t.Helper()
	s, err := f.repo.GetSession(f.ctx, "undo-restore")
	assert.Require(t, assert.NoErr(t, err))
	q, err := decodeUndoNotices(s.Meta[undoNoticeQueueKey])
	assert.Require(t, assert.NoErr(t, err))
	return q
}
