package sugasdk

// End-to-end test: an engine on a temporary data root, a fake provider over
// httptest, one session with a host tool and host context, two turns.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// allowAll permits every tool call (the SDK's fail-closed default is nil).
type allowAll struct{}

func (allowAll) Decide(context.Context, api.ToolCall, api.Tool) error { return nil }

// echoTool is the host's own tool: it proves ExtraTools reach the tool table.
type echoTool struct{ calls *int32 }

func (echoTool) Name() string        { return "sdk_echo" }
func (echoTool) Description() string { return "echoes the given text" }
func (echoTool) Schema() api.ToolSchema {
	return api.ToolSchema{Name: "sdk_echo", Description: "echoes the given text", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{"type": "string", "description": "text to echo"},
		},
	}}
}

func (t echoTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	atomic.AddInt32(t.calls, 1)
	return &api.ToolResult{CallID: call.CallID, Name: "sdk_echo", Content: "echo: " + argumentText(call)}, nil
}

// argumentText renders the tool call's text argument.
func argumentText(call api.ToolCall) string {
	text, _ := call.Arguments["text"].(string)
	return text
}

// sdkTestHome isolates the data root of one test and writes the configuration
// the engine loads (an OpenAI-compatible provider pointing at baseURL).
func sdkTestHome(t *testing.T, baseURL string) string {
	t.Helper()
	gkconfig.Reset()
	appconfig.ResetLoadedStateForTest()
	t.Cleanup(func() {
		gkconfig.Reset()
		appconfig.ResetLoadedStateForTest()
	})
	home := t.TempDir()
	config := "model: openai/gpt-4o\nproviders:\n  openai:\n    type: openai\n    api_key: local-test-key\n    base_url: " + baseURL + "\n"
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600))
	t.Setenv("SUGA_HOME", home)
	return home
}

