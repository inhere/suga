package native

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// forkAgents is a host that can start forks: the seed it returns goes back to
// the sub-agent run in AgentRequest.Seed.
type forkAgents struct {
	*fakeAgents
	seed    any
	seedErr error
	smu     sync.Mutex
	seeds   []AgentRequest
}

func (f *forkAgents) ForkSeed(_ context.Context, req AgentRequest) (any, error) {
	f.smu.Lock()
	f.seeds = append(f.seeds, req)
	f.smu.Unlock()
	if f.seedErr != nil {
		return nil, f.seedErr
	}
	return f.seed, nil
}

// seedRequests returns the seed requests so far.
func (f *forkAgents) seedRequests() []AgentRequest {
	f.smu.Lock()
	defer f.smu.Unlock()
	return append([]AgentRequest(nil), f.seeds...)
}

// forkRefused is a seed error with a translated hint, as the host returns one.
type forkRefused struct{}

func (forkRefused) Error() string { return "fork unavailable: context" }
func (forkRefused) Hint() string  { return "the session context is near its limit" }

// forkBody is a report of exactly total runes laid out in lines (the newlines
// are counted too). It uses a multibyte character, so a byte count would pass
// where a rune count does not.
func forkBody(total, lines int) string {
	content := total - (lines - 1)
	per, rest := content/lines, content%lines
	out := make([]string, lines)
	for i := range out {
		out[i] = strings.Repeat("注", per)
	}
	out[lines-1] += strings.Repeat("注", rest)
	return strings.Join(out, "\n")
}

