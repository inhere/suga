package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/backup"
	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/testutil"
)

func TestCLIRegistersPersonalCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"--help"})
	assert.NoErr(t, err)
	for _, name := range []string{"run", "chat", "acp", "inspect", "data"} {
		assert.Eq(t, true, strings.Contains(stdout.String(), name))
	}
	// The two groups name their subcommands in the one-line description.
	assert.StrContains(t, stdout.String(), "trace / stats / export / manifest")
	assert.StrContains(t, stdout.String(), "gc / bodies / vacuum / compress-spill / backup / restore")
}

func TestCLIRegistersProcessCommandsAndSendExitCodes(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	root := filepath.Join(home, "run")
	delivered := make(chan string, 1)
	server, err := process.Start(root, "session-cli-123", "suga-k7", t.TempDir(), func(mode, text, from string) (process.Delivery, error) {
		delivered <- from + ":" + text
		return process.Delivery{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	var stdout, stderr bytes.Buffer
	err = newCLI(context.Background(), strings.NewReader("stdin delivery"), &stdout, &stderr).RunWithArgs([]string{"send", "--session", "session-cli"})
	assert.NoErr(t, err)
	assert.Eq(t, "cli:stdin delivery", <-delivered)
	assert.Eq(t, "delivered\n", stderr.String())

	// --source replaces the default "cli" source label.
	stderr.Reset()
	err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"send", "--session", "session-cli", "--source", "web-ui", "--text", "hi"})
	assert.NoErr(t, err)
	assert.Eq(t, "web-ui:hi", <-delivered)
	assert.Eq(t, "delivered\n", stderr.String())

	stdout.Reset()
	stderr.Reset()
	err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"ps", "--json"})
	assert.NoErr(t, err)
	var records []process.Record
	assert.NoErr(t, json.Unmarshal(stdout.Bytes(), &records))
	assert.Eq(t, 1, len(records))
	assert.Eq(t, "session-cli-123", records[0].SessionID)
	// The name column is the short name `suga send --session <name>` takes; the
	// chat banner of that session shows the same one.
	assert.Eq(t, "suga-k7", records[0].Name)

	stdout.Reset()
	err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"ps"})
	assert.NoErr(t, err)
	assert.StrContains(t, stdout.String(), "session-cli-123\tsuga-k7\t")

	err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"send", "--session", "missing-session", "--text", "hello"})
	var exitCoder interface{ ExitCode() int }
	if !errors.As(err, &exitCoder) || exitCoder.ExitCode() != 3 {
		t.Fatalf("offline session should return exit code 3, got %v", err)
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("CLI main mapping returned %d for offline session", got)
	}
	if got := ExitCode(fmt.Errorf("wrapped: %w", personal.SessionNotFoundError{ID: "x"})); got != 4 {
		t.Fatalf("session not found exit code = %d, want 4", got)
	}
	if got := ExitCode(processExitError{code: 2, err: process.ErrAmbiguous}); got != 2 {
		t.Fatalf("CLI main mapping returned %d for ambiguous selector", got)
	}
}

func shortCLIHome(t *testing.T) string {
	t.Helper()
	return testutil.Dir(t)
}

func TestRestoreRejectsActiveProcess(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.NoErr(t, err)
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())
	archive := filepath.Join(home, "restore.zip")
	_, err = backup.Create(context.Background(), backup.Options{Home: home, Output: archive})
	assert.Require(t, assert.NoErr(t, err))
	server, err := process.Start(filepath.Join(home, "run"), "active-restore", "active", t.TempDir(), func(string, string, string) (process.Delivery, error) { return process.Delivery{}, nil })
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })
	var stdout, stderr bytes.Buffer
	err = newCLI(context.Background(), strings.NewReader("YES\n"), &stdout, &stderr).RunWithArgs([]string{"data", "restore", archive, "--no-auto-backup"})
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "ledger is in use")
}

// TestBackupRunsWhileTheLedgerIsInUse pins that a backup is an online
// snapshot: a running suga process does not stop it (only restore needs the
// exclusive window).
func TestBackupRunsWhileTheLedgerIsInUse(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.NoErr(t, err)
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())
	server, err := process.Start(filepath.Join(home, "run"), "active-backup", "active", t.TempDir(), func(string, string, string) (process.Delivery, error) { return process.Delivery{}, nil })
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })
	var stdout, stderr bytes.Buffer
	archive := filepath.Join(home, "online.suga-backup.zip")
	err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"data", "backup", "--output", archive})
	assert.NoErr(t, err)
	_, statErr := os.Stat(archive)
	assert.NoErr(t, statErr)
}

