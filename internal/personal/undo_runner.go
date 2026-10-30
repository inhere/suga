package personal

import "context"

// UndoCore builds the provider-independent checkpoint service for this runner.
// The snapshot store is supplied by wiring so the runner remains usable with
// snapshots disabled and no Provider is needed by the core itself.
func (r *Runner) UndoCore(snapshots *SessionSnapshots, sessionsRoot string) *UndoCore {
	if r == nil {
		return NewUndoCore(nil, snapshots, sessionsRoot, "")
	}
	core := NewUndoCore(r.cfg.Repository, snapshots, sessionsRoot, r.cfg.LockRoot)
	core.Gate = r.gateForUndo()
	core.HeldLock = r.heldLockForUndo()
	return core
}

// Checkpoints is a thin Runner adapter; all selection and locking stays in
// UndoCore so CLI and chat can use the same implementation without a Provider.
func (r *Runner) Checkpoints(ctx context.Context, snapshots *SessionSnapshots, sessionsRoot, sessionQuery string, limit int) ([]Checkpoint, error) {
	return r.UndoCore(snapshots, sessionsRoot).Checkpoints(ctx, sessionQuery, limit)
}

// Undo executes the same provider-independent file restoration used by chat and
// CLI. It is a thin adapter; lock ordering and conflict handling stay in core.
func (r *Runner) Undo(ctx context.Context, snapshots *SessionSnapshots, sessionsRoot, sessionQuery string, n int, force bool) (UndoResult, error) {
	result, err := r.UndoCore(snapshots, sessionsRoot).Undo(ctx, sessionQuery, n, force)
	// Files restored before a failure changed too.
	if r != nil && r.cfg.OnFilesRestored != nil && len(result.Files) > 0 {
		r.cfg.OnFilesRestored(result.Files)
	}
	return result, err
}

// SelectUndoCheckpoints is the corresponding locked Runner adapter for undo
// selection. It does not perform file restoration; later undo stages consume
// this stable, root-validated plan.
func (r *Runner) SelectUndoCheckpoints(ctx context.Context, snapshots *SessionSnapshots, sessionsRoot, sessionQuery string, n int) ([]Checkpoint, error) {
	return r.UndoCore(snapshots, sessionsRoot).Select(ctx, sessionQuery, n)
}

func (r *Runner) gateForUndo() UndoGate {
	return undoGate{acquire: func(ctx context.Context) (func(), error) {
		return r.gate(undoSessionID(ctx)).Acquire(ctx)
	}}
}

// undoGate carries the session ID through context because the existing gate is
// per-session while UndoGate remains a small provider-independent interface.
type undoGate struct {
	acquire func(context.Context) (func(), error)
}

func (g undoGate) Acquire(ctx context.Context) (func(), error) { return g.acquire(ctx) }

// undoSessionID is read by the Runner adapter after UndoCore has resolved the
// session prefix and placed the full ID in the gate context.
type undoSessionIDKey struct{}

func undoSessionID(ctx context.Context) string {
	id, _ := ctx.Value(undoSessionIDKey{}).(string)
	return id
}

func (r *Runner) heldLockForUndo() func(string) bool {
	return func(sessionID string) bool {
		r.locksMu.Lock()
		defer r.locksMu.Unlock()
		_, ok := r.heldLocks[sessionID]
		return ok
	}
}
