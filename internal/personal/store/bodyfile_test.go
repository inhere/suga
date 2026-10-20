package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// Body file name and the two migration passes (design 2026-10-20, revisions 3-5):
// the assistant pass keeps its own checkpoint, the file name is switched by
// --rename-files only, and both names are read with the sha256 check, so a store
// restored from an old backup with both files still reads the right record.

// bodyFileExists reports whether one of the two file names exists in a session.
func bodyFileExists(t *testing.T, s *Store, sessionID, name string) bool {
	t.Helper()
	path, ok := s.toolBodyPathName(sessionID, name)
	assert.Require(t, assert.True(t, ok))
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	assert.Require(t, assert.NoErr(t, err))
	return true
}

func TestBodyFileNameFollowsTheSwitch(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	appendAll(t, s, "s1", NewEntry{Type: EntryAssistant, JSON: msgJSON(t, bigAssistantMessage("c1"))})
	// A store of the v0.36 shape (no switch row) writes the old name, so a suga of
	// the previous version reading the same home still finds its bodies.
	assert.True(t, bodyFileExists(t, s, "s1", LegacyBodyFileName), "before --rename-files the write uses tools.jsonl")
	assert.False(t, bodyFileExists(t, s, "s1", BodyFileName))

	assert.Require(t, assert.NoErr(t, s.SetBodyFileNameSwitch(ctx, true)))
	createSession(t, s, "s2", "/w")
	appendAll(t, s, "s2", NewEntry{Type: EntryAssistant, JSON: msgJSON(t, bigAssistantMessage("c2"))})
	assert.True(t, bodyFileExists(t, s, "s2", BodyFileName), "after the switch the write uses bodies.jsonl")
	assert.False(t, bodyFileExists(t, s, "s2", LegacyBodyFileName))

	// Both sessions still read back their bodies, one from each name.
	for _, sessionID := range []string{"s1", "s2"} {
		entries, err := s.ListEntries(ctx, sessionID, EntryFilter{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(entries))
		assert.False(t, entries[0].Stub())
	}
}

func TestAssistantMoveAfterTheToolPassFinished(t *testing.T) {
	ctx := context.Background()
	// The rows are written with the threshold off, so they have the historical
	// inline shape an upgrade leaves behind.
	path := filepath.Join(t.TempDir(), "db", "sessions.db")
	inline, err := Open(path, WithToolBodyInlineBytes(0))
	assert.Require(t, assert.NoErr(t, err))
	createSession(t, inline, "s1", "/w")
	appended := appendAll(t, inline, "s1",
		NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c1"))},
		NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c2"))},
		NewEntry{Type: EntryAssistant, JSON: msgJSON(t, bigAssistantMessage("c3"))})
	assert.Require(t, assert.NoErr(t, inline.Close()))
	s, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = s.Close() })

	report, err := s.MoveToolBodies(ctx, ToolBodyMaintOptions{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(2), report.Rows)
	_, toolRef := storedRow(t, s, "s1", appended[0].Seq)
	assert.True(t, toolRef != "", "the tool row moved out")
	_, assistantRef := storedRow(t, s, "s1", appended[2].Seq)
	assert.Eq(t, "", assistantRef, "the tool pass leaves the assistant row alone")

	// The tool pass is complete and its checkpoint sits at the last row: the
	// assistant pass has its own checkpoint and still scans from the start.
	status, err := s.ToolBodyStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, status.MoveComplete, "the tool pass finished")
	assert.Eq(t, int64(1), status.AssistantPendingRows)

	report, err = s.MoveAssistantBodies(ctx, ToolBodyMaintOptions{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(1), report.Rows)
	_, assistantRef = storedRow(t, s, "s1", appended[2].Seq)
	assert.True(t, assistantRef != "", "the assistant row moved out")
}

