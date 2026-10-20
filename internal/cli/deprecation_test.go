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

// runDataCapture runs one `suga data ...` invocation and returns both streams,
// so a test can check that a deprecation notice goes to stderr only.
func runDataCapture(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).
		RunWithArgs(append([]string{"data"}, args...))
	return stdout.String(), stderr.String(), err
}

// TestDeprecatedFormsNoticeOnce: each form scheduled for removal in v0.41.0 says
// so once on stderr when used, and the report on stdout stays untouched (the
// stream-json contract and scripts parse stdout only). The jobs that stay
// (--status/--move/--undo/--check) print no notice.
func TestDeprecatedFormsNoticeOnce(t *testing.T) {
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

	t.Run("deprecated forms say so once on stderr", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			args  []string
			forms string
			want  string
		}{
			{"tool-bodies alias", []string{"tool-bodies", "--status"}, "suga data tool-bodies",
				"改用 suga data bodies"},
			{"dedupe-tool-results", []string{"dedupe-tool-results", "--status"}, "suga data dedupe-tool-results",
				"它用于把 v0.35 及更早的库迁移到当前格式，已迁移的库不需要"},
			{"dedupe-tool-results undo", []string{"dedupe-tool-results", "--undo"}, "suga data dedupe-tool-results",
				"已迁移的库不需要"},
			{"shrink-fts", []string{"shrink-fts", "--status"}, "suga data shrink-fts",
				"已迁移的库不需要"},
			{"bodies slim", []string{"bodies", "--slim"}, "suga data bodies --slim",
				"已迁移的库不需要"},
			{"bodies rename-files", []string{"bodies", "--rename-files"}, "suga data bodies --rename-files",
				"已迁移的库不需要"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stdout, stderr, err := runDataCapture(t, tc.args...)
				assert.Require(t, assert.NoErr(t, err))
				assert.Eq(t, 1, strings.Count(stderr, "已弃用"), "one notice, got:\n%s", stderr)
				assert.StrContains(t, stderr, "提示："+tc.forms+" 已弃用，将在 "+deprecationRemoval+" 移除")
				assert.StrContains(t, stderr, tc.want)
				assert.False(t, strings.Contains(stdout, "已弃用"), "the notice must not reach stdout:\n%s", stdout)
				assert.False(t, strings.Contains(stdout, "提示："), "stdout carries the report only:\n%s", stdout)
			})
		}
	})

	t.Run("the remaining jobs are not deprecated", func(t *testing.T) {
		for _, args := range [][]string{
			{"bodies", "--status"},
			{"bodies", "--check"},
			{"bodies", "--move"},
			{"bodies", "--undo"},
		} {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				_, stderr, err := runDataCapture(t, args...)
				assert.Require(t, assert.NoErr(t, err))
				assert.False(t, strings.Contains(stderr, "已弃用"), "no notice expected:\n%s", stderr)
			})
		}
	})
}
