package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/inhere/suga/internal/diffview"
	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

var safeSessionID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Session is a persisted conversation (alias of the core store model).
type Session = store.Session

// ErrSessionNotFound is returned when a session id does not exist.
var ErrSessionNotFound = store.ErrSessionNotFound

// RunState is the caller-facing run status reported in events and results
// (stream-json "status"). The values are a wire contract; do not change them.
type RunState string

const (
	RunCompleted     RunState = "COMPLETED"
	RunInputRequired RunState = "INPUT_REQUIRED"
	RunFailed        RunState = "FAILED"
	RunCanceled      RunState = "CANCELED"
)

// Config holds the core store and the model/tool wiring used by a personal agent.
type Config struct {
	// Repository is the single core store: sessions, turns, entries and
	// agentcore run state (run_id == turn_id) all live in it.
	Repository   *store.Store
	Provider     agentcore.Provider
	Model        string
	ProviderName string
	Tools        agentcore.ToolRegistry
	// Vision is the live capability of the current model: view_image leaves the
	// tool table of a model that cannot see images, and a :model switch updates
	// this gate instead of rebuilding the tools. nil = the model can see.
	Vision *tools.VisionGate
	// PromptBuilder is the stable system prompt (identity and working
	// conventions only, design D1); the runner sends it unchanged.
	PromptBuilder agentcore.PromptBuilder
	// RuleSource reads the user/project rules (content, hash). Rules go into
	// the segment context; a change inside a segment appends a context update.
	RuleSource func() (string, string, error)
	// EnvironmentSource and SkillsSource render the environment section (OS,
	// workspace, shell, read/write mode and how to enable writing) and the
	// skills listing. Both go into the segment context: read for every new
	// segment, and the environment is also compared at each turn start (a
	// resumed session in another mode or workspace gets a context update).
	// SegmentEnvironmentSource is read only when a new segment snapshot is built.
	// It may refresh cheap external facts without changing the current segment.
	EnvironmentSource        func() string
	SegmentEnvironmentSource func() string
	// PreferredToolsSource returns the names of the preferred tools the
	// environment section lists (the available declarations, in configuration
	// order); the turn_context event records them, so a trace shows which
	// preferred tools the turn saw. nil = this session lists none.
	PreferredToolsSource func() []string
	SkillsSource         func() string
	// AddDirs returns the session's additional directories (--add-dir) as
	// "path:ro"/"path:rw" entries; the turn_context event records them so a
	// trace shows what the session could reach. nil = none.
	AddDirs func() []string
	// SkillsStats counts the skills by how the listing presents them; the
	// context event records it.
	SkillsStats func() SkillsStats
	// Tasks hands over the notices of background tasks that ended unseen;
	// they are added before the next model request (nil: no tasks).
	Tasks              TaskNotices
	AppendSystemPrompt string
	// AppendSystemPromptSet marks AppendSystemPrompt as explicitly supplied by
	// the caller, so an empty value clears the saved prompt instead of keeping it.
	AppendSystemPromptSet bool
	Hooks                 []agentcore.Hook
	TranscriptDir         string
	// SessionsDir is the per-session file root ($SUGA_HOME/sessions). Message
	// images are stored below <SessionsDir>/<session_id>/images and read back
	// from there when a request is assembled; empty disables both.
	SessionsDir string
	LockRoot    string
	ProcessRoot string
	MaxSteps    int
	// MaxOutputTokens is the output token limit of the model's requests
	// (appconfig.ResolveMaxOutputTokens); 0 leaves it to the provider, which
	// applies its own default then.
	MaxOutputTokens int
	// MaxRunTokens and RunTimeout ask a run to wrap up (answer from what it
	// has) past that many tokens (uncached input plus output) or that long;
	// 0 = no limit. Sub-agent runners set them.
	MaxRunTokens int
	// Budget is the optional turn/session token and cost budget. A zero value
	// keeps the historical unbounded behavior.
	Budget BudgetSettings
	// DeadlineEnv names the environment variable carrying this run's job
	// deadline as RFC3339 (run.deadline_env; gofer sets GOFER_JOB_DEADLINE).
	// Non-empty attaches the transient deadline hint to every model request
	// (deadline.go); empty = off, the historical no-time-awareness path.
	DeadlineEnv string
	RunTimeout  time.Duration
	// Now is the clock of the run time limit (RunTimeout): a turn's deadline is
	// Now()+RunTimeout and the limit hook checks Now() against it after every
	// model call. nil means time.Now. A test injects a clock because the check
	// needs the clock to have moved on, which the real one does not guarantee
	// within so short a limit (its resolution can exceed the limit).
	Now func() time.Time
	// ContextWindow is the effective model's context window in tokens (0 = 128000).
	ContextWindow int
	// CompactReserveTokens overrides the compaction reserve (default 16000).
	// A new segment keeps only the last complete tool exchange verbatim.
	CompactReserveTokens int
	// FoldEvery/FoldKeep configure history folding of the request view
	// (context.fold_every / context.fold_keep, see foldHistory); FoldEvery 0 = off.
	FoldEvery, FoldKeep int
	// FoldAt is the size trigger of folding (context.fold_at): the segment
	// starts folding once the pre-fold request estimate passes FoldAt × the
	// compaction threshold. 0 turns the trigger off (FoldEvery then applies from
	// the first step, as before).
	FoldAt float64
	// ImageHistoryTurns, ImageRequestCount and ImageRequestBytes are the image
	// input limits of the request view (images.history_turns / max_request_count /
	// max_request_bytes); 0 uses the built-in default (appconfig.DefaultImage*).
	ImageHistoryTurns, ImageRequestCount, ImageRequestBytes int
	// CompactProvider/CompactModel summarize history when llm.compact_model is
	// set; nil means the turn's own provider is used.
	CompactProvider agentcore.Provider
	CompactModel    string
	// Plans is the update_plan sink handed to the tool at build time; New binds
	// it to this runner's store.
	Plans *PlanRecorder
	// Visibility is the deferred-tool layer already wrapped around Tools; New
	// binds it to this runner's store (loaded_tools meta) and event recorder,
	// and its tool list is added to the segment context.
	Visibility *tools.Visibility
	// Price looks up the configured price of a (provider id, model); nil or a
	// miss means cost stays 0 unless the provider reports it.
	Price func(provider, model string) (Price, bool)
	// InlineOutputBytes is the output layer's inline threshold
	// (tools.max_output_bytes; 0 = tools.DefaultInlineOutputBytes).
	InlineOutputBytes int
	// Spiller stores tool outputs above the threshold (e.g. a SessionSpiller);
	// nil means they are only truncated.
	Spiller tools.Spiller
	// MinimizeOutput tidies tool results before they enter the context
	// (tools.minimize_output, see tools.OutputConfig.Minimize); nil = off.
	MinimizeOutput func(call agentcore.ToolCall, content string) (string, bool)
	// PrefetchTools runs a reply's local read calls ahead concurrently
	// (tools.parallel_reads, see agentcore.Config.PrefetchTools); nil = off.
	PrefetchTools func(ctx context.Context, calls []agentcore.ToolCall)
	// Ledger backs session_search / recall; New binds it to this runner's
	// store (late binding, like Plans).
	Ledger *Ledger
	// TaintTools always taint the session when they return a result
	// (memory.taint_tools); TaintOnSpill taint it only when their output was
	// externalised (memory.taint_on_spill). Tools declaring External always
	// taint.
	TaintTools   []string
	TaintOnSpill []string
	// Mode is the run mode recorded in turn_context events: "read_only" or
	// "allow_write" (empty = unknown).
	Mode string
	// MemoryIndex returns the rendered memory indexes of every registered
	// scope (memory.Index.Render, concatenated). The runner freezes them in
	// the segment snapshot (re-read at every segment break) and renders them
	// after the rules; nil means no memory section. A failure is logged and
	// never affects the turn.
	MemoryIndex func(ctx context.Context) (string, error)
	// Version is the suga build version, recorded on each turn (turn_context)
	// and as the session's suga_version meta (the last version that ran it).
	Version string
	// Workspace is recorded as the cwd of the sessions this runner creates
	// (and of older sessions without one on their next turn); session_search
	// scope workspace matches sessions by it.
	Workspace string
	// DiffPreview attaches what a file tool call changes on disk
	// (internal/diffview, the same construction ACP sends its client) to its
	// "tool_call" event, read before the tool runs. Chat asks for it (chat.diff),
	// so it can show the diff before an approval question and under the tool
	// result; run, stream-json and ACP leave it off and carry no file text in
	// their events.
	DiffPreview bool
}

