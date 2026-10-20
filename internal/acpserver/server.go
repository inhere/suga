package acpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/output"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/llm"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// Runtime is one opening of the shared personal session store and Agent core.
type Runtime struct {
	Runner *personal.Runner
	Close  func() error
	// ContextWindow is the model context size in tokens; wiring.Runtime supplies the
	// effective model's window; 0 falls back to appconfig.DefaultContextWindow.
	ContextWindow int
	// LookupTool resolves a tool name to the registered tool, nil when
	// unknown. Permission handling classifies calls by the tool's tools.Meta,
	// never by name, so an MCP tool always needs permission.
	LookupTool func(name string) agentcore.Tool
	// Policy is the shared wiring policy. ACP adds session-level always rules
	// around this immutable per-runtime decision function.
	Policy PolicyDecision
}

func (r Runtime) close() error {
	if r.Close != nil {
		return r.Close()
	}
	return nil
}

// TurnGate is the per-prompt ACP policy handed to the runtime factory. Its Hook
// side sends tool_call notifications and collects usage; its Approver side is
// installed as the approval layer of the tool stack (observe -> visibility ->
// approval -> base) and asks the client via session/request_permission.
// ConfirmMemory asks the client (also via session/request_permission) to
// approve a memory write from a tainted session: nil allows it, a refusal
// returns an error, a cancelled request returns context.Canceled.
type TurnGate interface {
	agentcore.Hook
	tools.Approver
	ConfirmMemory(ctx context.Context, s memory.Scope, op memory.Op) error
}

// OpenRuntime assembles a turn with the selected workspace, tool mode and turn
// gate. gate is nil when the runtime only creates or loads a session (no turn
// runs); the factory must then fail closed (deny every tool call).
type OpenRuntime func(ctx context.Context, cwd string, readOnly bool, servers []mcp.ServerSettings, gate TurnGate) (Runtime, error)

type sessionState struct {
	cwd     string
	servers []mcp.ServerSettings
	mode    acp.SessionModeId
	cancel  context.CancelFunc
	// total and lastContext are guarded by Agent.mu; see recordUsage.
	total       agentcore.Usage
	lastContext int
	// always is intentionally kept in memory only. LoadSession can retain it
	// while this Agent process lives; a new process starts with no snapshot.
	always *sessionPolicy
}

// Agent owns ACP connection state; personal.Runner owns persisted conversation state.
type Agent struct {
	mu       sync.Mutex
	conn     *acp.AgentSideConnection
	sessions map[acp.SessionId]*sessionState
	open     OpenRuntime
	// promptImage declares ContentBlock::Image in initialize (design 0.3, ACP
	// second phase): true when the configuration holds any vision-capable
	// model, decided before the first session exists. A prompt that ends up on a
	// text-only model degrades the pictures to text notes instead.
	promptImage bool
}

var _ acp.Agent = (*Agent)(nil)
var _ acp.AgentLoader = (*Agent)(nil)

func New(open OpenRuntime, promptImage bool) *Agent {
	return &Agent{open: open, sessions: make(map[acp.SessionId]*sessionState), promptImage: promptImage}
}

// SetConnection runs after the connection has started serving requests, so conn
// is guarded by mu like the rest of the shared state.
func (a *Agent) SetConnection(conn *acp.AgentSideConnection) {
	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()
}

func (a *Agent) connection() *acp.AgentSideConnection {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn
}

// Serve uses stdout exclusively for ACP JSON-RPC frames. promptImage declares
// the image content block capability of session/prompt (this configuration's
// initialization has no session yet, so the caller decides from configuration).
func Serve(ctx context.Context, input io.Reader, output io.Writer, open OpenRuntime, promptImage bool) error {
	if open == nil {
		return fmt.Errorf("ACP runtime factory is required")
	}
	agent := New(open, promptImage)
	conn := acp.NewAgentSideConnection(agent, output, input)
	agent.SetConnection(conn)
	select {
	case <-ctx.Done():
		return nil
	case <-conn.Done():
		return nil
	}
}

func (a *Agent) Initialize(_ context.Context, req acp.InitializeRequest) (acp.InitializeResponse, error) {
	if req.ProtocolVersion != acp.ProtocolVersionNumber {
		return acp.InitializeResponse{}, acp.NewInvalidParams(map[string]any{"protocolVersion": req.ProtocolVersion})
	}
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: true,
			// Streamable HTTP MCP servers are accepted in session setup; stdio
			// stays the transport every client can use.
			McpCapabilities: acp.McpCapabilities{Http: true},
			// ContentBlock::Image is accepted in session/prompt when any model of
			// this configuration can see pictures; without one the client keeps
			// the attach-as-path fallback.
			PromptCapabilities: acp.PromptCapabilities{Image: a.promptImage},
		},
		AuthMethods: []acp.AuthMethod{},
	}, nil
}