func TestACPRejectsPositionalsWithoutWritingProtocolOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"acp", "extra"})
	assert.Err(t, err)
	assert.Eq(t, true, strings.Contains(err.Error(), "does not accept arguments"))
	assert.Eq(t, "", stdout.String())
}

func TestVersionFlagsWriteConfiguredOutputAndDoNotAffectNextApp(t *testing.T) {
	for _, flag := range []string{"--version", "-V"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{flag})
			assert.NoErr(t, err)
			assert.Eq(t, "suga dev (dev)\n", stdout.String())
			assert.Eq(t, "", stderr.String())
		})
	}

	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"run", "--help"})
	assert.NoErr(t, err)
	assert.Eq(t, true, strings.Contains(stderr.String(), "--prompt"))
	assert.Eq(t, "", stdout.String())
}

func TestVersionCommandWritesOneLine(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"version"})
	assert.NoErr(t, err)
	assert.Eq(t, "suga dev (dev)\n", stdout.String())
	assert.Eq(t, "", stderr.String())
}

func TestSendWaitReportsQueuedFate(t *testing.T) {
	start := func(t *testing.T, sessionID string, settled chan error) {
		t.Helper()
		home := shortCLIHome(t)
		t.Setenv("SUGA_HOME", home)
		server, err := process.StartWait(filepath.Join(home, "run"), sessionID, "wait", t.TempDir(), func(mode, text, from string) (process.Delivery, <-chan error, error) {
			return process.Delivery{Queued: true}, settled, nil
		})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = server.Close() })
	}
	t.Run("discarded message exits 3", func(t *testing.T) {
		settled := make(chan error, 1)
		settled <- process.ErrDiscarded
		start(t, "session-wait-a", settled)
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"send", "--session", "session-wait-a", "--text", "hi", "--wait"})
		assert.Eq(t, 3, ExitCode(err))
	})
	t.Run("started message reports delivered", func(t *testing.T) {
		settled := make(chan error, 1)
		settled <- nil
		start(t, "session-wait-b", settled)
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"send", "--session", "session-wait-b", "--text", "hi", "--wait"})
		assert.NoErr(t, err)
		assert.Eq(t, "delivered\n", stderr.String())
	})
	t.Run("without wait a queued message returns at once", func(t *testing.T) {
		start(t, "session-wait-c", make(chan error))
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"send", "--session", "session-wait-c", "--text", "hi"})
		assert.NoErr(t, err)
		assert.Eq(t, "queued\n", stderr.String())
	})
}

func TestSendRejectsOversizeStdinInsteadOfTruncating(t *testing.T) {
	var stdout, stderr bytes.Buffer
	big := strings.NewReader(strings.Repeat("a", maxSendStdinBytes+1))
	err := newCLI(context.Background(), big, &stdout, &stderr).RunWithArgs([]string{"send", "--session", "any"})
	assert.Require(t, assert.NotNil(t, err))
	assert.Eq(t, 4, ExitCode(err))
	assert.StrContains(t, err.Error(), "1 MiB")
}

