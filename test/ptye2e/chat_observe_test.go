//go:build ptye2e

package ptye2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

const (
	hookContext   = "SMOKE hook context: answer briefly."
	hookHelperEnv = "PTYE2E_HOOK_HELPER"
	waitBeforeYes = 1200 * time.Millisecond // at the question, so approval_wait_ms is visible
)

// TestHookHelper is not a test: the chat's hook command re-runs this test binary
// with -test.run=^TestHookHelper$ so the hook is portable (the Python script used
// a python hook). It answers UserPromptSubmit with additional context, as the
// script's hook did, and exits before the test framework prints its summary.
func TestHookHelper(t *testing.T) {
	if os.Getenv(hookHelperEnv) != "1" {
		return
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	var event map[string]any
	_ = json.Unmarshal([]byte(line), &event)
	for _, key := range []string{"hook_event_name", "name", "Name"} {
		if event[key] == "UserPromptSubmit" {
			out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"additionalContext": hookContext}})
			fmt.Println(string(out))
			break
		}
	}
	os.Exit(0)
}

// TestChatObserve ports scripts/ptysmoke/chat_observe.py: chat's hook runs and a
// person's approval reach the trace. One shell_exec waits at the approval
// question, y answers it, and `inspect trace --json` shows the hook events (with
// the injected chars), the shell_exec event with approval=user and its wait time,
// and the context event counting the hook context.
func TestChatObserve(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "tool", Delay: 500 * time.Millisecond})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal([]string{self, "-test.run=^TestHookHelper$"})
	s, env := startChatOpts(t, binary, chatOpts{
		Home: home, Work: work, URL: server.URL,
		Extra:  fmt.Sprintf("hooks:\n  command: %s\n", command),
		SetEnv: []string{"SUGA_HOOKS=on", hookHelperEnv + "=1"},
	})

	mark := s.Mark()
	expect(t, s, `❯ `, mark)
	mark = s.Mark()
	s.SendLine("跑一下")
	expect(t, s, `允许执行 shell_exec`, mark)
	time.Sleep(waitBeforeYes)
	s.SendLine("y")
	expect(t, s, `⎿ ok: SMOKE_OK`, mark)
	expect(t, s, `tokens · `, mark)
	mark = s.Mark()
	time.Sleep(300 * time.Millisecond)
	s.SendCtrlC()
	time.Sleep(300 * time.Millisecond)
	s.SendCtrlC()
	expect(t, s, `续接：suga chat --resume (?:sess_|sg_|s_)`, mark)
	id := resumeID.FindStringSubmatch(s.Since(mark))
	waitExit(t, s)
	check(t, "session id", id != nil)
	if id == nil {
		return
	}

	events := traceEvents(t, binary, work, env, id[1])
	hooks := map[string]map[string]any{}
	for _, e := range events {
		if e["kind"] == "hook" {
			name, _ := e["name"].(string)
			if hooks[name] == nil {
				hooks[name] = e
			}
		}
	}
	check(t, "trace: SessionStart hook", hooks["SessionStart"] != nil)
	chars, _ := attrs(hooks["UserPromptSubmit"])["additional_chars"].(float64)
	check(t, "trace: UserPromptSubmit hook with injected chars", int(chars) == len(hookContext))
	check(t, "trace: Stop hook", hooks["Stop"] != nil)
	shell := attrs(findEvent(events, "tool", "shell_exec"))
	waitMs, _ := shell["approval_wait_ms"].(float64)
	check(t, "trace: approval=user", shell["approval"] == "user")
	check(t, "trace: approval wait recorded", waitMs >= float64(waitBeforeYes.Milliseconds())*0.8)
	hookCtx, _ := attrs(findEvent(events, "context", ""))["hook_context"].(float64)
	check(t, "trace: context counts the hook context", int(hookCtx) >= len(hookContext))
}