func (a *Agent) NewSession(ctx context.Context, req acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	cwd, servers, err := sessionInputs(req.Cwd, req.AdditionalDirectories, req.McpServers)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	runtime, err := a.open(ctx, cwd, true, nil, nil)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	model, runErr := runtime.Runner.NewSessionFor(ctx, "", "", personal.OriginACP)
	if err := errors.Join(runErr, runtime.close()); err != nil {
		return acp.NewSessionResponse{}, err
	}
	id := acp.SessionId(model.ID)
	a.mu.Lock()
	a.sessions[id] = &sessionState{cwd: cwd, servers: servers, mode: "default", always: newSessionPolicy()}
	a.mu.Unlock()
	return acp.NewSessionResponse{SessionId: id, Modes: sessionModes("default")}, nil
}

func (a *Agent) LoadSession(ctx context.Context, req acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	cwd, servers, err := sessionInputs(req.Cwd, req.AdditionalDirectories, req.McpServers)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	runtime, err := a.open(ctx, cwd, true, nil, nil)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	model, loadErr := runtime.Runner.LoadSession(ctx, string(req.SessionId))
	closeErr := runtime.close()
	if loadErr != nil {
		return acp.LoadSessionResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": req.SessionId})
	}
	if closeErr != nil {
		return acp.LoadSessionResponse{}, closeErr
	}
	// The session was created in the directory the client passed to session/new,
	// and its tools are confined to that one. Loading it from another directory
	// would run its next turns against a different workspace than the history
	// they continue, so the request is refused (a client that really moved can
	// start a new session with session/new). A session created before cwd was
	// recorded has none and is accepted; the runner fills it in on the next turn.
	if recorded := strings.TrimSpace(model.Cwd); recorded != "" && !personal.SameDir(recorded, cwd) {
		return acp.LoadSessionResponse{}, cwdMismatchError(req.SessionId, recorded, cwd)
	}
	a.mu.Lock()
	current := a.sessions[req.SessionId]
	if current != nil && current.cancel != nil {
		a.mu.Unlock()
		return acp.LoadSessionResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": req.SessionId, "reason": "prompt active"})
	}
	always := newSessionPolicy()
	if current != nil && current.always != nil {
		always = current.always
	}
	a.sessions[req.SessionId] = &sessionState{cwd: cwd, servers: servers, mode: "default", always: always}
	a.mu.Unlock()
	return acp.LoadSessionResponse{Modes: sessionModes("default")}, nil
}