// AgentSummary is attached to an agent tool result so chat can report the
// completed child without changing stream-json or ACP event payloads.
type AgentSummary struct {
	Description string
	ToolCalls   int
	Duration    time.Duration
	Background  bool
}

// Event is one transport-neutral lifecycle or model-stream update.
type Event struct {
	Kind      string
	SessionID string
	TurnID    string
	RunID     string
	Text      string
	ToolName  string
	// ToolArgs is set on "tool_call" and "agent_progress" events: the call's
	// whole arguments, for chat's one-line summary of the call (Text carries
	// them as JSON cut at toolArgSummaryLimit, too short to decode a long shell
	// command or a file_write). It is never serialized: run, stream-json, ACP
	// and the trace keep using Text.
	ToolArgs map[string]any `json:"-"`
	// IsError is set on "tool_result" events whose tool call failed or was refused.
	IsError bool
	// Plan is set on "plan" events (the full replaced task list).
	Plan *native.Plan
	// Change is set on the "tool_call" event of a file tool call (file_edit,
	// file_write, file_remove) when this runner previews it (Config.DiffPreview):
	// what the call changes on disk, read before the tool ran. Every other event
	// leaves it nil, and a caller that did not ask for it (run, ACP) never sees
	// one.
	Change *diffview.Change
	// Usage is set on "usage" events (after each counted model call) and on
	// "run_finished" (the final run total).
	Usage *UsageUpdate
	// Budget is set on budget events and on usage updates when a budget is
	// configured. It is nil for the historical no-budget path.
	Budget *BudgetSnapshot
	// Retry is set on "retry" events: a model request failed for a transient
	// reason and is retried after Wait; Text is the reason.
	Retry *RetryInfo
	// Agent is set on "agent_progress" events (a sub-agent's tool call,
	// ToolName and Text as on "tool_call"): the sub-agent's description.
	Agent string
	// AgentSummary is set on the completed "agent" tool result.
	AgentSummary *AgentSummary
	// SteerID and SteerSource are set on "steer_delivered" and "steer_returned"
	// events - Text is the steer's text: the id and the source ("keyboard",
	// "send:<from>") of the message the event is about.
	SteerID     string
	SteerSource string
	Status      agentcore.RunStatus
	State       RunState
}

// RetryInfo describes a model request retry (see agentcore.StreamEventRetry).
type RetryInfo struct {
	// Attempt counts the failed attempts so far (1 for the first retry).
	Attempt int
	Wait    time.Duration
	// Restart: the failed attempt had already streamed text; the message is
	// streamed again from its start.
	Restart bool
}

type EmitFunc func(context.Context, Event) error

type RunRequest struct {
	SessionID       string
	CreateIfMissing bool
	AgentID         string
	Prompt          string
	// Images are pictures that go with Prompt (chat :image, Alt+V paste, or a
	// caller that hands bytes in). They travel in memory only; the turn's user
	// message carries them and the store writes their files (no bytes in the DB).
	Images        []agentcore.ImagePart
	SystemContext string
	Injected      bool
	// Hops is the automatic-reply depth of this turn (personal.TurnHops): how many
	// session messages it is away from the person. 0 for a turn a person started
	// (a typed line, `suga send`), h for a turn a hops-h session message started
	// (design session_message D3).
	Hops int
	Emit EmitFunc
}

