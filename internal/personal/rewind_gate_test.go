package personal

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
)

func newRewindGateCore(t *testing.T, r *Runner) *UndoCore {
	t.Helper()
	return r.UndoCore(NewSessionSnapshots(t.TempDir()), t.TempDir())
}

func TestRewindAcquireUnlessBusy(t *testing.T) {
	ctx := context.Background()
	t.Run("a turn holding the gate returns busy at once", func(t *testing.T) {
		r := &Runner{}
		release, err := r.gate("s").Acquire(ctx)
		assert.Require(t, assert.NoErr(t, err))
		defer release()
		start := time.Now()
		_, err = r.gate("s").AcquireUnlessBusy(ctx)
		assert.True(t, errors.Is(err, ErrRewindBusy))
		assert.True(t, time.Since(start) < time.Second)
		assert.Eq(t, 0, gateWaiters(r, "s"))
	})
	t.Run("an idle consolidation is preempted and waited for", func(t *testing.T) {
		r := &Runner{}
		held, preempted, err := r.gate("s").AcquirePreemptible(ctx)
		assert.Require(t, assert.NoErr(t, err))
		type result struct {
			release func()
			err     error
		}
		done := make(chan result, 1)
		go func() {
			rel, err := r.gate("s").AcquireUnlessBusy(ctx)
			done <- result{rel, err}
		}()
		select {
		case <-preempted:
		case <-time.After(5 * time.Second):
			t.Fatal("holder was not asked to yield")
		}
		select {
		case <-done:
			t.Fatal("must wait for the release")
		case <-time.After(20 * time.Millisecond):
		}
		held()
		res := <-done
		assert.Require(t, assert.NoErr(t, res.err))
		res.release()
	})
	t.Run("an idle gate is taken directly", func(t *testing.T) {
		r := &Runner{}
		release, err := r.gate("s").AcquireUnlessBusy(ctx)
		assert.Require(t, assert.NoErr(t, err))
		_, ok := r.gate("s").TryAcquire()
		assert.False(t, ok)
		release()
		again, ok := r.gate("s").TryAcquire()
		assert.True(t, ok)
		again()
	})
	t.Run("a waiting caller gives up with its context", func(t *testing.T) {
		r := &Runner{}
		held, _, err := r.gate("s").AcquirePreemptible(ctx)
		assert.Require(t, assert.NoErr(t, err))
		defer held()
		cctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		_, err = r.gate("s").AcquireUnlessBusy(cctx)
		assert.True(t, errors.Is(err, context.DeadlineExceeded))
	})
}

func TestWithSessionUnlessBusy(t *testing.T) {
	ctx := context.Background()
	t.Run("turn holding the gate", func(t *testing.T) {
		r := &Runner{}
		core := newRewindGateCore(t, r)
		release, err := r.gate("s").Acquire(ctx)
		assert.Require(t, assert.NoErr(t, err))
		defer release()
		called := false
		err = core.withSessionUnlessBusy(ctx, "s", func() error { called = true; return nil })
		assert.True(t, errors.Is(err, ErrRewindBusy))
		assert.False(t, called)
	})
	t.Run("idle consolidation is preempted", func(t *testing.T) {
		r := &Runner{}
		core := newRewindGateCore(t, r)
		held, preempted, err := r.gate("s").AcquirePreemptible(ctx)
		assert.Require(t, assert.NoErr(t, err))
		go func() { // the consolidation yields when asked
			<-preempted
			held()
		}()
		called := false
		err = core.withSessionUnlessBusy(ctx, "s", func() error { called = true; return nil })
		assert.NoErr(t, err)
		assert.True(t, called)
	})
	t.Run("idle gate", func(t *testing.T) {
		r := &Runner{}
		core := newRewindGateCore(t, r)
		assert.NoErr(t, core.withSessionUnlessBusy(ctx, "s", func() error {
			_, ok := r.gate("s").TryAcquire()
			assert.False(t, ok) // held while fn runs
			return nil
		}))
		release, ok := r.gate("s").TryAcquire()
		assert.True(t, ok) // released after
		release()
	})
	t.Run("nil gate (CLI)", func(t *testing.T) {
		core := NewUndoCore(nil, nil, "", "")
		called := false
		assert.NoErr(t, core.withSessionUnlessBusy(ctx, "s", func() error { called = true; return nil }))
		assert.True(t, called)
	})
	t.Run("file lock held elsewhere maps to busy", func(t *testing.T) {
		lockRoot := filepath.Join(t.TempDir(), "locks")
		lock, err := personalprocess.AcquireSessionLock(lockRoot, "s", 0)
		assert.Require(t, assert.NoErr(t, err))
		defer lock.Close()
		core := NewUndoCore(nil, nil, "", lockRoot)
		err = core.withSessionUnlessBusy(ctx, "s", func() error { return nil })
		assert.True(t, errors.Is(err, ErrRewindBusy))
		// A lock the chat itself holds is reused.
		core.HeldLock = func(string) bool { return true }
		assert.NoErr(t, core.withSessionUnlessBusy(ctx, "s", func() error { return nil }))
	})
	t.Run("test gate stub is used", func(t *testing.T) {
		repo := openObserveRepo(t)
		assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "g", Cwd: t.TempDir()}))
		gate := &undoTestGate{}
		core := NewUndoCore(repo, NewSessionSnapshots(t.TempDir()), t.TempDir(), "")
		core.Gate = gate
		assert.NoErr(t, core.withSessionUnlessBusy(ctx, "g", func() error { return nil }))
		assert.Eq(t, "g", gate.id)
	})
}
