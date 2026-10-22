package sugasdk

// Sessions: one conversation of the embedding host, running one turn at a
// time on its own assembled runtime.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// DefaultOrigin is the session origin the SDK records when SessionOptions.Origin
// is empty: the host application's sessions stay out of the default
// `suga sessions` list.
const DefaultOrigin = "xhen"

// steerSource is the source recorded on the steers this SDK sends; the trace
// then shows which front end inserted the message.
const steerSource = "sdk"

// Session is one conversation: a persisted suga session plus the runtime (tools,
// provider, rules) assembled for its workspace.
type Session struct {
	engine    *Engine
	rt        *wiring.Runtime
	id        string
	workspace string

	mu      sync.Mutex
	running bool
	closed  bool
}

// OpenSession opens a conversation: an existing session id continues it, an
// empty or unknown one starts a new session (with that id when one is given).
// The runtime of the session is assembled here, so SessionOptions (workspace,
// model, extra tools, approver) apply from its first turn.
func (e *Engine) OpenSession(ctx context.Context, id string, opts SessionOptions) (*Session, error) {
	if e == nil {
		return nil, ErrEngineClosed
	}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return nil, ErrEngineClosed
	}
	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" {
		wd, err := ctxGetwd()
		if err != nil {
			return nil, err
		}
		workspace = wd
	}
	abs, err := absDir(workspace)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = e.opts.Model
	}
	origin := strings.TrimSpace(opts.Origin)
	if origin == "" {
		origin = DefaultOrigin
	}
	rt, err := wiring.Open(ctx, wiring.Options{
		Workspace: abs, RuleCWD: abs, Model: model, AllowWrite: opts.AllowWrite,
		SessionOrigin: origin, ExtraTools: opts.ExtraTools, Hooks: opts.Hooks,
		HostContext: opts.HostContext, Approver: opts.Approver,
		MemoryConfirm: memoryConfirmAdapter(opts.MemoryConfirm),
	})
	if err != nil {
		return nil, fmt.Errorf("sugasdk: assemble session runtime: %w", err)
	}
	sessionID, err := openSessionID(ctx, rt.Runner, strings.TrimSpace(id), origin)
	if err != nil {
		_ = rt.Close()
		return nil, err
	}
	s := &Session{engine: e, rt: rt, id: sessionID, workspace: abs}
	if err := e.track(s); err != nil {
		_ = rt.Close()
		return nil, err
	}
	return s, nil
}

// openSessionID continues an existing session, or creates one with the
// requested id (empty: a generated id) under origin.
func openSessionID(ctx context.Context, runner *personal.Runner, id, origin string) (string, error) {
	if id != "" {
		model, err := runner.LoadSession(ctx, id)
		if err == nil {
			return model.ID, nil
		}
		if !errors.Is(err, personal.ErrSessionNotFound) {
			return "", fmt.Errorf("sugasdk: load session %q: %w", id, err)
		}
	}
	model, err := runner.NewSessionFor(ctx, id, "", origin)
	if err != nil {
		return "", fmt.Errorf("sugasdk: open session: %w", err)
	}
	return model.ID, nil
}

// memoryConfirmAdapter turns the SDK's confirmation into the runtime's: an
// unapproved (or missing) confirmation is an error, which refuses the write.
func memoryConfirmAdapter(confirm func(ctx context.Context, req MemoryConfirmRequest) bool) wiring.MemoryConfirmFunc {
	if confirm == nil {
		return nil
	}
	return func(ctx context.Context, scope memory.Scope, op memory.Op) error {
		req := MemoryConfirmRequest{Scope: scope.String(), Action: string(op.Action), Kind: string(op.Kind),
			ID: op.ID, Title: op.Title, Body: op.Body, Summary: op.Summary}
		if confirm(ctx, req) {
			return nil
		}
		return errors.New("sugasdk: memory write refused by the host")
	}
}

// ID is the persisted session id; `suga sessions --origin xhen` lists it.
func (s *Session) ID() string { return s.id }

