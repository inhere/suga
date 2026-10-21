package personal

// Session accessors, model switching and session lifecycle methods of Runner.

import (
	"context"
	"fmt"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

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
