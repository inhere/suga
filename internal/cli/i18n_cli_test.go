package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/outschema"
	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/api"
)

func TestCLIEnglishUserFacingOutput(t *testing.T) {
	t.Setenv("SUGA_LANGUAGE", "en")

	t.Run("config set and unset", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/test\n")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "language", "en"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "Written")
		stdout.Reset()
		err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "unset", "language"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "Removed language")
	})

	t.Run("data usage title", func(t *testing.T) {
		var out bytes.Buffer
		err := writeDataUsage(&out, store.UsageReport{})
		assert.NoErr(t, err)
		assert.StrContains(t, out.String(), "Database file")
	})

	t.Run("undo no snapshot", func(t *testing.T) {
		assert.Eq(t, "No snapshot to roll back (this session changed no file with a tool, or the snapshots were cleaned by tools.snapshots).", renderUndoCheckpoints(nil))
	})

	t.Run("send image error", func(t *testing.T) {
		_, err := sendImageRefs(process.Record{SessionID: "s-en", PID: 7}, []string{"picture.png"})
		assert.ErrMsgContains(t, err, "does not support images")
	})

	t.Run("cwd mismatch", func(t *testing.T) {
		err := cwdMismatchError("s-en", t.TempDir()+"/missing", t.TempDir())
		assert.ErrMsgContains(t, err, "current working directory")
	})

	t.Run("output schema compile error", func(t *testing.T) {
		// The real entry: a schema the compiler refuses is a usage error (exit 2)
		// whose line a person reads, so it must not stay Chinese in an en UI.
		verifyRunDir(t)
		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "p", "--session-id", "schema-en",
			"--output-schema", `{"$schema":"https://json-schema.org/draft/2030-01/schema","type":"object"}`,
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), "--output-schema is invalid")
		assert.StrContains(t, err.Error(), "The output contract's $schema")
		assert.StrContains(t, err.Error(), "is not supported (supported: draft-04/06/07/2019-09/2020-12)")
		assert.StrNotContains(t, err.Error(), "输出契约")
	})
}

// assertEnglishOutput checks the expected English lines of one report and that
// no Chinese line of the source text survived the migration.
func assertEnglishOutput(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		assert.StrContains(t, out, want)
	}
	for _, unwanted := range []string{"库文件", "待移出", "将删除", "已删除", "可处理", "结论：", "中断于", "全文索引", "会话目录", "页"} {
		assert.False(t, strings.Contains(out, unwanted), "untranslated "+unwanted+" in:\n"+out)
	}
}

// seedEnglishGovernanceHome seeds the ledger and the session files the English
// governance test reads: one old session with a tool body above the inline
// threshold (so `data bodies` has a moved body), one event and one spilled
// output (so `data usage` reports maintenance hints), and an old timestamp (so
// `data gc --older-than` selects it).
func seedEnglishGovernanceHome(t *testing.T, ctx context.Context, home string) {
	t.Helper()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	old := time.Now().Add(-40 * 24 * time.Hour)
	assert.Require(t, assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "en-old", Title: "old session", CreatedAt: old, UpdatedAt: old})))
	body, err := json.Marshal(api.Message{Role: "tool", Content: strings.Repeat("line\n", 400),
		ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: strings.Repeat("line\n", 400)}})
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(ctx, "en-old", store.NewEntry{Type: store.EntryTool, JSON: body})
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEvent(ctx, store.Event{SessionID: "en-old", Kind: store.EventTool, Name: "shell", StartedAt: time.Now()})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, repo.Close()))
	outputs := filepath.Join(home, "sessions", "en-old", "outputs")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(outputs, 0o700)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(outputs, "0123456789abcdef.txt"), []byte("out"), 0o600)))
}

// TestCLIEnglishGovernanceOutput drives the data commands this task migrated
// through the real CLI with SUGA_LANGUAGE=en: the `data bodies` reports (status,
// check, undo and move with their progress lines), the `data usage` table with
// the maintenance hints the store renders with the translator the CLI hands it,
// and the `data gc` / `data vacuum` summaries.
func TestCLIEnglishGovernanceOutput(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_LANGUAGE", "en")
	t.Setenv("SUGA_HOOKS", "off")
	ctx := context.Background()
	seedEnglishGovernanceHome(t, ctx, home)

	t.Run("data usage cells and maintenance hints", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "usage")
		assert.Require(t, assert.NoErr(t, err))
		assertEnglishOutput(t, out, "Database file", "Free pages", "entries", " rows", "rows",
			"Sessions directory", "sessions in the database", "files (", "Total ", "Actionable:",
			// The hints come from the store: the CLI must hand it the English catalog.
			"events are deleted with sessions", "spill output can be compressed")
	})

	t.Run("data bodies status", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "bodies", "--status")
		assert.Require(t, assert.NoErr(t, err))
		assertEnglishOutput(t, out, "Body files: threshold", "move new writes out on",
			"File name: still the old name (tools.jsonl)", "--move state: tool not started",
			"--undo state: not started", "moved out", "session files")
	})

	t.Run("data bodies check", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "bodies", "--check")
		assert.Require(t, assert.NoErr(t, err))
		assertEnglishOutput(t, out, "moved-out bodies (tool and assistant)",
			"Result: the stubs in the database and the session files agree")
	})

	t.Run("data bodies undo keeps the files", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "bodies", "--undo")
		assert.Require(t, assert.NoErr(t, err))
		assertEnglishOutput(t, out, "Restoring:", "Scanned to en-old/", "Restore complete:",
			"New writes no longer move out")
	})

	t.Run("data bodies move reports its progress", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "bodies", "--move")
		assert.Require(t, assert.NoErr(t, err))
		assertEnglishOutput(t, out, "Moving out:", "Scanned to en-old/", "Move complete:",
			"No assistant bodies need moving")
	})

	t.Run("data gc dry run and apply", func(t *testing.T) {
		// A session the ledger has not touched since 40 days: appended entries bump
		// updated_at, so it is created here instead of in the seed (gc_test.go).
		dbPath, err := appconfig.ResolveCoreStorePath()
		assert.Require(t, assert.NoErr(t, err))
		repo, err := store.Open(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		old := time.Now().Add(-40 * 24 * time.Hour)
		assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "en-gc", Title: "en-gc", CreatedAt: old, UpdatedAt: old}))
		assert.Require(t, assert.NoErr(t, repo.Close()))

		out, err := runCLI(t, ctx, "data", "gc", "--older-than", "30d")
		assert.Require(t, assert.NoErr(t, err))
		assertEnglishOutput(t, out, "en-gc", "will delete 1 sessions", "; pass --apply to execute")

		out, err = runCLI(t, ctx, "data", "gc", "--older-than", "30d", "--apply")
		assert.Require(t, assert.NoErr(t, err))
		assertEnglishOutput(t, out, "deleted 1 sessions", "; VACUUM was not run")
	})

	t.Run("data vacuum summary", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "vacuum")
		assert.Require(t, assert.NoErr(t, err))
		assertEnglishOutput(t, out, "VACUUM complete: sessions.db", "Overall database")
	})
}

