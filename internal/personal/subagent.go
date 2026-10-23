package personal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// Sub-agents (docs/design/2026-10-13-subagents-design.md): a turn's agent tool
// runs a task in a session of its own, on a Runner of its own (read-only
// tools, a sub-agent prompt, its own limits), and hands back the final
// answer as a report. The sub-agent session names its parent (store.MetaKind,
// store.MetaParentSession), its usage counts in the parent's run
// (withChildOf) and session total (Store.SumUsageTree).

// Stop reasons of a sub-agent run (ChildResult.Stop).
const (
	StopFinal     = "final"
	StopMaxSteps  = "max_steps"
	StopMaxTokens = "max_tokens"
	StopTimeout   = "timeout"
)

// Meta keys of a sub-agent session besides store.MetaKind and
// store.MetaParentSession.
const (
	metaAgentType   = "agent_type"
	metaParentTurn  = "parent_turn_id"
	metaParentCall  = "parent_call_id"
	childHardGrace  = 60 * time.Second
	childNoAnswer   = "(the sub-agent stopped before it gave an answer)"
	childCancelled  = "cancelled"
	childFailedStop = "failed"
)

// ChildRequest is one sub-agent run started by a turn's tool call.
type ChildRequest struct {
	ParentSession, ParentTurn, ParentCall string
	Type, Description, Prompt             string
	// Timeout is the soft limit: when it passes the sub-agent is asked to wrap
	// up (Config.RunTimeout of the child runner does that); a minute later the
	// run is cancelled and the last answer text is the report.
	Timeout time.Duration
	// Background children keep running under their own limits and do not use the
	// parent's budget to request a wrap-up.
	Background bool
	// Emit receives the sub-agent's progress ("agent_progress" events: its
	// tool calls); nil drops them.
	Emit EmitFunc
}

// ChildResult is the outcome of a sub-agent run.
type ChildResult struct {
	SessionID string
	Report    string
	// Stop is why it ended: StopFinal, StopMaxSteps, StopMaxTokens,
	// StopTimeout, "cancelled" or "failed".
	Stop      string
	Steps     int // model calls
	ToolCalls int
	Usage     agentcore.Usage // priced, every model call of the run
	Duration  time.Duration
	// Tainted: the sub-agent read external content (its session is marked).
	Tainted bool
}

// RunChild runs req on child, a Runner built for sub-agents, in a new session
// of its own. A model failure is returned as an error with the result so
// far (its SessionID set); a sub-agent that ran past its limits without
// answering gives its last answer text as the report.
func RunChild(ctx context.Context, child *Runner, req ChildRequest) (ChildResult, error) {
	start := time.Now()
	sess, err := child.NewSessionWithMeta(ctx, "", req.Description, map[string]any{
		store.MetaKind: store.KindAgent, store.MetaParentSession: req.ParentSession,
		store.MetaOrigin: store.OriginAgent,
		metaAgentType:    req.Type, metaParentTurn: req.ParentTurn, metaParentCall: req.ParentCall,
	})
	if err != nil {
		return ChildResult{}, fmt.Errorf("sub-agent session: %w", err)
	}
	out := ChildResult{SessionID: sess.ID}
	runCtx := withChildOf(ctx, req.ParentSession)
	runCtx = withChildBackground(runCtx, req.Background)
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, req.Timeout+childHardGrace)
		defer cancel()
	}
	res, runErr := child.Run(runCtx, RunRequest{SessionID: sess.ID, Prompt: req.Prompt, Emit: childProgress(req)})
	out.Duration = time.Since(start)
	out.Steps, out.ToolCalls, out.Usage = res.ModelCalls, res.RunUsage.ToolCalls, res.RunUsage.Usage
	out.Tainted = child.sessionTainted(context.WithoutCancel(ctx), sess.ID)
	switch {
	case runErr == nil:
		out.Report, out.Stop = res.Output, StopFinal
		if res.WrapUp != "" {
			out.Stop = res.WrapUp
		}
		return out, nil
	case ctx.Err() != nil: // the parent turn was cancelled
		out.Stop = childCancelled
		return out, ctx.Err()
	case errors.Is(runErr, agentcore.ErrWrapUpIgnored), errors.Is(runErr, context.DeadlineExceeded),
		strings.Contains(runErr.Error(), "max steps exceeded"):
		// Past its limits without an answer: what it last said is the report.
		switch msg := runErr.Error(); {
		case strings.Contains(msg, "max steps exceeded"):
			out.Stop = StopMaxSteps
		case errors.Is(runErr, agentcore.ErrWrapUpIgnored) && strings.Contains(msg, StopMaxTokens):
			out.Stop = StopMaxTokens
		default: // the wrap-up for time ignored, or the hard deadline
			out.Stop = StopTimeout
		}
		out.Report = child.lastAnswer(context.WithoutCancel(ctx), sess.ID)
		return out, nil
	default:
		out.Stop = childFailedStop
		return out, runErr
	}
}

