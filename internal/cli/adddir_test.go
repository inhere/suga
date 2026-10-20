package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// addDirsServer answers every model request with one plain answer.
func addDirsServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// setupAddDirsRun isolates the config, workspace and store of a run.
func setupAddDirsRun(t *testing.T) string {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", shortTestHome(t))
	workspace := t.TempDir()
	t.Chdir(workspace)
	assert.NoErr(t, gkconfig.Set("workspace", workspace))
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	return workspace
}

// TestChatAddDirCommands covers the chat front end of the mid-session
// authorization: the CLI hands chat the runtime's directory handle, so
// `:add-dir` from the keyboard writes the session metadata (and the resumed
// chat lists it again), while the same text delivered by a job runner cannot.
func TestChatAddDirCommands(t *testing.T) {
	setupAddDirsRun(t)
	setTestProvider(t, addDirsServer(t).URL)
	// A writable chat: only then may a directory be authorized read-write.
	assert.NoErr(t, gkconfig.Set("tools.allow_write", true))
	dir := t.TempDir()

	chat := func(input string, extra ...string) (string, string, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		argv := append([]string{}, extra...)
		err := RunPersonalCLI(context.Background(), "chat", argv, strings.NewReader(input), &stdout, &stderr)
		return stdout.String(), stderr.String(), err
	}

	t.Run(":add-dir writes the session metadata", func(t *testing.T) {
		stdout, stderr, err := chat(":dirs\n:add-dir " + dir + ":rw\n:dirs\n:quit\n")
		assert.NoErr(t, err)
		assert.StrContains(t, stdout, "附加目录：无")
		assert.StrContains(t, stdout, "已授权："+dir+"（读写）")
		assert.StrContains(t, stdout, "  "+dir+"（读写 · 会话中追加）")
		found := regexp.MustCompile(`session id: ((?:sess_|s_)\S+)`).FindStringSubmatch(stderr)
		assert.Require(t, assert.Len(t, found, 2))
		assert.Eq(t, []any{map[string]any{"path": dir, "mode": "rw", "source": "session"}},
			sessionAddDirs(t, found[1]))

		t.Run("a resumed chat still lists it", func(t *testing.T) {
			stdout, _, err := chat(":dirs\n:quit\n", "--resume", found[1])
			assert.NoErr(t, err)
			assert.StrContains(t, stdout, "  "+dir+"（读写 · 会话中追加）")
		})
	})

	t.Run(":rm-dir revokes it again", func(t *testing.T) {
		stdout, stderr, err := chat(":add-dir " + dir + "\n:rm-dir " + dir + "\n:dirs\n:quit\n")
		assert.NoErr(t, err)
		assert.StrContains(t, stdout, "已授权："+dir+"（只读）")
		assert.StrContains(t, stdout, "已撤销："+dir)
		assert.StrContains(t, stdout, "附加目录：无")
		found := regexp.MustCompile(`session id: ((?:sess_|s_)\S+)`).FindStringSubmatch(stderr)
		assert.Require(t, assert.Len(t, found, 2))
		assert.Empty(t, sessionAddDirs(t, found[1]))
	})
}

// sessionAddDirs reads the add_dirs metadata of a stored session.
func sessionAddDirs(t *testing.T, sessionID string) []any {
	t.Helper()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer func() { _ = repo.Close() }()
	model, err := repo.GetSession(context.Background(), sessionID)
	assert.Require(t, assert.NoErr(t, err))
	stored, _ := model.Meta["add_dirs"].([]any)
	return stored
}

// runAddDirs runs one "run" command and returns stdout, stderr and the error.
func runAddDirs(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	argv := append([]string{"run", "--prompt", "hi", "--output-format", "stream-json"}, args...)
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs(argv)
	return stdout.String(), stderr.String(), err
}

// TestRunAddDirs covers the CLI side of --add-dir and SUGA_ADD_DIRS: the flag is
// repeatable and its value must be an existing directory, SUGA_ADD_DIRS adds
// more (a bad entry only warns) and the recorded metadata keeps the mode the
// user asked for, even in a --read-only run, together with where the directory
// came from (source, which chat's :dirs shows).
func TestRunAddDirs(t *testing.T) {
	setupAddDirsRun(t)
	setTestProvider(t, addDirsServer(t).URL)
	ro, rw := t.TempDir(), t.TempDir()

	t.Run("the repeatable flag is recorded in the session", func(t *testing.T) {
		_, stderr, err := runAddDirs(t, "--session-id", "adddir-cli-1", "--allow-write",
			"--add-dir", ro, "--add-dir", rw+":rw")
		assert.NoErr(t, err)
		assert.Eq(t, "", stderr)
		assert.Eq(t, []any{
			map[string]any{"path": ro, "mode": "ro", "source": "flag"},
			map[string]any{"path": rw, "mode": "rw", "source": "flag"},
		}, sessionAddDirs(t, "adddir-cli-1"))
	})
	t.Run("a resumed session keeps its directories", func(t *testing.T) {
		_, _, err := runAddDirs(t, "--resume", "adddir-cli-1", "--allow-write", "--add-dir", ro+":rw")
		assert.NoErr(t, err)
		// The :ro entry stays, the path given again takes this invocation's mode.
		assert.Eq(t, []any{
			map[string]any{"path": ro, "mode": "rw", "source": "flag"},
			map[string]any{"path": rw, "mode": "rw", "source": "flag"},
		}, sessionAddDirs(t, "adddir-cli-1"))
	})
	t.Run("SUGA_ADD_DIRS adds directories and warns about a bad entry", func(t *testing.T) {
		t.Setenv("SUGA_ADD_DIRS", strings.Join([]string{rw, filepath.Join(ro, "missing")}, string(filepath.ListSeparator)))
		_, stderr, err := runAddDirs(t, "--session-id", "adddir-cli-2", "--allow-write")
		assert.NoErr(t, err)
		assert.StrContains(t, stderr, "warning: SUGA_ADD_DIRS: skipping")
		assert.StrContains(t, stderr, "is not an existing directory")
		assert.Eq(t, []any{map[string]any{"path": rw, "mode": "ro", "source": "env"}}, sessionAddDirs(t, "adddir-cli-2"))
	})
	t.Run("a --read-only run warns and keeps the requested mode", func(t *testing.T) {
		_, stderr, err := runAddDirs(t, "--session-id", "adddir-cli-3", "--read-only", "--add-dir", rw+":rw")
		assert.NoErr(t, err)
		assert.StrContains(t, stderr, "warning: --read-only: "+rw+" is readable only in this run")
		assert.Eq(t, []any{map[string]any{"path": rw, "mode": "rw", "source": "flag"}}, sessionAddDirs(t, "adddir-cli-3"))
	})
	t.Run("a bad --add-dir value fails before the model runs", func(t *testing.T) {
		stdout, _, err := runAddDirs(t, "--session-id", "adddir-cli-4", "--add-dir", filepath.Join(ro, "missing"))
		assert.ErrMsgContains(t, err, "--add-dir:")
		assert.ErrMsgContains(t, err, "is not an existing directory")
		assert.Eq(t, "", stdout)
		_, _, err = runAddDirs(t, "--session-id", "adddir-cli-4", "--add-dir", "docs")
		assert.ErrMsgContains(t, err, "is not an absolute path")
	})
}
