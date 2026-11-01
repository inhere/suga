package personal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// fakeNotices hands out its infos once they are set and keeps the ones put back
// (ReturnNotices), in front of what is left - like tasks.Manager.
type fakeNotices struct {
	mu       sync.Mutex
	infos    []tasks.Info
	returned []tasks.Info
	listed   []tasks.Info // what List returns
}

func (f *fakeNotices) set(infos ...tasks.Info) { f.mu.Lock(); f.infos = infos; f.mu.Unlock() }
func (f *fakeNotices) TakeNotices(string) []tasks.Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.infos
	f.infos = nil
	return out
}

func (f *fakeNotices) ReturnNotices(_ string, infos []tasks.Info) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.returned = append(f.returned, infos...)
	f.infos = append(append([]tasks.Info(nil), infos...), f.infos...)
}

// List reports the tasks the test registered with setTasks (none by default).
func (f *fakeNotices) List(string) []tasks.Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tasks.Info(nil), f.listed...)
}

// setTasks sets what List returns.
func (f *fakeNotices) setTasks(infos ...tasks.Info) { f.mu.Lock(); f.listed = infos; f.mu.Unlock() }

// pending reports what a later TakeNotices would hand out.
func (f *fakeNotices) pending() []tasks.Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tasks.Info(nil), f.infos...)
}

// putBack reports the notices ReturnNotices was called with.
func (f *fakeNotices) putBack() []tasks.Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tasks.Info(nil), f.returned...)
}

// scriptProvider answers a tool call first, then finals; it keeps every
// request's messages.
type scriptProvider struct {
	calls    int
	requests [][]agentcore.Message
}

func (*scriptProvider) Name() string { return "script" }
func (p *scriptProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.requests = append(p.requests, append([]agentcore.Message(nil), req.Messages...))
	p.calls++
	if p.calls == 1 {
		return &agentcore.GenerateResult{FinishReason: agentcore.FinishToolCalls,
			ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "work", Arguments: map[string]any{}}}}, nil
	}
	return &agentcore.GenerateResult{Content: "noted", FinishReason: agentcore.FinishFinal}, nil
}

// indexOf returns the index of the first message containing text, or -1.
func indexOf(msgs []agentcore.Message, text string) int {
	for i, m := range msgs {
		if strings.Contains(m.Content, text) {
			return i
		}
	}
	return -1
}

func TestTaskNoticesReachTheModel(t *testing.T) {
	repo := openObserveRepo(t)
	notices := &fakeNotices{}
	ended := tasks.Info{ID: "t1", Kind: "shell", Summary: "go test ./...", Status: tasks.Exited, ExitCode: 1,
		Started: time.Now().Add(-time.Minute), Ended: time.Now(), Lines: 2, Ref: "out:s:ab", Tail: []string{"--- FAIL: TestX", "FAIL"}}
	registry := tools.NewRegistry()
	registry.Register(fnTool{name: "work", fn: func() (*agentcore.ToolResult, error) {
		notices.set(ended) // the task ends while the tool runs
		return &agentcore.ToolResult{Content: "worked"}, nil
	}})
	provider := &scriptProvider{}
	runner, err := New(Config{Repository: repo, Tools: registry, Provider: provider, Tasks: notices})
	assert.Require(t, assert.NoErr(t, err))

	res, err := runner.Run(context.Background(), RunRequest{Prompt: "go"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, provider.requests, 2))
	assert.Eq(t, -1, indexOf(provider.requests[0], "background task notice"))
	second := provider.requests[1]
	notice := indexOf(second, "[background task notice]")
	assert.Require(t, assert.True(t, notice > 0))
	assert.Eq(t, notice-1, indexOf(second, "worked")) // right after the tool result
	assert.StrContains(t, second[notice].Content, "t1 exited (exit 1) after 1m0s")
	assert.StrContains(t, second[notice].Content, "last lines:\n  --- FAIL: TestX\n  FAIL")

	t.Run("a completed turn keeps its notices", func(t *testing.T) {
		// The notice entry of a completed turn replays in place, so the turn hands
		// nothing back and the next turn takes nothing from the manager.
		assert.Empty(t, notices.putBack())
		assert.Empty(t, notices.pending())
		last := provider.requests[len(provider.requests)-1]
		assert.Eq(t, 1, occurrences(last, "[background task notice]"))
	})
	t.Run("the next turn replays it in place", func(t *testing.T) {
		provider.calls = 1 // straight to the final answer
		_, err := runner.Run(context.Background(), RunRequest{SessionID: res.SessionID, Prompt: "again"})
		assert.Require(t, assert.NoErr(t, err))
		last := provider.requests[len(provider.requests)-1]
		at := indexOf(last, "[background task notice]")
		assert.Require(t, assert.True(t, at > 0))
		assert.Eq(t, at-1, indexOf(last, "worked"))
		assert.True(t, indexOf(last, "noted") > at)
	})
	t.Run("a task end is a trace event", func(t *testing.T) {
		ended.SessionID = res.SessionID
		runner.RecordTaskEnd(ended)
		events := eventsOf(t, repo, res.SessionID, store.EventTask)
		assert.Require(t, assert.Len(t, events, 1))
		assert.Eq(t, "t1", events[0].Name)
		assert.Eq(t, "exited", events[0].Status)
		assert.Eq(t, "exit code 1", events[0].Error)
		assert.Eq(t, "go test ./...", events[0].Attrs["arg"])
		assert.True(t, events[0].DurationMs >= 59000)
	})
	t.Run("a watch end carries its counters and progress", func(t *testing.T) {
		watch := tasks.Info{ID: "t2", SessionID: res.SessionID, Kind: "watch", Status: tasks.Killed, ExitCode: 1,
			Started: time.Now().Add(-time.Minute), Ended: time.Now(), Progress: "run 4, last exit 1, 2 changes",
			Counters: map[string]int{"runs": 4, "changes": 2}}
		runner.RecordTaskEnd(watch)
		failed := tasks.Info{ID: "t3", SessionID: res.SessionID, Kind: "hook", Status: tasks.Failed, ExitCode: -1,
			Started: time.Now(), Ended: time.Now(), Error: "exec: not found"}
		runner.RecordTaskEnd(failed)
		events := eventsOf(t, repo, res.SessionID, store.EventTask)
		assert.Require(t, assert.Len(t, events, 3))
		assert.Eq(t, "watch", events[1].Attrs["kind"])
		assert.Eq(t, float64(4), events[1].Attrs["runs"])
		assert.Eq(t, float64(2), events[1].Attrs["changes"])
		assert.Eq(t, "run 4, last exit 1, 2 changes", events[1].Attrs["progress"])
		assert.Eq(t, "failed", events[2].Status)
		assert.Eq(t, "exec: not found", events[2].Error)
	})
}