// Workspace is the directory the session's tools work in.
func (s *Session) Workspace() string { return s.workspace }

// Send runs one turn and streams its events to emit (which may be nil). The
// turn ends when ctx is cancelled; a panic inside the turn is recovered and
// returned as an error, so it never reaches the caller. A session runs one turn
// at a time: a second Send while one runs returns ErrBusy instead of queueing.
func (s *Session) Send(ctx context.Context, prompt Prompt, emit func(Event)) (res Result, err error) {
	if err := s.beginTurn(); err != nil {
		return Result{}, err
	}
	defer s.endTurn()
	// The turn runs suga's own code (tools, provider): a panic there must not
	// take the host down, and the session keeps whichever state suga left.
	defer func() {
		if recovered := recover(); recovered != nil {
			s.engine.logger.Error("sugasdk: recovered panic in a turn", "session", s.id, "panic", recovered)
			err = fmt.Errorf("sugasdk: turn panicked: %v", recovered)
			res = Result{SessionID: s.id, State: string(personal.RunFailed)}
		}
	}()
	images, err := s.prepareImages(prompt.Images)
	if err != nil {
		return Result{}, err
	}
	events := func(_ context.Context, ev personal.Event) error {
		if emit != nil {
			emit(convertEvent(ev))
		}
		return nil
	}
	runResult, runErr := s.rt.Runner.Run(ctx, personal.RunRequest{SessionID: s.id, Prompt: prompt.Text, Images: images, Emit: events})
	return convertResult(runResult), runErr
}

// beginTurn takes the session's single turn slot.
func (s *Session) beginTurn() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if s.running {
		return ErrBusy
	}
	s.running = true
	return nil
}

// endTurn releases the turn slot.
func (s *Session) endTurn() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

// Steer inserts text into the running turn of the session (the turn's next model
// request carries it). ErrNoRunningTurn is returned when no turn of the session
// runs, so the caller runs the text as its next turn instead.
func (s *Session) Steer(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("sugasdk: empty steer")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrSessionClosed
	}
	accepted, err := s.rt.Runner.Steer(s.id, personal.Steer{Text: text, Source: steerSource})
	if err != nil {
		return err
	}
	if !accepted {
		return ErrNoRunningTurn
	}
	return nil
}

// Card reads where the session stands (latest summary, open plan steps, usage,
// activity). It writes nothing.
func (s *Session) Card(ctx context.Context) (Card, error) {
	if err := s.checkOpen(); err != nil {
		return Card{}, err
	}
	card, err := s.rt.Runner.SessionCard(ctx, s.id)
	if err != nil {
		return Card{}, err
	}
	return convertCard(card), nil
}

// Compact starts a new segment of the session now. A session with nothing to
// compact counts as success.
func (s *Session) Compact(ctx context.Context) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if _, err := s.rt.Runner.Compact(ctx, s.id); err != nil && !errors.Is(err, personal.ErrNothingToCompact) {
		return err
	}
	return nil
}

// Close releases the session's runtime and forgets it in the engine. It is
// idempotent. Close it while no turn runs: the runtime the turn uses is closed
// here, so a Send still in flight fails.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	err := s.rt.Close()
	s.engine.untrack(s)
	return err
}

// checkOpen reports a use after Close.
func (s *Session) checkOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	return nil
}

// prepareImages reads the prompt's pictures with the session workspace's rules
// (the same checks, scaling and storage chat's :image uses).
func (s *Session) prepareImages(paths []string) ([]agentcore.ImagePart, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	parts := make([]agentcore.ImagePart, 0, len(paths))
	for _, path := range paths {
		part, _, err := wiring.ReadImageAt(s.workspace, path)
		if err != nil {
			return nil, fmt.Errorf("sugasdk: read image %q: %w", path, err)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// ctxGetwd is os.Getwd, indirected so a test can fix the directory.
var ctxGetwd = func() (string, error) { return os.Getwd() }

// absDir resolves a workspace path.
func absDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("sugasdk: resolve workspace %q: %w", dir, err)
	}
	return abs, nil
}
