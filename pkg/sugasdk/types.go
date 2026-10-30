package sugasdk

// The public value types of the SDK: every one of them is defined here and
// filled from the internal types by the convert* functions at the bottom of
// this file. No internal type appears in the exported API.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// EngineOptions configures one engine of this process.
type EngineOptions struct {
	// SugaHome is the data root (config.yaml, .env, db, sessions). Empty uses
	// $SUGA_HOME, or ~/.suga when that is unset too. suga's configuration is
	// process global: Open sets $SUGA_HOME to this root and loads it when the
	// process has not loaded one yet, reuses a configuration loaded from the
	// same root, and refuses a different one.
	SugaHome string
	// Model is the default model of the sessions this engine opens:
	// "<provider>/<model>" or a bare model id. Empty uses the configured model.
	Model string
	// Logger receives the engine's own diagnostics (nil = slog.Default()).
	Logger *slog.Logger
	// Origin is the session origin recorded by default on sessions this engine
	// creates; empty uses OriginSDK. Hosts that need longer retention should pin
	// sessions with Engine.Pin.
	Origin string
}

// SessionOptions configures one conversation.
type SessionOptions struct {
	// Workspace is the tool workspace root (absolute, or relative to the
	// process directory); empty uses the process directory. The session's
	// project rules, memory scope and file tools work in it.
	Workspace string
	// ProjectMemoryKey pins this session's project memory to a host-owned key.
	// A non-empty key ignores repository discovery and memory.project.backend.
	ProjectMemoryKey string
	// Model overrides EngineOptions.Model for this session.
	Model string
	// AllowWrite exposes the workspace write and shell tools.
	AllowWrite bool
	// ExtraTools are the host's own tools: they sit next to the built-in ones
	// (tool table, manifest, tool_search) and tools.disabled can remove them.
	// A name a built-in or MCP tool already has is an error. Execute reads
	// tools.InvocationID(ctx) - an id unique to the call, stable for the whole
	// call - so a host may use it as the idempotency key of its own side-effect
	// ledger (see doc.go, "Tool invocations").
	ExtraTools []api.Tool
	// Hooks observe the turn (agentcore.Hook).
	Hooks []agentcore.Hook
	// HostContext returns the host's context text (persona, topic index,
	// verbatim constraints) and a hash of it. The text is frozen into the
	// segment context; a hash change inside a segment appends an update.
	HostContext func() (content, hash string)
	// Approver decides every tool call. nil denies every call (fail closed).
	Approver tools.Approver
	// MemoryConfirm approves a memory write of a session that has read external
	// content. nil refuses such writes.
	MemoryConfirm func(ctx context.Context, req MemoryConfirmRequest) bool
	// Origin is the session origin recorded on the sessions this engine
	// creates; empty uses the engine origin, then OriginSDK. Hosts that need
	// longer retention should pin sessions with Engine.Pin.
	Origin string
}

// Prompt is one turn's input: text plus pictures.
type Prompt struct {
	// Text is the message a person or the host wants answered.
	Text string
	// Images are picture files attached to the message (same checks, scaling
	// and storage as chat's :image).
	Images []string
	// RequestID makes the turn an idempotent request of this session: the caller
	// generates it (a UUID is recommended) and it is scoped to one session.
	// Empty keeps the plain behaviour of Send. With an id, a Send for an id this
	// session already finished returns the stored first result with Result.Replayed
	// set instead of running the turn again; a Send for an id whose turn currently
	// runs errors with ErrRequestRunning; a record left running by a process that
	// died mid-turn reads as delivery uncertain (ErrDeliveryUncertain, and
	// "unknown" from RequestStatus): resend under a NEW id, nothing is replayed
	// on its own.
	RequestID string
	// OutputSchema is the JSON Schema this turn's final answer must satisfy (a
	// caller parameter of this send, not a session setting: empty means no
	// contract, and the next Send starts without one). A failing answer gets one
	// correction round, then Send returns ErrOutputContract with
	// Result.OutputContract.Passed=false (design 2026-10-22 2.2).
	OutputSchema json.RawMessage
}

// Event is one update of a running turn. Only stable fields are carried: the
// answer text as it streams, the tool name with a summary of its arguments and
// its result, usage and errors.
type Event struct {
	// Kind is the event kind: "text" (answer text), "tool_call" (before a tool
	// runs; Tool and Text name the call, ToolArgs its arguments), "tool_result"
	// (after it; Text is the result summary, IsError a failed or refused call),
	// "usage" (a counted model call), "run_finished" (the turn's total),
	// "retry" (a transient provider failure being retried, Text the reason),
	// "plan", "steer_delivered", "context_update", "session_recovered", ...
	Kind string
	// Text is the event's text: the streamed answer, the argument summary of a
	// tool call, the result summary of a tool result, the reason of a retry.
	Text string
	// Tool is the tool name of a tool_call / tool_result event.
	Tool string
	// ToolArgs are the whole arguments of a tool_call event (nil otherwise).
	ToolArgs map[string]any
	// IsError marks a tool result whose call failed or was refused.
	IsError bool
	// Usage is set on "usage" and "run_finished" events.
	Usage *Usage
	// Error is set on an "error" event.
	Error string
}

