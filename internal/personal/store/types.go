package store

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	// ErrSessionNotFound is returned when a session id does not exist.
	ErrSessionNotFound = errors.New("session not found")
	// ErrTurnNotFound is returned when a turn id does not exist.
	ErrTurnNotFound = errors.New("turn not found")
)

// Session lifecycle states stored in sessions.status.
const (
	SessionOpen     = "open"
	SessionRunning  = "running"
	SessionArchived = "archived"
)

// Turn lifecycle states stored in turns.status.
const (
	TurnRunning   = "running"
	TurnCompleted = "completed"
	TurnFailed    = "failed"
	TurnCancelled = "cancelled"
)

// Entry types stored in entries.type. A context_update records what changed
// inside a segment (rules, append prompt, environment, a missing memory
// index); it belongs to a turn, is adopted with it and is replayed as a user
// message at its seq until the next segment record supersedes it.
const (
	EntryUser          = "user"
	EntryAssistant     = "assistant"
	EntryTool          = "tool"
	EntryCompaction    = "compaction"
	EntryPlan          = "plan"
	EntryHandoff       = "handoff"
	EntryContextUpdate = "context_update"
)

// Event kinds stored in events.kind.
const (
	EventModel      = "model"
	EventTool       = "tool"
	EventDenial     = "denial"
	EventTurn       = "turn"
	EventCompaction = "compaction"
	EventToolSearch = "tool_search"
	// EventTurnContext records what a turn starts with (model, mode, tool
	// set, rules/memory snapshot hashes, taint); see D9.
	EventTurnContext = "turn_context"
	// EventSchedule records a host scheduler event (chat idle consolidation:
	// name = task name, status and attrs.event = what happened).
	EventSchedule = "schedule"
	// EventFold records a history folding boundary move: attrs boundary,
	// steps, chars_before and chars_after of the request view.
	EventFold = "fold"
	// EventContext records what the first model request of a turn is made
	// of: chars per part (system prompt, tools, segment context parts,
	// history, hook context, the turn's input) and the window.
	EventContext = "context"
	// EventHook records a run of the user's hook command: name = hook event,
	// attrs decision, additional_chars, notice.
	EventHook = "hook"
	// EventTask records the end of a background task: name = task id, attrs
	// arg (the command), exit_code, bytes, lines, ref, truncated; started_at
	// and duration cover the task's whole run.
	EventTask = "task"
	// EventSession records a change of the session's own lifecycle, outside any
	// turn: name "recovered" is a session left running by a process that died
	// mid-turn taken back to open (attrs previous_status, stale_ms, turn_status),
	// name "session_cwd_changed" a session continued in another working directory
	// than the one it was created in (attrs from, to).
	EventSession = "session"
	// EventSteer records one message a person typed into a running turn
	// (steering): name "delivered" is a steer a model request carried (attrs
	// steer_id, source, chars, step), "returned" one the turn handed back
	// (attrs steer_id, source, chars, reason, disposition), "withdrawn" one the
	// person took back before a request carried it (attrs steer_id, source,
	// chars, count).
	EventSteer = "steer"
	// EventAddDir records a change of the session's added directories
	// (--add-dir) outside any turn: name "add" or "remove", attrs path, mode
	// (ro/rw) and from (keyboard: typed in chat; ask: authorized at an
	// outside-directory question). It is how a trace shows who widened the
	// session's reach and when; chat's :add-dir/:rm-dir and the authorization
	// write it.
	EventAddDir = "add_dir"
	// EventLocalCommand records one local command a person typed at the chat
	// prompt ("!cmd"/"!!cmd") outside any turn: name "run", attrs command (the
	// command, redacted), exit_code, bytes (the output the command wrote),
	// sent (true: the result goes to the model with the next message),
	// cancelled and timeout. It is how a trace shows what was run outside the
	// model's turn; chat writes it.
	EventLocalCommand = "local_command"
	// EventBudget records a budget threshold or accounting warning. Its attrs
	// carry scope, limits, usage, remaining amounts and the reason.
	EventBudget = "budget"
	// EventUndo records a user rollback; its attrs include the file outcome
	// counts and the seq of the deferred suga.undo entry when available.
	EventUndo = "undo"
)

// Session is one conversation. Meta holds free-form keys (rules hash, append
// prompt, effective model, loaded deferred tools, usage ...).
type Session struct {
	ID         string
	Title      string
	Cwd        string
	Status     string
	LastTurnID string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Meta       map[string]any
}

// Turn is one user request and its agentcore run (run_id == turn id).
type Turn struct {
	ID         string
	SessionID  string
	Status     string
	StopReason string
	StateJSON  []byte // agentcore RunState snapshot; empty until SaveRun
	StartedAt  time.Time
	EndedAt    time.Time
}

// Entry is one record of a session's linear log. Seq is assigned by the store.
type Entry struct {
	SessionID string
	Seq       int64
	TurnID    string
	Type      string
	JSON      json.RawMessage
	CreatedAt time.Time

	// bodyRef is the entries.body_ref column of a row whose body moved to the
	// session file; it is internal on purpose, JSON is always the restored body.
	bodyRef string
}

// NewEntry is the input form of Entry for AppendEntries.
type NewEntry struct {
	TurnID string
	Type   string
	JSON   json.RawMessage
}

// EntryRef is the minimal entry data a reader needs to link events to
// entries: the call id (tool call or result), the turn, the type and the time,
// without the entry body. The trace page joins the events of an incremental
// load against the whole session this way, without sending or parsing the
// bodies it already showed.
type EntryRef struct {
	Seq       int64
	TurnID    string
	Type      string
	CallID    string
	CreatedAt time.Time
}

// EntryFilter narrows ListEntries; zero values mean no restriction.
type EntryFilter struct {
	SinceSeq int64 // only entries with seq > SinceSeq
	TurnID   string
	Types    []string
	Limit    int
}

// Event is one observability record (model call, tool call, turn ...).
type Event struct {
	ID               int64
	SessionID        string
	TurnID           string
	Kind             string
	Name             string
	Status           string
	StartedAt        time.Time
	DurationMs       int64
	TTFTMs           int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	Error            string
	Attrs            map[string]any
}

// EventFilter narrows ListEvents; zero values mean no restriction.
type EventFilter struct {
	SessionID string
	// WithAgents widens SessionID to its sub-agent sessions too.
	WithAgents bool
	TurnID     string
	Kinds      []string
	Since      time.Time
	Limit      int
}

// Usage is the token total returned by SumUsage.
type Usage struct {
	Calls            int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
}
