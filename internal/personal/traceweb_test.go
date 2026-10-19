package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// traceRun runs a turn with a reply text, a tool call and a final answer.
func traceRun(t *testing.T) (*store.Store, string) {
	repo := openObserveRepo(t)
	registry := tools.NewRegistry()
	registry.Register(fnTool{name: "file_read", fn: func() (*agentcore.ToolResult, error) {
		return &agentcore.ToolResult{Content: "1\tpackage a"}, nil
	}})
	runner, err := New(Config{Repository: repo, Tools: registry, Provider: mock.NewProvider(
		mock.ToolCall("c1", "file_read", map[string]any{"path": "a.go"}),
		mock.Final("done"),
	)})
	assert.Require(t, assert.NoErr(t, err))
	res, err := runner.Run(context.Background(), RunRequest{Prompt: "read a.go"})
	assert.Require(t, assert.NoErr(t, err))
	return repo, res.SessionID
}

func TestBuildTraceView(t *testing.T) {
	repo, sid := traceRun(t)
	view, err := BuildTraceView(context.Background(), repo, sid)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, sid, view.Session.ID)
	assert.Eq(t, 2, view.Analysis.ModelCalls)
	assert.Eq(t, "read a.go", view.Session.Title)

	t.Run("an old whole-prompt title shows as its task line", func(t *testing.T) {
		sess, err := repo.GetSession(context.Background(), sid)
		assert.Require(t, assert.NoErr(t, err))
		sess.Title = goferPrompt
		assert.NoErr(t, repo.UpdateSession(context.Background(), sess))
		v, err := BuildTraceView(context.Background(), repo, sid)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "修复 sessions 列表的排序", v.Session.Title)
	})

	entry := map[int64]traceEntry{}
	for _, en := range view.Entries {
		entry[en.Seq] = en
	}
	var tool, firstModel *webEvent
	for i := range view.Events {
		e := &view.Events[i]
		if e.Kind == store.EventTool && tool == nil {
			tool = e
		}
		if e.Kind == store.EventModel && firstModel == nil {
			firstModel = e
		}
	}
	assert.Require(t, assert.NotNil(t, tool))
	// the tool event carries its call (arguments) and its result
	var args map[string]any
	var result string
	for _, seq := range tool.Entries {
		if en := entry[seq]; en.Args != nil {
			args = en.Args
		} else if en.Role == "tool" {
			result = en.Content
		}
	}
	assert.Eq(t, map[string]any{"path": "a.go"}, args)
	assert.Eq(t, "1\tpackage a", result)
	// the first model call carries the assistant message that asked for the tool
	assert.Require(t, assert.NotEmpty(t, firstModel.Entries))
	assert.Eq(t, "file_read", entry[firstModel.Entries[0]].Tool)
}

// TestTraceViewTaskNotice: a user entry that carries the task notice metadata
// is marked, so the page shows it as a notice and not as the person's input.
func TestTraceViewTaskNotice(t *testing.T) {
	repo, sid := traceRun(t)
	raw, err := json.Marshal(TaskNoticeMessage([]tasks.Info{{ID: "t1", Kind: "shell", Summary: "go test ./...", Status: tasks.Exited}}))
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(context.Background(), sid, store.NewEntry{Type: store.EntryUser, JSON: raw})
	assert.Require(t, assert.NoErr(t, err))

	view, err := BuildTraceView(context.Background(), repo, sid)
	assert.Require(t, assert.NoErr(t, err))
	notices, inputs := 0, 0
	for _, en := range view.Entries {
		if en.Type != store.EntryUser {
			continue
		}
		if en.Notice {
			notices++
			assert.StrContains(t, en.Content, "[background task notice]")
			continue
		}
		inputs++
		assert.Eq(t, "read a.go", en.Content) // the prompt the turn stored
	}
	assert.Eq(t, 1, notices)
	assert.Eq(t, 1, inputs)
}

// TestTraceViewSteer: a user entry a person typed into a running turn carries
// the steering metadata and is marked, so the page shows it as a 插话 instead
// of plain input (and not as a notice).
func TestTraceViewSteer(t *testing.T) {
	repo, sid := traceRun(t)
	raw, err := json.Marshal(SteerMessage(Steer{ID: "s1", Text: "only change the README", Source: SteerSourceKeyboard}))
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(context.Background(), sid, store.NewEntry{Type: store.EntryUser, JSON: raw})
	assert.Require(t, assert.NoErr(t, err))

	view, err := BuildTraceView(context.Background(), repo, sid)
	assert.Require(t, assert.NoErr(t, err))
	var steers []traceEntry
	for _, en := range view.Entries {
		if en.Steer {
			steers = append(steers, en)
		}
		assert.False(t, en.Steer && en.Notice)
	}
	assert.Require(t, assert.Len(t, steers, 1))
	assert.Eq(t, "user", steers[0].Role)
	assert.Eq(t, "only change the README", steers[0].Content)
}

