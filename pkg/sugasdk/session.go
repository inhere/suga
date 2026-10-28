package sugasdk

// Sessions: one conversation of the embedding host, running one turn at a
// time on its own assembled runtime.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/outschema"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// OriginSDK is the session origin the SDK records when no origin is configured.
// Hosts that need longer retention should pin sessions with Engine.Pin.
const OriginSDK = store.OriginSDK

// DefaultOrigin is kept as a compatibility alias for OriginSDK.
const DefaultOrigin = OriginSDK

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
	// runningRequest is the Prompt.RequestID of the turn that currently holds
	// the slot ("" for a plain send without an id); it lets a second Send with
	// the same id read as ErrRequestRunning instead of a bare ErrBusy.
	runningRequest string
	cancel         context.CancelFunc
	closed         bool
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
		origin = strings.TrimSpace(e.opts.Origin)
	}
	if origin == "" {
		origin = OriginSDK
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

// ID is the persisted session id.
func (s *Session) ID() string { return s.id }

// Workspace is the directory the session's tools work in.
func (s *Session) Workspace() string { return s.workspace }

// Send runs one turn and streams its events to emit (which may be nil). The
// turn ends when ctx is cancelled; a panic inside the turn is recovered and
// returned as an error, so it never reaches the caller. A session runs one turn
// at a time: a second Send while one runs returns ErrBusy instead of queueing.
// With a RequestID the send is an idempotent request instead: an id already
// finished replays its stored first result (Result.Replayed), an id whose turn
// runs errors with ErrRequestRunning, and a record left running by a process
// that died mid-turn errors with ErrDeliveryUncertain (resend with a new id).
func (s *Session) Send(ctx context.Context, prompt Prompt, emit func(Event)) (res Result, err error) {
	requestID := strings.TrimSpace(prompt.RequestID)
	if prompt.RequestID != "" && requestID == "" {
		return Result{}, errors.New("sugasdk: the request id is empty")
	}
	// The request ledger lives in the session meta: one store handle covers the
	// dispatch checks, the claim and the records this turn writes.
	var repo *store.Store
	if requestID != "" {
		if repo, err = s.engine.openStore(); err != nil {
			return Result{}, err
		}
		// Registered before the other defers, so it runs last (after the turn
		// outcome was recorded).
		defer func() { _ = repo.Close() }()
		rec, rerr := s.readRequestRecord(ctx, repo, requestID)
		if rerr != nil {
			return Result{}, rerr
		}
		if rec != nil {
			// An existing claim answers the send: replay it, or report running /
			// uncertain, without taking the turn slot.
			return s.dispatchRecord(ctx, repo, requestID, rec)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	if err := s.claimRequest(requestID, cancel); err != nil {
		cancel()
		return Result{}, err
	}
	defer func() {
		cancel()
		s.endTurn()
	}()
	if requestID != "" {
		// Persist the claim (state running, no turn id yet) as a write-once key:
		// from here on a crash leaves the id marked running (delivery uncertain)
		// instead of silently lost.
		applied, perr := s.claimRequestRecord(ctx, repo, requestID)
		if perr != nil {
			return Result{}, fmt.Errorf("sugasdk: claim request %q: %w", requestID, perr)
		}
		if !applied {
			// Another claimer took the key between the read and this patch:
			// re-read and answer from the record.
			rec, rerr := s.readRequestRecord(ctx, repo, requestID)
			if rerr != nil {
				return Result{}, rerr
			}
			if rec == nil {
				// Unreachable (the guard key and the patch key are the same), but
				// a nil record must never reach dispatchRecord.
				return Result{}, fmt.Errorf("sugasdk: claim of request %q vanished", requestID)
			}
			return s.dispatchRecord(ctx, repo, requestID, rec)
		}
		// The claim grew the session's record registry: apply the size cap (best
		// effort, so an old finished record gets out of the meta again).
		s.trimRequestRecordsFor(ctx, repo)
	}
	// The turn runs suga's own code (tools, provider): a panic there must not
	// take the host down, and the session keeps whichever state suga left.
	// The outcome is recorded (or the panic recovered) below, before the turn
	// slot and the store close.
	var turnID string
	// The provider may call the stream callback from its own goroutine: the
	// turn id it records (below) must not race with the finish defer reading it.
	var turnMu sync.Mutex
	defer func() {
		if recovered := recover(); recovered != nil {
			s.engine.logger.Error("sugasdk: recovered panic in a turn", "session", s.id, "panic", recovered)
			err = fmt.Errorf("sugasdk: turn panicked: %v", recovered)
			res = Result{SessionID: s.id, State: string(personal.RunFailed)}
		}
		turnMu.Lock()
		turn, request := turnID, requestID
		turnMu.Unlock()
		if request != "" && turn != "" {
			finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(runCtx), 5*time.Second)
			defer finishCancel()
			if perr := s.requestTurnFinished(finishCtx, repo, request, turn, res, err); perr != nil {
				s.engine.logger.Warn("sugasdk: record the request outcome", "session", s.id, "request", request, "err", perr)
			}
		}
	}()
	images, err := s.prepareImages(prompt.Images)
	if err != nil {
		return Result{}, err
	}
	// The output contract is a parameter of this send: it compiles before the turn
	// starts (a bad schema asks the model nothing), its text travels in the turn's
	// per-request context, and it is judged after the turn finished (design
	// 2026-10-22 2.2).
	contract, schema, err := s.outputContract(prompt.OutputSchema)
	if err != nil {
		return Result{}, err
	}
	ephemeral := ""
	if contract != nil {
		ephemeral = outschema.PromptBlock(schema)
	}
	events := func(_ context.Context, ev personal.Event) error {
		if requestID != "" && ev.TurnID != "" {
			turnMu.Lock()
			if turnID == "" {
				// The turn's row exists (run_started and later): store the
				// id-to-turn mapping early, so a crash mid-turn still lets a
				// host line the request up with the turn it may have reached
				// the model with.
				turnID = ev.TurnID
				stored := turnID
				turnMu.Unlock()
				s.requestTurnStarted(ctx, repo, requestID, stored)
			} else {
				turnMu.Unlock()
			}
		}
		if emit != nil {
			emit(convertEvent(ev))
		}
		return nil
	}
	runResult, runErr := s.rt.Runner.Run(runCtx, personal.RunRequest{SessionID: s.id, Prompt: prompt.Text, Images: images,
		EphemeralContext: ephemeral, Emit: events})
	if runResult.TurnID != "" {
		turnMu.Lock()
		if turnID == "" {
			turnID = runResult.TurnID
		}
		turnMu.Unlock()
	}
	res = convertResult(runResult)
	if contract != nil && runErr == nil {
		// Judge the answer the caller finally gets; a failing one costs one
		// correction round. The request ledger keeps pointing at the first turn.
		var rep personal.ContractReport
		runResult, rep, runErr = s.rt.Runner.RunWithContract(runCtx, personal.RunRequest{SessionID: s.id},
			contract, schema, runResult, personal.ContractHooks{Emit: events})
		res = convertResult(runResult)
		res.OutputContract = convertContractReport(rep)
		if rep.Checked && rep.Passed {
			res.OutputValue = rep.Value
		} else if rep.Checked && runErr == nil {
			runErr = fmt.Errorf("%w: %d 条校验错误", ErrOutputContract, len(rep.Errors))
		}
	}
	return res, runErr
}

// outputContract compiles Prompt.OutputSchema; a send without one returns nil.
// A schema that does not compile fails before any turn starts.
func (s *Session) outputContract(raw json.RawMessage) (*outschema.Contract, []byte, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil, nil
	}
	contract, err := outschema.Compile(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidOutputSchema, err)
	}
	return contract, contract.Raw(), nil
}