// childProgress forwards the sub-agent's tool calls as "agent_progress" events
// (Description in Agent for display, the agent tool call's id in CallID so chat
// counts them on the right entry; the turn's other events are dropped).
func childProgress(req ChildRequest) EmitFunc {
	if req.Emit == nil {
		return nil
	}
	return func(ctx context.Context, e Event) error {
		if e.Kind != "tool_call" {
			return nil
		}
		return req.Emit(ctx, Event{Kind: "agent_progress", ToolName: e.ToolName, CallID: req.ParentCall,
			Text: e.Text, ToolArgs: e.ToolArgs, Agent: req.Description})
	}
}

// sessionTainted reports whether the session read external content.
func (r *Runner) sessionTainted(ctx context.Context, sessionID string) bool {
	sess, err := r.cfg.Repository.GetSession(ctx, sessionID)
	if err != nil {
		return false
	}
	at, _ := sess.Meta[metaTaintedAt].(string)
	return at != ""
}

// lastAnswer is the text of the last assistant message of the session that
// has any (a sub-agent stopped mid-way: what it found so far), or a note.
func (r *Runner) lastAnswer(ctx context.Context, sessionID string) string {
	text, _, err := r.LastReply(ctx, sessionID)
	if err != nil {
		slog.Warn("read sub-agent answers", "session_id", sessionID, "err", err)
		return childNoAnswer
	}
	if text == "" {
		return childNoAnswer
	}
	return text
}

type agentEmitKey struct{}

// AgentProgress is where a sub-agent started by the turn of ctx sends its
// progress ("agent_progress" events reach the turn's Emit); nil outside a
// turn or for a turn without Emit.
func AgentProgress(ctx context.Context) EmitFunc {
	fn, _ := ctx.Value(agentEmitKey{}).(EmitFunc)
	return fn
}

// CurrentTurn returns the session and turn of the tool call ctx belongs to
// ("" outside a turn).
func CurrentTurn(ctx context.Context) (sessionID, turnID string) {
	scope := observeScopeFrom(ctx)
	return scope.sessionID, scope.turnID
}

// limitHook asks a run to wrap up past its token budget (uncached input plus
// output of its model calls, so a long cached context does not eat it) or its
// time (Config.MaxRunTokens, Config.RunTimeout). It checks after each model
// call; the next request is the wrap-up. Each instruction names its own reason
// and makes the model state it in the report, so a short report is not read as
// a timeout (or the other way round).
type limitHook struct {
	agentcore.NopHook
	maxTokens int
	// now reads the clock the deadline was computed with (Runner.clock). It is
	// injected so a test reaches the limit deterministically instead of relying
	// on the real clock advancing between the two reads (see past).
	now      func() time.Time
	deadline time.Time
}

// past reports whether the run is past its time limit. It compares the
// injected clock (the real one when the hook was built without one) rather
// than calling time.Now directly, so the comparison is testable.
func (h limitHook) past() bool {
	if h.deadline.IsZero() {
		return false
	}
	now := h.now
	if now == nil {
		now = time.Now
	}
	return now().After(h.deadline)
}

func (h limitHook) AfterModel(ctx context.Context, _ *agentcore.ModelEvent, ctrl *agentcore.Control) error {
	if h.maxTokens > 0 {
		u := runUsageFrom(ctx).snapshot().Usage
		if spent := max(u.InputTokens-u.CacheReadTokens, 0) + u.OutputTokens; spent >= h.maxTokens {
			ctrl.RequestWrapUp(StopMaxTokens, fmt.Sprintf("已用掉本次任务的 token 预算（%d）。不要再调用任何工具，直接根据目前已获得的信息给出报告。报告开头必须如实写明：本次是因 token 预算用尽（%d）而收尾，不得写成超时或其它原因；然后给出结论、证据（文件:行号）、尚未查清的部分。", h.maxTokens, h.maxTokens))
			return nil
		}
	}
	if h.past() {
		ctrl.RequestWrapUp(StopTimeout, "已到本次任务的时间上限。不要再调用任何工具，直接根据目前已获得的信息给出报告。报告开头必须如实写明：本次是因时间上限而收尾，不得写成步数上限或 token 预算用尽；然后给出结论、证据（文件:行号）、尚未查清的部分。")
	}
	return nil
}
