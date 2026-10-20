package acpserver

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// recordingProvider wraps a scripted provider and keeps every request, so a
// test can check how many model calls a turn made and what tool results the
// model saw.
type recordingProvider struct {
	inner *mock.Provider
	mu    sync.Mutex
	reqs  []*agentcore.GenerateRequest
}

func newRecordingProvider(steps ...mock.Step) *recordingProvider {
	return &recordingProvider{inner: mock.NewProvider(steps...)}
}

func (p *recordingProvider) Name() string { return "recording" }
func (p *recordingProvider) Generate(ctx context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	return p.inner.Generate(ctx, req)
}

func (p *recordingProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.reqs)
}

// promptOnce opens a session (optionally read_only) and runs one prompt.
func promptOnce(t *testing.T, open OpenRuntime, client *testACPClient, mode acp.SessionModeId) acp.PromptResponse {
	t.Helper()
	conn := connectPersonalACP(t, open, client)
	// A hang guard, not a latency check: a slow Windows CI runner took 6.4s for
	// session setup plus a cancelled permission prompt.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	if mode != "" {
		_, err = conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: created.SessionId, ModeId: mode})
		assert.Require(t, assert.NoErr(t, err))
	}
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("go")}})
	assert.Require(t, assert.NoErr(t, err))
	return response
}

// toolCallTrace renders the tool_call notifications as "start:<status>" and
// "<status>" entries in order, e.g. [start:pending in_progress completed].
func toolCallTrace(client *testACPClient) []string {
	client.mu.Lock()
	defer client.mu.Unlock()
	var out []string
	for _, n := range client.updates {
		if start := n.Update.ToolCall; start != nil {
			out = append(out, "start:"+string(start.Status))
		}
		if u := n.Update.ToolCallUpdate; u != nil && u.Status != nil {
			out = append(out, string(*u.Status))
		}
	}
	return out
}

func agentText(client *testACPClient) string {
	client.mu.Lock()
	defer client.mu.Unlock()
	var b strings.Builder
	for _, n := range client.updates {
		if chunk := n.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
			b.WriteString(chunk.Content.Text.Text)
		}
	}
	return b.String()
}

// lastToolResult is the content of the last tool message the model was sent.
func lastToolResult(p *recordingProvider) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.reqs) == 0 {
		return ""
	}
	msgs := p.reqs[len(p.reqs)-1].Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			return msgs[i].Content
		}
	}
	return ""
}