// claimRequest takes the session's single turn slot. requestID (empty for a
// plain send) is recorded with the slot, so a second Send with the same id
// reads as ErrRequestRunning while a different turn in flight stays ErrBusy.
// cancel is installed as the cancellation hook used by Session.Cancel.
func (s *Session) claimRequest(requestID string, cancel context.CancelFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if s.running {
		if requestID != "" && s.runningRequest == requestID {
			return ErrRequestRunning
		}
		return ErrBusy
	}
	s.running = true
	s.runningRequest = requestID
	s.cancel = cancel
	return nil
}

// endTurn releases the turn slot.
func (s *Session) endTurn() {
	s.mu.Lock()
	s.running = false
	s.runningRequest = ""
	s.cancel = nil
	s.mu.Unlock()
}

// Cancel stops the current turn. It is safe to call concurrently with Send;
// after cancellation the session slot is released and a later Send can run.
func (s *Session) Cancel() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	cancel := s.cancel
	running := s.running
	s.mu.Unlock()
	if !running || cancel == nil {
		return ErrNoRunningTurn
	}
	cancel()
	return nil
}

// RequestStatus reports where the request requestID of this session stands:
// RequestStatusRunning while its turn runs here, RequestStatusDone or
// RequestStatusFailed once its stored outcome is replayable, and
// RequestStatusUnknown otherwise - including a record left running by a
// process that died mid-turn (delivery uncertain: resend with a new id).
func (s *Session) RequestStatus(ctx context.Context, requestID string) (string, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return "", errors.New("sugasdk: the request id is empty")
	}
	if err := s.checkOpen(); err != nil {
		return "", err
	}
	repo, err := s.engine.openStore()
	if err != nil {
		return "", err
	}
	defer func() { _ = repo.Close() }()
	rec, err := s.readRequestRecord(ctx, repo, requestID)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return RequestStatusUnknown, nil
	}
	switch strings.TrimSpace(rec.State) {
	case requestStateDone:
		return RequestStatusDone, nil
	case requestStateFailed:
		return RequestStatusFailed, nil
	case requestStateRunning:
		s.mu.Lock()
		mine := s.running && s.runningRequest == requestID
		s.mu.Unlock()
		if mine {
			return RequestStatusRunning, nil
		}
	}
	// Nothing running in a live place reads unknown: the claim is a leftover,
	// and the caller decides to resend under a new id.
	return RequestStatusUnknown, nil
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
