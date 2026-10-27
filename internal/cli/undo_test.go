package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
)

func TestCLIRegistersUndoAndListsWithoutProvider(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	ctx := context.Background()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "cli-undo-session", Status: store.SessionOpen}))
	assert.NoErr(t, repo.Close())

	var stdout, stderr bytes.Buffer
	err = newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"--help"})
	assert.NoErr(t, err)
	// The root help names the group the undo command moved into.
	assert.StrContains(t, stdout.String(), "session")

	stdout.Reset()
	err = newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"session", "undo", "cli-undo", "--list", "--json"})
	assert.NoErr(t, err)
	var rows []map[string]any
	assert.NoErr(t, json.Unmarshal(stdout.Bytes(), &rows))
	assert.Eq(t, 0, len(rows))
}

func TestUndoCLISafetyAndExitCodes(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	ctx := context.Background()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "cli-undo-safe", Status: store.SessionOpen}))
	assert.NoErr(t, repo.Close())

	t.Run("non-terminal destructive operation needs yes", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := newCLI(ctx, strings.NewReader("y\n"), &stdout, &stderr).RunWithArgs([]string{"session", "undo", "cli-undo-safe"})
		assert.ErrMsgContains(t, err, "requires --yes")
		assert.Eq(t, 1, ExitCode(err))
	})
	t.Run("force has the same confirmation requirement", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := newCLI(ctx, strings.NewReader("y\n"), &stdout, &stderr).RunWithArgs([]string{"session", "undo", "cli-undo-safe", "--force"})
		assert.ErrMsgContains(t, err, "requires --yes")
	})
	t.Run("empty checkpoint set has exit code three", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"session", "undo", "cli-undo-safe", "--yes"})
		assert.Eq(t, 3, ExitCode(err))
	})
	t.Run("busy session has exit code five", func(t *testing.T) {
		lock, err := personalprocess.AcquireSessionLock(filepath.Join(home, "run", "locks"), "cli-undo-safe", 0)
		assert.Require(t, assert.NoErr(t, err))
		defer lock.Close()
		var stdout, stderr bytes.Buffer
		err = newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"session", "undo", "cli-undo-safe", "--yes"})
		assert.Eq(t, 5, ExitCode(err))
	})
}

// TestRenderUndoPlanListsConflicts: the CLI plan, like chat's, shows a target
// changed after its snapshot as skipped, or restored with --force.
func TestRenderUndoPlanListsConflicts(t *testing.T) {
	rows := []personal.Checkpoint{{TurnID: "turn-1", Items: []personal.CheckpointItem{{
		Meta: personal.SnapshotMeta{Rel: "a.txt", Size: 4}, Action: personal.CheckpointActionRestore, Conflict: true}}}}
	t.Run("default skips", func(t *testing.T) {
		got := renderUndoPlan(rows, personal.UndoOptions{Count: 1})
		assert.StrContains(t, got, "跳过 a.txt")
		assert.StrContains(t, got, "冲突：快照之后被改动过")
	})
	t.Run("force restores", func(t *testing.T) {
		got := renderUndoPlan(rows, personal.UndoOptions{Count: 1, Force: true})
		assert.StrContains(t, got, "恢复 a.txt")
	})
}