// TestBodyFilesCoexist: after a restore of an old backup a session can hold both
// names at once. A read must land in the file the stub points at (the digest
// decides), and --check must not report the records of either file as orphans.
func TestBodyFilesCoexist(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	first := msgJSON(t, bigAssistantMessage("c1"))
	appendAll(t, s, "s1", NewEntry{Type: EntryAssistant, JSON: first})
	legacy, ok := s.toolBodyPathName("s1", LegacyBodyFileName)
	assert.Require(t, assert.True(t, ok))
	current, ok := s.toolBodyPathName("s1", BodyFileName)
	assert.Require(t, assert.True(t, ok))
	data, err := os.ReadFile(legacy)
	assert.Require(t, assert.NoErr(t, err))
	// A stale copy under the current name plus a new record in the legacy file:
	// the offsets and digests of the two files do not line up.
	assert.Require(t, assert.NoErr(t, os.WriteFile(current, data, 0o600)))
	second := msgJSON(t, bigAssistantMessage("c2"))
	appendAll(t, s, "s1", NewEntry{Type: EntryAssistant, JSON: second})

	entries, err := s.ListEntries(ctx, "s1", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 2, len(entries))
	assert.Eq(t, string(first), string(entries[0].JSON))
	assert.Eq(t, string(second), string(entries[1].JSON))

	report, err := s.ToolBodyCheck(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(2), report.Checked)
	assert.Eq(t, int64(0), report.Missing)
	assert.Eq(t, int64(0), report.OrphanRecords, "a record of either file counts as used: %v", report.Problems)
}

func TestRenameBodyFiles(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	for _, id := range []string{"s1", "s2"} {
		createSession(t, s, id, "/w")
		appendAll(t, s, id, NewEntry{Type: EntryAssistant, JSON: msgJSON(t, bigAssistantMessage("c-"+id))})
	}
	// The second session already holds a current-name file: --rename-files must
	// not overwrite it.
	current, ok := s.toolBodyPathName("s2", BodyFileName)
	assert.Require(t, assert.True(t, ok))
	legacy, ok := s.toolBodyPathName("s2", LegacyBodyFileName)
	assert.Require(t, assert.True(t, ok))
	legacyData, err := os.ReadFile(legacy)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, os.WriteFile(current, legacyData, 0o600)))

	report, err := s.RenameBodyFiles(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, report.Renamed)
	assert.Eq(t, 1, len(report.Skipped))
	assert.Eq(t, "s2", report.Skipped[0])
	assert.True(t, report.SwitchSet, "the switch is set after the pass")
	assert.True(t, bodyFileExists(t, s, "s1", BodyFileName))
	assert.False(t, bodyFileExists(t, s, "s1", LegacyBodyFileName))

	// A rerun renames nothing and sets the switch again: it resumes and is
	// idempotent.
	report, err = s.RenameBodyFiles(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 0, report.Renamed)
	assert.Eq(t, 1, len(report.Skipped))

	enabled, err := s.bodyFileNameSwitch(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, enabled)
	entries, err := s.ListEntries(ctx, "s1", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.False(t, entries[0].Stub(), "the renamed file still reads")
}

func TestUndoRestoresAssistantBodyBytes(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	body := msgJSON(t, bigAssistantMessage("c1"))
	appended := appendAll(t, s, "s1", NewEntry{Type: EntryAssistant, JSON: body})
	assert.Require(t, assert.NoErr(t, s.SetBodyFileNameSwitch(ctx, true)))
	_, err := s.MoveAssistantBodies(ctx, ToolBodyMaintOptions{})
	assert.Require(t, assert.NoErr(t, err))
	_, ref := storedRow(t, s, "s1", appended[0].Seq)
	assert.True(t, ref != "")

	report, err := s.UndoToolBodies(ctx, ToolBodyMaintOptions{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(1), report.Rows)
	row, ref := storedRow(t, s, "s1", appended[0].Seq)
	assert.Eq(t, "", ref, "the row is inline again")
	assert.Eq(t, string(body), row, "--undo restores the record byte for byte")
}
