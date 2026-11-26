package native

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// fakeAgents answers every run with rep (and err); it keeps the requests.
// block makes a run wait for its ctx to end (a task that is killed or runs into
// its limit), answering with the signal the cancelled ctx laid down.
type fakeAgents struct {
	rep   AgentReport
	err   error
	block bool
	mu    sync.Mutex
	reqs  []AgentRequest
}

func (f *fakeAgents) Scope(context.Context) (string, string) { return "s1", "turn_1" }
func (f *fakeAgents) RunAgent(ctx context.Context, req AgentRequest) (AgentReport, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return AgentReport{SessionID: "sess_blocked", Stop: "cancelled"}, ctx.Err()
	}
	return f.rep, f.err
}

// requests returns the runs so far (the requests of a background run arrive
// from another goroutine).
func (f *fakeAgents) requests() []AgentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]AgentRequest(nil), f.reqs...)
}

func TestAgentTool(t *testing.T) {
	report := AgentReport{SessionID: "sess_c", Report: "Foo is in a.go:3", Stop: "final", Steps: 4, ToolCalls: 3,
		Usage: api.Usage{InputTokens: 85000, CacheReadTokens: 60000, OutputTokens: 1200}, Duration: 41 * time.Second}
	run := func(tool *AgentTool, args map[string]any) (*api.ToolResult, error) {
		return tool.Execute(api.WithSessionID(context.Background(), "s1"), api.ToolCall{CallID: "c7", Name: "agent", Arguments: args})
	}

	t.Run("read-only, no background without a manager", func(t *testing.T) {
		tool := NewAgentTool(&fakeAgents{})
		assert.True(t, tools.MetaOf(tool).ReadOnly)
		props := tool.Schema().InputSchema["properties"].(map[string]any)
		assert.Nil(t, props["background"])
		assert.NotNil(t, props["type"])
	})
	t.Run("the report and its numbers", func(t *testing.T) {
		agents := &fakeAgents{rep: report}
		res, err := run(NewAgentTool(agents), map[string]any{"description": "find Foo", "prompt": "where is Foo?"})
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, res.IsError)
		assert.StrContains(t, res.Content, "Foo is in a.go:3\n\n[sub-agent sess_c · 4 steps · in 85k (cache 60k) / out 1k · 41s · stop=final]")
		assert.Eq(t, "sess_c", res.Metadata["child_session_id"])
		assert.Eq(t, "explore", res.Metadata["agent_type"])
		assert.Nil(t, res.Metadata["external"])
		assert.Require(t, assert.Len(t, agents.reqs, 1))
		got := agents.reqs[0]
		assert.Eq(t, AgentRequest{Type: "explore", Description: "find Foo", Prompt: "where is Foo?",
			ParentSession: "s1", ParentTurn: "turn_1", CallID: "c7"}, got)
	})
	t.Run("a tainted sub-agent taints the result", func(t *testing.T) {
		rep := report
		rep.Tainted = true
		res, err := run(NewAgentTool(&fakeAgents{rep: rep}), map[string]any{"prompt": "read the docs page", "type": "research"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, true, res.Metadata["external"])
		assert.Eq(t, "research", res.Metadata["agent_type"])
	})
	t.Run("a failure is a tool error with what is known", func(t *testing.T) {
		res, err := run(NewAgentTool(&fakeAgents{rep: AgentReport{SessionID: "sess_x", Stop: "failed"}, err: errors.New("model down")}),
			map[string]any{"prompt": "x"})
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.IsError)
		assert.StrContains(t, res.Content, "sub-agent failed: model down")
		assert.StrContains(t, res.Content, "[sub-agent sess_x")
	})
	t.Run("argument errors", func(t *testing.T) {
		_, err := run(NewAgentTool(&fakeAgents{}), map[string]any{"prompt": "x", "type": "writer"})
		assert.ErrMsgContains(t, err, "type must be explore or research")
		_, err = run(NewAgentTool(&fakeAgents{}), map[string]any{"description": "x"})
		assert.ErrMsgContains(t, err, "prompt is required")
	})
	t.Run("background: a task, the report in its output, a notice with its start", func(t *testing.T) {
		dir := t.TempDir()
		m := tasks.NewManager(tasks.Config{Output: func(sid, id string) (string, string, error) {
			return filepath.Join(dir, id+".txt"), "out:" + sid + ":" + id, nil
		}})
		t.Cleanup(m.Close)
		long := report
		long.Report = strings.Repeat("line\n", 20) + "the end"
		agents := &fakeAgents{rep: long}
		tool := NewAgentTool(agents)
		tool.SetTasks(m)
		assert.NotNil(t, tool.Schema().InputSchema["properties"].(map[string]any)["background"])
		res, err := run(tool, map[string]any{"description": "survey", "prompt": "survey the api", "background": true})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "[background agent task t1 started: survey]")
		ctx := api.WithSessionID(context.Background(), "s1")
		info, how, err := m.Wait(ctx, "s1", "t1", 5*time.Second)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, tasks.WaitEnded, how)
		assert.Eq(t, "agent", info.Kind)
		assert.Eq(t, 4, info.Counters["steps"])
		assert.Eq(t, 85000, info.Counters["input_tokens"])
		assert.Eq(t, "sess_c", info.Labels["child_session_id"])
		assert.Eq(t, "final", info.Labels["stop"])
		assert.True(t, agents.reqs[0].Background)
		lines, _, _, err := tasks.ReadLines(info.Path, 1, 100)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, strings.Join(lines, "\n"), "the end")
		// The wait took the report notice (no end notice besides it).
		assert.StrContains(t, info.Notice, `agent "survey" finished (stop=final):`)
		assert.StrContains(t, info.Notice, "[report cut; task output reads it all]")
		assert.StrContains(t, info.Notice, "[sub-agent sess_c")
		assert.Empty(t, m.TakeNotices("s1"))

		// Unwaited, the notice is what tells the model.
		_, err = run(tool, map[string]any{"description": "second", "prompt": "again", "background": true})
		assert.Require(t, assert.NoErr(t, err))
		var notes []tasks.Info
		for deadline := time.Now().Add(5 * time.Second); len(notes) == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			notes = m.TakeNotices("s1")
		}
		assert.Require(t, assert.Len(t, notes, 1))
		assert.StrContains(t, notes[0].Notice, `agent "second" finished`)
	})

	// A killed task is not a failure: its notice would wake a turn nobody asked
	// for and cut into the line being typed (issue suga-6gcy).
	t.Run("background: a killed agent leaves no failure notice", func(t *testing.T) {
		m := newAgentTaskManager(t, tasks.Limits{})
		agents := &fakeAgents{block: true}
		tool := NewAgentTool(agents)
		tool.SetTasks(m)
		res, err := run(tool, map[string]any{"description": "slow survey", "prompt": "survey", "background": true})
		assert.Require(t, assert.NoErr(t, err))
		taskID, _ := res.Metadata["task_id"].(string)
		assert.Require(t, assert.Eq(t, "t1", taskID))
		waitAgentRunning(t, agents, m)
		info, err := m.Kill("s1", taskID)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, tasks.Killed, info.Status)
		assert.Empty(t, m.TakeNotices("s1"))
		lines, _, _, err := tasks.ReadLines(info.Path, 1, 100)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, strings.Join(lines, "\n"), "已终止")
		assert.NotContains(t, strings.Join(lines, "\n"), "sub-agent failed")
	})

	// The time limit is not a kill, so it keeps the failure and its notice.
	t.Run("background: a timed-out agent still reports its failure", func(t *testing.T) {
		m := newAgentTaskManager(t, tasks.Limits{MaxDuration: 50 * time.Millisecond})
		agents := &fakeAgents{block: true}
		tool := NewAgentTool(agents)
		tool.SetTasks(m)
		_, err := run(tool, map[string]any{"description": "slow survey", "prompt": "survey", "background": true})
		assert.Require(t, assert.NoErr(t, err))
		var notes []tasks.Info
		for deadline := time.Now().Add(5 * time.Second); len(notes) == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			notes = m.TakeNotices("s1")
		}
		assert.Require(t, assert.Len(t, notes, 1))
		assert.StrContains(t, notes[0].Notice, `agent "slow survey" failed: context canceled`)
		settled, err := m.Get("s1", "t1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, tasks.TimedOut, settled.Status)
		lines, _, _, err := tasks.ReadLines(settled.Path, 1, 100)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, strings.Join(lines, "\n"), "sub-agent failed: context canceled")
	})
}

// newAgentTaskManager is a manager of one temp dir's task outputs.
func newAgentTaskManager(t *testing.T, limits tasks.Limits) *tasks.Manager {
	t.Helper()
	dir := t.TempDir()
	m := tasks.NewManager(tasks.Config{Limits: limits, Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, id+".txt"), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(m.Close)
	return m
}

// waitAgentRunning waits until the task's run func reached the sub-agent: the
// kill then ends a task that is really running, not a queued one.
func waitAgentRunning(t *testing.T, agents *fakeAgents, m *tasks.Manager) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		info, err := m.Get("s1", "t1")
		if err == nil && info.Status == tasks.Running && len(agents.requests()) == 1 {
			return
		}
	}
	t.Fatal("the agent task did not start in time")
}
