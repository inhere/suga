package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// protectedCLIEnv isolates SUGA_HOME, writes a config file whose protected
// settings changed since the user last confirmed them (the snapshot holds
// another value of tools.read_roots) and loads it, as the process start does.
func protectedCLIEnv(t *testing.T) (home, snapshotPath string) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home = t.TempDir()
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_CONFIG", "")
	t.Setenv("SUGA_SESSION_ID", "")
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte("model: openai/test\ntools:\n  read_roots: [a]\n"), 0o600))
	snapshotDir := filepath.Join(home, "run", "protected-config")
	assert.NoErr(t, os.MkdirAll(snapshotDir, 0o700))
	snapshotPath = filepath.Join(snapshotDir, "default.json")
	assert.NoErr(t, os.WriteFile(snapshotPath,
		[]byte(`{"version":1,"mode":"keys","keys":{"tools.read_roots":"other"},"file":""}`), 0o600))
	appconfig.ResetProtectedStateForTest()
	appconfig.ResetLoadedStateForTest()
	t.Cleanup(func() {
		appconfig.ResetProtectedStateForTest()
		appconfig.ResetLoadedStateForTest()
	})
	assert.NoErr(t, appconfig.Load())
	return home, snapshotPath
}

// TestRunProtectedNoticeOnStderr: run names the changed protected settings on
// stderr before the turn and leaves the snapshot as it is (only chat advances).
func TestRunProtectedNoticeOnStderr(t *testing.T) {
	_, snapshotPath := protectedCLIEnv(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}))
	t.Cleanup(provider.Close)
	setTestProvider(t, provider.URL)
	t.Chdir(t.TempDir())
	before, err := os.ReadFile(snapshotPath)
	assert.Require(t, assert.NoErr(t, err))

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"run", "--prompt", "hi"})
	assert.NoErr(t, err)
	assert.StrContains(t, stderr.String(), "安全相关配置自上次确认以来有变化：tools.read_roots")
	assert.StrContains(t, stderr.String(), filepath.Join("config.yaml"))
	// run reports the change, it does not confirm it.
	after, err := os.ReadFile(snapshotPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(before), string(after))
}

// TestACPProtectedNoticeOnce: ACP opens a runtime for every prompt, so the same
// session asks twice; its trace holds one kind=config event.
func TestACPProtectedNoticeOnce(t *testing.T) {
	home, _ := protectedCLIEnv(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}))
	t.Cleanup(provider.Close)
	setTestProvider(t, provider.URL)
	cwd := t.TempDir()
	t.Chdir(cwd)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, _ := connectAppACP(t)
	_, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	assert.Require(t, assert.NoErr(t, err))
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	for i := 0; i < 2; i++ {
		_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(fmt.Sprintf("prompt-%d", i))}})
		assert.Require(t, assert.NoErr(t, err))
	}

	repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	defer func() { _ = repo.Close() }()
	events, err := repo.ListEvents(ctx, store.EventFilter{SessionID: string(created.SessionId), Kinds: []string{store.EventConfig}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(events)))
	assert.Eq(t, "protected_config_changed", events[0].Name)
}

// TestCompactCreatesNoSnapshot: `suga session compact` opens a runtime without a
// session, so it must not create the protected settings snapshot (it would
// confirm a change nobody saw).
func TestCompactCreatesNoSnapshot(t *testing.T) {
	home, snapshotPath := protectedCLIEnv(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}))
	t.Cleanup(provider.Close)
	setTestProvider(t, provider.URL)
	t.Chdir(t.TempDir())

	// A session to compact, then no snapshot at all: compact has to leave it that
	// way instead of comparing and creating one.
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"run", "--prompt", "hi"})
	assert.Require(t, assert.NoErr(t, err))
	sessionID := lastSessionID(t, home)
	assert.NoErr(t, os.Remove(snapshotPath))

	stdout.Reset()
	stderr.Reset()
	// The compact itself may fail on a one-turn session (nothing to summarize);
	// what this checks is that opening its runtime wrote no snapshot.
	_ = newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"session", "compact", sessionID})
	_, err = os.Stat(snapshotPath)
	assert.True(t, os.IsNotExist(err), "compact created the protected snapshot: %v", err)
}

// lastSessionID returns the id of the only session in the store.
func lastSessionID(t *testing.T, home string) string {
	t.Helper()
	repo, err := store.Open(filepath.Join(home, "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	defer func() { _ = repo.Close() }()
	sessions, err := repo.ListSessions(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(sessions)))
	return sessions[0].ID
}
