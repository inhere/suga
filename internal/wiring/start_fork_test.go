package wiring

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// TestStartFork: the runtime exposes the :bg entry only with the agent tool
// (agents.enabled), and its seed is the between-turns one of the session on the
// context - a session without a completed turn has none, which is the refusal
// chat shows.
func TestStartFork(t *testing.T) {
	manifestConfig(t)
	assert.NoErr(t, gkconfig.Set("mcp.servers", nil))
	assert.NoErr(t, gkconfig.Set("agents.enabled", true))
	dir := t.TempDir()
	manager := tasks.NewManager(tasks.Config{Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, id), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(manager.Close)

	rt, err := OpenCLI(context.Background(), Options{Tasks: manager})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	assert.Require(t, assert.NotNil(t, rt.StartFork))

	// A session without a completed turn has no request to carry: the command
	// gets the refusal, not a task.
	sess, err := rt.Runner.NewSession(context.Background(), "", "bg test")
	assert.Require(t, assert.NoErr(t, err))
	_, err = rt.StartFork(api.WithSessionID(context.Background(), sess.ID), "查一下 X")
	var unavailable *personal.ForkUnavailableError
	assert.Require(t, assert.True(t, errors.As(err, &unavailable)))
	assert.NotEmpty(t, unavailable.Reason)
	assert.Empty(t, manager.List(sess.ID))

	// Without the agent tool there is no fork to start: chat's :bg says it is
	// unavailable instead (cmd.bg.disabled).
	assert.NoErr(t, gkconfig.Set("agents.enabled", false))
	off, err := OpenCLI(context.Background(), Options{Tasks: manager})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = off.Close() })
	assert.Nil(t, off.StartFork)
}