func TestAgentToolFork(t *testing.T) {
	run := func(tool *AgentTool, sid string, args map[string]any) (*api.ToolResult, error) {
		return tool.Execute(api.WithSessionID(context.Background(), sid), api.ToolCall{CallID: "c7", Name: "agent", Arguments: args})
	}
	// forkArgs is the model's call; background is deliberately absent: a fork
	// always runs as a task.
	forkArgs := func(desc string) map[string]any {
		return map[string]any{"description": desc, "prompt": "do " + desc, "type": "fork"}
	}
	ctx := func(sid string) context.Context { return api.WithSessionID(context.Background(), sid) }

	t.Run("a fork is an agent task with its own label", func(t *testing.T) {
		m := newAgentTaskManager(t, tasks.Limits{})
		agents := &forkAgents{fakeAgents: &fakeAgents{rep: AgentReport{SessionID: "sess_f", Report: "the answer", Stop: "final", Steps: 2}},
			seed: "seed-1"}
		tool := NewAgentTool(agents)
		tool.SetTasks(m)

		props := tool.Schema().InputSchema["properties"].(map[string]any)
		assert.Eq(t, []any{"explore", "research", "fork"}, props["type"].(map[string]any)["enum"])
		assert.StrContains(t, props["type"].(map[string]any)["description"].(string), "fork: carries this whole conversation's context")

		res, err := run(tool, "s1", forkArgs("survey the api"))
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, res.IsError)
		assert.StrContains(t, res.Content, "[background fork task t1 started: survey the api]")
		assert.Eq(t, "t1", res.Metadata["task_id"])
		assert.Eq(t, AgentFork, res.Metadata["agent_type"])

		assert.Require(t, assert.Len(t, agents.seedRequests(), 1))
		info, how, err := m.Wait(ctx("s1"), "s1", "t1", 5*time.Second)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, tasks.WaitEnded, how)
		// The kind and summary are those of any background sub-agent; only the
		// label tells a fork apart (F4).
		assert.Eq(t, "agent", info.Kind)
		assert.Eq(t, "agent: survey the api", info.Summary)
		assert.Eq(t, AgentFork, info.Labels["agent_type"])
		assert.Eq(t, "final", info.Labels["stop"])
		assert.Eq(t, "sess_f", info.Labels["child_session_id"])
		// The seed the host returned reached the run.
		assert.Require(t, assert.Len(t, agents.requests(), 1))
		got := agents.requests()[0]
		assert.Eq(t, AgentFork, got.Type)
		assert.True(t, got.Background)
		assert.Eq(t, "seed-1", got.Seed)
	})

	t.Run("the notice carries the report whole up to 4000 runes", func(t *testing.T) {
		cases := []struct {
			name  string
			body  string
			runes int
			whole bool
		}{
			{"3999 runes", forkBody(3999, 1), 3999, true},
			{"4000 runes", forkBody(4000, 1), 4000, true},
			{"4001 runes", forkBody(4001, 13), 4001, false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				assert.Eq(t, c.runes, utf8.RuneCountInString(strings.TrimSpace(c.body)))
				m := newAgentTaskManager(t, tasks.Limits{})
				agents := &forkAgents{fakeAgents: &fakeAgents{rep: AgentReport{SessionID: "sess_f", Report: c.body, Stop: "final"}},
					seed: "seed"}
				tool := NewAgentTool(agents)
				tool.SetTasks(m)
				res, err := run(tool, "s1", forkArgs("survey"))
				assert.Require(t, assert.NoErr(t, err))
				assert.False(t, res.IsError)

				info, _, err := m.Wait(ctx("s1"), "s1", "t1", 5*time.Second)
				assert.Require(t, assert.NoErr(t, err))
				assert.StrContains(t, info.Notice, `agent "survey" finished (stop=final):`)
				assert.StrContains(t, info.Notice, "[sub-agent sess_f") // the footer stays outside the count
				if c.whole {
					assert.StrContains(t, info.Notice, strings.TrimSpace(c.body))
					assert.NotContains(t, info.Notice, "[report cut")
				} else {
					// The first 12 lines, then the cut note: the last line is
					// only in the task output.
					assert.StrContains(t, info.Notice, strings.Repeat("注", 306))
					assert.NotContains(t, info.Notice, strings.Repeat("注", 317))
					assert.StrContains(t, info.Notice, "[report cut; task output reads it all]")
					lines, _, _, err := tasks.ReadLines(info.Path, 1, 100)
					assert.Require(t, assert.NoErr(t, err))
					assert.StrContains(t, strings.Join(lines, "\n"), strings.Repeat("注", 317))
				}
			})
		}
	})

	// D4 is for forks only: a plain background agent keeps its first lines.
	t.Run("a plain agent keeps its 12 lines", func(t *testing.T) {
		m := newAgentTaskManager(t, tasks.Limits{})
		body := forkBody(200, 13) // short, but more lines than a notice shows
		tool := NewAgentTool(&fakeAgents{rep: AgentReport{SessionID: "sess_p", Report: body, Stop: "final"}})
		tool.SetTasks(m)
		res, err := run(tool, "s1", map[string]any{"description": "plain", "prompt": "plain", "background": true})
		assert.Require(t, assert.NoErr(t, err))
		assert.NotContains(t, res.Content, "fork")
		info, _, err := m.Wait(ctx("s1"), "s1", "t1", 5*time.Second)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, info.Notice, strings.Repeat("注", 14))
		assert.NotContains(t, info.Notice, strings.Repeat("注", 20)) // the 13th line
		assert.StrContains(t, info.Notice, "[report cut; task output reads it all]")
		assert.Eq(t, "", info.Labels["agent_type"])
	})

	t.Run("a full manager refuses the fork", func(t *testing.T) {
		m := newAgentTaskManager(t, tasks.Limits{MaxRunning: 1})
		agents := &forkAgents{fakeAgents: &fakeAgents{block: true}, seed: "seed"}
		tool := NewAgentTool(agents)
		tool.SetTasks(m)

		first, err := tool.StartFork(ctx("s1"), AgentRequest{Description: "first", Prompt: "first"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "t1", first.ID)
		assert.Eq(t, "agent: first", first.Summary)
		waitAgentRunning(t, agents.fakeAgents, m)

		_, err = tool.StartFork(ctx("s1"), AgentRequest{Description: "second", Prompt: "second"})
		var limit *ForkLimitError
		assert.Require(t, assert.True(t, errors.As(err, &limit)))
		assert.Eq(t, ForkLimitError{Running: 1, Limit: 1}, *limit)
		assert.Eq(t, "agent: 1 background tasks are running (limit 1); task wait or task kill one before starting a fork",
			limit.Error())
		// Refused before a seed was built: the snapshot is the expensive part.
		assert.Len(t, agents.seedRequests(), 1)

		// Through the tool it is a tool error result: the turn goes on.
		res, err := run(tool, "s1", forkArgs("third"))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.IsError)
		assert.Eq(t, limit.Error(), res.Content)
		assert.Len(t, agents.seedRequests(), 1)
	})

	// Kill follows suga-6gcy (no notice): a fork leaves nothing to read.
	t.Run("a killed fork leaves no notice", func(t *testing.T) {
		m := newAgentTaskManager(t, tasks.Limits{})
		agents := &forkAgents{fakeAgents: &fakeAgents{block: true}, seed: "seed"}
		tool := NewAgentTool(agents)
		tool.SetTasks(m)
		res, err := run(tool, "s1", forkArgs("slow survey"))
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "[background fork task t1 started: slow survey]")
		waitAgentRunning(t, agents.fakeAgents, m)

		info, err := m.Kill("s1", "t1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, tasks.Killed, info.Status)
		assert.Eq(t, "cancelled", info.Labels["stop"])
		assert.Eq(t, AgentFork, info.Labels["agent_type"])
		assert.Empty(t, m.TakeNotices("s1"))
		lines, _, _, err := tasks.ReadLines(info.Path, 1, 100)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, strings.Join(lines, "\n"), "已终止")
		assert.NotContains(t, strings.Join(lines, "\n"), "sub-agent failed")
	})

	t.Run("fork needs tasks and a seeder", func(t *testing.T) {
		// No task manager: fork is not offered and the call is refused.
		bare := NewAgentTool(&fakeAgents{})
		props := bare.Schema().InputSchema["properties"].(map[string]any)
		assert.Eq(t, []any{"explore", "research"}, props["type"].(map[string]any)["enum"])
		res, err := run(bare, "s1", forkArgs("x"))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.IsError)
		assert.Eq(t, "agent: fork needs background tasks", res.Content)

		// A manager, but a host that does not implement ForkSeeder: SubAgents
		// is unchanged, so the fork reports itself as unavailable.
		m := newAgentTaskManager(t, tasks.Limits{})
		plain := NewAgentTool(&fakeAgents{rep: AgentReport{SessionID: "sess_p", Report: "x", Stop: "final"}})
		plain.SetTasks(m)
		props = plain.Schema().InputSchema["properties"].(map[string]any)
		assert.Eq(t, []any{"explore", "research", "fork"}, props["type"].(map[string]any)["enum"])
		res, err = run(plain, "s1", forkArgs("x"))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.IsError)
		assert.Eq(t, "agent: fork is not available", res.Content)
		assert.Empty(t, m.List("s1"))
	})

	t.Run("a refused seed is a tool error with its hint", func(t *testing.T) {
		m := newAgentTaskManager(t, tasks.Limits{})
		tool := NewAgentTool(&forkAgents{fakeAgents: &fakeAgents{}, seedErr: forkRefused{}})
		tool.SetTasks(m)
		res, err := run(tool, "s1", forkArgs("x"))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.IsError)
		assert.Eq(t, "agent: fork unavailable: context\nthe session context is near its limit", res.Content)
		assert.Empty(t, m.List("s1"))
	})
}
