package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
)

// clearCtxProvider is the local provider of the --clear-ctx tests: it answers
// with a numbered reply and keeps the messages of every request, so a test can
// assert what the second turn actually sent.
type clearCtxProvider struct {
	messages [][]string
	reply    func(n int) string
}

func newClearCtxProvider() *clearCtxProvider {
	return &clearCtxProvider{reply: func(n int) string { return fmt.Sprintf("reply-%d", n) }}
}

func (p *clearCtxProvider) serve(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		var lines []string
		for _, message := range body.Messages {
			lines = append(lines, message.Role+": "+message.Content)
		}
		p.messages = append(p.messages, lines)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
			p.reply(len(p.messages)))
	}
}

// last returns the message lines of the latest request.
func (p *clearCtxProvider) last() []string {
	if len(p.messages) == 0 {
		return nil
	}
	return p.messages[len(p.messages)-1]
}

// contains reports whether any line of the request carries text.
func containsLine(lines []string, text string) bool {
	for _, line := range lines {
		if strings.Contains(line, text) {
			return true
		}
	}
	return false
}

// setupClearCtxHome isolates the run's home, workspace and provider, and returns
// the store path the run writes to.
func setupClearCtxHome(t *testing.T) string {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", "")
	// The host running the tests may inject an append prompt of its own; the
	// session's saved one has to win, so the variable is cleared.
	t.Setenv("SUGA_APPEND_SYSTEM_PROMPT", "")
	workspace := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("project-level rule"), 0o644))
	t.Chdir(workspace)
	_ = gkconfig.Set("workspace", workspace)
	_ = gkconfig.Set("model", "openai/gpt-4o")
	return filepath.Join(home, ".suga", "db", "sessions.db")
}

func TestRunClearCtxStartsTheTurnOnAnEmptySegment(t *testing.T) {
	dbPath := setupClearCtxHome(t)
	provider := newClearCtxProvider()
	server := httptest.NewServer(provider.serve(t))
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)

	var out, diag bytes.Buffer
	err := RunPersonalCLI(context.Background(), "run",
		[]string{"--prompt", "remember the plan", "--session-id", "gofer-clear", "--output-format", "stream-json", "--read-only", "--append-system-prompt", "saved append prompt"},
		strings.NewReader(""), &out, &diag)
	assert.NoErr(t, err)
	// The resident parts of the first turn: what the clear has to keep.
	first := provider.last()
	assert.Eq(t, true, containsLine(first, "project-level rule"))
	assert.Eq(t, true, containsLine(first, "saved append prompt"))

	out.Reset()
	diag.Reset()
	err = RunPersonalCLI(context.Background(), "run",
		[]string{"--prompt", "what is next", "--session-id", "gofer-clear", "--clear-ctx", "--output-format", "stream-json", "--read-only"},
		strings.NewReader(""), &out, &diag)
	assert.NoErr(t, err)
	assert.Eq(t, true, strings.Contains(out.String(), `"result":"reply-2"`))

	// The second request starts on an empty segment: neither the first prompt nor
	// its answer is sent, while the resident parts (system prompt and the segment
	// context with the rules) still are.
	second := provider.last()
	assert.Eq(t, false, containsLine(second, "remember the plan"))
	assert.Eq(t, false, containsLine(second, "reply-1"))
	assert.Eq(t, true, containsLine(second, "what is next"))
	assert.Eq(t, true, containsLine(second, "以下是本段对话的上下文"))
	// The rules and the session's saved append prompt are the resident parts and
	// survive the clear.
	assert.Eq(t, true, containsLine(second, "project-level rule"))
	assert.Eq(t, true, containsLine(second, "saved append prompt"))

	repo, err := store.Open(dbPath)
	assert.NoErr(t, err)
	defer repo.Close()
	ctx := context.Background()
	// The same session, with its title, and its earlier messages still stored.
	sessions, err := repo.ListSessions(ctx)
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(sessions))
	assert.Eq(t, "gofer-clear", sessions[0].ID)
	assert.Eq(t, "remember the plan", sessions[0].Title)
	// The old messages are out of the context but still searchable.
	hits, err := repo.SearchEntries(ctx, store.EntrySearch{SessionIDs: []string{"gofer-clear"}, Query: "remember"})
	assert.NoErr(t, err)
	assert.Eq(t, true, len(hits) > 0)
	// The trace carries the clear with what it took out of the context.
	events, err := repo.ListEvents(ctx, store.EventFilter{SessionID: "gofer-clear", Kinds: []string{store.EventContextCleared}})
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(events))
	assert.Eq(t, 2, attrIntOf(events[0].Attrs["dropped_messages"]))
	assert.Eq(t, true, attrIntOf(events[0].Attrs["est_tokens"]) > 0)
}