// Usage is model token usage.
type Usage struct {
	InputTokens      int     `json:"input_tokens,omitempty"`
	OutputTokens     int     `json:"output_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	// ModelCalls counts the model calls this invocation made (a result's
	// Usage only).
	ModelCalls int `json:"model_calls,omitempty"`
}

// Result is the outcome of one turn.
type Result struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	// State is COMPLETED, INPUT_REQUIRED, FAILED or CANCELED.
	State string `json:"state"`
	// Output is the turn's final answer.
	Output string `json:"output"`
	// Usage is this turn's usage, SessionUsage the session's cumulative one.
	Usage        Usage `json:"usage"`
	SessionUsage Usage `json:"session_usage"`
	// DeniedTools lists the tools the approver refused, first refusal first.
	DeniedTools []string `json:"denied_tools,omitempty"`
	// WrapUp names why the answer stopped early ("max_steps", a hook's reason);
	// empty for a normal answer.
	WrapUp string `json:"wrap_up,omitempty"`
	// Truncated marks an answer cut off at the output token limit.
	Truncated bool `json:"truncated,omitempty"`
	// ReturnedSteers are the steering messages the turn did not deliver, as
	// text: the caller runs them as the next turn.
	ReturnedSteers []string `json:"returned_steers,omitempty"`
	// Replayed marks a result returned from the stored record of an earlier
	// Send with the same RequestID (the model was not called again).
	Replayed bool `json:"replayed,omitempty"`
	// OutputValue is the value that satisfied Prompt.OutputSchema: the extracted
	// JSON text, compacted (numbers keep their literal text). Empty without a
	// contract or when the contract failed.
	OutputValue json.RawMessage `json:"output_value,omitempty"`
	// OutputContract reports the output contract check; nil when the send had no
	// Prompt.OutputSchema.
	OutputContract *OutputContract `json:"output_contract,omitempty"`
}

// OutputContract is the outcome of judging one send's answer against
// Prompt.OutputSchema. It is defined here (not taken from an internal package)
// so a host compiles against public types only.
type OutputContract struct {
	// Checked is false when the turn's end left nothing to judge (a failed,
	// yielded, wrapped-up or truncated turn, or an empty answer).
	Checked bool `json:"checked"`
	// Passed is the final check's outcome (meaningful only when Checked).
	Passed bool `json:"passed"`
	// Rounds counts the checks: 1 when the first answer was fine, 2 when the one
	// correction round was needed.
	Rounds int `json:"rounds"`
	// Source says where the checked JSON came from: whole, fenced, substring or
	// no_json.
	Source string `json:"source,omitempty"`
	// Reason says why the answer was not checked (Checked=false only).
	Reason string `json:"reason,omitempty"`
	// Errors are the last check's validation problems.
	Errors []ContractError `json:"errors,omitempty"`
}

// ContractError is one validation problem of a checked answer.
type ContractError struct {
	// Path is the JSON path of the offending value, e.g. $ or $.items[0].id.
	Path string `json:"path"`
	// Message is the one-line reason.
	Message string `json:"message"`
}

// Card is a read-only snapshot of where a session stands.
type Card struct {
	SessionID string    `json:"session_id"`
	Title     string    `json:"title,omitempty"`
	State     string    `json:"state"`
	Origin    string    `json:"origin,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// LastActiveAt is the newest entry of the session (its update time for an
	// empty session).
	LastActiveAt time.Time `json:"last_active_at"`
	// LatestSummary is the newest handoff or compaction summary; SummaryKind
	// says which ("handoff" or "compaction").
	LatestSummary string    `json:"latest_summary,omitempty"`
	SummaryKind   string    `json:"summary_kind,omitempty"`
	SummaryAt     time.Time `json:"summary_at"`
	// OpenSteps are the pending and in_progress steps of the latest plan.
	OpenSteps     []string  `json:"open_steps,omitempty"`
	PlanUpdatedAt time.Time `json:"plan_updated_at"`
	Usage         Usage     `json:"usage"`
	// SegmentCount counts the segment records (1 = never compacted), Turns the
	// turns started in the session.
	SegmentCount int `json:"segment_count"`
	Turns        int `json:"turns"`
}