// TestCompileErrorText covers the display of a *outschema.CompileError: the zh
// catalog value is the error's own Message (the migration changed nothing a
// Chinese user reads), the en value is translated, and a kind the catalog does
// not know falls back to Message.
func TestCompileErrorText(t *testing.T) {
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	zh, err := i18n.New(i18n.LanguageZH)
	assert.Require(t, assert.NoErr(t, err))

	schemas := map[string]string{
		"empty":          "  \n",
		"syntax":         `{"type":`,
		"top level":      `[{"type":"object"}]`,
		"size":           `{"description":"` + strings.Repeat("x", outschema.MaxSchemaBytes) + `"}`,
		"external $ref":  `{"$ref":"https://example.com/s.json"}`,
		"unknown draft":  `{"$schema":"https://json-schema.org/draft/2030-01/schema","type":"object"}`,
		"invalid schema": `{"type":"object","required":"a"}`,
	}
	for name, raw := range schemas {
		t.Run(name, func(t *testing.T) {
			_, compileErr := outschema.Compile([]byte(raw))
			assert.Require(t, assert.Err(t, compileErr))
			var ce *outschema.CompileError
			assert.Require(t, assert.True(t, errors.As(compileErr, &ce)))
			assert.Eq(t, ce.Message, compileErrorText(zh, compileErr))
			enText := compileErrorText(en, compileErr)
			assert.NotEq(t, ce.Message, enText)
			// The compiler library puts the sandbox's own refusal inside its
			// sentence (failing loading ...), so that detail keeps its text; the line
			// in front of it is the catalog's.
			assert.StrContains(t, enText, "output contract")
		})
	}

	t.Run("an unknown kind keeps the message", func(t *testing.T) {
		ce := &outschema.CompileError{Kind: "future_kind", Message: "原始提示"}
		assert.Eq(t, "原始提示", compileErrorText(en, ce))
	})
	t.Run("a non-compile error keeps its text", func(t *testing.T) {
		assert.Eq(t, "boom", compileErrorText(en, errors.New("boom")))
	})
}

// TestCLIEnglishUndoDetail covers the `suga undo` lines this task migrated: the
// confirmation prompt, the action labels of the plan and the result sentence.
func TestCLIEnglishUndoDetail(t *testing.T) {
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("confirmation prompt", func(t *testing.T) {
		var stderr bytes.Buffer
		assert.False(t, confirmUndo(strings.NewReader("n\n"), &stderr, false, en))
		assert.Eq(t, "Roll back the file changes? [y/N] ", stderr.String())

		stderr.Reset()
		assert.True(t, confirmUndo(strings.NewReader("y\n"), &stderr, true, en))
		assert.Eq(t, "Force the rollback and overwrite content conflicts? [y/N] ", stderr.String())
	})

	rows := []personal.Checkpoint{{
		SessionID: "s-en", TurnID: "turn-1", StartedAt: time.Date(2026, 10, 26, 9, 0, 0, 0, time.Local),
		Calls: 2, Files: 2, Bytes: 2048,
		Items: []personal.CheckpointItem{
			{Action: personal.CheckpointActionRestore, Meta: personal.SnapshotMeta{Rel: "a.txt", Size: 1024}},
			{Action: personal.CheckpointActionDelete, Meta: personal.SnapshotMeta{Rel: "b.txt"}},
			{Action: personal.CheckpointActionSkip, Conflict: true, Meta: personal.SnapshotMeta{Rel: "c.txt"}},
		},
		Shell: []personal.ShellCommand{{Command: "go test ./...", TaskID: "t2"}},
	}}

	t.Run("plan labels", func(t *testing.T) {
		plan := renderUndoPlanWithTranslator(rows, personal.UndoOptions{Count: 1}, en)
		for _, want := range []string{"Rolling back the last 1 turns", "restore a.txt", "delete b.txt",
			"skip c.txt", "conflict: changed after the snapshot", "shell_exec: go test ./... (task t2)"} {
			assert.StrContains(t, plan, want)
		}
	})

	t.Run("result sentence", func(t *testing.T) {
		// The parts are the chat keys (cmd.undo.result.*), joined with the CLI
		// separator and closed by its end mark.
		result := renderUndoResultWithTranslator(personal.UndoResult{Restored: 2, Skipped: 1, Turns: 1}, nil, en)
		assert.Eq(t, "Rolled back 2 files; Skipped 1 conflicts.", result)
	})
}
