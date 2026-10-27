package sugasdk

// The engine: one per process, owning the loaded suga configuration and the
// sessions it opened.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/redact"
)

// Errors of the engine and its sessions; test them with errors.Is.
var (
	// ErrEngineOpen reports a second Open in a process that has an engine.
	ErrEngineOpen = errors.New("sugasdk: an engine is already open in this process")
	// ErrEngineClosed reports a use of an engine (or of a session it opened)
	// after Close.
	ErrEngineClosed = errors.New("sugasdk: engine is closed")
	// ErrSugaHomeMismatch reports an Open for a data root other than the one
	// this process already loaded: suga's configuration is process global.
	ErrSugaHomeMismatch = errors.New("sugasdk: this process already loaded suga configuration from another SUGA_HOME")
	// ErrBusy reports Send on a session whose previous turn still runs; the
	// turns of one session never queue.
	ErrBusy = errors.New("sugasdk: a turn is already running in this session")
	// ErrNoRunningTurn reports Steer while no turn of the session runs.
	ErrNoRunningTurn = errors.New("sugasdk: no turn is running in this session")
	// ErrRequestRunning reports Send with a RequestID whose turn is currently
	// running in this session (the caller retries when it finishes).
	ErrRequestRunning = errors.New("sugasdk: this request is already running in this session")
	// ErrDeliveryUncertain reports Send with a RequestID whose record says
	// running but whose turn is not running in any live place (the claiming
	// process died mid-turn): whether the prompt was delivered is unknown.
	// Send the prompt again under a NEW request id; the old one is never
	// replayed on its own.
	ErrDeliveryUncertain = errors.New("sugasdk: the delivery of this request is uncertain (it was interrupted while running); resend with a new request id")
	// ErrRequestOutputGone reports a replayable request whose turn's answer is
	// no longer in the session log (the entries were trimmed after the request
	// aged past what the storage keeps), so its output cannot be rebuilt.
	ErrRequestOutputGone = errors.New("sugasdk: the stored answer of the request is gone from the session log")
	// ErrSessionClosed reports a use of a session after Close.
	ErrSessionClosed = errors.New("sugasdk: session is closed")
	// ErrOutputContract reports a send whose final answer does not satisfy
	// Prompt.OutputSchema, even after the one correction round: the turn itself
	// completed (Result.State stays COMPLETED), the answer's shape did not meet the
	// caller's contract. Result.OutputContract carries the report.
	ErrOutputContract = errors.New("sugasdk: the answer does not satisfy the output schema")
	// ErrInvalidOutputSchema reports a Prompt.OutputSchema that does not compile:
	// the send fails before any turn starts.
	ErrInvalidOutputSchema = errors.New("sugasdk: the output schema is invalid")
)

// engineMu guards openEngine, the one engine of this process.
var (
	engineMu   sync.Mutex
	openEngine *Engine
)

// Engine is the embedding host's handle on suga in this process: the loaded
// configuration, the sessions it opened and the memory and history of the data
// root. One process holds at most one engine at a time.
type Engine struct {
	opts EngineOptions
	// home is the absolute data root this process loaded (the value
	// $SUGA_HOME was set to).
	home   string
	logger *slog.Logger

	mu       sync.Mutex
	closed   bool
	sessions map[*Session]struct{}
	// memRepo and mem store the lazily built memory layer of Engine.Memory: the
	// provenance records need the session database open while the store is used.
	memRepo *store.Store
	mem     *memory.Governed
}

