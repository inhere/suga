package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

type appACPClient struct {
	acp.Client
	mu      sync.Mutex
	updates []acp.SessionNotification
}

func (c *appACPClient) SessionUpdate(_ context.Context, update acp.SessionNotification) error {
	c.mu.Lock()
	c.updates = append(c.updates, update)
	c.mu.Unlock()
	return nil
}

func connectAppACP(t *testing.T) (*acp.ClientSideConnection, *appACPClient) {
	t.Helper()
	client := &appACPClient{}
	return connectAppACPWith(t, client), client
}

// connectAppACPWith serves RunPersonalACP to client over pipes; the server
// stops (and must return) at cleanup.
func connectAppACPWith(t *testing.T, client acp.Client) *acp.ClientSideConnection {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunPersonalACP(ctx, clientToAgentR, agentToClientW, "") }()
	conn := acp.NewClientSideConnection(client, clientToAgentW, agentToClientR)
	t.Cleanup(func() {
		cancel()
		_ = clientToAgentW.Close()
		_ = clientToAgentR.Close()
		_ = agentToClientW.Close()
		_ = agentToClientR.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("ACP stdio server did not exit")
		}
	})
	return conn
}

func TestAppACPUsesPersonalStorageAndLoadsInNewServer(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", "") // fall back to HOME/.suga despite the TestMain default
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("ACP project rule"), 0644); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		n := calls.Add(1)
		if n == 1 {
			found := false
			for _, message := range body.Messages {
				// Rules travel in the segment context (a user message), not the system prompt.
				found = found || message.Role == "user" && strings.Contains(message.Content, "ACP project rule")
			}
			if !found {
				t.Error("ACP new session omitted project rule")
			}
		}
		if n == 2 {
			found := false
			for _, message := range body.Messages {
				found = found || message.Content == "reply-1"
			}
			if !found {
				t.Error("loaded ACP session omitted previous answer")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":"reply-%d"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`, n)
	}))
	t.Cleanup(provider.Close)
	_ = gkconfig.Set("model", "openai/gpt-4o")
	setTestProvider(t, provider.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	first, firstClient := connectAppACP(t)
	if _, err := first.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		t.Fatal(err)
	}
	created, err := first.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := first.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("remember")}})
	if err != nil || result.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("first prompt: %+v, %v", result, err)
	}
	if !hasACPAnswer(firstClient, "reply-1") {
		t.Fatal("first ACP process did not emit answer")
	}

	second, secondClient := connectAppACP(t)
	if _, err := second.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.LoadSession(ctx, acp.LoadSessionRequest{SessionId: created.SessionId, Cwd: cwd, McpServers: []acp.McpServer{}}); err != nil {
		t.Fatal(err)
	}
	result, err = second.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("continue")}})
	if err != nil || result.StopReason != acp.StopReasonEndTurn || !hasACPAnswer(secondClient, "reply-2") {
		t.Fatalf("loaded prompt: %+v, %v", result, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", calls.Load())
	}
	if _, err := os.Stat(filepath.Join(home, ".suga", "db", "sessions.db")); err != nil {
		t.Fatal(err)
	}
	assertOnlySessionsDB(t, filepath.Join(home, ".suga", "db"))
	// The session-cumulative usage_update keeps growing after session/load in a
	// new Agent (one update after the model call, one at turn end).
	if got := usageTotals(firstClient); len(got) != 2 || got[0] != 6 || got[1] != 6 {
		t.Fatalf("first process usage totals = %v, want [6 6]", got)
	}
	if got := usageTotals(secondClient); len(got) != 2 || got[0] != 12 || got[1] != 12 {
		t.Fatalf("loaded process usage totals = %v, want [12 12]", got)
	}
	if _, err := os.Stat(filepath.Join(cwd, "db")); !os.IsNotExist(err) {
		t.Fatalf("ACP wrote a database inside workspace: %v", err)
	}
}

func hasACPAnswer(client *appACPClient, text string) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	for _, update := range client.updates {
		chunk := update.Update.AgentMessageChunk
		if chunk != nil && chunk.Content.Text != nil && chunk.Content.Text.Text == text {
			return true
		}
	}
	return false
}

// usageTotals returns the total_tokens of every usage_update the client received.
func usageTotals(client *appACPClient) []float64 {
	client.mu.Lock()
	defer client.mu.Unlock()
	var out []float64
	for _, update := range client.updates {
		if u := update.Update.UsageUpdate; u != nil {
			session, _ := u.Meta["session"].(map[string]any)
			if total, ok := session["total_tokens"].(float64); ok {
				out = append(out, total)
			}
		}
	}
	return out
}

// assertOnlySessionsDB fails unless dir holds sessions.db and, at most, its
// SQLite -wal/-shm sidecars: the single-store design leaves no other database.
func assertOnlySessionsDB(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		switch entry.Name() {
		case "sessions.db":
			found = true
		case "sessions.db-wal", "sessions.db-shm":
		default:
			t.Errorf("unexpected file in %s: %s", dir, entry.Name())
		}
	}
	if !found {
		t.Errorf("sessions.db missing in %s", dir)
	}
}

// The usage_update size is the context window wiring resolved for the main
// model (providers.<id>.models.<model>.context_window), not a config re-read.
func TestAppACPContextWindowComesFromWiring(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", shortTestHome(t))
	t.Setenv("SUGA_MODEL", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}))
	t.Cleanup(provider.Close)
	assert.NoErr(t, gkconfig.Set("workspace", cwd))
	assert.NoErr(t, gkconfig.Set("model", "openai/ctx-model"))
	assert.NoErr(t, gkconfig.Set("providers", map[string]any{"openai": map[string]any{
		"type": "openai", "api_key": "k", "base_url": provider.URL,
		"models": map[string]any{"ctx-model": map[string]any{"context_window": 654321}},
	}}))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, client := connectAppACP(t)
	_, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	assert.NoErr(t, err)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("hi")}})
	assert.NoErr(t, err)

	client.mu.Lock()
	defer client.mu.Unlock()
	var sizes []int
	for _, update := range client.updates {
		if u := update.Update.UsageUpdate; u != nil {
			sizes = append(sizes, int(u.Size))
		}
	}
	// At least one update after a model call and one at turn end. The window
	// is a distinctive value large enough that prompt growth never triggers
	// compaction here; every update must carry it.
	assert.Require(t, assert.True(t, len(sizes) >= 2, "usage updates: %v", sizes))
	for _, size := range sizes {
		assert.Eq(t, 654321, size)
	}
}
