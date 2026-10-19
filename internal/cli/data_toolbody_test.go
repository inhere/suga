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

// `suga data tool-bodies --slim` through the command line: the flag picks the
// pass, the other modes are refused and a slimmed ledger reports no old stub.

// runDataToolBodies args runs one `suga data tool-bodies` invocation on home.
func runDataToolBodies(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).
		RunWithArgs(append([]string{"data", "tool-bodies"}, args...))
	return stdout.String(), err
}

func TestToolBodiesSlimCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)

	// A moved tool body, then the stub of the previous release: the pass has one
	// row to narrow.
	repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.DB().Exec(`INSERT INTO sessions(id, cwd, created_at, updated_at) VALUES ('s1', '/w', 't', 't')`)
	assert.Require(t, assert.NoErr(t, err))
	body, err := json.Marshal(api.Message{Role: "tool", Content: strings.Repeat("line\n", 400),
		ToolCall:   &api.ToolCall{CallID: "c1", Name: "shell", Arguments: map[string]any{"cmd": "ls"}},
		ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: strings.Repeat("line\n", 400)}})
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(context.Background(), "s1", store.NewEntry{Type: store.EntryTool, JSON: body})
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.DB().Exec(`UPDATE entries SET json = ? WHERE session_id = 's1' AND seq = 1`,
		`{"Role":"tool","Content":"line","ToolCall":{"CallID":"c1","Name":"shell","Arguments":null,"ArgumentsError":""},`+
			`"ToolResult":{"CallID":"c1","Name":"shell","Preview":"line"},"Reasoning":"why"}`)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, repo.Close()))

	t.Run("modes are exclusive", func(t *testing.T) {
		_, err := runDataToolBodies(t, "--slim", "--move")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "different jobs")
		_, err = runDataToolBodies(t, "--slim", "--remove-files")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "only applies to --undo")
	})

	t.Run("status counts the old-shape stubs", func(t *testing.T) {
		out, err := runDataToolBodies(t, "--status")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "旧形状的桩：1 个")
	})

	t.Run("slim rewrites it", func(t *testing.T) {
		out, err := runDataToolBodies(t, "--slim", "--batch", "10")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "瘦身完成：1 个桩改写为当前形状")
		assert.StrContains(t, out, "suga data vacuum")
		out, err = runDataToolBodies(t, "--status")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "旧形状的桩：无")
		// The body still comes back from the session file.
		repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
		assert.Require(t, assert.NoErr(t, err))
		defer repo.Close()
		entries, err := repo.ListEntries(context.Background(), "s1", store.EntryFilter{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(entries))
		assert.Eq(t, string(body), string(entries[0].JSON))
	})
}