type RunResult struct {
	SessionID string
	TurnID    string
	RunID     string
	Status    agentcore.RunStatus
	State     RunState
	Output    string
	// Usage is this invocation's usage (provider-normalized: input includes cache).
	Usage agentcore.Usage
	// SessionUsage is the session-cumulative usage persisted after this turn.
	SessionUsage agentcore.Usage
	// ModelCalls counts the successful model calls of this invocation,
	// including those of a failed or cancelled turn.
	ModelCalls int
	// RunUsage is the observe-layer run total: every model call of this run
	// (compaction and failed attempts that reported usage included) and the
	// tool calls (refused included). stream-json and ACP report it as "usage".
	RunUsage RunUsage
	// LastInputTokens is the input tokens (cache included) of the most recent
	// model call, i.e. the context size of the last request; 0 without a call.
	LastInputTokens int
	// DeniedTools lists the distinct tools refused by the approval layer in
	// this turn (first-refusal order); empty when nothing was refused.
	DeniedTools []string
	// DenialLimitReached is true when the turn was ended because DenialLimit
	// refusals accumulated (ACP maps it to stopReason refusal).
	DenialLimitReached bool
	// WrapUp is why the answer was a wrap-up ("max_steps", or the reason a
	// hook asked for one, e.g. a sub-agent's "max_tokens"); empty for a
	// normal answer.
	WrapUp string
	// EmptyFinal marks a normal completion whose final model answer was empty
	// after repeated empty replies following a successful tool call.
	EmptyFinal bool
	// Truncated is true when this turn's answer was cut off at the output token
	// limit and may be incomplete; OutputLimit names that limit (0 = the
	// provider's or server's own default).
	Truncated   bool
	OutputLimit int
	// ErrorKind says why a failed turn failed (llm.ErrorKind):
	// "provider_transient" for a cut or broken stream whose retries ran out,
	// "provider_bad_response" for a provider answer that could not be used;
	// empty for a completed turn, a cancelled one, or a failure that is not a
	// provider error. The turn event carries it as attr error_kind and the
	// stream-json error line as error_kind, so a caller such as gofer can decide
	// whether continuing the session makes sense.
	ErrorKind string
	// ReturnedSteers are the steering messages the caller has to take care of
	// after the turn (design D4), in arrival order: the ones the turn did not
	// deliver (a completed turn) or all of them (a cancelled or failed turn, the
	// delivered ones first). The caller runs them as the next turn or puts them
	// back into its input line.
	ReturnedSteers []Steer
}

// Runner owns one synchronous turn at a time for a persisted personal session.
type Runner struct {
	cfg Config
	// modelMu guards the provider/model settings while a turn or compaction is
	// using them. A chat model switch waits for the session gate first, then
	// changes the settings for the next turn.
	modelMu   sync.RWMutex
	locksMu   sync.Mutex
	heldLocks map[string]*process.SessionLock
	// steerMu guards steers, the open per-session steering inboxes (one while a
	// turn of that session runs in this process, see steer.go).
	steerMu sync.Mutex
	steers  map[string]*steerInbox
	// gateMu guards gates, the in-process single-flight gates by session
	// (see gate.go).
	gateMu sync.Mutex
	gates  map[string]*gateState
	rec    *recorder
	// deadline is the run's job-deadline state (nil-equivalent when
	// DeadlineEnv is empty); the deadline hint hook shares it across turns.
	deadline *deadlineState
}

func New(cfg Config) (*Runner, error) {
	if cfg.Repository == nil || cfg.Provider == nil {
		return nil, fmt.Errorf("personal runner requires repository and provider")
	}
	// Observation wraps the provider and tools once here, so every model call
	// (including later compaction calls) and tool call emits events.
	if cfg.Plans != nil {
		cfg.Plans.bind(cfg.Repository)
	}
	if cfg.Ledger != nil {
		cfg.Ledger.bind(cfg.Repository)
	}
	rec := &recorder{sink: cfg.Repository, price: cfg.Price,
		taint:  newTaintMarker(cfg.Repository, cfg.TaintTools, cfg.TaintOnSpill),
		prefix: newPrefixTracker(), workspace: cfg.Workspace, vision: cfg.Vision}
	cfg.Provider = rec.observeProvider(cfg.Provider, cfg.ProviderName, cfg.Model)
	if cfg.Tools != nil {
		// Stack: observe -> output -> (visibility -> approval -> base). The
		// output layer sits inside observe so tool events see the shortened
		// result and its OutputInfo.
		cfg.Tools = rec.observeTools(tools.NewOutput(cfg.Tools, tools.OutputConfig{
			InlineBytes: cfg.InlineOutputBytes, Spiller: cfg.Spiller, Sanitize: redact.Secrets,
			Minimize: cfg.MinimizeOutput,
		}))
	}
	if cfg.CompactProvider != nil {
		cfg.CompactProvider = rec.observeProvider(cfg.CompactProvider, cfg.CompactProvider.Name(), cfg.CompactModel)
	}
	runner := &Runner{cfg: cfg, heldLocks: make(map[string]*process.SessionLock),
		steers: make(map[string]*steerInbox), rec: rec}
	runner.deadline = newDeadlineState(cfg.DeadlineEnv, runner.clock())
	runner.bindVisibility()
	return runner, nil
}

// SessionsDir returns the per-session file root ($SUGA_HOME/sessions): images of a
// session live below <SessionsDir>/<session_id>/images (design 0.3).
func (r *Runner) SessionsDir() string { return r.cfg.SessionsDir }

func (r *Runner) ProcessRoot() string { return r.cfg.ProcessRoot }

// ProviderName and Model identify the provider config id and model this runner
// calls; chat shows them in its per-turn status line.
func (r *Runner) ProviderName() string {
	if r == nil {
		return ""
	}
	r.modelMu.RLock()
	defer r.modelMu.RUnlock()
	return r.cfg.ProviderName
}

func (r *Runner) Model() string {
	if r == nil {
		return ""
	}
	r.modelMu.RLock()
	defer r.modelMu.RUnlock()
	return r.cfg.Model
}

