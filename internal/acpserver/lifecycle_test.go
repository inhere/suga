package acpserver

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

// TestACPLoadReplaysHistory drives one real turn over the connection (mock
// provider with a tool call), closes nothing and loads the session again: the
// client must receive the persisted segment as notifications (users, the
// assistant text, the tool call and its terminal update) — the reverse
// verification of "去掉回放后 load 收到历史通知" fails on an agent without
// the replay.
func TestACPLoadReplaysHistory(t *testing.T) {
	provider := mock.NewProvider(mock.ToolCall("call-1", "file_write", map[string]any{"path": "a.txt"}), mock.Final("done"))
	client := &replayClient{testACPClient: testACPClient{selected: "allow"}}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, &countingTool{}), client)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cwd := t.TempDir()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId,
		Prompt: []acp.ContentBlock{acp.TextBlock("把 a.txt 写出来")}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)

	client.mu.Lock()
	client.armed = true
	client.mu.Unlock()
	_, err = conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: created.SessionId, Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	client.mu.Lock()
	defer client.mu.Unlock()
	client.armed = false
	kinds, titles := client.loadReplay, client.titles
	assert.Require(t, assert.True(t, len(kinds) > 0))
	assert.Eq(t, "user", kinds[0])
	assert.Eq(t, true, contains(kinds, "assistant"))
	// The tool call pair: the title names the tool, the terminal update follows.
	assert.Eq(t, true, contains(kinds, "tool_call"))
	assert.Eq(t, true, contains(kinds, "tool_call_update"))
	assert.Eq(t, true, anyPrefix(titles, "file_write"))
}

// TestACPResumeAndClose: resume runs load's checks without replaying anything
// and close releases the state (a prompt on it is refused afterwards).
func TestACPResumeAndClose(t *testing.T) {
	// cwdRuntime records the session's cwd (Workspace) like the app wiring:
	// the mismatch check of resume needs it.
	conn := connectPersonalACP(t, cwdRuntime(t, filepath.Join(t.TempDir(), "db", "sessions.db")), &testACPClient{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dirA, dirB := t.TempDir(), t.TempDir()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: dirA, McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))

	t.Run("resume returns modes and options, no notifications", func(t *testing.T) {
		resp, err := conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: created.SessionId, Cwd: dirA, McpServers: []acp.McpServer{}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.True(t, resp.Modes != nil))
		assert.Eq(t, true, len(resp.ConfigOptions) > 0)
	})
	t.Run("resume refuses another directory with load's reason", func(t *testing.T) {
		_, err := conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: created.SessionId, Cwd: dirB, McpServers: []acp.McpServer{}})
		assert.Require(t, assert.Err(t, err))
		var rpcErr *acp.RequestError
		assert.Require(t, assert.True(t, errors.As(err, &rpcErr)))
		assert.Eq(t, -32602, rpcErr.Code)
		assert.Eq(t, true, strings.Contains(rpcErr.Message, "start a new session"))
	})
	t.Run("close then prompt is refused", func(t *testing.T) {
		_, err := conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: created.SessionId})
		assert.NoErr(t, err)
		_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("还在吗")}})
		assert.Require(t, assert.Err(t, err))
		var rpcErr *acp.RequestError
		assert.Require(t, assert.True(t, errors.As(err, &rpcErr)))
		assert.Eq(t, -32602, rpcErr.Code)
	})
}

// TestACPLoadStillRejectsUnknownSession still refuses an unknown session after
// the replay landed.
func TestACPLoadStillRejectsUnknownSession(t *testing.T) {
	open := testACPRuntime(t, mock.NewProvider(mock.Final("ok")), nil)
	conn := connectPersonalACP(t, open, &testACPClient{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: "sess_missing", Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Err(t, err)
	var rpcErr *acp.RequestError
	assert.Require(t, assert.True(t, errors.As(err, &rpcErr)))
	assert.Eq(t, -32602, rpcErr.Code)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func anyPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// TestSetSessionConfigOption exercises the option application path against a
// runtime carrying a fake model catalog.
func TestSetSessionConfigOption(t *testing.T) {
	switched := ""
	open := func(_ context.Context, _ string, _ bool, _ []mcp.ServerSettings, _ TurnGate) (Runtime, error) {
		return Runtime{
			Models: []ModelOption{{Ref: "p/m1", Name: "m1"}, {Ref: "p/m2", Name: "m2"}},
			CurrentModel: func() string {
				if switched == "" {
					return "p/m1"
				}
				return switched
			},
			SwitchModel: func(_ context.Context, _, ref string) (string, error) {
				if ref != "p/m1" && ref != "p/m2" {
					return "", errors.New("model " + ref + " is not configured")
				}
				switched = ref
				return ref, nil
			},
			Close: func() error { return nil },
		}, nil
	}
	agent := New(open, false)
	ctx := context.Background()
	sid := acp.SessionId("sess_cfg")
	agent.mu.Lock()
	agent.sessions[sid] = &sessionState{cwd: t.TempDir(), mode: "default", always: newSessionPolicy()}
	agent.mu.Unlock()

	t.Run("the model select switches and returns the full list", func(t *testing.T) {
		resp, err := agent.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: "model", Value: "p/m2"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "p/m2", currentModelRef(resp.ConfigOptions))
		assert.Eq(t, "p/m2", switched)
		assert.Eq(t, 2, len(resp.ConfigOptions))
	})
	t.Run("an unknown model is refused with the configured list in the message", func(t *testing.T) {
		_, err := agent.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: "model", Value: "p/m3"}})
		assert.ErrMsgContains(t, err, "p/m3")
	})
	t.Run("the mode select moves the session mode", func(t *testing.T) {
		resp, err := agent.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: "mode", Value: "read_only"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "read_only", string(resp.ConfigOptions[1].Select.CurrentValue))
	})
	t.Run("unknown config option and unknown session are invalid params", func(t *testing.T) {
		_, err := agent.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: "thought_level", Value: "high"}})
		assert.Require(t, assert.Err(t, err))
		var rpcErr *acp.RequestError
		assert.Require(t, assert.True(t, errors.As(err, &rpcErr)))
		assert.Eq(t, -32602, rpcErr.Code)
		_, err = agent.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: "sess_missing", ConfigId: "model", Value: "p/m1"}})
		assert.Require(t, assert.Err(t, err))
		rpcErr = nil
		assert.Require(t, assert.True(t, errors.As(err, &rpcErr)))
		assert.Eq(t, -32602, rpcErr.Code)
	})
	t.Run("a running turn refuses the switch", func(t *testing.T) {
		agent.mu.Lock()
		agent.sessions[sid].cancel = func() {}
		agent.mu.Unlock()
		_, err := agent.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: "model", Value: "p/m1"}})
		assert.Require(t, assert.Err(t, err))
		var rpcErr *acp.RequestError
		assert.Require(t, assert.True(t, errors.As(err, &rpcErr)))
		assert.Eq(t, -32602, rpcErr.Code)
	})
}

// currentModelRef reads the model select's current value out of a configOptions
// list ("" when there is no model option).
func currentModelRef(options []acp.SessionConfigOption) string {
	for _, o := range options {
		if o.Select != nil && string(o.Select.Id) == "model" {
			return string(o.Select.CurrentValue)
		}
	}
	return ""
}