func (a *Agent) SetSessionMode(_ context.Context, req acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	if req.ModeId != "default" && req.ModeId != "read_only" {
		return acp.SetSessionModeResponse{}, acp.NewInvalidParams(map[string]any{"modeId": req.ModeId})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[req.SessionId]
	if state == nil || state.cancel != nil {
		return acp.SetSessionModeResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": req.SessionId})
	}
	state.mode = req.ModeId
	return acp.SetSessionModeResponse{}, nil
}

func (a *Agent) Prompt(ctx context.Context, req acp.PromptRequest) (acp.PromptResponse, error) {
	if a.connection() == nil {
		return acp.PromptResponse{}, fmt.Errorf("ACP connection is not ready")
	}
	prompt, err := promptText(req.Prompt)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	a.mu.Lock()
	state := a.sessions[req.SessionId]
	if state == nil || state.cancel != nil {
		a.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": req.SessionId})
	}
	turnCtx, cancel := context.WithCancel(ctx)
	state.cancel = cancel
	cwd, servers, readOnly := state.cwd, state.servers, state.mode == "read_only"
	always := state.always
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		state.cancel = nil
		a.mu.Unlock()
	}()

	hook := &permissionHook{conn: a.connection(), sessionID: req.SessionId, cwd: cwd, readOnly: readOnly,
		session: always}
	runtime, err := a.open(turnCtx, cwd, readOnly, servers, hook)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	hook.lookup = runtime.LookupTool
	hook.policy = runtime.Policy
	window := runtime.ContextWindow
	if window <= 0 {
		window = appconfig.DefaultContextWindow
	}
	textSent := false
	// Budget metadata is carried by the final run_finished event. Keep it
	// separate from RunResult: ACP must not expand the personal run contract.
	var finalBudget *personal.BudgetSnapshot
	captureBudget := func(event personal.Event) {
		if event.Budget != nil {
			finalBudget = event.Budget
		}
		if event.Usage != nil && event.Usage.Budget != nil {
			finalBudget = event.Usage.Budget
		}
	}
	emit := func(ctx context.Context, event personal.Event) error {
		captureBudget(event)
		if event.Kind == "usage" && event.Usage != nil {
			return a.sendRunningUsage(ctx, req.SessionId, state, window, *event.Usage)
		}
		if event.Kind == "run_finished" && event.Usage != nil {
			return nil
		}
		if event.Kind == "plan" && event.Plan != nil {
			return a.connection().SessionUpdate(ctx, acp.SessionNotification{SessionId: req.SessionId, Update: planUpdate(event.Plan)})
		}
		if event.Kind == string(agentcore.StreamEventRetry) {
			// A thought, so the notice never becomes part of the answer text.
			return a.connection().SessionUpdate(ctx, acp.SessionNotification{SessionId: req.SessionId, Update: acp.UpdateAgentThoughtText(output.RetryNotice(event) + "\n")})
		}
		if event.Kind != string(agentcore.StreamEventTextDelta) || event.Text == "" {
			return nil
		}
		textSent = true
		return a.connection().SessionUpdate(ctx, acp.SessionNotification{SessionId: req.SessionId, Update: acp.UpdateAgentMessageText(event.Text)})
	}
	result, runErr := runtime.Runner.Run(turnCtx, personal.RunRequest{SessionID: string(req.SessionId), Prompt: prompt, Emit: emit})
	closeErr := runtime.close()
	if turnCtx.Err() != nil || errors.Is(runErr, context.Canceled) {
		// A cancelled turn still consumed tokens; report them before returning.
		// The SDK cancels the request ctx on session/cancel, so detach from it
		// (bounded) or the notification could never be written.
		usageCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		usageErr := a.sendUsage(usageCtx, req.SessionId, state, window, result, finalBudget)
		stop()
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, errors.Join(closeErr, usageErr)
	}
	if err := a.sendUsage(ctx, req.SessionId, state, window, result, finalBudget); err != nil {
		return acp.PromptResponse{}, errors.Join(runErr, closeErr, err)
	}
	// Refused tool calls are tool errors and the turn continues (end_turn); only
	// a hook-level failure (no connection) still surfaces as a refusal error.
	if errors.Is(runErr, ErrPermissionDenied) || errors.Is(runErr, ErrReadOnly) {
		return acp.PromptResponse{StopReason: acp.StopReasonRefusal}, closeErr
	}
	if err := errors.Join(runErr, closeErr); err != nil {
		return acp.PromptResponse{}, promptError(err)
	}
	if !textSent && result.Output != "" {
		if err := a.connection().SessionUpdate(ctx, acp.SessionNotification{SessionId: req.SessionId, Update: acp.UpdateAgentMessageText(result.Output)}); err != nil {
			return acp.PromptResponse{}, err
		}
	}
	// personal.DenialLimit refusals in one turn end it: report refusal.
	if result.DenialLimitReached {
		return acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}
	if result.Status == agentcore.RunYielded {
		return acp.PromptResponse{StopReason: acp.StopReasonMaxTurnRequests}, nil
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// promptError turns a failed prompt into the JSON-RPC error the client sees.
// The SDK's own shape (Internal error, data.error with the text) is kept; when
// the turn died on a model request, the provider failure's class is added to
// the data as error_kind (llm.ErrorKind), so a client such as gofer can tell a
// cut stream from a request the provider refused - the same value the CLI
// writes on its stream-json error line. A failure without a model error (a
// storage or hook error, a cancelled turn) carries no error_kind and is
// reported as before. An exhausted quota whose provider named a reset time adds
// it as reset_at (llm.ResetAt), like the stream-json error line.
func promptError(err error) error {
	if kind := llm.ErrorKind(err); kind != "" {
		data := map[string]any{"error": err.Error(), "error_kind": kind}
		if reset := llm.ResetAt(err); reset != "" {
			data["reset_at"] = reset
		}
		return acp.NewInternalError(data)
	}
	return err
}

// sendUsage reports the final usage of a turn (completed or cancelled): the
// run total plus the session-cumulative total.
func (a *Agent) sendUsage(ctx context.Context, id acp.SessionId, state *sessionState, window int, result personal.RunResult, budget ...*personal.BudgetSnapshot) error {
	total, used := state.recordUsage(a, result)
	if err := a.connection().SessionUpdate(ctx, acp.SessionNotification{SessionId: id, Update: usageUpdate(result.RunUsage, total, used, window, budget...)}); err != nil {
		return fmt.Errorf("send usage update: %w", err)
	}
	return nil
}

// sendRunningUsage reports the usage after one model call in the middle of a
// turn, so clients (gofer budgets) see it before the turn ends.
func (a *Agent) sendRunningUsage(ctx context.Context, id acp.SessionId, state *sessionState, window int, u personal.UsageUpdate) error {
	used := u.LastInputTokens
	if used <= 0 {
		used = state.lastContextSize(a)
	}
	if err := a.connection().SessionUpdate(ctx, acp.SessionNotification{SessionId: id, Update: usageUpdate(u.Run, u.Session, used, window)}); err != nil {
		return fmt.Errorf("send usage update: %w", err)
	}
	return nil
}

// planUpdate maps an update_plan event onto an ACP plan update (full replace).
func planUpdate(plan *native.Plan) acp.SessionUpdate {
	entries := make([]acp.PlanEntry, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		status := acp.PlanEntryStatusPending
		switch step.Status {
		case native.PlanInProgress:
			status = acp.PlanEntryStatusInProgress
		case native.PlanCompleted:
			status = acp.PlanEntryStatusCompleted
		}
		entries = append(entries, acp.PlanEntry{Content: step.Step, Status: status, Priority: acp.PlanEntryPriorityMedium})
	}
	return acp.UpdatePlan(entries...)
}

func (a *Agent) Cancel(_ context.Context, req acp.CancelNotification) error {
	a.mu.Lock()
	state := a.sessions[req.SessionId]
	var cancel context.CancelFunc
	if state != nil {
		cancel = state.cancel
	}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func sessionModes(current acp.SessionModeId) *acp.SessionModeState {
	return &acp.SessionModeState{CurrentModeId: current, AvailableModes: []acp.SessionMode{
		{Id: "default", Name: "Default"}, {Id: "read_only", Name: "Read only"},
	}}
}

// invalidParamsCode is the JSON-RPC error code of an invalid parameter
// (-32602), the class a session/load with a foreign cwd is refused with.
const invalidParamsCode = -32602

// cwdMismatchError is that refusal: the message names the directory the
// session belongs to and the one the client asked for, the data carries both
// for a client that maps the error itself.
func cwdMismatchError(sessionID acp.SessionId, sessionCwd, cwd string) *acp.RequestError {
	return &acp.RequestError{
		Code: invalidParamsCode,
		Message: fmt.Sprintf("invalid params: session %s was created in %s, not in %s; "+
			"start a new session (session/new) to work in %s", sessionID, sessionCwd, cwd, cwd),
		Data: map[string]any{"sessionId": sessionID, "sessionCwd": sessionCwd, "cwd": cwd},
	}
}

func sessionInputs(cwd string, roots []string, mcpServers []acp.McpServer) (string, []mcp.ServerSettings, error) {
	if !filepath.IsAbs(cwd) || len(roots) != 0 {
		return "", nil, acp.NewInvalidParams(map[string]any{"cwd": cwd, "reason": "absolute cwd required; additional roots unsupported"})
	}
	servers, err := mcpSettings(mcpServers)
	if err != nil {
		return "", nil, err
	}
	return filepath.Clean(cwd), servers, nil
}

func promptText(blocks []acp.ContentBlock) (string, error) {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch {
		case block.Text != nil:
			parts = append(parts, block.Text.Text)
		case block.ResourceLink != nil:
			parts = append(parts, "Resource: "+block.ResourceLink.Uri)
		default:
			return "", acp.NewInvalidParams(map[string]any{"prompt": "unsupported content block"})
		}
	}
	return strings.Join(parts, "\n"), nil
}

func (*Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, acp.NewMethodNotFound(acp.AgentMethodAuthenticate)
}
func (*Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, acp.NewMethodNotFound(acp.AgentMethodLogout)
}
func (*Agent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionClose)
}
func (*Agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionList)
}
func (*Agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionResume)
}
func (*Agent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetConfigOption)
}
