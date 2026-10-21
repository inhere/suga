package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

// fakeNudgeRunner hands out prepared findings per turn and counts the pushed
// turns; it never touches the store.
type fakeNudgeRunner struct {
	findings [][]personal.NudgeFinding
	turns    int
	events   []personal.HostEvent
}

func (f *fakeNudgeRunner) Run(_ context.Context, _ personal.RunRequest) (personal.RunResult, error) {
	f.turns++
	return personal.RunResult{State: personal.RunCompleted}, nil
}

func (f *fakeNudgeRunner) NudgeFindings(_ context.Context, _ string, _ personal.RunResult) []personal.NudgeFinding {
	index := f.turns
	if index >= len(f.findings) {
		return nil
	}
	return f.findings[index]
}

func (f *fakeNudgeRunner) RecordEvent(_ context.Context, _ string, ev personal.HostEvent) {
	f.events = append(f.events, ev)
}

func TestNudgeAfterTurnLimits(t *testing.T) {
	unverified := []personal.NudgeFinding{{Rule: personal.NudgeRuleUnverified, Evidence: "改后没跑"}}
	promise := []personal.NudgeFinding{{Rule: personal.NudgeRulePromise, Evidence: "承诺继续"}}
	t.Run("N2 is pushed twice, then the run ends", func(t *testing.T) {
		f := &fakeNudgeRunner{findings: [][]personal.NudgeFinding{promise, promise, promise, promise}}
		result, err := nudgeAfterTurn(context.Background(), f, "s1", personal.RunResult{State: personal.RunCompleted},
			false, nil, nil, nil, io.Discard)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, personal.RunCompleted, result.State)
		assert.Eq(t, maxNudgePromisePushes, f.turns)
	})
	t.Run("N1 is pushed once", func(t *testing.T) {
		f := &fakeNudgeRunner{findings: [][]personal.NudgeFinding{unverified, unverified, unverified}}
		_, err := nudgeAfterTurn(context.Background(), f, "s1", personal.RunResult{State: personal.RunCompleted},
			false, nil, nil, nil, io.Discard)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, maxNudgeUnverifiedPushes, f.turns)
	})
	t.Run("--verify skips N1 and leaves N2", func(t *testing.T) {
		f := &fakeNudgeRunner{findings: [][]personal.NudgeFinding{
			{unverified[0], promise[0]},
			{promise[0]},
			{promise[0]},
		}}
		_, err := nudgeAfterTurn(context.Background(), f, "s1", personal.RunResult{State: personal.RunCompleted},
			true, nil, nil, nil, io.Discard)
		assert.Require(t, assert.NoErr(t, err))
		// N2 keeps its own limit; N1 is never pushed.
		assert.Eq(t, maxNudgePromisePushes, f.turns)
		for _, event := range f.events {
			assert.Eq(t, personal.NudgeRulePromise, event.Attrs["rule"])
		}
	})
	t.Run("no finding means no turn", func(t *testing.T) {
		f := &fakeNudgeRunner{findings: [][]personal.NudgeFinding{nil}}
		_, err := nudgeAfterTurn(context.Background(), f, "s1", personal.RunResult{State: personal.RunCompleted},
			false, nil, nil, nil, io.Discard)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, f.turns)
	})
	t.Run("a pushed turn that wraps up ends the loop", func(t *testing.T) {
		f := &wrappingNudgeRunner{fakeNudgeRunner: &fakeNudgeRunner{findings: [][]personal.NudgeFinding{promise, promise, promise}}}
		_, err := nudgeAfterTurn(context.Background(), f, "s1", personal.RunResult{State: personal.RunCompleted},
			false, nil, nil, nil, io.Discard)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, f.turns)
	})
}

// wrappingNudgeRunner reports a wrapped-up turn after the first push.
type wrappingNudgeRunner struct{ *fakeNudgeRunner }

func (w *wrappingNudgeRunner) Run(ctx context.Context, req personal.RunRequest) (personal.RunResult, error) {
	result, err := w.fakeNudgeRunner.Run(ctx, req)
	result.WrapUp = "max_steps"
	return result, err
}

// nudgeEvents returns the session's "nudge" trace events.
func nudgeEvents(t *testing.T, sessionID string) []store.Event {
	t.Helper()
	repo, err := openCoreStore()
	assert.Require(t, assert.NoErr(t, err))
	defer repo.Close()
	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: sessionID, Kinds: []string{store.EventNudge}})
	assert.Require(t, assert.NoErr(t, err))
	return events
}

// nudgeServer answers every turn with answer(index).
func nudgeServer(t *testing.T, answer func(index int) string) (*httptest.Server, *int) {
	t.Helper()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, jsonBody(answer(calls)))
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

// jsonBody is a plain-text completion of content.
func jsonBody(content string) string {
	quoted, err := json.Marshal(content)
	if err != nil {
		return `{}`
	}
	return `{"choices":[{"message":{"content":` + string(quoted) +
		`},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
}

func TestRunNudgeEndToEnd(t *testing.T) {
	t.Run("a promised continuation is pushed back once", func(t *testing.T) {
		verifyRunDir(t)
		server, calls := nudgeServer(t, func(index int) string {
			if index == 1 {
				return "文件已改好。接下来我去跑测试。"
			}
			return "测试已通过，全部完成。"
		})
		setTestProvider(t, server.URL)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "do it", "--output-format", "stream-json", "--session-id", "nudge-e2e",
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, *calls)

		var nudges []map[string]any
		for _, line := range streamLines(t, out.Bytes()) {
			if line["type"] == "nudge" {
				nudges = append(nudges, line)
			}
		}
		assert.Require(t, assert.Len(t, nudges, 1))
		assert.Eq(t, "N2", nudges[0]["rule"])
		assert.Eq(t, "pushed", nudges[0]["action"])
		assert.StrContains(t, nudges[0]["evidence"].(string), "结尾说")
		assert.Eq(t, "result", lastStreamLine(t, out.Bytes())["type"])

		events := nudgeEvents(t, "nudge-e2e")
		assert.Require(t, assert.Len(t, events, 1))
		assert.Eq(t, "N2", events[0].Attrs["rule"])
		assert.Eq(t, "pushed", events[0].Attrs["action"])
	})
	t.Run("N2 is pushed twice at most", func(t *testing.T) {
		verifyRunDir(t)
		server, calls := nudgeServer(t, func(int) string { return "接下来我去跑测试。" })
		setTestProvider(t, server.URL)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "do it", "--output-format", "stream-json", "--session-id", "nudge-limit",
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, maxNudgePromisePushes+1, *calls)
		var nudges int
		for _, line := range streamLines(t, out.Bytes()) {
			if line["type"] == "nudge" {
				nudges++
			}
		}
		assert.Eq(t, maxNudgePromisePushes, nudges)
	})
	t.Run("agents.nudge off pushes nothing", func(t *testing.T) {
		verifyRunDir(t)
		assert.NoErr(t, gkconfig.Set("agents.nudge", "off"))
		server, calls := nudgeServer(t, func(int) string { return "接下来我去跑测试。" })
		setTestProvider(t, server.URL)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "do it", "--output-format", "stream-json", "--session-id", "nudge-off",
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, *calls)
		assert.Len(t, nudgeEvents(t, "nudge-off"), 0)
	})
}
