package personal

import (
	"context"
	"sync"

	"github.com/inhere/suga/internal/personal/process"
)

// Session single flight (design D5). Three mechanisms keep one session's work
// from overlapping; each covers a different scope:
//
//   - sessionGate (this file) is in-process only: Run, Compact (:compact) and
//     the idle consolidation of one session never run at the same time inside
//     this process. The idle consolidation holds it preemptibly: a Run or
//     Compact arriving meanwhile cancels it and waits for it to let go.
//   - the session file lock (process.SessionLock) is cross-process: chat holds
//     it for its whole lifetime (HoldSessionLock, recorded in heldLocks);
//     otherwise Run/Compact take it for one call. It does not order the
//     callers inside one process (chat already holds it for all of them).
//   - markRunning / store.SessionRunning is the persisted status other
//     processes (suga ps, a second suga run) read; it is set only while a turn
//     runs, under both of the above.
//
// Callers take the gate first, then the file lock, so a waiting caller never
// sits on the file lock.

// gateState is one session's gate; guarded by Runner.gateMu. An entry exists
// only while the gate is held or awaited.
type gateState struct {
	held bool
	// freed is closed when the current holder releases (a fresh one per hold).
	freed chan struct{}
	// preempt is the current holder's preemption signal; nil for a regular
	// holder. preempted records that it was closed (once).
	preempt   chan struct{}
	preempted bool
	// waiters counts Acquire calls blocked on this gate.
	waiters int
}

// sessionGate is the gate of one session of a Runner.
type sessionGate struct {
	r   *Runner
	sid string
}

// gate returns the in-process gate of sessionID.
func (r *Runner) gate(sessionID string) sessionGate {
	return sessionGate{r: r, sid: sessionID}
}

// stateLocked returns the gate state, creating it on first use.
func (g sessionGate) stateLocked() *gateState {
	if g.r.gates == nil {
		g.r.gates = map[string]*gateState{}
	}
	st := g.r.gates[g.sid]
	if st == nil {
		st = &gateState{}
		g.r.gates[g.sid] = st
	}
	return st
}

// dropLocked forgets an idle gate state (not held, nobody waiting).
func (g sessionGate) dropLocked(st *gateState) {
	if !st.held && st.waiters == 0 && g.r.gates[g.sid] == st {
		delete(g.r.gates, g.sid)
	}
}

// preemptLocked asks a preemptible holder to yield (no-op otherwise).
func preemptLocked(st *gateState) {
	if st.preempt != nil && !st.preempted {
		close(st.preempt)
		st.preempted = true
	}
}

// takeLocked makes the caller the holder and returns its release func.
func (g sessionGate) takeLocked(st *gateState, preemptible bool) func() {
	st.held, st.freed, st.preempt, st.preempted = true, make(chan struct{}), nil, false
	if preemptible {
		st.preempt = make(chan struct{})
		if st.waiters > 0 {
			// Someone is already waiting: yield at once.
			preemptLocked(st)
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			g.r.gateMu.Lock()
			defer g.r.gateMu.Unlock()
			st.held, st.preempt = false, nil
			close(st.freed)
			g.dropLocked(st)
		})
	}
}

