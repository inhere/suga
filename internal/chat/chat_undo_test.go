package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

func TestParseUndoCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		line  string
		want  undoCommand
		valid bool
	}{
		{name: "default", line: ":undo", want: undoCommand{n: 1}, valid: true},
		{name: "count", line: ":undo 3", want: undoCommand{n: 3}, valid: true},
		{name: "force", line: ":undo force", want: undoCommand{n: 1, force: true}, valid: true},
		{name: "count and force", line: ":undo 2 force", want: undoCommand{n: 2, force: true}, valid: true},
		{name: "force and count", line: ":undo force 2", want: undoCommand{n: 2, force: true}, valid: true},
		{name: "zero", line: ":undo 0", valid: false},
		{name: "path is not accepted", line: ":undo a.txt", valid: false},
		{name: "duplicate force", line: ":undo force force", valid: false},
		{name: "other command", line: ":checkpoints", valid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := parseUndoCommand(tc.line)
			assert.Eq(t, tc.valid, valid)
			if valid {
				assert.Eq(t, tc.want, got)
			}
		})
	}
}

func TestBuildCheckpointTextCleansExternalValues(t *testing.T) {
	when := time.Date(2026, 10, 17, 12, 34, 0, 0, time.UTC)
	row := personal.Checkpoint{
		TurnID: "turn-\x1b[2J-one", StartedAt: when, Calls: 2, Files: 1, Bytes: 2048,
		Items: []personal.CheckpointItem{{
			Meta:   personal.SnapshotMeta{Rel: "src/\x1b]52;c;bad\a.go", Size: 2048},
			Action: personal.CheckpointActionSkip, Reason: "冲突\x1b[31m",
		}},
		Shell: []personal.ShellCommand{{Command: "echo bad\x1b[2J", TaskID: "task\x00-1"}},
	}
	got := BuildCheckpoints(testTranslator, []personal.Checkpoint{row})
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\x00")
	assert.Contains(t, got, "turn--one")
	assert.Contains(t, got, "src/.go")
	assert.Contains(t, got, "shell_exec：echo bad")
}

func TestBuildUndoPlanShowsShellBoundary(t *testing.T) {
	row := personal.Checkpoint{
		TurnID: "turn-1",
		Items:  []personal.CheckpointItem{{Meta: personal.SnapshotMeta{Rel: "a.txt", Size: 4}, Action: personal.CheckpointActionRestore}},
		Shell:  []personal.ShellCommand{{Command: "git reset --hard", TaskID: "bg-1"}},
	}
	got := BuildUndoPlan(testTranslator, []personal.Checkpoint{row}, undoCommand{n: 1})
	assert.Contains(t, got, "恢复 a.txt")
	assert.Contains(t, got, "shell_exec：git reset --hard")
	assert.Contains(t, got, "改动不在快照范围内")
	assert.Contains(t, got, "task bg-1")
}

func TestUndoConfirmationDefaultsToCancel(t *testing.T) {
	assert.False(t, undoConfirmed(""))
	assert.False(t, undoConfirmed("n"))
	assert.True(t, undoConfirmed(" y "))
	assert.True(t, undoConfirmed("YES"))
}

func TestChatPromptUndoCommandsDoNotStartATurn(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("answer")))
	defer repo.Close()
	var stdout, stderr strings.Builder
	err := Run(context.Background(), runner, Options{}, strings.NewReader(":checkpoints\n:undo\n:quit\n"), &stdout, &stderr, nil)
	assert.NoErr(t, err)
	assert.Contains(t, stdout.String(), testTranslator.T(undoNoHintKey))
	assert.NotContains(t, stdout.String(), "Error: prompt is required")
}

func TestRunningTurnKeepsColonCommandOutOfSteering(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	done := h.startTurn()
	<-h.cast.started()

	h.kb.Type(":undo\r")
	waitUntil(t, "local command busy hint", func() bool { return strings.Contains(h.out.String(), zhUndoBusyHint) })
	assert.Eq(t, ":undo", h.turnInputLine(t))

	h.cast.letGo()
	assert.NoErr(t, <-done)
	assert.Eq(t, []string{"干活"}, h.cast.prompts())
}

// TestBuildUndoPlanListsConflicts: a target changed after its snapshot is shown
// as skipped with the conflict reason before the confirmation; with force it is
// restored and still marked as a conflict.
func TestBuildUndoPlanListsConflicts(t *testing.T) {
	row := personal.Checkpoint{
		TurnID: "turn-1",
		Items: []personal.CheckpointItem{{Meta: personal.SnapshotMeta{Rel: "a.txt", Size: 4},
			Action: personal.CheckpointActionRestore, Conflict: true}},
	}
	t.Run("default skips the conflict", func(t *testing.T) {
		got := BuildUndoPlan(testTranslator, []personal.Checkpoint{row}, undoCommand{n: 1})
		assert.Contains(t, got, "跳过 a.txt")
		assert.Contains(t, got, "冲突：快照之后被改动过")
	})
	t.Run("force restores it", func(t *testing.T) {
		got := BuildUndoPlan(testTranslator, []personal.Checkpoint{row}, undoCommand{n: 1, force: true})
		assert.Contains(t, got, "恢复 a.txt")
		assert.Contains(t, got, "冲突：快照之后被改动过")
	})
}
