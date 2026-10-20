package cli

import (
	"context"
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

// TestGCVacuumCommand covers `suga data vacuum` through the CLI (data
// governance P1). The ledger is isolated in a temp SUGA_HOME.
func TestGCVacuumCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_HOOKS", "off")
	ctx := context.Background()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))

	// A ledger with one sizeable entry, so VACUUM has a file to rewrite.
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "s1", Title: "s1", Cwd: "/w"}))
	body := `{"Role":"tool","Content":"` + strings.Repeat("中", 3000) + `","ToolCall":{"CallID":"c1","Name":"shell"}}`
	_, err = repo.AppendEntries(ctx, "s1", store.NewEntry{Type: store.EntryTool, JSON: []byte(body)})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.Close())

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
