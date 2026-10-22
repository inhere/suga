package wiring

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

const snapshotUnrecoverableError = "snapshot_error"

// policySnapshotter adapts the storage error policy to native's small hook
// contract. deny returns an error before I/O; continue deliberately returns a
// visible unrecoverable result so a write is never presented as undoable.
type policySnapshotter struct {
	onError string
	// unavailable is a permanent failure (configuration that names no usable
	// sessions directory, or one this process may not write). It is reported on
	// every call instead of being retried.
	unavailable error

	mu          sync.Mutex
	store       *personal.SessionSnapshots
	settings    appconfig.SnapshotSettings
	sessionsDir string
	// ready is true once the initial retention scan succeeded. A failure only
	// leaves the policy unready so the next call retries it: one unreadable
	// snapshot directory must not disable snapshots for the whole session.
	ready   bool
	lastErr error
}

var _ native.Snapshotter = (*policySnapshotter)(nil)

func (s *policySnapshotter) BeforeCall(ctx context.Context, req native.SnapshotRequest) (native.SnapshotBefore, error) {
	// Tools assembled for catalog/approval tests may be called outside a runner;
	// only a session-scoped call can produce a useful checkpoint.
	if api.SessionIDFrom(ctx) == "" {
		return native.SnapshotBefore{}, nil
	}
	store, err := s.ensureStore()
	if err != nil {
		return s.degradedBefore(err)
	}
	before, err := store.BeforeCall(ctx, req)
	if err == nil || s.onError != appconfig.SnapshotOnErrorContinue {
		return before, err
	}
	return native.SnapshotBefore{
		Unrecoverable: snapshotUnrecoverableError,
		Notice:        "本次写入不可撤销：快照不可用（" + err.Error() + "）",
	}, nil
}

func (s *policySnapshotter) AfterCall(ctx context.Context, result native.SnapshotResult) error {
	if api.SessionIDFrom(ctx) == "" {
		return nil
	}
	store, err := s.ensureStore()
	if err != nil {
		if s.onError == appconfig.SnapshotOnErrorContinue {
			return nil
		}
		return err
	}
	err = store.AfterCall(ctx, result)
	if err != nil && s.onError == appconfig.SnapshotOnErrorContinue {
		return nil
	}
	return err
}

// ensureStore returns the snapshot store once its initial retention scan
// succeeded. The scan is the only expensive part; a failure is retried on the
// next call, which keeps a transient problem local to the call that hit it.
func (s *policySnapshotter) ensureStore() (*personal.SessionSnapshots, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable != nil {
		return nil, s.unavailable
	}
	if s.ready && s.store != nil {
		return s.store, nil
	}
	if s.store == nil {
		if s.sessionsDir == "" {
			s.unavailable = errors.New("snapshot sessions directory is empty")
			return nil, s.unavailable
		}
		s.store = personal.NewSessionSnapshotsWithLimits(s.sessionsDir, personal.SnapshotLimits{
			MaxFileBytes: s.settings.MaxFileBytes, MaxSessionBytes: s.settings.MaxSessionBytes,
			MaxGlobalBytes: s.settings.MaxGlobalBytes, KeepTurns: s.settings.KeepTurns, KeepDays: s.settings.KeepDays,
		})
	}
	if _, err := s.store.CleanupAll(); err != nil {
		err = fmt.Errorf("snapshot cleanup: %w", err)
		s.lastErr = err
		if snapshotPermanentFailure(err) {
			s.unavailable = err
		}
		return nil, err
	}
	s.ready, s.lastErr = true, nil
	return s.store, nil
}

// degradedBefore applies the configured policy to a call that cannot snapshot.
func (s *policySnapshotter) degradedBefore(cause error) (native.SnapshotBefore, error) {
	if s.onError == appconfig.SnapshotOnErrorContinue {
		return native.SnapshotBefore{
			Unrecoverable: snapshotUnrecoverableError,
			Notice:        "本次写入不可撤销：快照不可用（" + cause.Error() + "）",
		}, nil
	}
	return native.SnapshotBefore{}, cause
}

// snapshotPermanentFailure reports whether retrying cannot succeed in this
// process. Everything else (a directory a concurrent cleanup removed, a sharing
// violation while another process writes) is transient and retried later.
func snapshotPermanentFailure(err error) bool {
	return errors.Is(err, fs.ErrPermission)
}

// configuredSnapshotter builds the one policy used by every native tool catalog
// assembled in this process. The first retention scan runs here so a broken
// snapshot tree is visible at startup, but its failure is kept as a transient
// error: the next tool call retries instead of failing for the whole session.
func configuredSnapshotter() native.Snapshotter {
	settings, err := appconfig.ReadSnapshotSettings()
	if err != nil {
		return &policySnapshotter{onError: appconfig.SnapshotOnErrorDeny, unavailable: err}
	}
	if !settings.Enabled {
		return nil
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return &policySnapshotter{onError: settings.OnError, unavailable: err}
	}
	policy := &policySnapshotter{onError: settings.OnError, settings: settings, sessionsDir: sessionsDir}
	_, _ = policy.ensureStore()
	return policy
}