// chatServer answers the first model request with a call of name and every
// later one with finalText; bodies collects the raw requests.
func chatServer(t *testing.T, toolName, finalText string, bodies *[]string) *httptest.Server {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if bodies != nil {
			*bodies = append(*bodies, string(raw))
		}
		w.Header().Set("Content-Type", "application/json")
		const usage = `"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}`
		if atomic.AddInt32(&calls, 1) == 1 {
			args, err := json.Marshal(map[string]string{"text": "hello"})
			assert.NoErr(t, err)
			quoted, err := json.Marshal(string(args))
			assert.NoErr(t, err)
			_, _ = io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"id":"c1","function":{"name":"`+toolName+`","arguments":`+string(quoted)+`}}]},"finish_reason":"tool_calls"}],`+usage+`}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"`+finalText+`"},"finish_reason":"stop"}],`+usage+`}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestEngineEndToEnd(t *testing.T) {
	ctx := context.Background()
	var bodies []string
	server := chatServer(t, "sdk_echo", "all done", &bodies)
	home := sdkTestHome(t, server.URL)
	workspace := t.TempDir()

	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })

	var calls int32
	var events []Event
	session, err := engine.OpenSession(ctx, "", SessionOptions{
		Workspace:  workspace,
		ExtraTools: []api.Tool{echoTool{calls: &calls}},
		HostContext: func() (string, string) {
			return "host persona: answer briefly", "hash-1"
		},
		Approver: allowAll{},
	})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = session.Close() })
	assert.Require(t, assert.True(t, strings.HasPrefix(session.ID(), "sg_")))

	result, err := session.Send(ctx, Prompt{Text: "use the echo tool"}, func(ev Event) {
		events = append(events, ev)
	})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "COMPLETED", result.State)
	assert.Eq(t, "all done", result.Output)
	assert.Eq(t, session.ID(), result.SessionID)
	assert.True(t, result.Usage.InputTokens > 0)
	assert.True(t, result.SessionUsage.InputTokens > 0)

	t.Run("the host tool ran", func(t *testing.T) {
		assert.Eq(t, int32(1), atomic.LoadInt32(&calls))
		var call, result Event
		for _, ev := range events {
			switch {
			case ev.Kind == "tool_call" && ev.Tool == "sdk_echo":
				call = ev
			case ev.Kind == "tool_result" && ev.Tool == "sdk_echo":
				result = ev
			}
		}
		assert.Eq(t, "sdk_echo", call.Tool)
		assert.StrContains(t, call.Text, "hello")
		assert.Eq(t, "hello", argumentText(api.ToolCall{Arguments: call.ToolArgs}))
		assert.StrContains(t, result.Text, "echo: hello")
	})
	t.Run("the events carry the answer and usage", func(t *testing.T) {
		var text strings.Builder
		var usage, finished bool
		for _, ev := range events {
			switch ev.Kind {
			case string(agentcore.StreamEventTextDelta):
				text.WriteString(ev.Text)
			case "usage":
				usage = usage || ev.Usage != nil
			case "run_finished":
				finished = true
				assert.Eq(t, "all done", ev.Text)
			}
		}
		assert.Eq(t, "all done", text.String())
		assert.True(t, usage)
		assert.True(t, finished)
	})
	t.Run("the host context is in the request", func(t *testing.T) {
		assert.Require(t, assert.True(t, len(bodies) >= 1))
		assert.StrContains(t, bodies[0], "host persona: answer briefly")
	})

	t.Run("the card describes the session", func(t *testing.T) {
		card, err := session.Card(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, session.ID(), card.SessionID)
		assert.Eq(t, DefaultOrigin, card.Origin)
		assert.Eq(t, 1, card.Turns)
		assert.True(t, card.Usage.InputTokens > 0)
		assert.StrNotContains(t, card.State, "running")
	})

	t.Run("the same id continues the conversation", func(t *testing.T) {
		resumed, err := engine.OpenSession(ctx, session.ID(), SessionOptions{Workspace: workspace, Approver: allowAll{}})
		assert.Require(t, assert.NoErr(t, err))
		defer resumed.Close()
		assert.Eq(t, session.ID(), resumed.ID())
		next, err := resumed.Send(ctx, Prompt{Text: "and again"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "COMPLETED", next.State)
		card, err := resumed.Card(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, card.Turns)
	})

	t.Run("Sessions lists the host's session by origin", func(t *testing.T) {
		list, err := engine.Sessions(ctx, SessionFilter{Origin: DefaultOrigin})
		assert.Require(t, assert.NoErr(t, err))
		var found bool
		for _, info := range list {
			if info.ID == session.ID() {
				found = true
				assert.Eq(t, store.OriginXhen, info.Origin)
				assert.Eq(t, workspace, info.Cwd)
			}
		}
		assert.True(t, found)
		other, err := engine.Sessions(ctx, SessionFilter{Origin: "chat"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, other, 0)
	})
	t.Run("Sessions filters by directory", func(t *testing.T) {
		here, err := engine.Sessions(ctx, SessionFilter{Origin: DefaultOrigin, Cwd: workspace})
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, here, 1)
		assert.Eq(t, session.ID(), here[0].ID)
		elsewhere, err := engine.Sessions(ctx, SessionFilter{Origin: DefaultOrigin, Cwd: t.TempDir()})
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, elsewhere, 0)
	})
	t.Run("suga's default session list hides it", func(t *testing.T) {
		dbPath, err := appconfig.ResolveCoreStorePath()
		assert.Require(t, assert.NoErr(t, err))
		repo, err := store.Open(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		defer repo.Close()
		model, err := repo.GetSession(ctx, session.ID())
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, store.HostManagedSession(model), "origin xhen sessions are host managed")
	})
	t.Run("the transcript is searchable", func(t *testing.T) {
		hits, err := engine.Search(ctx, "echo", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, len(hits) > 0)
		assert.Eq(t, session.ID(), hits[0].SessionID)
		assert.StrContains(t, hits[0].Ref, session.ID()+"/")
	})
	t.Run("compact on a short session succeeds", func(t *testing.T) {
		// Nothing to compact counts as success.
		assert.NoErr(t, session.Compact(ctx))
	})
	t.Run("memory reads and writes the user scope", func(t *testing.T) {
		mem := engine.Memory()
		written, err := mem.Add(ctx, "user", MemoryEntry{Title: "Sdk pref", Body: "the host prefers short answers", Kind: "user"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "the host prefers short answers", written.Body)
		entries, err := mem.List(ctx, "user")
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, len(entries) >= 1)
		found, err := mem.Search(ctx, "user", "short answers", 5)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, len(found) >= 1)
	})
}

// TestOpenSessionNewID covers a caller that names the session id itself.
func TestOpenSessionNewID(t *testing.T) {
	ctx := context.Background()
	server := chatServer(t, "sdk_echo", "ok", nil)
	home := sdkTestHome(t, server.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	defer engine.Close()

	session, err := engine.OpenSession(ctx, "xhen-topic-1", SessionOptions{Workspace: t.TempDir(), Approver: allowAll{}})
	assert.Require(t, assert.NoErr(t, err))
	defer session.Close()
	assert.Eq(t, "xhen-topic-1", session.ID())
	_, err = session.Send(ctx, Prompt{Text: "hi"}, nil)
	assert.Require(t, assert.NoErr(t, err))

	// A second OpenSession with the same id continues it instead of failing.
	again, err := engine.OpenSession(ctx, "xhen-topic-1", SessionOptions{Workspace: t.TempDir(), Approver: allowAll{}})
	assert.Require(t, assert.NoErr(t, err))
	defer again.Close()
	assert.Eq(t, "xhen-topic-1", again.ID())
}
