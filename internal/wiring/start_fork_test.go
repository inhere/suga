package wiring

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestStartForkThroughAssembly: :bg through the real runtime (the entry chat
// calls) starts a fork after a completed turn - in the process that ran the turn
// (the tracked request is continued) and in a fresh one that only has the
// persisted session (resume, :compact; the view is rebuilt). The fork's first
// request on the wire holds the parent's turn and the directive.
func TestStartForkThroughAssembly(t *testing.T) {
	resetConfig(t)
	t.Setenv("SUGA_HOME", t.TempDir())
	t.Setenv("SUGA_SESSION_ID", "")
	fake := &betweenTurnsServer{}
	server := fake.start(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k", "base_url": server.URL}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	assert.NoErr(t, gkconfig.Set("mcp.servers", nil))
	assert.NoErr(t, gkconfig.Set("agents.enabled", true))
	dir := t.TempDir()
	manager := tasks.NewManager(tasks.Config{Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, id), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(manager.Close)
	open := func() *Runtime {
		rt, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir(), Tasks: manager})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = rt.Close() })
		return rt
	}
	// forkRequest starts a :bg and returns the messages of the fork's own model
	// call: the one request after the ones already seen that holds the task.
	startBg := func(t *testing.T, rt *Runtime, sid, task string) []map[string]any {
		t.Helper()
		before := fake.count()
		info, err := rt.StartFork(api.WithSessionID(context.Background(), sid), task)
		assert.Require(t, assert.NoErr(t, err))
		_, _, err = manager.Wait(api.WithSessionID(context.Background(), sid), sid, info.ID, 15*time.Second)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, before+1, fake.count()))
		return fake.last()
	}
	hasText := func(msgs []map[string]any, text string) bool {
		for _, m := range msgs {
			if c, _ := m["content"].(string); strings.Contains(c, text) {
				return true
			}
		}
		return false
	}

	first := open()
	res, err := first.Runner.Run(context.Background(), personal.RunRequest{Prompt: "修一下解析器"})
	assert.Require(t, assert.NoErr(t, err))

	t.Run("the process that ran the turn continues its request", func(t *testing.T) {
		msgs := startBg(t, first, res.SessionID, "查一下 ALPHA")
		assert.True(t, hasText(msgs, "修一下解析器"))
		assert.True(t, hasText(msgs, "查一下 ALPHA"))
	})
	t.Run("a fresh process rebuilds the persisted view", func(t *testing.T) {
		msgs := startBg(t, open(), res.SessionID, "查一下 BETA")
		assert.True(t, hasText(msgs, "修一下解析器"))
		assert.True(t, hasText(msgs, "查一下 BETA"))
	})
}