// SwitchModel changes the main model used by the next turn and records the
// change outside a turn, so it remains visible in the session metadata and trace.
// provider must already include any fallback pool the caller wants to retain.
func (r *Runner) SwitchModel(ctx context.Context, sessionID string, provider agentcore.Provider, providerName, model string, contextWindow, maxOutputTokens int) error {
	if r == nil || provider == nil {
		return fmt.Errorf("personal runner and provider are required")
	}
	if sessionID == "" || providerName == "" || model == "" {
		return fmt.Errorf("session, provider and model are required")
	}
	release, err := r.gate(sessionID).Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	r.modelMu.Lock()
	defer r.modelMu.Unlock()
	from := r.cfg.ProviderName + "/" + r.cfg.Model
	to := providerName + "/" + model
	if from == to {
		return nil
	}
	if err := r.cfg.Repository.PatchSessionMeta(ctx, sessionID, map[string]any{"provider": providerName, "model": model}); err != nil {
		return fmt.Errorf("persist model switch: %w", err)
	}
	r.cfg.Provider = r.rec.observeProvider(provider, providerName, model)
	r.cfg.ProviderName, r.cfg.Model = providerName, model
	r.cfg.ContextWindow, r.cfg.MaxOutputTokens = contextWindow, maxOutputTokens
	if _, err := r.cfg.Repository.AppendEvent(ctx, store.Event{
		SessionID: sessionID, Kind: store.EventSession, Name: "model_switch", Status: eventOK,
		StartedAt: time.Now(), Attrs: map[string]any{"from": from, "to": to},
	}); err != nil {
		return fmt.Errorf("record model switch: %w", err)
	}
	return nil
}

// ModelSettings returns the effective main model settings used by the runner.
func (r *Runner) ModelSettings() (provider, model string, contextWindow, maxOutputTokens int) {
	if r == nil {
		return "", "", 0, 0
	}
	r.modelMu.RLock()
	defer r.modelMu.RUnlock()
	return r.cfg.ProviderName, r.cfg.Model, r.cfg.ContextWindow, r.cfg.MaxOutputTokens
}

// ToolRegistry is the tool table the runtime lists at every step
// (observe -> output -> visibility -> approval -> base), so it reflects the
// model-scoped filtering a turn sees (e.g. view_image and the current model's
// vision). nil when the runner was built without tools.
func (r *Runner) ToolRegistry() agentcore.ToolRegistry {
	if r == nil {
		return nil
	}
	return r.cfg.Tools
}

// MaxSteps is this runner's per-turn step limit (0 = no limit); the CLI names
// it in the text-mode wrap-up notice.
func (r *Runner) MaxSteps() int { return r.cfg.MaxSteps }

// Fold returns the history folding settings (every, keep) and the size
// trigger (at); every 0 = no step cadence, at 0 = no size trigger.
func (r *Runner) Fold() (every, keep int, at float64) {
	return r.cfg.FoldEvery, r.cfg.FoldKeep, r.cfg.FoldAt
}

func (r *Runner) NewSession(ctx context.Context, id, title string) (Session, error) {
	return r.NewSessionWithMeta(ctx, id, title, nil)
}

// NewSessionWithMeta is NewSession with extra meta stored as the session is
// created (a sub-agent session names its parent from the start, so it is
// never listed as a session of its own, not even after a crash). Without an id
// one is generated per attempt: a generated id already taken (a primary-key
// conflict) is replaced by a fresh one, at most maxIDAttempts times; an id the
// caller supplied is never retried.
func (r *Runner) NewSessionWithMeta(ctx context.Context, id, title string, extra map[string]any) (Session, error) {
	attempts := maxIDAttempts
	if id != "" {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if id == "" {
			id = newSessionID()
		}
		model, err := r.insertSession(ctx, id, title, extra)
		if err == nil {
			return model, nil
		}
		if !store.IsDuplicateKey(err) {
			return Session{}, err
		}
		lastErr = err
		id = ""
	}
	return Session{}, lastErr
}

// insertSession is one attempt of NewSessionWithMeta: it builds the session
// model with its first segment and inserts it.
func (r *Runner) insertSession(ctx context.Context, id, title string, extra map[string]any) (Session, error) {
	if err := validateSessionID(id); err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	model := Session{
		ID:        id,
		Title:     SessionTitle(title),
		Cwd:       r.cfg.Workspace,
		Status:    store.SessionOpen,
		CreatedAt: now,
		UpdatedAt: now,
		Meta:      map[string]any{},
	}
	if model.Title == "" {
		model.Title = defaultTitle(id)
	}
	// The first segment's snapshot (design D2). A RuleSource error fails the
	// session; a memory index that cannot be read leaves the snapshot without
	// one and the first turn that can read it appends a context update.
	seg, err := r.freshSnapshot(ctx, id, nil)
	if err != nil {
		return Session{}, err
	}
	for key, value := range segmentMeta(seg) {
		model.Meta[key] = value
	}
	model.Meta["model"] = r.cfg.Model
	model.Meta["provider"] = r.cfg.ProviderName
	for key, value := range extra {
		model.Meta[key] = value
	}
	if err := r.cfg.Repository.CreateSession(ctx, model); err != nil {
		return Session{}, fmt.Errorf("create personal session: %w", err)
	}
	// Every new session starts with a segment start record freezing the
	// snapshot above (design D2). Without it the session is not created.
	if err := r.writeStartSegment(ctx, model.ID, seg); err != nil {
		_ = r.cfg.Repository.DeleteSession(context.WithoutCancel(ctx), model.ID)
		// The session directory must not outlive the ledger row: its body file
		// would be an orphan file no stub points at.
		_ = r.cfg.Repository.DeleteSessionFiles(model.ID)
		return Session{}, err
	}
	return model, nil
}