func TestPersonalACPDenialSemantics(t *testing.T) {
	t.Run("one rejection then the model finishes: end_turn", func(t *testing.T) {
		tool := &countingTool{}
		provider := newRecordingProvider(mock.ToolCall("edit-1", "file_write", nil), mock.Final("worked around it"))
		client := &testACPClient{selected: "reject"}
		response := promptOnce(t, testACPRuntime(t, provider, tool), client, "")
		assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
		assert.Eq(t, int32(0), tool.calls.Load())
		assert.Eq(t, 2, provider.calls())
		// The refusal reached the model as a tool error.
		assert.StrContains(t, lastToolResult(provider), ErrPermissionDenied.Error())
		// The refused call is reported failed once (no completed after it).
		assert.Eq(t, []string{"start:pending", "failed"}, toolCallTrace(client))
	})

	t.Run("three rejections end the turn: refusal", func(t *testing.T) {
		tool := &countingTool{}
		provider := newRecordingProvider(
			mock.ToolCall("edit-1", "file_write", nil), mock.ToolCall("edit-2", "file_write", nil),
			mock.ToolCall("edit-3", "file_write", nil), mock.Final("never reached"),
		)
		client := &testACPClient{selected: "reject"}
		response := promptOnce(t, testACPRuntime(t, provider, tool), client, "")
		assert.Eq(t, acp.StopReasonRefusal, response.StopReason)
		assert.Eq(t, int32(0), tool.calls.Load())
		assert.Eq(t, 3, provider.calls()) // no model call after the third refusal
		client.mu.Lock()
		assert.Eq(t, 3, len(client.requests))
		client.mu.Unlock()
		assert.StrContains(t, agentText(client), "3 次工具调用被拒绝")
	})

	t.Run("cancelled permission request aborts the turn: cancelled", func(t *testing.T) {
		tool := &countingTool{}
		provider := newRecordingProvider(mock.ToolCall("edit-1", "file_write", nil), mock.Final("must not run"))
		client := &testACPClient{cancelOutcome: true}
		response := promptOnce(t, testACPRuntime(t, provider, tool), client, "")
		assert.Eq(t, acp.StopReasonCancelled, response.StopReason)
		assert.Eq(t, int32(0), tool.calls.Load())
		// The cancellation is not fed back to the model as a tool result.
		assert.Eq(t, 1, provider.calls())
		assert.Eq(t, []string{"start:pending"}, toolCallTrace(client))
	})

	t.Run("read_only refusals count as denials", func(t *testing.T) {
		tool := &countingTool{}
		provider := newRecordingProvider(
			mock.ToolCall("edit-1", "file_write", nil), mock.ToolCall("edit-2", "file_write", nil),
			mock.ToolCall("edit-3", "file_write", nil), mock.Final("never reached"),
		)
		client := &testACPClient{selected: "allow"}
		response := promptOnce(t, testACPRuntime(t, provider, tool), client, "read_only")
		assert.Eq(t, acp.StopReasonRefusal, response.StopReason)
		assert.Eq(t, int32(0), tool.calls.Load())
		client.mu.Lock()
		assert.Eq(t, 0, len(client.requests)) // read_only never asks
		client.mu.Unlock()
		assert.StrContains(t, lastToolResult(provider), ErrReadOnly.Error())
	})

	t.Run("allowed path keeps the tool_call status sequence", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			tool     *countingTool
			requests int
		}{
			{name: "write tool asks", tool: &countingTool{}, requests: 1},
			{name: "read tool does not ask", tool: &countingTool{name: "web_search"}, requests: 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				provider := mock.NewProvider(mock.ToolCall("call-1", tc.tool.Name(), nil), mock.Final("done"))
				client := &testACPClient{selected: "allow"}
				response := promptOnce(t, testACPRuntime(t, provider, tc.tool), client, "")
				assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
				assert.Eq(t, int32(1), tc.tool.calls.Load())
				// Same as before approval moved into the tool layer.
				assert.Eq(t, []string{"start:pending", "in_progress", "completed"}, toolCallTrace(client))
				client.mu.Lock()
				assert.Eq(t, tc.requests, len(client.requests))
				client.mu.Unlock()
			})
		}
	})

	t.Run("unloaded deferred MCP tool does not trigger approval", func(t *testing.T) {
		tool := &countingTool{name: "srv_write", mcp: true}
		provider := newRecordingProvider(mock.ToolCall("call-1", "srv_write", nil), mock.Final("done"))
		client := &testACPClient{selected: "allow"}
		response := promptOnce(t, testACPRuntimeWith(t, provider, tool, acpTestOptions{deferred: true}), client, "")
		assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
		assert.Eq(t, int32(0), tool.calls.Load())
		client.mu.Lock()
		assert.Eq(t, 0, len(client.requests))
		client.mu.Unlock()
		// Never approved, so the call closes as failed.
		assert.Eq(t, []string{"start:pending", "failed"}, toolCallTrace(client))
	})

	t.Run("observe records a denial event", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "db", "sessions.db")
		provider := mock.NewProvider(mock.ToolCall("edit-1", "file_write", nil), mock.Final("done"))
		client := &testACPClient{selected: "reject"}
		promptOnce(t, testACPRuntimeWith(t, provider, &countingTool{}, acpTestOptions{dbPath: dbPath}), client, "")
		repo, err := store.Open(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		defer repo.Close()
		events, err := repo.ListEvents(context.Background(), store.EventFilter{Kinds: []string{store.EventDenial}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, 1, len(events)))
		assert.Eq(t, "file_write", events[0].Name)
		assert.Eq(t, "denied", events[0].Status)
	})
}