func TestServeTraceView(t *testing.T) {
	repo, sid := traceRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	urls := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- ServeTraceView(ctx, "127.0.0.1:0", func(ctx context.Context, q TraceQuery) (*TraceView, error) {
			return BuildTraceViewSince(ctx, repo, sid, q)
		}, func(context.Context) (any, error) {
			return map[string]any{"workspace": "/w"}, nil
		}, func(url string) { urls <- url })
	}()
	var url string
	select {
	case url = <-urls:
	case err := <-done:
		t.Fatalf("serve: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no url")
	}
	get := func(path string) (string, string) {
		resp, err := http.Get(url + path)
		assert.Require(t, assert.NoErr(t, err))
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.Header.Get("Content-Type"), string(body)
	}
	ctype, page := get("")
	assert.StrContains(t, ctype, "text/html")
	assert.StrContains(t, page, "<title>suga inspect trace</title>")
	ctype, body := get("api/session")
	assert.StrContains(t, ctype, "application/json")
	var view TraceView
	assert.NoErr(t, json.Unmarshal([]byte(body), &view))
	assert.Eq(t, sid, view.Session.ID)
	ctype, body = get("api/env")
	assert.StrContains(t, ctype, "application/json")
	assert.StrContains(t, body, `"workspace":"/w"`)
	// An incremental request asks for the new part only (since_seq /
	// since_event): what the page already shows does not travel again.
	var lastEvent, lastSeq int64
	for _, e := range view.Events {
		if e.ID > lastEvent {
			lastEvent = e.ID
		}
	}
	for _, en := range view.Entries {
		if en.Seq > lastSeq {
			lastSeq = en.Seq
		}
	}
	ctype, body = get(fmt.Sprintf("api/session?since_seq=%d&since_event=%d", lastSeq, lastEvent))
	assert.StrContains(t, ctype, "application/json")
	var delta TraceView
	assert.NoErr(t, json.Unmarshal([]byte(body), &delta))
	assert.True(t, delta.Delta)
	assert.Empty(t, delta.Events)
	assert.Empty(t, delta.Entries)
	assert.Eq(t, sid, delta.Session.ID)
	cancel()
	select {
	case err := <-done:
		assert.NoErr(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}

	err := ServeTraceView(context.Background(), "0.0.0.0:0", nil, nil, nil)
	assert.ErrMsgContains(t, err, "loopback address only")
}

func TestCapWebText(t *testing.T) {
	assert.Eq(t, "short", capWebText("short", 10))
	got := capWebText(strings.Repeat("中", 10), 7) // cut inside a rune: back to its start
	assert.True(t, strings.HasPrefix(got, "中中\n[... 24 more bytes"))
}

// TestJoinEntriesSameMillisecond: a fast provider puts two model calls of a
// turn and their messages in the same millisecond (Windows CI); each call
// still gets the message it produced, in order.
func TestJoinEntriesSameMillisecond(t *testing.T) {
	at := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	model := func(id int64, status string) webEvent {
		return webEvent{ID: id, TurnID: "u1", Kind: store.EventModel, Status: status, Start: at}
	}
	answer := func(seq int64) traceEntry {
		return traceEntry{Seq: seq, TurnID: "u1", Type: store.EntryAssistant, Time: at}
	}
	t.Run("pairs in order", func(t *testing.T) {
		view := &TraceView{Events: []webEvent{model(1, eventOK), model(2, eventOK)},
			Entries: []traceEntry{answer(10), answer(11)}}
		joinEntries(view)
		assert.Eq(t, []int64{10}, view.Events[0].Entries)
		assert.Eq(t, []int64{11}, view.Events[1].Entries)
	})
	t.Run("a failed call writes no message", func(t *testing.T) {
		view := &TraceView{Events: []webEvent{model(1, "error"), model(2, eventOK)},
			Entries: []traceEntry{answer(10)}}
		joinEntries(view)
		assert.Empty(t, view.Events[0].Entries)
		assert.Eq(t, []int64{10}, view.Events[1].Entries)
	})
	t.Run("counts that differ fall back to times", func(t *testing.T) {
		later := func(e traceEntry, ms int) traceEntry { e.Time = at.Add(time.Duration(ms) * time.Millisecond); return e }
		m2 := model(2, eventOK)
		m2.Start = at.Add(5 * time.Millisecond)
		view := &TraceView{Events: []webEvent{model(1, eventOK), m2},
			Entries: []traceEntry{later(answer(10), 1), later(answer(11), 2), later(answer(12), 6)}}
		joinEntries(view)
		assert.Eq(t, []int64{10, 11}, view.Events[0].Entries)
		assert.Eq(t, []int64{12}, view.Events[1].Entries)
	})
}
