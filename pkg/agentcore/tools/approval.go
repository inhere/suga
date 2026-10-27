package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// ErrAborted marks an approval that could not be completed (the approver was
// cancelled or the turn context ended). Unlike ErrDenied it must end the turn:
// the runner's tool policy fails the turn instead of turning it into a tool result.
var ErrAborted = errors.New("tool approval aborted")

// abortedError keeps the original cause (context.Canceled, ...) reachable with
// errors.Is while also matching ErrAborted.
type abortedError struct{ err error }

func (e *abortedError) Error() string   { return e.err.Error() }
func (e *abortedError) Unwrap() []error { return []error{ErrAborted, e.err} }

// Approver decides whether one tool call may run.
//
// Decide returns nil to allow the call. An error matching ErrDenied (see
// Denied) refuses it: the model gets the message as a tool error and the turn
// continues. Any other error (typically context.Canceled from a cancelled
// approval prompt) aborts the turn; it is returned wrapped so it matches both
// ErrAborted and the original cause.
type Approver interface {
	Decide(ctx context.Context, call api.ToolCall, tool api.Tool) error
}

// ApproverFunc adapts a function to Approver.
type ApproverFunc func(ctx context.Context, call api.ToolCall, tool api.Tool) error

func (f ApproverFunc) Decide(ctx context.Context, call api.ToolCall, tool api.Tool) error {
	return f(ctx, call, tool)
}

// ApprovedCallFunc is the callback of the approval layer that runs once a call
// passed the decision and right before the tool executes (SetOnApproved). It is
// the seam for a host's record of what is about to have effects (the admit
// ledger of internal/personal), so that record is never written for a call the
// person refused or a prompt that was cancelled. It cannot refuse the call: a
// failure to record must not stop the tool.
type ApprovedCallFunc func(ctx context.Context, call api.ToolCall, tool api.Tool)

// CallRewriter is an optional extension of an Approver: it reprocesses a call
// before the permission decision (a hook rewrites the arguments). The approval
// layer runs it first and then decides on the rewritten call, so a change can
// never be approved on the arguments it replaced.
type CallRewriter interface {
	Rewrite(ctx context.Context, call api.ToolCall, tool api.Tool) (api.ToolCall, error)
}

// Approval is the permission layer of the tool registry stack (order:
// observe -> visibility -> approval -> base). Every call that reaches it is
// resolved against the base registry and handed to the Approver before it runs.
// Unloaded deferred tools never get here (visibility answers them first), so
// they never trigger an approval prompt.
type Approval struct {
	inner    lookupRegistry
	approver Approver
	// rewriter runs before the decision (SetRewriter); nil leaves calls alone.
	rewriter CallRewriter
	// onApproved runs after the decision let the call through and before it
	// executes (SetOnApproved); nil records nothing.
	onApproved ApprovedCallFunc
	// disabled maps known tools this runtime did not register to why and how
	// to enable them; see SetDisabled.
	disabled map[string]string
}

// SetRewriter installs the pre-decision rewriter (chat's PreToolUse hook). It
// narrows a call before the policy sees it; it never replaces the decision, so
// a rewriter cannot let a call through that the approver refuses.
func (a *Approval) SetRewriter(rewriter CallRewriter) { a.rewriter = rewriter }

// SetOnApproved installs the callback that runs after the approval decision
// allowed a call and before the tool executes. A refused call never reaches it,
// and neither does a decision cancelled or timed out at the prompt (those abort
// the turn instead of producing a result). It has to be set before the first
// turn runs.
func (a *Approval) SetOnApproved(fn ApprovedCallFunc) { a.onApproved = fn }

