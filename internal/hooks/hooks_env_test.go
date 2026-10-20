package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// TestFakeHookEnvProcess is the fake hook command of the environment test: it
// writes the session variables it was run with to SUGA_FAKE_HOOK_ENV_LOG as one
// "|" separated line, then exits before the testing framework prints anything.
func TestFakeHookEnvProcess(t *testing.T) {
	logPath := os.Getenv("SUGA_FAKE_HOOK_ENV_LOG")
	if logPath == "" {
		return
	}
	line := strings.Join([]string{
		os.Getenv("SUGA_SESSION_ID"), os.Getenv("SUGA_SESSION_NAME"),
		os.Getenv("SUGA_TURN_ID"), os.Getenv("GOFER_SESSION_ID"),
	}, "|")
	if err := os.WriteFile(logPath, []byte(line), 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

// hookEnvClient builds a client running TestFakeHookEnvProcess, with logPath
// recording the environment of the last call.
func hookEnvClient(t *testing.T, logPath string, opts ...Option) Client {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "")
	t.Setenv("SUGA_FAKE_HOOK_ENV_LOG", logPath)
	assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.command",
		[]string{os.Args[0], "-test.run=^TestFakeHookEnvProcess$"})))
	client, err := New(opts...)
	assert.Require(t, assert.NoErr(t, err))
	return client
}

func readHookEnv(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	assert.Require(t, assert.NoErr(t, err))
	return string(data)
}

// TestHookCommandSessionEnv: the hook command runs with the session variables of
// the call; an inherited value of those names is replaced, GOFER_SESSION_ID is
// passed through.
func TestHookCommandSessionEnv(t *testing.T) {
	t.Run("provider values reach the command", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "hook-env.txt")
		t.Setenv("SUGA_SESSION_NAME", "parent-name")
		t.Setenv("SUGA_TURN_ID", "parent-turn")
		t.Setenv("GOFER_SESSION_ID", "gofer-9")
		client := hookEnvClient(t, logPath, WithExtraEnv(func(context.Context) []string {
			return []string{"SUGA_SESSION_NAME=ws-k7z", "SUGA_TURN_ID=turn-1"}
		}))
		client.Call(context.Background(), Event{Name: "UserPromptSubmit", SessionID: "sess-hook"})
		assert.Eq(t, "sess-hook|ws-k7z|turn-1|gofer-9", readHookEnv(t, logPath))
	})
	t.Run("the event session fills in without a provider session", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "hook-env.txt")
		t.Setenv("GOFER_SESSION_ID", "gofer-9")
		client := hookEnvClient(t, logPath, WithExtraEnv(func(context.Context) []string { return nil }))
		client.Call(context.Background(), Event{Name: "SessionStart", SessionID: "sess-start"})
		assert.Eq(t, "sess-start|||gofer-9", readHookEnv(t, logPath))
	})
	t.Run("the ctx session wins over the event", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "hook-env.txt")
		t.Setenv("GOFER_SESSION_ID", "gofer-9")
		client := hookEnvClient(t, logPath, WithExtraEnv(func(ctx context.Context) []string {
			return []string{"SUGA_SESSION_ID=" + api.SessionIDFrom(ctx)}
		}))
		client.Call(api.WithSessionID(context.Background(), "sess-ctx"), Event{Name: "PostToolUse", SessionID: "sess-event"})
		assert.Eq(t, "sess-ctx|||gofer-9", readHookEnv(t, logPath))
	})
	t.Run("the event session's short name is resolved", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "hook-env.txt")
		t.Setenv("GOFER_SESSION_ID", "gofer-9")
		client := hookEnvClient(t, logPath, WithExtraEnv(func(ctx context.Context) []string {
			session := api.SessionIDFrom(ctx)
			if session == "" {
				return nil
			}
			return []string{"SUGA_SESSION_NAME=name-" + session}
		}))
		client.Call(context.Background(), Event{Name: "SessionStart", SessionID: "sess-start"})
		assert.Eq(t, "sess-start|name-sess-start||gofer-9", readHookEnv(t, logPath))
	})
	t.Run("an inherited session variable is not passed on", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "hook-env.txt")
		t.Setenv("SUGA_SESSION_ID", "parent-session")
		client := hookEnvClient(t, logPath)
		client.Call(context.Background(), Event{Name: "Stop"})
		assert.Eq(t, "|||", readHookEnv(t, logPath))
	})
}