func TestTraceCommandPrintsSessionTimeline(t *testing.T) {
	t.Setenv("SUGA_HOME", shortCLIHome(t))
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.NoErr(t, err)
	repo, err := store.Open(dbPath)
	assert.NoErr(t, err)
	ctx := context.Background()
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "trace-s1", Status: store.SessionOpen}))
	for _, e := range []store.Event{
		{SessionID: "trace-s1", TurnID: "t1", Kind: store.EventTurn, Name: "turn", Status: "completed"},
		{SessionID: "trace-s1", TurnID: "t1", Kind: store.EventModel, Name: "primary", Status: "error", Error: "boom"},
		{SessionID: "trace-s1", TurnID: "t2", Kind: store.EventDenial, Name: "mcp_write", Status: "denied"},
	} {
		_, err := repo.AppendEvent(ctx, e)
		assert.NoErr(t, err)
	}
	assert.NoErr(t, repo.Close())

	run := func(args ...string) string {
		var stdout, stderr bytes.Buffer
		assert.NoErr(t, newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs(args))
		return stdout.String()
	}
	t.Run("text", func(t *testing.T) {
		out := run("inspect", "trace", "trace-s1")
		assert.Eq(t, true, strings.Contains(out, " model primary error "))
		assert.Eq(t, true, strings.Contains(out, "err=boom"))
		assert.Eq(t, true, strings.Contains(out, " denial mcp_write denied "))
	})
	t.Run("analyze", func(t *testing.T) {
		out := run("inspect", "trace", "trace-s1", "--analyze")
		assert.StrContains(t, out, "steps                  1 model calls, 1 tool calls")
		assert.StrContains(t, out, "mcp_write x1 (1 failed)")
		js := run("inspect", "trace", "trace-s1", "--analyze", "--json")
		assert.StrContains(t, js, `"model_calls":1`)
	})
	t.Run("turn filter with json", func(t *testing.T) {
		out := run("inspect", "trace", "trace-s1", "--turn", "t2", "--json")
		lines := strings.Split(strings.TrimSpace(out), "\n")
		assert.Eq(t, 1, len(lines))
		var row map[string]any
		assert.NoErr(t, json.Unmarshal([]byte(lines[0]), &row))
		assert.Eq(t, "denial", row["kind"])
	})
	t.Run("unknown session", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"inspect", "trace", "nope"})
		assert.Err(t, err)
	})
}

func TestTraceCommandShowsPlan(t *testing.T) {
	t.Setenv("SUGA_HOME", shortCLIHome(t))
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.NoErr(t, err)
	repo, err := store.Open(dbPath)
	assert.NoErr(t, err)
	ctx := context.Background()
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "trace-plan", Status: store.SessionOpen}))
	// Event, plan, event in time order: the plan must land between the events.
	_, err = repo.AppendEvent(ctx, store.Event{SessionID: "trace-plan", TurnID: "t1", Kind: store.EventTurn, Name: "turn", Status: "completed", StartedAt: time.Now().Add(-time.Hour)})
	assert.NoErr(t, err)
	_, err = repo.AppendEntries(ctx, "trace-plan",
		store.NewEntry{TurnID: "t1", Type: store.EntryPlan, JSON: json.RawMessage(`{"plan":[{"step":"design","status":"completed"},{"step":"build","status":"in_progress"},{"step":"ship","status":"pending"}]}`)},
		store.NewEntry{TurnID: "t2", Type: store.EntryPlan, JSON: json.RawMessage(`{"explanation":"redo","plan":[{"step":"other","status":"pending"}]}`)})
	assert.NoErr(t, err)
	_, err = repo.AppendEvent(ctx, store.Event{SessionID: "trace-plan", TurnID: "t2", Kind: store.EventDenial, Name: "late", Status: "denied", StartedAt: time.Now().Add(time.Hour)})
	assert.NoErr(t, err)
	assert.NoErr(t, repo.Close())

	run := func(args ...string) string {
		var stdout, stderr bytes.Buffer
		assert.NoErr(t, newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs(args))
		return stdout.String()
	}
	t.Run("text merges plan steps by time", func(t *testing.T) {
		out := run("inspect", "trace", "trace-plan")
		assert.StrContains(t, out, " plan ✔ design turn=t1")
		assert.StrContains(t, out, " plan ▶ build turn=t1")
		assert.StrContains(t, out, " plan ○ ship turn=t1")
		turn, design, late := strings.Index(out, " turn turn "), strings.Index(out, "design"), strings.Index(out, " denial late ")
		assert.True(t, turn < design && design < late)
	})
	t.Run("turn filter", func(t *testing.T) {
		out := run("inspect", "trace", "trace-plan", "--turn", "t2")
		assert.StrContains(t, out, " plan ○ other turn=t2")
		assert.StrNotContains(t, out, "design")
	})
	t.Run("json", func(t *testing.T) {
		out := run("inspect", "trace", "trace-plan", "--turn", "t2", "--json")
		var plan map[string]any
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			var row map[string]any
			assert.NoErr(t, json.Unmarshal([]byte(line), &row))
			if row["kind"] == "plan" {
				plan = row
			}
		}
		assert.NotNil(t, plan)
		assert.Eq(t, "t2", plan["turn_id"])
		assert.Eq(t, "redo", plan["explanation"])
		steps := plan["plan"].([]any)
		assert.Eq(t, 1, len(steps))
		assert.Eq(t, "pending", steps[0].(map[string]any)["status"])
	})
}