// Hit is one match of Engine.Search.
type Hit struct {
	// Ref locates the entry ("<session>/<seq>", readable with recall).
	Ref       string    `json:"ref"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Snippet   string    `json:"snippet"`
	CreatedAt time.Time `json:"created_at"`
}

// Entry is one immutable session record returned by Engine.Entry. JSON is the
// stored record body; Seq remains stable for the lifetime of the session.
type Entry struct {
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	TurnID    string          `json:"turn_id,omitempty"`
	Type      string          `json:"type"`
	JSON      json.RawMessage `json:"json"`
	CreatedAt time.Time       `json:"created_at"`
}

// SessionInfo is one row of Engine.Sessions.
type SessionInfo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	Status    string    `json:"status"`
	Origin    string    `json:"origin,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionFilter narrows Engine.Sessions.
type SessionFilter struct {
	// Origin keeps one origin ("sdk", "chat", ...); "all" or empty keeps
	// every one.
	Origin string
	// Cwd keeps sessions of this directory and its descendants; empty keeps
	// every directory.
	Cwd string
	// IncludeAgents includes sub-agent sessions (hidden by default).
	IncludeAgents bool
	// IncludeArchived includes archived sessions (hidden by default).
	IncludeArchived bool
}

// MemoryEntry is one memory item.
type MemoryEntry struct {
	UUID    string `json:"uuid,omitempty"`
	ID      string `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	Body    string `json:"body,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Scope is "user" or "project:<key>".
	Scope   string    `json:"scope,omitempty"`
	Updated time.Time `json:"updated"`
}

// MemoryConfirmRequest is what a memory write would do; MemoryConfirm approves
// or refuses it.
type MemoryConfirmRequest struct {
	// Scope is "user" or "project:<key>".
	Scope string
	// Action is "add", "replace" or "remove".
	Action  string
	Kind    string
	ID      string
	Title   string
	Body    string
	Summary string
}

// usageOf converts internal usage.
func usageOf(u agentcore.Usage) Usage {
	return Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, TotalTokens: u.TotalTokens,
		CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostUSD: u.CostUSD}
}

// convertEvent maps one internal event; only the stable fields are copied.
func convertEvent(ev personal.Event) Event {
	out := Event{Kind: ev.Kind, Text: ev.Text, Tool: ev.ToolName, ToolArgs: ev.ToolArgs, IsError: ev.IsError}
	if ev.Usage != nil {
		usage := usageOf(ev.Usage.Session)
		out.Usage = &usage
	}
	if ev.Kind == "error" {
		out.Error = ev.Text
	}
	return out
}

// convertResult maps a turn result.
func convertResult(res personal.RunResult) Result {
	out := Result{
		SessionID: res.SessionID, TurnID: res.TurnID, State: string(res.State), Output: res.Output,
		Usage: usageOf(res.Usage), SessionUsage: usageOf(res.SessionUsage),
		DeniedTools: res.DeniedTools, WrapUp: res.WrapUp, Truncated: res.Truncated,
	}
	out.Usage.ModelCalls = res.ModelCalls
	for _, s := range res.ReturnedSteers {
		out.ReturnedSteers = append(out.ReturnedSteers, s.Text)
	}
	return out
}

// convertContractReport maps the internal output contract report to the public
// one: no internal type crosses the SDK's surface.
func convertContractReport(rep personal.ContractReport) *OutputContract {
	out := &OutputContract{Checked: rep.Checked, Passed: rep.Passed, Rounds: rep.Rounds,
		Source: rep.Source, Reason: rep.Reason}
	for _, err := range rep.Errors {
		out.Errors = append(out.Errors, ContractError{Path: err.Path, Message: err.Message})
	}
	return out
}

// convertCard maps a session card.
func convertCard(c personal.SessionCard) Card {
	return Card{
		SessionID: c.SessionID, Title: c.Title, State: c.State, Origin: c.Origin,
		CreatedAt: c.CreatedAt, LastActiveAt: c.LastActiveAt,
		LatestSummary: c.LatestSummary, SummaryKind: c.SummaryKind, SummaryAt: c.SummaryAt,
		OpenSteps: c.OpenSteps, PlanUpdatedAt: c.PlanUpdatedAt,
		Usage: Usage{ModelCalls: int(c.Usage.Calls), InputTokens: int(c.Usage.InputTokens), OutputTokens: int(c.Usage.OutputTokens),
			CacheReadTokens: int(c.Usage.CacheReadTokens), CacheWriteTokens: int(c.Usage.CacheWriteTokens),
			CostUSD: c.Usage.CostUSD},
		SegmentCount: c.SegmentCount, Turns: c.Turns,
	}
}

// convertEntry maps an internal store entry without exposing internal types.
func convertEntry(e store.Entry) Entry {
	return Entry{SessionID: e.SessionID, Seq: e.Seq, TurnID: e.TurnID, Type: e.Type, JSON: e.JSON, CreatedAt: e.CreatedAt}
}

// convertMemoryEntry maps one memory entry of a scope.
func convertMemoryEntry(scope memory.Scope, e memory.Entry) MemoryEntry {
	return MemoryEntry{ID: e.ID, Title: e.Title, Body: e.Body, Kind: string(e.Kind), Summary: e.Summary,
		Scope: scope.String(), Updated: e.Updated}
}
