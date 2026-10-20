package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// `suga data bodies` through the command line: the jobs of the command that
// stayed (--status, --move, --undo, --check), the two removed one-time flags and
// the v0.36 name are covered by legacy_args_test.go.

// runData runs one `suga data ...` invocation on the test home.
func runData(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).
		RunWithArgs(append([]string{"data"}, args...))
	return stdout.String(), err
}

// TestDataBodiesCommand covers the renamed command: `suga data bodies` runs the
// jobs, and a stub of the previous release still reads back from its file.
func TestDataBodiesCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.DB().Exec(`INSERT INTO sessions(id, cwd, created_at, updated_at) VALUES ('s1', '/w', 't', 't')`)
	assert.Require(t, assert.NoErr(t, err))
	body, err := json.Marshal(api.Message{Role: "tool", Content: strings.Repeat("line\n", 400),
		ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: strings.Repeat("line\n", 400)}})
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(context.Background(), "s1", store.NewEntry{Type: store.EntryTool, JSON: body})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, repo.Close()))

	t.Run("status tells the two types and the file name apart", func(t *testing.T) {
		out, err := runData(t, "bodies", "--status")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "tool：待移出")
		assert.StrContains(t, out, "assistant：可移出")
		assert.StrContains(t, out, "移出不省空间 0 条")
		assert.StrContains(t, out, "仍是旧名（tools.jsonl）")
	})

	t.Run("check accepts the old file name", func(t *testing.T) {
		out, err := runData(t, "bodies", "--check")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "结论：库内桩与会话文件一致")
		assert.False(t, strings.Contains(out, "孤儿记录"), out)
	})

	t.Run("the removed flags are refused", func(t *testing.T) {
		for _, flag := range []string{"--slim", "--rename-files"} {
			_, err := runData(t, "bodies", flag)
			assert.ErrMsgContains(t, err, "flag provided but not defined")
		}
	})
}
