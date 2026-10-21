package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// fakeVerifyRunner stands in for *personal.Runner in the check loop: each
// injected turn answers what the model would, and after may play the model's
// fix (write a file, for one).
type fakeVerifyRunner struct {
	turns  []personal.RunResult
	errs   []error
	after  func(turn int)
	calls  []personal.RunRequest
	events []personal.HostEvent
}

func (f *fakeVerifyRunner) Run(_ context.Context, req personal.RunRequest) (personal.RunResult, error) {
	turn := len(f.calls)
	f.calls = append(f.calls, req)
	if f.after != nil {
		f.after(turn)
	}
	if turn < len(f.errs) && f.errs[turn] != nil {
		return personal.RunResult{}, f.errs[turn]
	}
	if turn < len(f.turns) {
		return f.turns[turn], nil
	}
	return personal.RunResult{State: personal.RunCompleted, Status: agentcore.RunCompleted}, nil
}

func (f *fakeVerifyRunner) RecordEvent(_ context.Context, _ string, ev personal.HostEvent) {
	f.events = append(f.events, ev)
}

// completedTurn is what a normally finished turn reports.
func completedTurn(answer string) personal.RunResult {
	return personal.RunResult{State: personal.RunCompleted, Status: agentcore.RunCompleted, Output: answer}
}

// checkVerify runs the check loop once with the given command.
func checkVerify(t *testing.T, f *fakeVerifyRunner, workspace string, rounds int, command string) (personal.RunResult, *verifyReport, string, error) {
	t.Helper()
	var diag bytes.Buffer
	set := verifySettings{Command: command, Rounds: rounds, Timeout: 10 * time.Second, Workspace: workspace}
	result, report, err := verifyAfterTurn(context.Background(), f, set, "s1", completedTurn("done"), nil, nil, nil, &diag)
	return result, report, diag.String(), err
}

func TestVerifyAfterTurn(t *testing.T) {
	t.Run("a passing check injects no turn", func(t *testing.T) {
		f := &fakeVerifyRunner{}
		_, report, diag, err := checkVerify(t, f, t.TempDir(), 2, "echo ok")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, true, report.Passed)
		assert.Eq(t, 1, report.Rounds)
		assert.Len(t, f.calls, 0)
		assert.StrContains(t, diag, "verify #1: echo ok → exit 0")
		assert.Require(t, assert.Len(t, f.events, 1))
		assert.Eq(t, store.EventVerify, f.events[0].Kind)
		assert.Eq(t, "ok", f.events[0].Status)
		assert.Eq(t, true, f.events[0].Attrs["passed"])
	})
	t.Run("a failing check comes back as a turn and a fixed one passes", func(t *testing.T) {
		workspace := t.TempDir()
		f := &fakeVerifyRunner{turns: []personal.RunResult{completedTurn("fixed")}}
		// The injected turn is the model fixing the file.
		f.after = func(turn int) {
			if turn == 0 {
				assert.NoErr(t, os.WriteFile(filepath.Join(workspace, "fixed.txt"), []byte("ok"), 0o644))
			}
		}
		_, report, diag, err := checkVerify(t, f, workspace, 2, "cat fixed.txt")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, true, report.Passed)
		assert.Eq(t, 2, report.Rounds)
		assert.Require(t, assert.Len(t, f.calls, 1))
		// The failure text carries the command and the rule about the check itself.
		assert.True(t, f.calls[0].Injected)
		assert.StrContains(t, f.calls[0].Prompt, "[verify failed]")
		assert.StrContains(t, f.calls[0].Prompt, "命令: cat fixed.txt")
		assert.StrContains(t, f.calls[0].Prompt, "退出码: 1")
		assert.StrContains(t, f.calls[0].Prompt, "耗时: ")
		assert.StrContains(t, f.calls[0].Prompt, "不要修改验证命令或删除测试来让它通过")
		assert.StrContains(t, diag, "verify #1: cat fixed.txt → exit 1")
		assert.StrContains(t, diag, "verify #2: cat fixed.txt → exit 0")
		assert.Require(t, assert.Len(t, f.events, 2))
		assert.Eq(t, "error", f.events[0].Status)
		assert.Eq(t, "ok", f.events[1].Status)
		assert.Eq(t, 1, f.events[1].Attrs["round"])
	})
	t.Run("rounds run out with the check still failing", func(t *testing.T) {
		f := &fakeVerifyRunner{after: func(int) {}}
		_, report, _, err := checkVerify(t, f, t.TempDir(), 2, "exit 3")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, false, report.Passed)
		assert.Eq(t, 3, report.Rounds)
		assert.Len(t, f.calls, 2)
		assert.Eq(t, 3, report.Last.ExitCode)
	})
	t.Run("a timeout counts as a failure", func(t *testing.T) {
		f := &fakeVerifyRunner{}
		set := verifySettings{Command: "sleep 5", Rounds: 1, Timeout: time.Second, Workspace: t.TempDir()}
		_, report, err := verifyAfterTurn(context.Background(), f, set, "s1", completedTurn("done"), nil, nil, nil, io.Discard)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, true, report.Last.TimedOut)
		assert.Eq(t, false, report.Passed)
		// One check plus the one round --verify-rounds allows.
		assert.Eq(t, 2, report.Rounds)
	})
	t.Run("a fix round that yields is not checked again", func(t *testing.T) {
		f := &fakeVerifyRunner{turns: []personal.RunResult{{
			State: personal.RunCompleted, Status: agentcore.RunYielded,
		}}}
		_, report, _, err := checkVerify(t, f, t.TempDir(), 3, "exit 1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, false, report.Passed)
		assert.Eq(t, 1, report.Rounds)
		assert.Require(t, assert.Len(t, f.calls, 1))
	})
	t.Run("a fix round that fails ends the loop with its error", func(t *testing.T) {
		boom := fmt.Errorf("provider down")
		f := &fakeVerifyRunner{errs: []error{boom}}
		_, _, _, err := checkVerify(t, f, t.TempDir(), 2, "exit 1")
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "provider down")
	})
	t.Run("a round that ended on a limit gets no fix round", func(t *testing.T) {
		f := &fakeVerifyRunner{}
		set := verifySettings{Command: "exit 1", Rounds: 2, Timeout: 10 * time.Second, Workspace: t.TempDir()}
		limited := completedTurn("cut short")
		limited.WrapUp = "budget"
		_, report, err := verifyAfterTurn(context.Background(), f, set, "s1", limited, nil, nil, nil, io.Discard)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, report.Rounds)
		assert.Len(t, f.calls, 0)
	})
	t.Run("a cancelled check kills the command and ends cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		set := verifySettings{Command: "sleep 5", Rounds: 2, Timeout: 10 * time.Second, Workspace: t.TempDir()}
		f := &fakeVerifyRunner{}
		_, _, err := verifyAfterTurn(ctx, f, set, "s1", completedTurn("done"), nil, nil, nil, io.Discard)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, true, errors.Is(err, context.Canceled))
	})
}