// Open loads suga's configuration for opts.SugaHome if this process has not
// loaded one yet, and returns the engine. A process that already loaded the
// same root reuses it; another root is an error (suga's settings are process
// global). A second Open while an engine is open is an error too: Close it
// first.
func Open(ctx context.Context, opts EngineOptions) (*Engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	engineMu.Lock()
	defer engineMu.Unlock()
	if openEngine != nil {
		return nil, ErrEngineOpen
	}
	home, err := resolveHome(opts.SugaHome)
	if err != nil {
		return nil, err
	}
	if err := ensureConfigHome(home); err != nil {
		return nil, err
	}
	// Remember the root the configuration really came from: ResolveSugaHome
	// answers the value $SUGA_HOME carries now.
	if actual, rerr := appconfig.ResolveSugaHome(); rerr == nil {
		home = actual
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	engine := &Engine{opts: opts, home: home, logger: logger, sessions: map[*Session]struct{}{}}
	openEngine = engine
	return engine, nil
}

// resolveHome turns the requested data root into an absolute one, defaulting to
// $SUGA_HOME and then to ~/.suga.
func resolveHome(requested string) (string, error) {
	home := strings.TrimSpace(requested)
	if home == "" {
		resolved, err := appconfig.ResolveSugaHome()
		if err != nil {
			return "", fmt.Errorf("sugasdk: resolve SUGA_HOME: %w", err)
		}
		return resolved, nil
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("sugasdk: resolve SUGA_HOME %q: %w", home, err)
	}
	return abs, nil
}

// ensureConfigHome loads the configuration of home when this process has none,
// and refuses a root another one was loaded from. The loaded root is recorded
// by the configuration package, so an embedding process that loaded suga before
// it opened the engine is honoured as well.
func ensureConfigHome(home string) error {
	if loaded, ok := appconfig.LoadedHome(); ok {
		if !personal.SameDir(loaded, home) {
			return fmt.Errorf("%w (loaded %q, requested %q)", ErrSugaHomeMismatch, loaded, home)
		}
		return nil
	}
	if err := os.Setenv("SUGA_HOME", home); err != nil {
		return fmt.Errorf("sugasdk: set SUGA_HOME: %w", err)
	}
	if err := appconfig.Load(); err != nil {
		return fmt.Errorf("sugasdk: load suga configuration from %q: %w", home, err)
	}
	return nil
}

// Home is the data root the engine's configuration came from.
func (e *Engine) Home() string { return e.home }

// Model is the engine's default model ref ("" = the configured model).
func (e *Engine) Model() string { return e.opts.Model }

// Close releases the engine: the memory layer, the store behind it and every
// session this engine opened. Sessions closed by the caller first are skipped.
// Close it while no turn runs: a session's runtime is closed here, so a Send
// still in flight fails. The loaded suga configuration stays in the process (it
// cannot be unloaded); after Close another Open is allowed, for the same root
// only.
func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	sessions := make([]*Session, 0, len(e.sessions))
	for s := range e.sessions {
		sessions = append(sessions, s)
	}
	e.sessions = map[*Session]struct{}{}
	repo := e.memRepo
	e.memRepo, e.mem = nil, nil
	e.mu.Unlock()
	var errs []error
	for _, s := range sessions {
		errs = append(errs, s.Close())
	}
	if repo != nil {
		errs = append(errs, repo.Close())
	}
	engineMu.Lock()
	if openEngine == e {
		openEngine = nil
	}
	engineMu.Unlock()
	return errors.Join(errs...)
}

// track registers a session with the engine; a closed engine refuses it.
func (e *Engine) track(s *Session) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrEngineClosed
	}
	e.sessions[s] = struct{}{}
	return nil
}

// untrack forgets a session the caller closed.
func (e *Engine) untrack(s *Session) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.sessions, s)
}

// Memory returns the memory API of this engine's data root and the project
// scope of the process directory. The memory layer is built on first use.
func (e *Engine) Memory() *Memory { return &Memory{engine: e} }

