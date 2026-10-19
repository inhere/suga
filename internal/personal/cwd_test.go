package personal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// TestSameDir covers the comparison the front ends make before a session is
// continued: symlinks and relative spellings of the same directory are equal,
// different directories are not, and an unresolvable path still compares.
func TestSameDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "work")
	assert.NoErr(t, os.MkdirAll(dir, 0o755))

	t.Run("spellings of one directory", func(t *testing.T) {
		t.Chdir(base)
		assert.Eq(t, true, SameDir(dir, filepath.Join("work")))          // relative vs absolute
		assert.Eq(t, true, SameDir(dir, dir+string(filepath.Separator))) // trailing separator
		assert.Eq(t, true, SameDir(dir, filepath.Join(dir, ".", "..", "work")))
		assert.Eq(t, false, SameDir(dir, base))
		assert.Eq(t, false, SameDir(dir, filepath.Join(base, "work2")))
	})

	t.Run("empty directory never matches", func(t *testing.T) {
		assert.Eq(t, false, SameDir("", dir))
		assert.Eq(t, false, SameDir(dir, "   "))
		assert.Eq(t, true, SameDir("", ""))
	})

	t.Run("a symbolic link names its target", func(t *testing.T) {
		link := filepath.Join(base, "link")
		if err := os.Symlink(dir, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		assert.Eq(t, true, SameDir(link, dir))
		assert.Eq(t, NormalizeDir(dir), NormalizeDir(link))
	})

	t.Run("a directory that is gone still compares by name", func(t *testing.T) {
		gone := filepath.Join(base, "gone")
		assert.Eq(t, gone, NormalizeDir(gone))
		assert.Eq(t, true, SameDir(gone, gone))
		assert.Eq(t, false, SameDir(gone, dir))
	})

	t.Run("the windows rule folds case on paths that do not resolve", func(t *testing.T) {
		// EvalSymlinks already canonicalizes an existing path (and a file system
		// that ignores case resolves both spellings to one directory), so the
		// folding matters for the fallback form: a recorded directory that is
		// gone keeps the spelling it was recorded with.
		lower, upper := filepath.Join(base, "gone-dir"), filepath.Join(base, "GONE-DIR")
		assert.Eq(t, true, sameDir(lower, upper, true))
		assert.Eq(t, false, sameDir(lower, upper, false))
	})
}

// TestTraceSessionCwdLine: `suga inspect trace` names both directories of a session
// that was continued elsewhere; the other session events keep their own parts.
func TestTraceSessionCwdLine(t *testing.T) {
	line := traceLine(store.Event{Kind: store.EventSession, Name: sessionCwdChangedEvent, Status: eventOK,
		Attrs: map[string]any{"from": "/first/dir", "to": "/second/dir"}})
	assert.StrContains(t, line, "session session_cwd_changed ok 0ms from=/first/dir to=/second/dir")

	t.Run("a recovery line stays without from/to", func(t *testing.T) {
		line := traceLine(store.Event{Kind: store.EventSession, Name: recoveredEventName, Status: eventOK, DurationMs: 5,
			Attrs: map[string]any{"previous_status": store.SessionRunning}})
		assert.Eq(t, false, strings.Contains(line, "from="))
	})
}

// TestAdoptSessionCwd: continuing a session elsewhere moves its recorded
// directory and leaves a trace event naming both directories.
func TestAdoptSessionCwd(t *testing.T) {
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(mock.Final("ok")), Workspace: "/first/dir"})
	assert.NoErr(t, err)
	ctx := context.Background()
	sess, err := runner.NewSession(ctx, "cwd-1", "cwd")
	assert.NoErr(t, err)

	cwd, err := runner.SessionCwd(ctx, sess.ID)
	assert.NoErr(t, err)
	assert.Eq(t, "/first/dir", cwd)

	t.Run("a move updates the session and is recorded", func(t *testing.T) {
		from, err := runner.AdoptSessionCwd(ctx, sess.ID, "/second/dir")
		assert.NoErr(t, err)
		assert.Eq(t, "/first/dir", from)
		stored, err := repo.GetSession(ctx, sess.ID)
		assert.NoErr(t, err)
		assert.Eq(t, "/second/dir", stored.Cwd)

		events := eventsOf(t, repo, sess.ID, store.EventSession)
		assert.Require(t, assert.Eq(t, 1, len(events)))
		assert.Eq(t, sessionCwdChangedEvent, events[0].Name)
		assert.Eq(t, eventOK, events[0].Status)
		assert.Eq(t, "/first/dir", events[0].Attrs["from"])
		assert.Eq(t, "/second/dir", events[0].Attrs["to"])
	})

	t.Run("the same directory changes nothing", func(t *testing.T) {
		_, err := runner.AdoptSessionCwd(ctx, sess.ID, "/second/dir")
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(eventsOf(t, repo, sess.ID, store.EventSession)))
	})

	t.Run("an empty directory changes nothing", func(t *testing.T) {
		_, err := runner.AdoptSessionCwd(ctx, sess.ID, "")
		assert.NoErr(t, err)
		stored, err := repo.GetSession(ctx, sess.ID)
		assert.NoErr(t, err)
		assert.Eq(t, "/second/dir", stored.Cwd)
	})

	t.Run("an unknown session fails", func(t *testing.T) {
		_, err := runner.AdoptSessionCwd(ctx, "missing", "/x")
		assert.Err(t, err)
	})
}