func TestVerifyTail(t *testing.T) {
	t.Run("the last 120 lines are kept", func(t *testing.T) {
		var b strings.Builder
		for i := 1; i <= 300; i++ {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		tail := verifyTail(b.String())
		lines := strings.Split(tail, "\n")
		assert.Eq(t, verifyTailLines, len(lines))
		assert.Eq(t, "line 181", lines[0])
		assert.Eq(t, "line 300", lines[len(lines)-1])
	})
	t.Run("the text stays within 8000 characters", func(t *testing.T) {
		tail := verifyTail(strings.Repeat("x", verifyTailChars*2))
		assert.Eq(t, verifyTailChars, len([]rune(tail)))
	})
	t.Run("secrets are masked and control codes dropped", func(t *testing.T) {
		tail := verifyTail("api_key=abc123\n\x1b[31mred\x1b[0m\n")
		assert.Eq(t, "api_key=[REDACTED]\nred", tail)
	})
	t.Run("a cut never leaves half a masked secret", func(t *testing.T) {
		text := strings.Repeat("y", verifyTailChars) + "api_key=abc123"
		assert.StrNotContains(t, verifyTail(text), "abc123")
	})
}

func TestVerifyFlagValidation(t *testing.T) {
	t.Run("--verify-rounds is capped", func(t *testing.T) {
		for _, value := range []string{"0", "6"} {
			err := RunPersonalCLI(context.Background(), "run", []string{"--prompt", "p", "--verify", "echo ok", "--verify-rounds", value},
				strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			assert.Require(t, assert.Err(t, err))
			assert.StrContains(t, err.Error(), "--verify-rounds must be a whole number between 1 and 5")
		}
	})
	t.Run("chat has no --verify", func(t *testing.T) {
		err := RunPersonalCLI(context.Background(), "chat", []string{"--verify", "echo ok"},
			strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "verify")
	})
}

// verifyRunDir isolates the config and workspace of a --verify end-to-end run
// and returns the workspace the check command runs in.
func verifyRunDir(t *testing.T) string {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", shortTestHome(t))
	workspace := t.TempDir()
	t.Chdir(workspace)
	assert.NoErr(t, gkconfig.Set("workspace", workspace))
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	return workspace
}

// verifyFixServer answers the first turn without fixing anything and fixes the
// file when the verify failure comes back as a turn: what a model that repairs
// its delivery looks like.
func verifyFixServer(t *testing.T, path string) *httptest.Server {
	t.Helper()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		const usage = `"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}`
		if calls == 2 {
			args, err := json.Marshal(map[string]string{"path": path, "content": "fixed\n"})
			assert.NoErr(t, err)
			quoted, err := json.Marshal(string(args))
			assert.NoErr(t, err)
			_, _ = io.WriteString(w, fmt.Sprintf(
				`{"choices":[{"message":{"tool_calls":[{"id":"c1","function":{"name":"file_write","arguments":%s}}]},"finish_reason":"tool_calls"}],%s}`,
				quoted, usage))
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],`+usage+`}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// verifyEvents returns the session's "verify" trace events.
func verifyEvents(t *testing.T, sessionID string) []store.Event {
	t.Helper()
	repo, err := openCoreStore()
	assert.Require(t, assert.NoErr(t, err))
	defer repo.Close()
	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: sessionID, Kinds: []string{store.EventVerify}})
	assert.Require(t, assert.NoErr(t, err))
	return events
}