// SetSessionTitle updates the persisted display title after an interactive chat's first prompt.
func (r *Runner) SetSessionTitle(ctx context.Context, sessionID, title string) error {
	model, err := r.LoadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	model.Title = SessionTitle(title)
	model.UpdatedAt = time.Now().UTC()
	if err := r.cfg.Repository.UpdateSessionFields(ctx, model); err != nil {
		return fmt.Errorf("update personal session title: %w", err)
	}
	return nil
}

func (r *Runner) LoadSession(ctx context.Context, id string) (Session, error) {
	if err := validateSessionID(id); err != nil {
		return Session{}, err
	}
	model, err := r.cfg.Repository.GetSession(ctx, id)
	if err != nil {
		return Session{}, fmt.Errorf("load personal session: %w", err)
	}
	return model, nil
}

// SaveSessionMeta merges patch into the stored session metadata (a nil value
// deletes a key). The front end records the session's added directories here.
func (r *Runner) SaveSessionMeta(ctx context.Context, sessionID string, patch map[string]any) error {
	if err := r.cfg.Repository.PatchSessionMeta(ctx, sessionID, patch); err != nil {
		return fmt.Errorf("update personal session metadata: %w", err)
	}
	return nil
}

// Run executes one new turn (run_id == turn_id) of the session. A turn that
// yielded for input is finished as INPUT_REQUIRED and is not resumed in place:
// the caller's next prompt is simply a new turn that sees it as history.
func (r *Runner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if r == nil {
		return RunResult{}, fmt.Errorf("personal runner is nil")
	}
	if req.SessionID == "" && strings.TrimSpace(req.Prompt) == "" {
		return RunResult{}, fmt.Errorf("prompt is required to create a session")
	}
	model, err := r.findSession(ctx, req)
	if err != nil {
		return RunResult{}, err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return RunResult{}, fmt.Errorf("prompt is required for a new turn")
	}
	// Session single flight (design D5): another Run or a :compact of this
	// session in this process finishes first, an idle consolidation is
	// preempted. Then re-read the session the previous holder may have changed.
	release, err := r.gate(model.ID).Acquire(ctx)
	if err != nil {
		return r.withSessionUsage(ctx, model.ID, RunResult{SessionID: model.ID}), err
	}
	defer release()
	fresh, err := r.LoadSession(ctx, model.ID)
	if err != nil {
		return r.withSessionUsage(ctx, model.ID, RunResult{SessionID: model.ID}), err
	}
	model = fresh
	if model.Status == store.SessionArchived {
		return r.withSessionUsage(ctx, model.ID, RunResult{SessionID: model.ID}), fmt.Errorf("session %q is not ready for a new turn: %s", model.ID, model.Status)
	}
	r.locksMu.Lock()
	_, heldByChat := r.heldLocks[model.ID]
	r.locksMu.Unlock()
	// lockHeld: this process holds the session's cross-process lock, so no other
	// live process can be inside a turn of it. Either chat holds it for its whole
	// lifetime (HoldSessionLock) or the turn takes it just below (recovery.go).
	lockHeld := r.cfg.LockRoot != "" && heldByChat
	if r.cfg.LockRoot != "" && !heldByChat {
		lock, err := process.AcquireSessionLockContext(ctx, r.cfg.LockRoot, model.ID, 30*time.Second)
		if err != nil {
			return r.withSessionUsage(ctx, model.ID, RunResult{SessionID: model.ID}), err
		}
		defer lock.Close()
		lockHeld = true
	}
	// A session left running by a process that died mid-turn is taken back to
	// open when nothing live can be running it; a running status this process
	// cannot disprove is still refused (recovery.go).
	if model.Status == store.SessionRunning {
		if model, err = r.recoverStaleSession(ctx, model, lockHeld, req.Emit); err != nil {
			return r.withSessionUsage(ctx, model.ID, RunResult{SessionID: model.ID}), err
		}
	}
	if model.Status == store.SessionRunning {
		return r.withSessionUsage(ctx, model.ID, RunResult{SessionID: model.ID}), fmt.Errorf("session %q is not ready for a new turn: %s", model.ID, model.Status)
	}
	transcript := r.openTranscript(model.ID)
	if transcript != nil {
		defer func() {
			if err := transcript.Close(); err != nil {
				slog.Warn("close personal transcript", "session_id", model.ID, "err", err)
			}
		}()
		appendTurnInput(transcript, model.ID, req)
	}

	// run_id == turn_id: the store's agentcore adapter keys run state and
	// messages by turn id, and the turn row must exist before the runtime saves.
	turnID := newTurnID()
	turnStart := time.Now()
	ctx = withObserveScope(ctx, model.ID, turnID)
	// Records written while this turn runs carry session_id and turn_id, so a
	// log line can be lined up with `suga trace` (see logscope.go).
	ctx = withTurnLogger(ctx, model.ID, turnID)
	ctx = context.WithValue(ctx, turnInputKey{}, turnInput{chars: len(req.Prompt), injected: req.Injected})
	// The automatic-reply depth of this turn (design session_message D3): the
	// session_message tool reads it and sends h+1.
	ctx = WithTurnHops(ctx, req.Hops)
	// Refused tool calls are counted per turn in ctx (see denial.go).
	ctx, denials := withTurnDenials(ctx)
	// The run-scoped usage is counted at the observe layer and published after
	// every model call (design D8).
	meter := r.newRunUsageMeter(ctx, model.ID, turnID, req.Emit)
	ctx = withRunUsage(ctx, meter)
	result := RunResult{SessionID: model.ID, TurnID: turnID, RunID: turnID}
	history, err := r.loadHistory(ctx, model.ID)
	if err != nil {
		return r.withSessionUsage(ctx, model.ID, result), err
	}
	// The segment in effect comes from the adopted records only (never from
	// meta, which a failed turn's mid-turn break may already have moved).
	r.resolveSegment(&history, model.Meta)
	// Rules/append prompt/environment changed inside the segment (or the
	// memory index is still missing): this turn appends a context update.
	update, err := r.contextUpdate(ctx, model.ID, history.eff)
	if err != nil {
		return r.withSessionUsage(ctx, model.ID, result), err
	}

	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = "suga"
	}
	// The turn row is inserted here (run_id == turn_id); a generated id that is
	// already taken is replaced by a fresh one, and the turn-scoped values built
	// above are derived again from the id actually stored.
	storedTurnID, err := r.createTurn(ctx, model.ID, turnID)
	if err != nil {
		return r.withSessionUsage(ctx, model.ID, result), fmt.Errorf("create turn: %w", err)
	}
	if storedTurnID != turnID {
		turnID = storedTurnID
		result.TurnID, result.RunID = turnID, turnID
		ctx = withObserveScope(ctx, model.ID, turnID)
		ctx = withTurnLogger(ctx, model.ID, turnID)
		meter = r.newRunUsageMeter(ctx, model.ID, turnID, req.Emit)
		ctx = withRunUsage(ctx, meter)
	}
	// Sub-agents of this session count their usage in this run (usage.go). The
	// meter is activated last, so it is the one the turn is stored under.
	defer activateMeter(model.ID, meter)()
	// Collect this turn's per-call usage (the session total comes from model events).
	usage := &usageCollector{cost: func(u agentcore.Usage) float64 {
		return costOf(r.cfg.Price, r.cfg.ProviderName, r.cfg.Model, u)
	}}
	// Keep session metadata aligned with the provider selected for this turn.
	metaPatch := map[string]any{"model": r.cfg.Model, "provider": r.cfg.ProviderName}
	if r.cfg.Version != "" {
		metaPatch[metaSugaVersion] = r.cfg.Version
	}
	if err := r.cfg.Repository.PatchSessionMeta(ctx, model.ID, metaPatch); err != nil {
		return r.finish(ctx, model, result, turnStart, usage, nil, err, req.Emit)
	}
	// Re-read so the prompt builder and the status write see the patched,
	// current metadata (including usage written by another process).
	model, err = r.markRunning(ctx, model.ID, turnID)
	if err != nil {
		return r.finish(ctx, model, result, turnStart, usage, nil, err, req.Emit)
	}
	// The update is an entry of this turn written before the turn's user
	// message, so it is adopted with the turn and replayed at the same place
	// (a ctxItem: stable position, a cut candidate, never in state.Messages).
	if update != nil {
		item, err := r.writeContextUpdate(ctx, model.ID, turnID, update)
		if err != nil {
			return r.finish(ctx, model, result, turnStart, usage, nil, err, req.Emit)
		}
		history.items = append(history.items, item)
		update.applyTo(history.eff)
	}
	// Undo notices are persisted after history loading has finished. This keeps
	// an interrupted turn's replay ahead of the fact that the user rolled back
	// its file changes, and avoids opening a turn merely to deliver the notice.
	if item, ok, err := r.injectUndoNotice(ctx, model.ID, turnID); err != nil {
		return r.finish(ctx, model, result, turnStart, usage, nil, err, req.Emit)
	} else if ok {
		history.items = append(history.items, item)
	}
	// What this turn starts with (D9); best-effort like every event.
	r.recordTurnContext(ctx, model, history.segment, history.eff)
	if err := emit(ctx, req.Emit, Event{Kind: "run_started", SessionID: result.SessionID, TurnID: result.TurnID, RunID: result.RunID}); err != nil {
		return r.finish(ctx, model, result, turnStart, usage, nil, err, req.Emit)
	}

	stream := func(ctx context.Context, update agentcore.StreamEvent) error {
		return emit(ctx, req.Emit, Event{
			Kind:      string(update.Kind),
			SessionID: result.SessionID,
			TurnID:    result.TurnID,
			RunID:     result.RunID,
			Text:      update.Delta,
			Retry:     retryInfoOf(update),
		})
	}
	// update_plan reaches the caller through this turn's Emit callback.
	ctx = withPlanEmitter(ctx, func(ctx context.Context, plan native.Plan) error {
		return emit(ctx, req.Emit, Event{Kind: "plan", SessionID: result.SessionID, TurnID: result.TurnID, RunID: result.RunID, Plan: &plan})
	})
	// So does a sub-agent's progress (AgentProgress; subagent.go).
	if req.Emit != nil {
		ctx = context.WithValue(ctx, agentEmitKey{}, EmitFunc(func(ctx context.Context, e Event) error {
			e.SessionID, e.TurnID, e.RunID = result.SessionID, result.TurnID, result.RunID
			return emit(ctx, req.Emit, e)
		}))
	}
	var storage agentcore.Storage = r.cfg.Repository.AgentStorage().WithSessionsDir(r.cfg.SessionsDir)
	if req.Injected {
		// Persist the existing "not typed by a person" marker on the turn's
		// request, so a later handoff package can tell it apart (D4).
		storage = &injectedStorage{Storage: storage}
	}
	promptBuilder := stablePromptBuilder{base: r.cfg.PromptBuilder}
	// The hook context (req.SystemContext) is a user message of this turn.
	contextBuilder := r.newContextBuilder(model.ID, turnID, history, promptBuilder, usage, req.SystemContext)
	// Fold events already recorded in this segment (an earlier turn's builder),
	// and whether the size trigger already turned folding on for it.
	contextBuilder.foldBase = metaCount(model.Meta[metaFoldBoundary])
	contextBuilder.foldActive = metaBool(model.Meta[metaFoldActive])
	// The session's steering inbox is open from here to the end of the turn
	// (design N7): every early return above happens before this point, so a Steer
	// arriving earlier is not accepted and becomes the caller's next turn. finish
	// closes it (the defer is only the safety net for a panic). The builder takes
	// the pending steers before every non-wrap-up request and reports them.
	inbox := r.openSteerInbox(model.ID, turnID)
	defer r.dropSteerInbox(model.ID, inbox)
	contextBuilder.steers, contextBuilder.emit = inbox, req.Emit
	ctx = withSteerInbox(ctx, inbox)
	// The sink of this turn's task notices: finish puts them back into the task
	// manager when the turn ends cancelled or failed (issue suga-o26e).
	notices := &turnNotices{}
	contextBuilder.notices = notices
	ctx = withTurnNotices(ctx, notices)
	runtimeHooks := r.turnHooks(usage, meter, denials, req.Emit, result)
	runtime := agentcore.New(agentcore.Config{
		Provider:        r.cfg.Provider,
		Storage:         storage,
		Tools:           r.cfg.Tools,
		PromptBuilder:   promptBuilder,
		ContextBuilder:  contextBuilder,
		Hooks:           runtimeHooks,
		MaxSteps:        r.cfg.MaxSteps,
		MaxOutputTokens: r.cfg.MaxOutputTokens,
		MaxStepsWrapUp:  maxStepsWrapUp(r.cfg.MaxSteps),
		// Refused calls become tool results; aborted approvals end the turn.
		ToolExecutionPolicy: abortingToolPolicy{},
		PrefetchTools:       r.cfg.PrefetchTools,
	})
	coreResult, runErr := runtime.Run(ctx, agentcore.RunInput{
		RunID:       result.RunID,
		AgentID:     agentID,
		Instruction: req.Prompt,
		Images:      req.Images,
		ResumeMode:  agentcore.ResumeAuto,
		Stream:      stream,
	})
	if transcript != nil {
		appendTurnOutput(context.WithoutCancel(ctx), transcript, storage, model.ID, result.RunID, coreResult)
	}
	return r.finish(ctx, model, result, turnStart, usage, coreResult, runErr, req.Emit)
}