// rewrite runs the rewriter with the fail-closed rules of a hook: a failure or
// a panic of the rewriter refuses the call instead of running the original.
func (a *Approval) rewrite(ctx context.Context, call api.ToolCall, tool api.Tool) (rewritten api.ToolCall, err error) {
	if a.rewriter == nil {
		return call, nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			rewritten, err = call, Denied(fmt.Sprintf("工具 %s 的调用前 hook 崩溃，已拒绝执行：%v", call.Name, recovered))
		}
	}()
	rewritten, err = a.rewriter.Rewrite(ctx, call, tool)
	if err != nil {
		return call, err
	}
	if rewritten.Name == "" {
		// A rewriter may only replace the arguments of the call it was given.
		rewritten.Name = call.Name
	}
	return rewritten, nil
}

// SetDisabled names the known tools that are off in this runtime (write tools
// in read-only mode, web_search without a key) with an enable hint. A call to
// one of them is refused with ErrDenied, so repeated attempts count toward the
// denial limit; any other unknown name is a plain error that does not count.
func (a *Approval) SetDisabled(hints map[string]string) { a.disabled = hints }

// maxListedTools bounds the available-tools list in a not-found error.
const maxListedTools = 40

// NewApproval wraps inner with approver. A nil approver denies every call
// (fail closed), so a runtime opened without a policy cannot run tools.
func NewApproval(inner interface {
	api.ToolRegistry
	Lookup
}, approver Approver) *Approval {
	return &Approval{inner: inner, approver: approver}
}

func (a *Approval) ListTools(ctx context.Context) ([]api.ToolSchema, error) {
	return a.inner.ListTools(ctx)
}

// Get passes tool lookup through so outer layers can still classify tools.
func (a *Approval) Get(name string) (api.Tool, error) { return a.inner.Get(name) }

func (a *Approval) ExecuteTool(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	tool, err := a.inner.Get(call.Name)
	if err != nil {
		if hint, ok := a.disabled[call.Name]; ok {
			return nil, Denied(fmt.Sprintf("工具 %s 在本会话未启用：%s。不要重试，请告诉用户开启方法。", call.Name, hint))
		}
		return nil, fmt.Errorf("%w; available tools: %s", err, a.availableNames(ctx))
	}
	if a.approver == nil {
		return nil, Denied(fmt.Sprintf("tool %q is not permitted: no approval policy", call.Name))
	}
	// A rewrite (hook) comes first: Decide below, and the tool itself, only see
	// the rewritten call, so rewritten arguments are never approved unchecked.
	call, err = a.rewrite(ctx, call, tool)
	if err != nil {
		if errors.Is(err, ErrDenied) {
			return nil, err
		}
		return nil, &abortedError{err: err}
	}
	if err := a.approver.Decide(ctx, call, tool); err != nil {
		if errors.Is(err, ErrDenied) {
			return nil, err
		}
		return nil, &abortedError{err: err}
	}
	if r := approvalReportOf(ctx); r != nil && r.Source == "" {
		r.Source = ApprovedByRule
	}
	// The call is allowed and runs from here: the last point before its effects
	// to tell a host what it is about to do (the admit ledger).
	if a.onApproved != nil {
		a.onApproved(ctx, call, tool)
	}
	return a.inner.ExecuteTool(ctx, call)
}