// Acquire waits until the caller holds the gate (Run, Compact). A preemptible
// holder is asked to yield first. It fails only when ctx ends while waiting.
func (g sessionGate) Acquire(ctx context.Context) (release func(), err error) {
	g.r.gateMu.Lock()
	defer g.r.gateMu.Unlock()
	for {
		// Re-read the state each round: a released, unawaited state is
		// dropped from the map and a later caller starts a new one.
		st := g.stateLocked()
		if !st.held {
			return g.takeLocked(st, false), nil
		}
		preemptLocked(st)
		st.waiters++
		freed := st.freed
		g.r.gateMu.Unlock()
		select {
		case <-freed:
		case <-ctx.Done():
		}
		g.r.gateMu.Lock()
		st.waiters--
		g.dropLocked(st)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// AcquireUnlessBusy is Acquire for the rewind/fork operations: a regular
// holder (a turn, Compact) makes it fail at once with ErrRewindBusy instead
// of waiting, because rewinding under a running turn would race its writes.
// A preemptible holder (the idle consolidation) is asked to yield and waited
// for, exactly as Acquire does; free gates are taken directly. It also fails
// when ctx ends while waiting for the preempted holder.
func (g sessionGate) AcquireUnlessBusy(ctx context.Context) (release func(), err error) {
	g.r.gateMu.Lock()
	defer g.r.gateMu.Unlock()
	for {
		st := g.stateLocked()
		if !st.held {
			return g.takeLocked(st, false), nil
		}
		// preempt is non-nil only for a preemptible holder. Re-checked each
		// round: a Run may take the gate between the release and our wakeup.
		if st.preempt == nil {
			return nil, ErrRewindBusy
		}
		preemptLocked(st)
		st.waiters++
		freed := st.freed
		g.r.gateMu.Unlock()
		select {
		case <-freed:
		case <-ctx.Done():
		}
		g.r.gateMu.Lock()
		st.waiters--
		g.dropLocked(st)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// TryAcquire takes the gate only when it is free right now.
func (g sessionGate) TryAcquire() (release func(), ok bool) {
	g.r.gateMu.Lock()
	defer g.r.gateMu.Unlock()
	st := g.stateLocked()
	if st.held {
		return nil, false
	}
	return g.takeLocked(st, false), true
}

// AcquirePreemptible waits until the caller holds the gate as a preemptible
// holder (the idle consolidation itself does not wait: it uses
// TryAcquirePreemptible). preempted is closed when an Acquire wants the
// gate (immediately if one is already waiting): the holder must then cancel
// its work and release; the Acquire proceeds after the release. It never
// preempts anyone itself.
func (g sessionGate) AcquirePreemptible(ctx context.Context) (release func(), preempted <-chan struct{}, err error) {
	g.r.gateMu.Lock()
	defer g.r.gateMu.Unlock()
	for {
		st := g.stateLocked()
		if !st.held {
			release = g.takeLocked(st, true)
			return release, st.preempt, nil
		}
		freed := st.freed
		g.r.gateMu.Unlock()
		select {
		case <-freed:
		case <-ctx.Done():
		}
		g.r.gateMu.Lock()
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
	}
}

// TryAcquirePreemptible takes the gate as a preemptible holder only when it
// is free right now (idle consolidation: "取不到闸门就放弃本次"). ok is false
// while anyone holds it; it never waits and never preempts.
func (g sessionGate) TryAcquirePreemptible() (release func(), preempted <-chan struct{}, ok bool) {
	g.r.gateMu.Lock()
	defer g.r.gateMu.Unlock()
	st := g.stateLocked()
	if st.held {
		return nil, nil, false
	}
	release = g.takeLocked(st, true)
	return release, st.preempt, true
}

// HoldSessionLock reserves a session for the lifetime of an interactive chat
// (the cross-process file lock; see the single flight notes above).
func (r *Runner) HoldSessionLock(sessionID string) (func() error, error) {
	if r.cfg.LockRoot == "" {
		return func() error { return nil }, nil
	}
	r.locksMu.Lock()
	if _, exists := r.heldLocks[sessionID]; exists {
		r.locksMu.Unlock()
		return func() error { return nil }, nil
	}
	r.locksMu.Unlock()
	lock, err := process.AcquireSessionLock(r.cfg.LockRoot, sessionID, 0)
	if err != nil {
		return nil, err
	}
	r.locksMu.Lock()
	if existing := r.heldLocks[sessionID]; existing != nil {
		r.locksMu.Unlock()
		_ = lock.Close()
		return func() error { return nil }, nil
	}
	r.heldLocks[sessionID] = lock
	r.locksMu.Unlock()
	var once sync.Once
	return func() error {
		var closeErr error
		once.Do(func() {
			r.locksMu.Lock()
			delete(r.heldLocks, sessionID)
			r.locksMu.Unlock()
			closeErr = lock.Close()
		})
		return closeErr
	}, nil
}