// memoryStore returns the governed memory store, building it on first use.
func (e *Engine) memoryStore() (*memory.Governed, error) {
	if e == nil {
		return nil, ErrEngineClosed
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrEngineClosed
	}
	if e.mem != nil {
		return e.mem, nil
	}
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return nil, fmt.Errorf("sugasdk: resolve session database: %w", err)
	}
	repo, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("sugasdk: open session database: %w", err)
	}
	// The host writes on its own behalf, not inside a turn, so no session of
	// this engine is tainted here: a tainted write stays a turn's business.
	workspace, err := os.Getwd()
	if err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("sugasdk: resolve process directory: %w", err)
	}
	gov, err := wiring.OpenEmbeddedMemory(context.Background(), workspace, repo, nil, nil)
	if err != nil {
		_ = repo.Close()
		return nil, err
	}
	e.memRepo, e.mem = repo, gov
	return gov, nil
}

// memoryDatabase returns the metadata store kept open alongside the governed
// memory layer. Initializing the layer first keeps SDK and tool writes on the
// same SQLite database and policy path.
func (e *Engine) memoryDatabase() (*store.Store, error) {
	if _, err := e.memoryStore(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.memRepo == nil {
		return nil, ErrEngineClosed
	}
	return e.memRepo, nil
}

// Search runs the session_search query of the whole store: every term must
// occur in one entry. limit <= 0 means 20, at most 100.
func (e *Engine) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	if e == nil {
		return nil, ErrEngineClosed
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("sugasdk: empty search query")
	}
	repo, err := e.openStore()
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	result, err := repo.SearchEntriesPage(ctx, store.EntrySearch{Query: query, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("sugasdk: search sessions: %w", err)
	}
	hits := make([]Hit, 0, len(result.Hits))
	for _, h := range result.Hits {
		hits = append(hits, Hit{Ref: h.SessionID + "/" + strconv.FormatInt(h.Seq, 10), SessionID: h.SessionID,
			Role: h.Role, Snippet: redact.Clean(h.Snippet), CreatedAt: h.CreatedAt})
	}
	return hits, nil
}

// Sessions lists the stored sessions (newest first) matching filter. Sub-agent
// sessions and archived ones stay hidden unless the filter asks for them.
func (e *Engine) Sessions(ctx context.Context, filter SessionFilter) ([]SessionInfo, error) {
	if e == nil {
		return nil, ErrEngineClosed
	}
	repo, err := e.openStore()
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	all, err := repo.ListSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("sugasdk: list sessions: %w", err)
	}
	var scope string
	if strings.TrimSpace(filter.Cwd) != "" {
		if scope, err = filepath.Abs(filter.Cwd); err != nil {
			return nil, fmt.Errorf("sugasdk: resolve filter directory: %w", err)
		}
	}
	origin := strings.TrimSpace(filter.Origin)
	out := make([]SessionInfo, 0, len(all))
	for _, s := range all {
		if store.IsAgentSession(s) && !filter.IncludeAgents {
			continue
		}
		if s.Status == store.SessionArchived && !filter.IncludeArchived {
			continue
		}
		sessionOrigin := store.SessionOrigin(s)
		if origin != "" && origin != "all" && sessionOrigin != origin {
			continue
		}
		if scope != "" && !sessionInDir(s.Cwd, scope) {
			continue
		}
		out = append(out, SessionInfo{ID: s.ID, Title: personal.SessionTitle(s.Title), Cwd: s.Cwd,
			Status: s.Status, Origin: sessionOrigin, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
	}
	return out, nil
}

// sessionInDir reports whether cwd is dir or below it (path boundaries, case
// folded on Windows).
func sessionInDir(cwd, dir string) bool {
	if strings.TrimSpace(cwd) == "" {
		return false
	}
	if personal.SameDir(cwd, dir) {
		return true
	}
	rel, err := filepath.Rel(dir, cwd)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// openStore opens the session database of the engine's data root; the caller
// closes it.
func (e *Engine) openStore() (*store.Store, error) {
	if e == nil {
		return nil, ErrEngineClosed
	}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return nil, ErrEngineClosed
	}
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return nil, fmt.Errorf("sugasdk: resolve session database: %w", err)
	}
	repo, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("sugasdk: open session database: %w", err)
	}
	return repo, nil
}
