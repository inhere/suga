package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// helper_placeholder

// seedSessions creates 12 sessions, one per hour back from now (s00 newest):
// the even ones in the current directory (the default list scope), the odd
// ones under /work/b, s03 titled "schedule fix", s05 with "flaky ping test"
// in its conversation.
func seedSessions(t *testing.T) context.Context {
	t.Helper()
	t.Setenv("SUGA_HOME", shortCLIHome(t))
	t.Setenv("SUGA_HOOKS", "off")
	homeCwd, err := os.Getwd()
	assert.Require(t, assert.NoErr(t, err))
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.NoErr(t, err)
	repo, err := store.Open(dbPath)
	assert.NoErr(t, err)
	defer repo.Close()
	ctx := context.Background()
	now := time.Now()
	for i := 0; i < 12; i++ {
		cwd := "/work/b"
		if i%2 == 0 {
			cwd = homeCwd
		}
		title := fmt.Sprintf("task %d", i)
		if i == 3 {
			title = "schedule fix"
		}
		at := now.Add(-time.Duration(i) * time.Hour)
		assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: fmt.Sprintf("s%02d", i), Title: title, Cwd: cwd,
			CreatedAt: at, UpdatedAt: at, Meta: map[string]any{"provider": "cc", "model": "flash", "suga_version": "0.14.1"}}))
	}
	_, err = repo.AppendEntries(ctx, "s05", store.NewEntry{Type: store.EntryUser,
		JSON: json.RawMessage(`{"Role":"user","Content":"why does the flaky ping test fail?"}`)})
	assert.NoErr(t, err)
	_, err = repo.AppendEntries(ctx, "s06", store.NewEntry{Type: store.EntryUser,
		JSON: json.RawMessage(`{"Role":"user","Content":"中文 token"}`)})
	assert.NoErr(t, err)
	// Appending touched s05 and s06: put their activity times back.
	for id, ago := range map[string]time.Duration{"s05": 5 * time.Hour, "s06": 6 * time.Hour} {
		sess, err := repo.GetSession(ctx, id)
		assert.NoErr(t, err)
		sess.UpdatedAt = now.Add(-ago)
		assert.NoErr(t, repo.UpdateSession(ctx, sess))
	}
	return ctx
}

func listed(t *testing.T, ctx context.Context, args ...string) []string {
	t.Helper()
	out, _, err := runCLIAll(t, ctx, append([]string{"session", "list", "--json"}, args...)...)
	assert.Require(t, assert.NoErr(t, err))
	var rows []sessionRow
	assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(out), &rows)))
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

// runCLIAll runs the CLI and returns stdout and stderr separately.
func runCLIAll(t *testing.T, ctx context.Context, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs(args)
	return stdout.String(), stderr.String(), err
}