// recentRequests never counts the entry injected into a running turn (a task
// notice) as a user request (issue suga-0lxy).
func TestRecentRequestsSkipTaskNotices(t *testing.T) {
	repo := openObserveRepo(t)
	notices := &fakeNotices{}
	registry := tools.NewRegistry()
	registry.Register(fnTool{name: "work", fn: func() (*agentcore.ToolResult, error) {
		notices.set(tasks.Info{ID: "t1", Kind: "shell", Summary: "go test ./...", Status: tasks.Exited,
			ExitCode: 1, Started: time.Now(), Ended: time.Now(), Tail: []string{"FAIL"}})
		return &agentcore.ToolResult{Content: "worked"}, nil
	}})
	runner, err := New(Config{Repository: repo, Tools: registry, Provider: &scriptProvider{}, Tasks: notices})
	assert.Require(t, assert.NoErr(t, err))
	res, err := runner.Run(context.Background(), RunRequest{Prompt: "fix the parser bug"})
	assert.Require(t, assert.NoErr(t, err))

	// The notice of the turn is stored as a user entry ...
	es, err := repo.ListEntries(context.Background(), res.SessionID, store.EntryFilter{Types: []string{store.EntryUser}})
	assert.NoErr(t, err)
	noticed := 0
	for _, e := range es {
		var m agentcore.Message
		assert.NoErr(t, json.Unmarshal(e.JSON, &m))
		if isInjectedItem(m) {
			noticed++
		}
	}
	assert.Eq(t, 1, noticed)
	// ... but it is not one of the turn's user requests.
	got, err := runner.recentRequests(context.Background(), res.SessionID, "", recentRequestCount)
	assert.NoErr(t, err)
	assert.Eq(t, []string{"fix the parser bug"}, got)
}

func TestTaskNoticeText(t *testing.T) {
	ended := tasks.Info{ID: "t1", Kind: "shell", Summary: "go test ./...", Status: tasks.Exited,
		Started: time.Now().Add(-time.Second), Ended: time.Now(), Tail: []string{"ok"}}
	t.Run("ended tasks", func(t *testing.T) {
		text := TaskNoticeText([]tasks.Info{ended})
		assert.StrContains(t, text, "[background task notice] These background tasks ended")
		assert.StrContains(t, text, "- t1 exited (exit 0)")
	})
	t.Run("a running task's report", func(t *testing.T) {
		watch := tasks.Info{ID: "t2", Kind: "watch", Status: tasks.Running, Notice: "run 3 (files: a.go): exit 0 -> 1\nlast lines:\nFAIL"}
		text := TaskNoticeText([]tasks.Info{watch, ended})
		assert.StrContains(t, text, "[background task notice] Background task updates")
		assert.StrContains(t, text, "- t2 (watch) reports: run 3 (files: a.go): exit 0 -> 1\n  last lines:\n  FAIL")
		assert.StrContains(t, text, "- t1 exited (exit 0)")
	})
}