// appendTurnInput writes the turn's user lines to the session transcript.
func appendTurnInput(transcript *Transcript, sessionID string, req RunRequest) {
	if req.SystemContext != "" {
		if err := transcript.AppendUser(req.SystemContext, true); err != nil {
			slog.Warn("append personal transcript", "session_id", sessionID, "err", err)
		}
	}
	if req.Prompt != "" {
		// Only the caller knows whether a person typed this prompt.
		if err := transcript.AppendUser(req.Prompt, req.Injected); err != nil {
			slog.Warn("append personal transcript", "session_id", sessionID, "err", err)
		}
	}
}

// turnHooks assembles the runtime hooks of one turn. The collector goes first
// so a later hook short-circuiting the chain cannot hide a model call from the
// usage total. The denial limit hook ends the turn before the next model call
// once DenialLimit refusals accumulated.
func (r *Runner) turnHooks(usage *usageCollector, meter *runUsageMeter, denials *turnDenials, sink EmitFunc, result RunResult) []agentcore.Hook {
	hooks := []agentcore.Hook{usage}
	if r.cfg.Budget.enabled() {
		settings := r.cfg.Budget.normalized()
		var priceAvailable bool
		if r.cfg.Price != nil {
			_, priceAvailable = r.cfg.Price(r.cfg.ProviderName, r.cfg.Model)
		}
		budget := &budgetHook{settings: settings, meter: meter, usage: usage, priceAvailable: priceAvailable,
			sessionID: result.SessionID, turnID: result.TurnID}
		budget.onBudget = func(ctx context.Context, snapshot BudgetSnapshot, reason string) {
			r.rec.record(ctx, budgetEvent(result.SessionID, result.TurnID, "exceeded", snapshot, reason))
			if err := emit(ctx, sink, Event{Kind: "budget", SessionID: result.SessionID, TurnID: result.TurnID, RunID: result.RunID, Budget: &snapshot}); err != nil {
				turnLogger(ctx).Warn("emit budget event", "err", err)
			}
		}
		budget.onWarning = func(ctx context.Context, message string) {
			snapshot := meter.budgetSnapshot()
			r.rec.record(ctx, budgetWarningEvent(result.SessionID, result.TurnID, snapshot, message))
			if err := emit(ctx, sink, Event{Kind: "budget", SessionID: result.SessionID, TurnID: result.TurnID, RunID: result.RunID, Text: message, Budget: &snapshot}); err != nil {
				turnLogger(ctx).Warn("emit budget warning", "err", err)
			}
		}
		hooks = append(hooks, budget)
	}
	hooks = append(hooks, denialLimitHook{denials: denials, usage: usage},
		prefixHook{tracker: r.rec.prefix})
	// The deadline hint goes after the prefix tracker so its appended
	// transient message stays out of the recorded request fingerprint; see
	// deadline.go for the message and its wording.
	hooks = append(hooks, deadlineHook{state: r.deadline})
	hooks = append(hooks, r.cfg.Hooks...)
	if r.cfg.MaxRunTokens > 0 || r.cfg.RunTimeout > 0 {
		limits := limitHook{maxTokens: r.cfg.MaxRunTokens, now: r.clock()}
		if r.cfg.RunTimeout > 0 {
			limits.deadline = limits.now().Add(r.cfg.RunTimeout)
		}
		hooks = append(hooks, limits)
	}
	if sink != nil {
		hooks = append(hooks, toolEventHook{sink: sink, sessionID: result.SessionID, turnID: result.TurnID,
			runID: result.RunID, workspace: r.cfg.Workspace, diffPreview: r.cfg.DiffPreview})
	}
	return hooks
}

