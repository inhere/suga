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

// TestGCMaintenanceCommand covers `suga gc --shrink-fts` and `suga gc --vacuum`
// through the CLI (data governance P1). The ledger is isolated in a temp
// SUGA_HOME, and the FTS row is put into its pre-P1 state by hand: the
// migration only backfills entries that exist when it runs.
func TestGCMaintenanceCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_HOOKS", "off")
	ctx := context.Background()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))

	// One tool entry whose indexed text is the full result, as a ledger written
	// before P1 holds it.
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "s1", Title: "s1", Cwd: "/w"}))
	body := `{"Role":"tool","Content":"` + strings.Repeat("中", 3000) + `","ToolCall":{"CallID":"c1","Name":"shell"}}`
	_, err = repo.AppendEntries(ctx, "s1", store.NewEntry{Type: store.EntryTool, JSON: []byte(body)})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())
	setFTSText(t, dbPath, "s1", 1, strings.Repeat("中", 3000))

	t.Run("maintenance flags need their own form", func(t *testing.T) {
		_, err := runCLI(t, ctx, "gc", "--status")
		assert.ErrMsgContains(t, err, "--shrink-fts")
		_, err = runCLI(t, ctx, "gc", "--shrink-fts", "extra")
		assert.ErrMsgContains(t, err, "usage")
	})

	t.Run("status reports the pending rows", func(t *testing.T) {
		out, err := runCLI(t, ctx, "gc", "--shrink-fts", "--status")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "未开始")
		assert.StrContains(t, out, "待处理 tool 行：1")
	})

	t.Run("shrink rewrites the row and status turns complete", func(t *testing.T) {
		out, err := runCLI(t, ctx, "gc", "--shrink-fts")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "瘦身完成")
		assert.StrContains(t, out, "重写 1 行")
		assert.StrContains(t, out, "suga gc --vacuum")
		assert.Eq(t, store.FTSToolMaxChars+3, len([]rune(ftsText(t, dbPath, "s1", 1))))

		out, err = runCLI(t, ctx, "gc", "--shrink-fts", "--status")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "已完成")
		assert.StrContains(t, out, "待处理 tool 行：0")
	})

	t.Run("abort clears the checkpoint", func(t *testing.T) {
		out, err := runCLI(t, ctx, "gc", "--shrink-fts", "--abort")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "已清除")
		out, err = runCLI(t, ctx, "gc", "--shrink-fts", "--status")
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

		_, err = runCLI(t, ctx, "gc", "--vacuum")
		assert.ErrMsgContains(t, err, "sess-live")
	})

	t.Run("vacuum returns the space", func(t *testing.T) {
		before := store.DatabaseFileBytes(dbPath)
		out, err := runCLI(t, ctx, "gc", "--vacuum")
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

// setFTSText overwrites the indexed text of one entry, the pre-P1 state.
func setFTSText(t *testing.T, dbPath, sessionID string, seq int64, text string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer db.Close()
	_, err = db.Exec(`UPDATE entries_fts SET text = ? WHERE session_id = ? AND seq = ?`, text, sessionID, seq)
	assert.Require(t, assert.NoErr(t, err))
}

// ftsText reads the indexed text of one entry.
func ftsText(t *testing.T, dbPath, sessionID string, seq int64) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer db.Close()
	var text string
	assert.Require(t, assert.NoErr(t, db.QueryRow(
		`SELECT text FROM entries_fts WHERE session_id = ? AND seq = ?`, sessionID, seq).Scan(&text)))
	return text
}