// failProv answers the first model call with a tool call and fails every later
// one: the turn ends failed after it took its task notices.
type failProv struct{ calls int }

func (p *failProv) Name() string { return "fail" }

func (p *failProv) Generate(_ context.Context, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.calls++
	if p.calls == 1 {
		return workCall("c1")
	}
	return nil, errors.New("the model gave up")
}

// noticeInfo is the ended task the notice tests hand out.
func noticeInfo() tasks.Info {
	return tasks.Info{ID: "t1", Kind: "shell", Summary: "go test ./...", Status: tasks.Exited, ExitCode: 1,
		Started: time.Now().Add(-time.Minute), Ended: time.Now(), Tail: []string{"--- FAIL: TestX", "FAIL"}}
}

// A turn that ends cancelled after it took a task notice puts it back into the
// manager's queue: the interrupted turn's replay skips its notices
// (replayablePrefix), so the next turn has to take the notice again - and only
// the next turn delivers it to the model (issue suga-o26e).
func TestTaskNoticesGoBackOnACancelledTurn(t *testing.T) {
	repo := openObserveRepo(t)
	const sid = "s-notice-cancel"
	ended := noticeInfo()
	notices := &fakeNotices{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The turn is cancelled right before its second model request goes out, i.e.
	// after that step's context build took the notice the tool left behind.
	abort := &abortOnModel{at: 2, cancel: cancel}
	runner := compactRunner(t, repo, &abortProv{}, workRegistry(func() { notices.set(ended) }), func(c *Config) {
		c.Tasks = notices
		c.Hooks = []agentcore.Hook{abort}
	})
	_, err := runner.NewSession(context.Background(), sid, "notice cancel")
	assert.Require(t, assert.NoErr(t, err))

	res, err := runner.Run(ctx, RunRequest{SessionID: sid, Prompt: "run the job"})
	assert.Require(t, assert.True(t, errors.Is(err, context.Canceled)))
	assert.Eq(t, RunCanceled, res.State)

	t.Run("the notice is back in the manager's queue", func(t *testing.T) {
		assert.Eq(t, []tasks.Info{ended}, notices.putBack())
		assert.Eq(t, []tasks.Info{ended}, notices.pending())
	})

	t.Run("the next turn takes it and sees it once", func(t *testing.T) {
		next := &scriptProvider{}
		next.calls = 1 // straight to the final answer
		nextRunner, err := New(Config{Repository: repo, Provider: next, Tools: tools.NewRegistry(), Tasks: notices})
		assert.Require(t, assert.NoErr(t, err))
		_, err = nextRunner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: "go on"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, next.requests, 1))
		last := next.requests[0]
		// Once: the replay of the cancelled turn drops its notice entry, the
		// notice the manager handed out again is the only one.
		assert.Eq(t, 1, occurrences(last, "[background task notice]"))
		assert.Eq(t, 1, occurrences(last, "t1 exited (exit 1)"))
		assert.Empty(t, notices.pending()) // taken, so not handed out twice
	})
}

// A turn that ends failed puts its task notices back the same way.
func TestTaskNoticesGoBackOnAFailedTurn(t *testing.T) {
	repo := openObserveRepo(t)
	const sid = "s-notice-fail"
	ended := noticeInfo()
	notices := &fakeNotices{}
	provider := &failProv{}
	runner := compactRunner(t, repo, provider, workRegistry(func() { notices.set(ended) }), func(c *Config) {
		c.Tasks = notices
	})
	_, err := runner.NewSession(context.Background(), sid, "notice fail")
	assert.Require(t, assert.NoErr(t, err))

	res, err := runner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: "run the job"})
	assert.Require(t, assert.True(t, err != nil))
	assert.Eq(t, RunFailed, res.State)
	assert.Eq(t, []tasks.Info{ended}, notices.putBack())
	assert.Eq(t, []tasks.Info{ended}, notices.pending())

	t.Run("the next turn sees the notice once", func(t *testing.T) {
		next := &scriptProvider{}
		next.calls = 1
		nextRunner, err := New(Config{Repository: repo, Provider: next, Tools: tools.NewRegistry(), Tasks: notices})
		assert.Require(t, assert.NoErr(t, err))
		_, err = nextRunner.Run(context.Background(), RunRequest{SessionID: sid, Prompt: "go on"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, next.requests, 1))
		assert.Eq(t, 1, occurrences(next.requests[0], "[background task notice]"))
		assert.Empty(t, notices.pending())
	})
}