// clock is the time source of the run time limit (Config.Now; the real clock
// when none was injected).
func (r *Runner) clock() func() time.Time {
	if r.cfg.Now != nil {
		return r.cfg.Now
	}
	return time.Now
}

// markRunning flips the session to running with this turn as last_turn_id and
// returns the fresh session (metadata included).
func (r *Runner) markRunning(ctx context.Context, sessionID, turnID string) (Session, error) {
	fresh, err := r.cfg.Repository.GetSession(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	fresh.Status, fresh.LastTurnID, fresh.UpdatedAt = store.SessionRunning, turnID, time.Now().UTC()
	// Sessions created before cwd was recorded pick it up on their next turn.
	if fresh.Cwd == "" {
		fresh.Cwd = r.cfg.Workspace
	}
	if err := r.cfg.Repository.UpdateSessionFields(ctx, fresh); err != nil {
		return fresh, err
	}
	return fresh, nil
}

// toolArgSummaryLimit caps the argument summary carried by "tool_call" events.
const toolArgSummaryLimit = 200

// toolEventHook publishes "tool_call" (before execution) and "tool_result"
// (after) events. Emit failures are only logged: returning them from a hook
// stage would abort the tool or the turn, and display must never do that.
// With Config.DiffPreview the tool_call event carries the change the call makes
// on disk, read here - before the tool runs, so the file still holds the text
// before it.
type toolEventHook struct {
	agentcore.NopHook
	sink      EmitFunc
	sessionID string
	turnID    string
	runID     string
	// workspace is where a file tool call works; diffPreview turns the change
	// Config.DiffPreview asks for on.
	workspace   string
	diffPreview bool
}

func (h toolEventHook) publish(ctx context.Context, event Event) {
	event.SessionID, event.TurnID, event.RunID = h.sessionID, h.turnID, h.runID
	if err := emit(ctx, h.sink, event); err != nil {
		turnLogger(ctx).Warn("emit tool event", "kind", event.Kind, "tool", event.ToolName, "err", err)
	}
}

func (h toolEventHook) BeforeTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event == nil || event.Call == nil {
		return nil
	}
	call := Event{Kind: "tool_call", ToolName: event.Call.Name, Text: toolArgSummary(event.Call.Arguments), ToolArgs: event.Call.Arguments}
	if h.diffPreview {
		if change, ok := diffview.Call(h.workspace, event.Call.Name, event.Call.Arguments); ok {
			call.Change = change
		}
	}
	h.publish(ctx, call)
	return nil
}

