package acpserver

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
)

// compatAgent exercises the Gofer-facing ACP methods before wiring a model runtime.
type compatAgent struct {
	acp.Agent
	conn    *acp.AgentSideConnection
	started chan struct{}
}

func (*compatAgent) Initialize(_ context.Context, req acp.InitializeRequest) (acp.InitializeResponse, error) {
	if req.ProtocolVersion != acp.ProtocolVersionNumber {
		return acp.InitializeResponse{}, acp.NewInvalidParams(map[string]any{"protocolVersion": req.ProtocolVersion})
	}
	return acp.InitializeResponse{
		ProtocolVersion:   acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{LoadSession: true},
		AuthMethods:       []acp.AuthMethod{},
	}, nil
}

func (*compatAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return acp.NewSessionResponse{SessionId: "compat-session", Modes: compatModes("default")}, nil
}

func (*compatAgent) LoadSession(_ context.Context, req acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	if req.SessionId != "compat-session" {
		return acp.LoadSessionResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": req.SessionId})
	}
	return acp.LoadSessionResponse{Modes: compatModes("default")}, nil
}

func (*compatAgent) SetSessionMode(_ context.Context, req acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	if req.ModeId != "read_only" {
		return acp.SetSessionModeResponse{}, acp.NewInvalidParams(map[string]any{"modeId": req.ModeId})
	}
	return acp.SetSessionModeResponse{}, nil
}

func (*compatAgent) Cancel(context.Context, acp.CancelNotification) error { return nil }

func (a *compatAgent) Prompt(ctx context.Context, req acp.PromptRequest) (acp.PromptResponse, error) {
	if a.started != nil {
		close(a.started)
		<-ctx.Done()
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	if err := a.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: req.SessionId,
		Update:    acp.UpdateAgentMessageText("compatibility check"),
	}); err != nil {
		return acp.PromptResponse{}, err
	}
	decision, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: req.SessionId,
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: "compat-edit",
			Title:      acp.Ptr("Edit a file"),
			Kind:       acp.Ptr(acp.ToolKindEdit),
			Status:     acp.Ptr(acp.ToolCallStatusPending),
		},
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow", OptionId: "allow"},
			{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "reject"},
		},
	})
	if err != nil {
		return acp.PromptResponse{}, err
	}
	if decision.Outcome.Selected == nil || decision.Outcome.Selected.OptionId != "allow" {
		return acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func compatModes(current acp.SessionModeId) *acp.SessionModeState {
	return &acp.SessionModeState{
		CurrentModeId: current,
		AvailableModes: []acp.SessionMode{
			{Id: "default", Name: "Default"},
			{Id: "read_only", Name: "Read only"},
		},
	}
}

type compatClient struct {
	acp.Client
	selected   acp.PermissionOptionId
	updates    chan acp.SessionNotification
	permission chan acp.RequestPermissionRequest
}

func (c *compatClient) RequestPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.permission <- req
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Selected: &acp.RequestPermissionOutcomeSelected{OptionId: c.selected},
		},
	}, nil
}

func (c *compatClient) SessionUpdate(_ context.Context, update acp.SessionNotification) error {
	c.updates <- update
	return nil
}

func connectCompat(t *testing.T, agent *compatAgent, client *compatClient) *acp.ClientSideConnection {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()
	agent.conn = acp.NewAgentSideConnection(agent, agentToClientW, clientToAgentR)
	conn := acp.NewClientSideConnection(client, clientToAgentW, agentToClientR)
	t.Cleanup(func() {
		_ = clientToAgentW.Close()
		_ = clientToAgentR.Close()
		_ = agentToClientW.Close()
		_ = agentToClientR.Close()
	})
	return conn
}

func TestACPGoSDKSupportsClientSessionAndPermissionContract(t *testing.T) {
	agent := &compatAgent{}
	client := &compatClient{
		selected:   "reject",
		updates:    make(chan acp.SessionNotification, 2),
		permission: make(chan acp.RequestPermissionRequest, 2),
	}
	conn := connectCompat(t, agent, client)
	ctx := testCtx(t)

	initialized, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, acp.ProtocolVersion(acp.ProtocolVersionNumber), initialized.ProtocolVersion)
	assert.Eq(t, true, initialized.AgentCapabilities.LoadSession)

	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, acp.SessionId("compat-session"), created.SessionId)
	assert.Eq(t, acp.SessionModeId("read_only"), created.Modes.AvailableModes[1].Id)

	loaded, err := conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: created.SessionId, Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, false, strings.Contains(string(encoded), "sessionId"))
	if _, err := conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: created.SessionId, ModeId: "read_only"}); err != nil {
		t.Fatal(err)
	}

	denied, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("test permission")}})
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, acp.StopReasonRefusal, denied.StopReason)
	assert.Eq(t, created.SessionId, (<-client.updates).SessionId)
	assert.Eq(t, acp.ToolKindEdit, *(<-client.permission).ToolCall.Kind)

	client.selected = "allow"
	allowed, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("allow permission")}})
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, acp.StopReasonEndTurn, allowed.StopReason)
}

func TestACPGoSDKCancelEndsAnActivePrompt(t *testing.T) {
	agent := &compatAgent{started: make(chan struct{})}
	client := &compatClient{updates: make(chan acp.SessionNotification, 1), permission: make(chan acp.RequestPermissionRequest, 1)}
	conn := connectCompat(t, agent, client)
	ctx := testCtx(t)

	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan acp.PromptResponse, 1)
	errs := make(chan error, 1)
	go func() {
		response, promptErr := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("wait")}})
		result <- response
		errs <- promptErr
	}()
	select {
	case <-agent.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := conn.Cancel(ctx, acp.CancelNotification{SessionId: created.SessionId}); err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, acp.StopReasonCancelled, (<-result).StopReason)
}