func TestSessionsCommand(t *testing.T) {
	ctx := seedSessions(t)
	// The current-dir seeds are the even ones; --all sees all 12.
	t.Run("the last 10, newest first", func(t *testing.T) {
		ids := listed(t, ctx)
		assert.Eq(t, 6, len(ids))
		assert.Eq(t, "s00", ids[0])
		// --all is still capped by the default limit of 10; -n 0 shows everything.
		assert.Eq(t, 10, len(listed(t, ctx, "--all")))
		assert.Eq(t, 12, len(listed(t, ctx, "--all", "-n", "0")))
		assert.Eq(t, []string{"s00", "s02", "s04"}, listed(t, ctx, "--limit", "3"))
	})
	t.Run("ls is the list alias", func(t *testing.T) {
		// `suga session ls` is the same listing as `session list`.
		aliased, _, err := runCLIAll(t, ctx, "session", "ls", "--json")
		assert.Require(t, assert.NoErr(t, err))
		full, _, err := runCLIAll(t, ctx, "session", "list", "--json")
		assert.Require(t, assert.NoErr(t, err))
		assert.NotEmpty(t, full)
		assert.Eq(t, full, aliased)
	})
	t.Run("time range", func(t *testing.T) {
		assert.Eq(t, 2, len(listed(t, ctx, "--since", "3h")))
		assert.Eq(t, 3, len(listed(t, ctx, "--since", "3h", "--all")))
		assert.Eq(t, 1, len(listed(t, ctx, "--until", "10h")))
		_, err := runCLI(t, ctx, "session", "list", "--since", "soon")
		assert.ErrMsgContains(t, err, "want 12h, 7d or a date")
	})
	t.Run("keywords in the fields or the conversation", func(t *testing.T) {
		// s03 lives in /work/b: --all reaches it, the default scope does not.
		assert.Eq(t, []string{"s03"}, listed(t, ctx, "-q", "schedule", "--all"))
		assert.Eq(t, []string{}, listed(t, ctx, "-q", "schedule"))
		assert.Eq(t, []string{"s03"}, listed(t, ctx, "SCHEDULE", "fix", "--all")) // words after the flags, any case
		out, _, err := runCLIAll(t, ctx, "session", "list", "--json", "-q", "flaky ping", "--all")
		assert.Require(t, assert.NoErr(t, err))
		var rows []sessionRow
		assert.NoErr(t, json.Unmarshal([]byte(out), &rows))
		assert.Require(t, assert.Len(t, rows, 1))
		assert.Eq(t, "s05", rows[0].ID)
		assert.StrContains(t, rows[0].Match, "flaky ping test")
		// A mixed Chinese/English query reaches the new term-splitting store path.
		assert.Eq(t, []string{"s06"}, listed(t, ctx, "-q", "中文 token"))
		assert.Eq(t, []string{}, listed(t, ctx, "-q", "nothing-like-this", "--all"))
	})
	t.Run("directory prefix", func(t *testing.T) {
		ids := listed(t, ctx, "--cwd", "/work/b", "-n", "0") // whole path elements
		assert.Eq(t, 6, len(ids))
		assert.Eq(t, 0, len(listed(t, ctx, "--cwd", "/work/b/nothing")))
		assert.True(t, pathHasPrefix(`D:\Work\x\ws`, "d:/work/x"))
		assert.Eq(t, "…/x/ws", shortDir(`D:\work\x\ws`))
	})
	t.Run("--cwd and --all are mutually exclusive", func(t *testing.T) {
		_, _, err := runCLIAll(t, ctx, "session", "list", "--all", "--cwd", "/work/b")
		assert.ErrMsgContains(t, err, "mutually exclusive")
	})
	t.Run("time and cwd filters remain session filters", func(t *testing.T) {
		// s05's matching entry is recent, but the session was last active five
		// hours ago. CLI boundaries must use sessions.updated_at, not entries.created_at.
		assert.Eq(t, []string{"s05"}, listed(t, ctx, "--query", "flaky", "--since", "6h", "--all"))
		assert.Eq(t, []string{"s05"}, listed(t, ctx, "--query", "flaky", "--until", "4h", "--all"))
		assert.Eq(t, []string{}, listed(t, ctx, "--query", "flaky", "--since", "3h", "--all"))

		// The match is in /work/b; applying --cwd must filter the session after
		// the conversation search rather than changing the entry search scope.
		assert.Eq(t, []string{"s05"}, listed(t, ctx, "--query", "flaky", "--cwd", "/work/b"))
		assert.Eq(t, []string{}, listed(t, ctx, "--query", "flaky", "--cwd", "/work/a"))
	})
	t.Run("table", func(t *testing.T) {
		out, _, err := runCLIAll(t, ctx, "session", "list", "-n", "2")
		assert.Require(t, assert.NoErr(t, err))
		// The header names the scope, so a list read from another directory
		// never looks like the whole catalog.
		assert.StrContains(t, out, "当前目录")
		assert.StrContains(t, out, "（--all 列出全部）")
		assert.StrContains(t, out, "UPDATED")
		assert.StrContains(t, out, "cc/flash")
		assert.StrContains(t, out, "0.14.1")
		assert.StrContains(t, out, "2 of 6 sessions; -n 0 shows all")
		out, _, err = runCLIAll(t, ctx, "session", "list", "flaky", "--all")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "↳")
		assert.True(t, strings.Contains(out, "s05"))
	})
	t.Run("empty default view hints on stderr", func(t *testing.T) {
		// From a directory nothing is recorded for, the default view is empty:
		// the hint (stderr only) says the directory may be wrong and names --all.
		t.Chdir("/")
		out, errOut, err := runCLIAll(t, ctx, "session", "list")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, errOut, "当前目录")
		assert.StrContains(t, errOut, "--all")
		assert.Eq(t, "", out)
		// --json keeps stdout machine data ([]) while the hint rides on stderr.
		out, _, err = runCLIAll(t, ctx, "session", "list", "--json")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "[]\n", out)
		assert.StrContains(t, errOut, "--all")
	})
	t.Run("sessions in other directories, hint names their count", func(t *testing.T) {
		// From a directory nothing is recorded for, the default view is empty
		// and the hint counts every session somewhere else (all 12 seeds).
		t.Chdir("/")
		out, errOut, err := runCLIAll(t, ctx, "session", "list")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "", out)
		assert.StrContains(t, errOut, "其它目录共有 12 个")
	})
	t.Run("nothing anywhere, the hint has no count", func(t *testing.T) {
		// A fresh store holding one session of this directory only: narrowing
		// time to before it empties the result of every directory (N = 0).
		ctx2 := seedOnlyHomeSessions(t, 1)
		out, errOut, err := runCLIAll(t, ctx2, "session", "list", "--json", "--until", "12h")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "[]\n", out) // --json form
		assert.StrContains(t, errOut, "还没有任何会话")
	})
}

// seedOnlyHomeSessions seeds n sessions of the current directory into a fresh
// store and returns the run context; used for hint tests.
func seedOnlyHomeSessions(t *testing.T, n int) context.Context {
	t.Helper()
	t.Setenv("SUGA_HOME", shortCLIHome(t))
	t.Setenv("SUGA_HOOKS", "off")
	homeCwd, err := os.Getwd()
	assert.Require(t, assert.NoErr(t, err))
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer repo.Close()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: fmt.Sprintf("h%02d", i),
			Title: fmt.Sprintf("home task %d", i), Cwd: homeCwd}))
	}
	return ctx
}

func TestSessionsCommandAgentSessions(t *testing.T) {
	t.Setenv("SUGA_HOME", shortCLIHome(t))
	t.Setenv("SUGA_HOOKS", "off")
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	ctx := context.Background()
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "parent", Title: "main work"}))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "child", Title: "find callers",
		Meta: map[string]any{store.MetaKind: store.KindAgent, store.MetaParentSession: "parent"}}))
	assert.NoErr(t, repo.Close())

	// The agent sessions have no recorded cwd: the default scope keeps them
	// out; --all lists the parent, --agents the sub-agent too.
	assert.Eq(t, []string{}, listed(t, ctx))
	assert.Eq(t, []string{"parent"}, listed(t, ctx, "--all"))
	assert.Eq(t, 2, len(listed(t, ctx, "--agents", "--all")))
	out, _, err := runCLIAll(t, ctx, "session", "list", "--agents", "--all")
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, out, "[agent of parent] find callers")
}