func (h toolEventHook) AfterTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event == nil || event.Result == nil {
		return nil
	}
	resultEvent := Event{Kind: "tool_result", ToolName: event.Result.Name, Text: event.Result.Content, IsError: event.Result.IsError}
	if event.Result.Name == "agent" {
		resultEvent.AgentSummary = agentSummary(event.Result.Metadata)
	}
	h.publish(ctx, resultEvent)
	return nil
}

func agentSummary(metadata map[string]any) *AgentSummary {
	if metadata == nil {
		return nil
	}
	description, _ := metadata["description"].(string)
	calls := metadataInt(metadata["tool_calls"])
	duration := time.Duration(metadataInt64(metadata["duration_ms"])) * time.Millisecond
	background, _ := metadata["background"].(bool)
	if description == "" && calls == 0 && duration == 0 && !background {
		return nil
	}
	return &AgentSummary{Description: description, ToolCalls: calls, Duration: duration, Background: background}
}

func metadataInt(value any) int {
	return int(metadataInt64(value))
}

func metadataInt64(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// toolArgSummary renders call arguments as compact JSON, redacted and truncated.
func toolArgSummary(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	data, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return truncateRunes(redact.Secrets(string(data)), toolArgSummaryLimit)
}

func (r *Runner) openTranscript(sessionID string) *Transcript {
	if r.cfg.TranscriptDir == "" {
		return nil
	}
	transcript, err := OpenTranscript(r.cfg.TranscriptDir, sessionID)
	if err != nil {
		slog.Warn("open personal transcript", "session_id", sessionID, "err", err)
		return nil
	}
	return transcript
}

func (r *Runner) findSession(ctx context.Context, req RunRequest) (Session, error) {
	if req.SessionID == "" {
		return r.NewSession(ctx, "", req.Prompt)
	}
	model, err := r.LoadSession(ctx, req.SessionID)
	if err == nil {
		// A session created without a title (ACP session/new) takes its
		// first prompt's.
		if model.Title == defaultTitle(model.ID) && !req.Injected {
			if title := SessionTitle(req.Prompt); title != "" {
				if err := r.SetSessionTitle(ctx, model.ID, title); err != nil {
					return Session{}, err
				}
				model.Title = title
			}
		}
		return model, nil
	}
	if req.CreateIfMissing && errors.Is(err, ErrSessionNotFound) {
		if strings.TrimSpace(req.Prompt) == "" {
			return Session{}, fmt.Errorf("prompt is required to create session %q", req.SessionID)
		}
		return r.NewSession(ctx, req.SessionID, req.Prompt)
	}
	return Session{}, err
}

// maxStepsWrapUp is the request-only instruction used when a turn reaches its
// step limit: the model answers from what it has instead of the turn failing
// and losing the work done so far. It must name the real reason (the step
// limit, not a timeout or an interruption) and make the model state it in the
// report: the reason is what a reader of a short answer needs.
func maxStepsWrapUp(maxSteps int) string {
	if maxSteps <= 0 {
		return ""
	}
	return fmt.Sprintf("已达到本轮的步数上限（%d 步）。不要再调用任何工具，直接根据目前已获得的信息回答。回答开头必须如实写明：本次是本轮步数上限（%d 步）收尾，不得写成超时、被中断或其它原因；然后说明已完成的部分、尚未完成的部分，以及继续时建议的下一步。", maxSteps, maxSteps)
}

func validateSessionID(id string) error {
	if id == "." || id == ".." || !safeSessionID.MatchString(id) {
		return fmt.Errorf("invalid session ID %q", id)
	}
	return nil
}

func emit(ctx context.Context, sink EmitFunc, event Event) error {
	if sink == nil {
		return nil
	}
	return sink(ctx, event)
}

// metaSugaVersion is the session meta key of the suga version that last ran
// a turn of it.
const metaSugaVersion = "suga_version"

// retryInfoOf reads a StreamEventRetry's metadata; nil for other events.
func retryInfoOf(ev agentcore.StreamEvent) *RetryInfo {
	if ev.Kind != agentcore.StreamEventRetry {
		return nil
	}
	info := &RetryInfo{}
	info.Attempt, _ = ev.Metadata["attempt"].(int)
	if ms, ok := ev.Metadata["wait_ms"].(int64); ok {
		info.Wait = time.Duration(ms) * time.Millisecond
	}
	info.Restart, _ = ev.Metadata["restart"].(bool)
	return info
}
