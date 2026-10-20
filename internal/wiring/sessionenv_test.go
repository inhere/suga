package wiring

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// envCaptureTool records the pairs the provider returns inside a tool call, so
// the test sees the values a subprocess would get in that very turn.
type envCaptureTool struct {
	provider *sessionEnvProvider
	calls    chan []string
}

func (t *envCaptureTool) Name() string        { return "capture_env" }
func (t *envCaptureTool) Description() string { return "test only" }
func (t *envCaptureTool) Schema() api.ToolSchema {
	return api.ToolSchema{Name: t.Name(), Description: t.Description(), InputSchema: map[string]any{"type": "object"}}
}

func (t *envCaptureTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	t.calls <- t.provider.Pairs(ctx)
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: "ok"}, nil
}

// TestSessionEnvPairsInTurn: inside a real turn the provider reports the
// session, its short name and the turn; a session without a short name gets no
// name entry, and the next turn reports its own session and turn.
func TestSessionEnvPairsInTurn(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	assert.Require(t, assert.NoErr(t, err))
	defer func() { _ = repo.Close() }()
	provider := newSessionEnvProvider()
	capture := &envCaptureTool{provider: provider, calls: make(chan []string, 4)}
	registry := tools.NewRegistry()
	registry.Register(capture)
	runner, err := personal.New(personal.Config{Repository: repo, Tools: registry,
		Provider: mock.NewProvider(
			mock.ToolCall("c1", "capture_env", map[string]any{}), mock.Final("done"),
			mock.ToolCall("c2", "capture_env", map[string]any{}), mock.Final("done"))})
	assert.Require(t, assert.NoErr(t, err))
	provider.Bind(runner)

	named, err := runner.NewSession(context.Background(), "ws-k7z", "first")
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, runner.SaveSessionShortName(context.Background(), named.ID, "ws-k7z")))
	first, err := runner.Run(context.Background(), personal.RunRequest{SessionID: named.ID, Prompt: "go"})
	assert.Require(t, assert.NoErr(t, err))
	pairs := <-capture.calls
	assert.Eq(t, []string{
		"SUGA_SESSION_ID=" + named.ID, "SUGA_SESSION_NAME=ws-k7z", "SUGA_TURN_ID=" + first.TurnID,
	}, pairs)

	// A second session (no short name) runs its own turn: nothing of the first
	// session lingers.
	plain, err := runner.NewSession(context.Background(), "", "second")
	assert.Require(t, assert.NoErr(t, err))
	second, err := runner.Run(context.Background(), personal.RunRequest{SessionID: plain.ID, Prompt: "go"})
	assert.Require(t, assert.NoErr(t, err))
	pairs = <-capture.calls
	sort.Strings(pairs)
	assert.Eq(t, []string{"SUGA_SESSION_ID=" + plain.ID, "SUGA_TURN_ID=" + second.TurnID}, pairs)
}

// TestSessionEnvPairsSessionSwitch: a call outside a turn still reports the
// session of its ctx (a hook or a local command), and another session id is a
// different value on the next call.
func TestSessionEnvPairsSessionSwitch(t *testing.T) {
	provider := newSessionEnvProvider()
	for _, session := range []string{"sess-a", "sess-b"} {
		pairs := provider.Pairs(api.WithSessionID(context.Background(), session))
		assert.Eq(t, []string{"SUGA_SESSION_ID=" + session}, pairs)
	}
	assert.Eq(t, 0, len(provider.Pairs(context.Background())))
}