// availableNames lists the registered tool names for a not-found error.
func (a *Approval) availableNames(ctx context.Context) string {
	schemas, err := a.inner.ListTools(ctx)
	if err != nil || len(schemas) == 0 {
		return "(none)"
	}
	names := make([]string, 0, min(len(schemas), maxListedTools))
	for i, s := range schemas {
		if i == maxListedTools {
			names = append(names, fmt.Sprintf("… %d more", len(schemas)-i))
			break
		}
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

// Approval sources (ApprovalReport.Source).
const (
	// ApprovedByRule: the policy let the call run without asking (read-only
	// or agent-state tool, approval auto, an MCP rule).
	ApprovedByRule = "rule"
	// ApprovedByUser: the person was asked and allowed it.
	ApprovedByUser = "user"
	// ApprovedAlways: the person had answered "always" for this tool.
	ApprovedAlways = "always"
	// AskedAndDenied: the person was asked and refused.
	AskedAndDenied = "user_denied"
)

// ApprovalReport says how a call was approved: who decided and how long the
// person took to answer. The observing layer puts one in the context
// (WithApprovalReport) and reads it after the call; approvers that ask a
// person fill it in (ReportApproval); a call the Approver let through
// without reporting is ApprovedByRule.
type ApprovalReport struct {
	Source string
	Wait   time.Duration
	// Danger is why the call needed a stricter decision (a dangerous delete
	// command), "" for an ordinary call.
	Danger string
	// Rule is the stable summary of the representative approval rule. An empty
	// value means no declarative rule matched the call (the policy default ran).
	Rule string
	// Scope is the session rule's scope key; it is empty for user, project and
	// legacy rules, and for calls without a representative rule.
	Scope string
	// Answered marks a call the person was asked about in this very call and
	// allowed (allow_once, and the allow_always of the same prompt: the person
	// did approve this call). Later calls a remembered "always" rule lets
	// through are not Answered, so the model-visible note is not added there.
	Answered bool
}

type approvalReportKey struct{}

// WithApprovalReport returns ctx carrying a fresh report.
func WithApprovalReport(ctx context.Context) (context.Context, *ApprovalReport) {
	r := &ApprovalReport{}
	return context.WithValue(ctx, approvalReportKey{}, r), r
}

// ReportApproval records the source and the person's answer time in ctx's
// report, if any.
func ReportApproval(ctx context.Context, source string, wait time.Duration) {
	if r, _ := ctx.Value(approvalReportKey{}).(*ApprovalReport); r != nil {
		r.Source, r.Wait = source, wait
	}
}

// ReportRule records the stable rule summary and optional session scope in the
// current approval report. Empty values deliberately mean that no matching
// rule or session scope was available.
func ReportRule(ctx context.Context, summary, scope string) {
	if r, _ := ctx.Value(approvalReportKey{}).(*ApprovalReport); r != nil {
		r.Rule, r.Scope = summary, scope
	}
}

// ReportApprovalAnswer is ReportApproval plus Answered: the person answered a
// prompt in this very call and let it run (allow once or allow always; either
// way the model-visible result gets the human-approved note, see
// annotateApproved). Later calls with ReportApproval(ApprovedAlways, 0) come
// from a remembered session rule, not from a fresh answer, and stay unmarked.
func ReportApprovalAnswer(ctx context.Context, source string, wait time.Duration) {
	if r, _ := ctx.Value(approvalReportKey{}).(*ApprovalReport); r != nil {
		r.Source, r.Wait, r.Answered = source, wait, true
	}
}

// ReportDanger records in ctx's report why the call is dangerous.
func ReportDanger(ctx context.Context, reason string) {
	if r, _ := ctx.Value(approvalReportKey{}).(*ApprovalReport); r != nil {
		r.Danger = reason
	}
}

// ApprovedResultNote goes after (annotateApproved) the model-visible tool
// result of a call a person approved this time (ApprovalReport.Answered, the
// ACP allow/allow_always answer and the chat y/a answer). It tells the model
// truth about who let the call run; it is never added for a rule- or
// remembered-always allowed call.
const ApprovedResultNote = "[已经人工批准执行]"

// annotateApproved appends note to the result a person approved this very
// call; note is ApprovedResultNote, or the translation the host injected
// (OutputConfig.Texts, see ModelTexts). The note goes after the content, not
// before it: the first
// line of a tool result is what the chat `⎿ ok:` summary (and any head-kept
// truncation) shows, and that should stay the tool's own output. It only
// shapes the text the model reads: the tool's exit codes and error semantics,
// and the result the trace stores, stay untouched.
func annotateApproved(ctx context.Context, res *api.ToolResult, note string) {
	if res == nil {
		return
	}
	r := approvalReportOf(ctx)
	if r == nil || !r.Answered {
		return
	}
	res.Content = res.Content + "\n" + note
}

func approvalReportOf(ctx context.Context) *ApprovalReport {
	r, _ := ctx.Value(approvalReportKey{}).(*ApprovalReport)
	return r
}
