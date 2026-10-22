package wiring

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

func TestNativeEnvSnapshotConfiguration(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	enabled := nativeEnvWithSnapshot(t.TempDir(), nil, configuredSnapshotter())
	assert.NotNil(t, enabled.Snapshots)

	assert.NoErr(t, gkconfig.Set("tools.snapshots.enabled", false))
	disabled := nativeEnvWithSnapshot(t.TempDir(), nil, configuredSnapshotter())
	assert.Nil(t, disabled.Snapshots)
}

func TestSnapshotSettingsUseConfiguredLimits(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	assert.NoErr(t, gkconfig.Set("tools.snapshots", map[string]any{
		"enabled":           true,
		"max_file_bytes":    123,
		"max_session_bytes": 456,
		"max_global_bytes":  789,
		"keep_turns":        2,
		"keep_days":         3,
	}))

	policy, ok := nativeEnvWithSnapshot(t.TempDir(), nil, configuredSnapshotter()).Snapshots.(*policySnapshotter)
	assert.True(t, ok)
	assert.Eq(t, int64(123), policy.store.Limits().MaxFileBytes)
	assert.Eq(t, int64(456), policy.store.Limits().MaxSessionBytes)
	assert.Eq(t, int64(789), policy.store.Limits().MaxGlobalBytes)
	assert.Eq(t, 2, policy.store.Limits().KeepTurns)
	assert.Eq(t, 3, policy.store.Limits().KeepDays)
}

func TestSnapshotContinueMakesUnavailableWriteExplicit(t *testing.T) {
	policy := &policySnapshotter{onError: "continue", unavailable: errors.New("directory is not writable")}
	ctx := api.WithSessionID(context.Background(), "session")
	before, err := policy.BeforeCall(ctx, native.SnapshotRequest{CallID: "call"})
	assert.NoErr(t, err)
	assert.Eq(t, snapshotUnrecoverableError, before.Unrecoverable)
	assert.StrContains(t, before.Notice, "不可撤销")

	policy.onError = "deny"
	_, err = policy.BeforeCall(ctx, native.SnapshotRequest{CallID: "call"})
	assert.ErrMsgContains(t, err, "not writable")
}

// TestSnapshotCleanupFailureIsRetriedOnTheNextCall covers the 2026-10-22
// incident: a snapshot tree that fails to scan once (a directory removed by a
// concurrent cleanup here) used to disable snapshots for the rest of the
// session. The failure now only degrades the calls that hit it.
func TestSnapshotCleanupFailureIsRetriedOnTheNextCall(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	assert.NoErr(t, gkconfig.Set("tools.snapshots.enabled", true))
	sessionsDir, err := appconfig.ResolveSessionsDir()
	assert.Require(t, assert.NoErr(t, err))
	// A session directory whose name is not a valid id makes CleanupAll fail.
	assert.NoErr(t, os.MkdirAll(filepath.Join(sessionsDir, "bad name"), 0o700))

	policy, ok := configuredSnapshotter().(*policySnapshotter)
	assert.Require(t, assert.True(t, ok))
	ctx := api.WithSessionID(context.Background(), "session-retry")
	_, err = policy.BeforeCall(ctx, native.SnapshotRequest{CallID: "call"})
	assert.ErrMsgContains(t, err, "snapshot cleanup")
	assert.NotNil(t, policy.lastErr)

	// Repairing the tree is enough: the next call retries the scan instead of
	// replaying the failure for the whole session.
	assert.NoErr(t, os.Remove(filepath.Join(sessionsDir, "bad name")))
	store, err := policy.ensureStore()
	assert.Require(t, assert.NoErr(t, err))
	assert.NotNil(t, store)
	assert.True(t, policy.ready)
	assert.Nil(t, policy.lastErr)
	// The call reaches the store again (which rejects the turnless context).
	_, err = policy.BeforeCall(ctx, native.SnapshotRequest{CallID: "call"})
	assert.ErrMsgContains(t, err, "snapshots: invalid session id")
}

// TestSnapshotPermanentFailureClassification keeps a permanently unusable store
// (a directory this process may not write) from being retried on every call.
func TestSnapshotPermanentFailureClassification(t *testing.T) {
	assert.True(t, snapshotPermanentFailure(fmt.Errorf("snapshot cleanup: %w", fs.ErrPermission)))
	assert.False(t, snapshotPermanentFailure(fmt.Errorf("snapshot cleanup: %w", fs.ErrNotExist)))
}