func TestRunClearCtxCreatesAMissingSession(t *testing.T) {
	dbPath := setupClearCtxHome(t)
	provider := newClearCtxProvider()
	server := httptest.NewServer(provider.serve(t))
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)

	var out, diag bytes.Buffer
	err := RunPersonalCLI(context.Background(), "run",
		[]string{"--prompt", "first ever", "--session-id", "fresh-clear", "--clear-ctx", "--output-format", "stream-json", "--read-only"},
		strings.NewReader(""), &out, &diag)
	assert.NoErr(t, err)
	assert.Eq(t, true, strings.Contains(out.String(), `"result":"reply-1"`))
	// No clear happened: nothing was there to clear.
	assert.Eq(t, false, strings.Contains(out.String(), "context_cleared"))

	repo, err := store.Open(dbPath)
	assert.NoErr(t, err)
	defer repo.Close()
	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: "fresh-clear", Kinds: []string{store.EventContextCleared}})
	assert.NoErr(t, err)
	assert.Eq(t, 0, len(events))
}

func TestRunClearCtxRejectsInvalidCombinations(t *testing.T) {
	setupClearCtxHome(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "without a session", args: []string{"--prompt", "hi", "--clear-ctx"}, want: "--clear-ctx requires --session-id or --resume"},
		{name: "with from", args: []string{"--prompt", "hi", "--from", "src", "--clear-ctx"}, want: "--clear-ctx and --from cannot be combined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diag bytes.Buffer
			err := RunPersonalCLI(context.Background(), "run", tc.args, strings.NewReader(""), &out, &diag)
			assert.Eq(t, true, err != nil && strings.Contains(err.Error(), tc.want))
			assert.Eq(t, 2, ExitCode(err))
		})
	}
}

func TestRunStreamJSONReportsContextOccupancy(t *testing.T) {
	setupClearCtxHome(t)
	provider := newClearCtxProvider()
	server := httptest.NewServer(provider.serve(t))
	t.Cleanup(server.Close)
	// A known window, so the reported one can be checked.
	assert.NoErr(t, gkconfig.Set("providers", map[string]any{"openai": map[string]any{
		"type": "openai", "api_key": "local-test-key", "base_url": server.URL,
		"models": map[string]any{"gpt-4o": map[string]any{"context_window": 200000}},
	}}))

	var out, diag bytes.Buffer
	err := RunPersonalCLI(context.Background(), "run",
		[]string{"--prompt", "say done", "--session-id", "ctx-turn", "--output-format", "stream-json", "--read-only"},
		strings.NewReader(""), &out, &diag)
	assert.NoErr(t, err)

	var turnEnd, result map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode stream-json line %q: %v", line, err)
		}
		switch event["type"] {
		case "turn_end":
			turnEnd = event
		case "result":
			result = event
		}
	}
	assert.Require(t, assert.NotNil(t, turnEnd))
	assert.Require(t, assert.NotNil(t, result))
	// turn_end carries the turn's own estimate and window.
	ctxView, ok := turnEnd["context"].(map[string]any)
	assert.Require(t, assert.True(t, ok))
	est, window := attrIntOf(ctxView["est_tokens"]), attrIntOf(ctxView["window"])
	assert.Eq(t, true, est > 0)
	assert.Eq(t, 200000, window)
	assert.Eq(t, true, ctxView["ratio"].(float64) > 0 && ctxView["ratio"].(float64) < 1)
	// The result line reports the same estimate; usage keeps its shape.
	resultCtx, ok := result["context"].(map[string]any)
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, est, attrIntOf(resultCtx["est_tokens"]))
	assert.Eq(t, window, attrIntOf(resultCtx["window"]))
	usage, ok := result["usage"].(map[string]any)
	assert.Require(t, assert.True(t, ok))
	for _, key := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "total_tokens", "cost_usd", "tool_calls"} {
		_, present := usage[key]
		assert.Eq(t, true, present, key)
	}
	_, ok = usage["est_tokens"]
	assert.Eq(t, false, ok)
}

// attrIntOf reads a JSON-decoded number attribute as int (0 for anything else).
func attrIntOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
