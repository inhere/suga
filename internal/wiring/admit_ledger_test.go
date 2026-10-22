package wiring

// Tests of the tool-invocation ledger through the real assembly (Open + runner +
// approval layer): the admit row is written after the approval decision and
// before the tool runs, never for a call whose prompt was cancelled (issue
// suga-r72b; a cancelled prompt used to leave a "state unknown" result).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// candidateTool is a host tool with side effects whose call the fake model asks
// for; it keeps the invocation id it saw, the id the ledger row carries.
type candidateTool struct {
	name       string
	ran        int
	invocation string
}

func (t *candidateTool) Name() string { return t.name }
func (t *candidateTool) Description() string {
	return "host tool " + t.name
}
func (t *candidateTool) ToolMeta() tools.Meta { return tools.Meta{Category: tools.CategoryEdit} }
func (t *candidateTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.name,
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
	}
}
func (t *candidateTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	t.ran++
	t.invocation = tools.InvocationID(ctx)
	return &api.ToolResult{CallID: call.CallID, Name: t.name, Content: "written"}, nil
}

// admitServer is an OpenAI-compatible fake: its first request (of a test case)
// is answered with a call of toolName, every later one with a final message.
// Every request body is kept for the next-turn assertion.
type admitServer struct {
	toolName string
	mu       sync.Mutex
	requests [][]map[string]any
}

func (s *admitServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.Unmarshal(raw, &body)
		answered := false
		s.mu.Lock()
		s.requests = append(s.requests, body.Messages)
		answered = len(s.requests) > 1
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !answered {
			_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"tool_calls":[{"id":"call-1","function":{"name":%q,"arguments":"{\"path\":\"a.txt\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`, s.toolName)
			return
		}
		_, _ = io.WriteString(w, strings.Replace(okBody, "%s", "done", 1))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// lastMessages returns the messages of the most recent request.
func (s *admitServer) lastMessages() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

// openAdmitRuntime opens the runtime the way every entry point does, with the
// fake provider and the given approval policy.
func openAdmitRuntime(t *testing.T, server *httptest.Server, approver tools.Approver, extra ...api.Tool) *Runtime {
	t.Helper()
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k", "base_url": server.URL}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	rt, err := Open(context.Background(), Options{
		Workspace: t.TempDir(), RuleCWD: t.TempDir(), Approver: approver, ExtraTools: extra,
	})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// admitRows counts the turn's admitted ledger rows.
func admitRows(t *testing.T, rt *Runtime, sessionID, turnID string) []store.Entry {
	t.Helper()
	entries, err := rt.repo.ListEntries(context.Background(), sessionID, store.EntryFilter{
		TurnID: turnID, Types: []string{store.EntryAdmit},
	})
	assert.Require(t, assert.NoErr(t, err))
	return entries
}

// TestAdmitLedgerThroughApproval drives real turns of a runtime assembled by
// Open: the ledger follows the approval decision instead of preceding it.
func TestAdmitLedgerThroughApproval(t *testing.T) {
	t.Run("a prompt cancelled mid-question records nothing", func(t *testing.T) {
		resetConfig(t)
		fake := &admitServer{toolName: "host_write"}
		server := fake.start(t)
		host := &candidateTool{name: "host_write"}
		var cancel context.CancelFunc
		// Ctrl-C on the approval question: the turn's context ends while the
		// approver waits, which it reports as an error (as the chat asker does).
		approver := tools.ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error {
			cancel()
			return context.Canceled
		})
		rt := openAdmitRuntime(t, server, approver, host)
		runCtx, cancelRun := context.WithCancel(context.Background())
		cancel = cancelRun
		// The cancelled turn reports its storage write failing on the dead context;
		// the terminal state is what matters here.
		res, _ := rt.Runner.Run(runCtx, personal.RunRequest{Prompt: "写一下"})
		assert.Eq(t, personal.RunCanceled, res.State)

		assert.Len(t, admitRows(t, rt, res.SessionID, res.TurnID), 0)
		assert.Eq(t, 0, host.ran)

		// The next turn must not be told that a call is in an unknown state: the
		// call never ran and has no admit row.
		_, err := rt.Runner.Run(context.Background(), personal.RunRequest{SessionID: res.SessionID, Prompt: "继续"})
		assert.NoErr(t, err)
		for _, m := range fake.lastMessages() {
			text, _ := m["content"].(string)
			assert.NotContains(t, text, "状态未知")
		}
	})

	t.Run("an allowed call is recorded before it runs", func(t *testing.T) {
		resetConfig(t)
		fake := &admitServer{toolName: "host_write"}
		server := fake.start(t)
		host := &candidateTool{name: "host_write"}
		allowAll := tools.ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error { return nil })
		rt := openAdmitRuntime(t, server, allowAll, host)
		res, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "写一下"})
		assert.NoErr(t, err)
		assert.Eq(t, personal.RunCompleted, res.State)
		assert.Eq(t, 1, host.ran)

		entries := admitRows(t, rt, res.SessionID, res.TurnID)
		assert.Require(t, assert.Eq(t, 1, len(entries)))
		var rec struct {
			InvocationID string `json:"invocation_id"`
			CallID       string `json:"call_id"`
			Tool         string `json:"tool"`
		}
		assert.Require(t, assert.NoErr(t, json.Unmarshal(entries[0].JSON, &rec)))
		assert.Eq(t, "host_write", rec.Tool)
		assert.Eq(t, "call-1", rec.CallID)
		assert.Eq(t, res.TurnID+"/call-1", rec.InvocationID)
		// The row carries the id the tool itself saw: both are the same call.
		assert.Eq(t, host.invocation, rec.InvocationID)
	})

	t.Run("a refused call records nothing", func(t *testing.T) {
		resetConfig(t)
		fake := &admitServer{toolName: "host_write"}
		server := fake.start(t)
		host := &candidateTool{name: "host_write"}
		deny := tools.ApproverFunc(func(context.Context, api.ToolCall, api.Tool) error {
			return tools.Denied("用户拒绝执行 host_write")
		})
		rt := openAdmitRuntime(t, server, deny, host)
		res, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "写一下"})
		assert.NoErr(t, err)
		assert.Eq(t, personal.RunCompleted, res.State)
		assert.Eq(t, 0, host.ran)
		assert.Len(t, admitRows(t, rt, res.SessionID, res.TurnID), 0)
	})
}
