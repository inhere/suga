package wiring

import (
	"context"
	"sync"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// The variables suga exports to the subprocesses of a session (shell_exec,
// code_exec, watch, chat's local commands and the hook command): which session
// and turn a command runs for, so the command (a nested agent, a wrapper script,
// gofer) can address or report on the conversation. GOFER_SESSION_ID is not set
// here: gofer owns that name and reads it as its own session.
const (
	envSessionID   = "SUGA_SESSION_ID"
	envSessionName = "SUGA_SESSION_NAME"
	envTurnID      = "SUGA_TURN_ID"
)

// sessionEnvProvider builds those variables at every command. They are read
// from the call's ctx (a sub-agent runs its tools in its own session, and an ACP
// process serves several sessions) and from the session store (the short name a
// chat registered); both change while the runtime lives, so nothing is frozen
// at build time. The runner is bound after personal.New, because the native
// tools are built before it exists.
type sessionEnvProvider struct {
	mu     sync.Mutex
	runner *personal.Runner
	// names caches the short name of each session: it is written once and kept
	// across resumes, so one store read per session is enough.
	names map[string]string
}

func newSessionEnvProvider() *sessionEnvProvider {
	return &sessionEnvProvider{names: map[string]string{}}
}

// Bind sets the runner the short name comes from (called once the runner of the
// runtime exists).
func (p *sessionEnvProvider) Bind(runner *personal.Runner) {
	p.mu.Lock()
	p.runner = runner
	p.mu.Unlock()
}

// Pairs returns the environment entries of the session and turn ctx belongs to.
// A session without a short name gets none (the variable is left unset rather
// than empty); outside a turn the turn id is left out too.
func (p *sessionEnvProvider) Pairs(ctx context.Context) []string {
	sessionID, turnID := personal.CurrentTurn(ctx)
	if sessionID == "" {
		sessionID = api.SessionIDFrom(ctx)
	}
	pairs := make([]string, 0, 3)
	if sessionID != "" {
		pairs = append(pairs, envSessionID+"="+sessionID)
	}
	if name := p.shortName(ctx, sessionID); name != "" {
		pairs = append(pairs, envSessionName+"="+name)
	}
	if turnID != "" {
		pairs = append(pairs, envTurnID+"="+turnID)
	}
	return pairs
}

// shortName reads the short name of a session from the store, cached once found.
// A hook or a command of a cancelled turn still needs it, so the read ignores
// the cancellation of ctx.
func (p *sessionEnvProvider) shortName(ctx context.Context, sessionID string) string {
	if sessionID == "" || p == nil {
		return ""
	}
	p.mu.Lock()
	runner, cached := p.runner, p.names[sessionID]
	p.mu.Unlock()
	if cached != "" {
		return cached
	}
	if runner == nil {
		return ""
	}
	name, err := runner.SessionShortName(context.WithoutCancel(ctx), sessionID)
	if err != nil || name == "" {
		return ""
	}
	p.mu.Lock()
	p.names[sessionID] = name
	p.mu.Unlock()
	return name
}

// SessionEnv is the environment suga exports to a subprocess of ctx's session:
// SUGA_SESSION_ID, SUGA_SESSION_NAME (only when the session has a short name)
// and SUGA_TURN_ID (only inside a turn). A nil runtime exports nothing.
func (rt *Runtime) SessionEnv(ctx context.Context) []string {
	if rt == nil || rt.sessionEnv == nil {
		return nil
	}
	return rt.sessionEnv.Pairs(ctx)
}
