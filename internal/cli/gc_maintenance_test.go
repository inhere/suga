package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// TestGCMaintenanceCommand covers `suga data shrink-fts` and `suga data vacuum`
// through the CLI (data governance P1). The ledger is isolated in a temp
// SUGA_HOME, and the historical tool row is put into the index by hand: the
// write path no longer indexes tool results, only the old backfill did.
func TestGCMaintenanceCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_HOOKS", "off")
	ctx := context.Background()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))

	// One tool entry the old backfill indexed in full, as a ledger written before
	// tool results left the index holds it.
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "s1", Title: "s1", Cwd: "/w"}))
	body := `{"Role":"tool","Content":"` + strings.Repeat("中", 3000) + `","ToolCall":{"CallID":"c1","Name":"shell"}}`
	_, err = repo.AppendEntries(ctx, "s1", store.NewEntry{Type: store.EntryTool, JSON: []byte(body)})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())
	insertFTSToolRow(t, dbPath, "s1", 1, strings.Repeat("中", 3000))

	t.Run("maintenance flags need their own subcommand", func(t *testing.T) {
		_, err := runCLI(t, ctx, "data", "gc", "--status")
		assert.ErrMsgContains(t, err, "status")
		_, err = runCLI(t, ctx, "data", "shrink-fts", "extra")
		assert.ErrMsgContains(t, err, "usage")
	})

	t.Run("status reports the pending rows", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "shrink-fts", "--status")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "未开始")
		assert.StrContains(t, out, "剩余 tool 行：1")
	})

	t.Run("shrink deletes the row and status turns complete", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "shrink-fts")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "瘦身完成")
		assert.StrContains(t, out, "删除 1 行")
		assert.StrContains(t, out, "suga data vacuum")
		assert.Eq(t, 0, ftsRowCount(t, dbPath, "s1"))

		out, err = runCLI(t, ctx, "data", "shrink-fts", "--status")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "已完成")
		assert.StrContains(t, out, "剩余 tool 行：0")

		// A second run has nothing left to delete.
		out, err = runCLI(t, ctx, "data", "shrink-fts")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "无需瘦身")
	})

	t.Run("abort clears the checkpoint", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "shrink-fts", "--abort")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "已清除")
		out, err = runCLI(t, ctx, "data", "shrink-fts", "--status")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "未开始")
	})

	t.Run("vacuum refuses while another suga process uses the ledger", func(t *testing.T) {
		runDir := filepath.Join(home, "run")
		record := map[string]any{
			"pid": os.Getpid(), "session_id": "sess-live",
			"socket": filepath.Join(runDir, fmt.Sprintf("%d.sock", os.Getpid())),
		}
		data, err := json.Marshal(record)
		assert.Require(t, assert.NoErr(t, err))
		assert.NoErr(t, os.MkdirAll(runDir, 0o700))
		recordPath := filepath.Join(runDir, fmt.Sprintf("%d.json", os.Getpid()))
		assert.NoErr(t, os.WriteFile(recordPath, data, 0o600))
		defer os.Remove(recordPath)

		_, err = runCLI(t, ctx, "data", "vacuum")
		assert.ErrMsgContains(t, err, "sess-live")
	})

	t.Run("vacuum returns the space", func(t *testing.T) {
		before := store.DatabaseFileBytes(dbPath)
		out, err := runCLI(t, ctx, "data", "vacuum")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "VACUUM 完成")
		after := store.DatabaseFileBytes(dbPath)
		assert.True(t, after > 0 && before > 0)
		assert.True(t, !strings.Contains(out, "库文件总体 0 B"))
	})
}

// TestVacuumSpaceCheck covers the free-space refusal of `gc --vacuum` without
// needing a file that large: the decision is the helper the command calls.
func TestVacuumSpaceCheck(t *testing.T) {
	t.Run("refuses without twice the database size", func(t *testing.T) {
		need := 2 * uint64(1000)
		err := requireVacuumSpace(1000, need-1)
		assert.ErrMsgContains(t, err, "not enough free disk space")
	})
	t.Run("accepts with enough space", func(t *testing.T) {
		assert.NoErr(t, requireVacuumSpace(1000, 2*uint64(1000)))
	})
}

// insertFTSToolRow indexes one tool entry the way the historical backfill did.
func insertFTSToolRow(t *testing.T, dbPath, sessionID string, seq int64, text string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer db.Close()
	_, err = db.Exec(`INSERT INTO entries_fts(session_id, seq, role, text) VALUES (?, ?, 'tool', ?)`,
		sessionID, seq, text)
	assert.Require(t, assert.NoErr(t, err))
}

// ftsRowCount counts the index rows of one session.
func ftsRowCount(t *testing.T, dbPath, sessionID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer db.Close()
	var n int
	assert.Require(t, assert.NoErr(t, db.QueryRow(
		`SELECT COUNT(*) FROM entries_fts WHERE session_id = ?`, sessionID).Scan(&n)))
	return n
}