func TestRunVerifyEndToEnd(t *testing.T) {
	t.Run("the model fixes the delivery on the second round", func(t *testing.T) {
		workspace := verifyRunDir(t)
		path := filepath.Join(workspace, "fixed.txt")
		setTestProvider(t, verifyFixServer(t, path).URL)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "write the file", "--allow-write", "--output-format", "stream-json",
			"--session-id", "verify-e2e", "--verify", "cat fixed.txt",
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))

		var checks []map[string]any
		for _, line := range streamLines(t, out.Bytes()) {
			if line["type"] == "verify" {
				checks = append(checks, line)
			}
		}
		assert.Require(t, assert.Len(t, checks, 2))
		assert.Eq(t, float64(0), checks[0]["round"])
		assert.Eq(t, false, checks[0]["passed"])
		assert.Eq(t, float64(1), checks[1]["round"])
		assert.Eq(t, true, checks[1]["passed"])
		assert.Eq(t, "cat fixed.txt", checks[1]["command"])

		last := lastStreamLine(t, out.Bytes())
		assert.Eq(t, "result", last["type"])
		verify, ok := last["verify"].(map[string]any)
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, true, verify["passed"])
		assert.Eq(t, float64(2), verify["rounds"])
		assert.Len(t, verifyEvents(t, "verify-e2e"), 2)
	})
	t.Run("a check that never passes ends the run with exit code 5", func(t *testing.T) {
		verifyRunDir(t)
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			const usage = `"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}`
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],`+usage+`}`)
		}))
		t.Cleanup(server.Close)
		setTestProvider(t, server.URL)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "write the file", "--output-format", "stream-json", "--verify", "cat missing.txt",
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 5, ExitCode(err))
		assert.StrContains(t, err.Error(), "验证未通过")

		var checks int
		for _, line := range streamLines(t, out.Bytes()) {
			if line["type"] == "verify" {
				checks++
			}
		}
		// The check plus two fix rounds by default.
		assert.Eq(t, 3, checks)
		assert.Eq(t, 3, calls)
		last := lastStreamLine(t, out.Bytes())
		assert.Eq(t, "result", last["type"])
		verify, ok := last["verify"].(map[string]any)
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, false, verify["passed"])
	})
	t.Run("text mode names every check on stderr only", func(t *testing.T) {
		verifyRunDir(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			const usage = `"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}`
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],`+usage+`}`)
		}))
		t.Cleanup(server.Close)
		setTestProvider(t, server.URL)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "write the file", "--verify", "cat missing.txt", "--verify-rounds", "1",
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 5, ExitCode(err))
		assert.StrContains(t, diag.String(), "verify #1: cat missing.txt → exit 1")
		assert.StrContains(t, diag.String(), "verify #2: cat missing.txt → exit 1")
		assert.StrContains(t, diag.String(), "验证未通过")
		// stdout stays the answers only: no check line, no summary.
		assert.StrNotContains(t, out.String(), "verify")
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			assert.Eq(t, "done", line)
		}
	})
	t.Run("a run without --verify keeps its result line unchanged", func(t *testing.T) {
		verifyRunDir(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			const usage = `"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}`
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],`+usage+`}`)
		}))
		t.Cleanup(server.Close)
		setTestProvider(t, server.URL)

		var out, diag bytes.Buffer
		assert.NoErr(t, RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "hi", "--output-format", "stream-json",
		}, strings.NewReader(""), &out, &diag))
		last := lastStreamLine(t, out.Bytes())
		_, has := last["verify"]
		assert.False(t, has)
	})
}

// TestVerifyGate covers which finished turns are checked: the CLI only checks a
// turn that completed normally, so a failed, cancelled or yielded turn keeps its
// own exit semantics.
func TestVerifyGate(t *testing.T) {
	withCommand := personalOptions{Verify: "echo ok"}
	t.Run("a completed turn is checked", func(t *testing.T) {
		assert.True(t, withCommand.needsVerify(completedTurn("done"), nil))
	})
	t.Run("no command means no check", func(t *testing.T) {
		assert.False(t, personalOptions{}.needsVerify(completedTurn("done"), nil))
	})
	t.Run("a failed turn is not checked", func(t *testing.T) {
		assert.False(t, withCommand.needsVerify(personal.RunResult{State: personal.RunFailed}, errors.New("boom")))
		assert.False(t, withCommand.needsVerify(completedTurn("done"), errors.New("boom")))
	})
	t.Run("a yielded or cancelled turn is not checked", func(t *testing.T) {
		assert.False(t, withCommand.needsVerify(personal.RunResult{State: personal.RunInputRequired}, nil))
		assert.False(t, withCommand.needsVerify(personal.RunResult{State: personal.RunCanceled}, nil))
	})
}
