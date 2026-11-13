//go:build ptye2e

package ptye2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	pretoolDenyReason = "SMOKE pre-tool deny"
	pretoolRewritten  = "echo SMOKE_REWRITTEN"
	pretoolHelperEnv  = "PTYE2E_PRETOOL_HELPER"
)

// TestPretoolHookHelper is not a test: the chat's hook command re-runs this test
// binary with -test.run=^TestPretoolHookHelper$ (the Python script used a python
// hook). It answers PreToolUse: deny for a command containing SMOKE_DENY, a
// rewritten command for one containing SMOKE_OK. It exits before the test
// framework prints its summary.
func TestPretoolHookHelper(t *testing.T) {
	if os.Getenv(pretoolHelperEnv) != "1" {
		return
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	var event map[string]any
	_ = json.Unmarshal([]byte(line), &event)
	if name, _ := event["hook_event_name"].(string); name == "PreToolUse" {
		input, _ := event["tool_input"].(map[string]any)
		command, _ := input["command"].(string)
		var out map[string]any
		switch {
		case strings.Contains(command, "SMOKE_DENY"):
			out = map[string]any{"hookSpecificOutput": map[string]any{
				"permissionDecision": "deny", "permissionDecisionReason": pretoolDenyReason}}
		case strings.Contains(command, "SMOKE_OK"):
			input["command"] = pretoolRewritten
			out = map[string]any{"hookSpecificOutput": map[string]any{"updatedInput": input}}
		}
		if out != nil {
			encoded, _ := json.Marshal(out)
			fmt.Println(string(encoded))
		}
	}
	os.Exit(0)
}

// TestChatPretool ports scripts/ptysmoke/chat_pretool.py: the PreToolUse hook
// gates a shell_exec call, first denying it (no approval question, nothing runs)
// and then rewriting its command (the question and the run show the rewritten
// one). `hooks.pre_tool_use.tools: [shell_exec]` keeps the hook to that tool.
func TestChatPretool(t *testing.T) {
	binary := buildSuga(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal([]string{self, "-test.run=^TestPretoolHookHelper$"})
	hooks := fmt.Sprintf("hooks:\n  command: %s\n  pre_tool_use:\n    tools: [shell_exec]\n", command)

	t.Run("deny", func(t *testing.T) {
		fake := &fakeLLM{Mode: "tool", Delay: 500 * time.Millisecond, Command: "echo SMOKE_DENY"}
		server := httptest.NewServer(fake)
		defer server.Close()
		work := t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: work, URL: server.URL,
			Extra: hooks, SetEnv: []string{"SUGA_HOOKS=on", pretoolHelperEnv + "=1"}})

		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		mark = s.Mark()
		s.SendLine("跑一下")
		expect(t, s, pretoolDenyReason, mark)
		check(t, "no approval question", !s.Expect(`允许执行 shell_exec`, mark, 2*time.Second))
		// The command text shows on the call line itself: only a result line counts.
		check(t, "nothing ran", !s.Expect("⎿[^\\n]*SMOKE_DENY", mark, 500*time.Millisecond))
		expect(t, s, `tokens · `, mark)
		id := exitAfterTurn(t, s, mark)
		check(t, "session id", id != "")
		if id == "" {
			return
		}
		events := traceEvents(t, binary, work, env, id)
		checkDenyTrace(t, events)
	})

	t.Run("rewrite", func(t *testing.T) {
		fake := &fakeLLM{Mode: "tool", Delay: 500 * time.Millisecond, Command: "echo SMOKE_OK"}
		server := httptest.NewServer(fake)
		defer server.Close()
		work := t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: work, URL: server.URL,
			Extra: hooks, SetEnv: []string{"SUGA_HOOKS=on", pretoolHelperEnv + "=1"}})

		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		mark = s.Mark()
		s.SendLine("跑一下")
		ok := check2(t, "question shows the rewritten command", s.Expect(`允许执行 shell_exec`, mark, 15*time.Second))
		check(t, "question shows the rewritten command", ok && strings.Contains(s.Since(mark), pretoolRewritten))
		s.SendLine("y")
		expect(t, s, `⎿ ok: SMOKE_REWRITTEN`, mark)
		expect(t, s, `tokens · `, mark)
		id := exitAfterTurn(t, s, mark)
		check(t, "session id", id != "")
		if id == "" {
			return
		}
		events := traceEvents(t, binary, work, env, id)
		checkRewriteTrace(t, events)
	})
}

// checkDenyTrace asserts the deny decision and that the call was recorded
// without a run.
func checkDenyTrace(t *testing.T, events []map[string]any) {
	t.Helper()
	var hooks []map[string]any
	for _, e := range events {
		if e["kind"] == "hook" && e["name"] == "PreToolUse" {
			hooks = append(hooks, e)
		}
	}
	check(t, "trace: PreToolUse hook event", len(hooks) >= 1)
	denied := false
	for _, e := range hooks {
		if attrs(e)["decision"] == "deny" {
			denied = true
		}
	}
	check(t, "trace: decision=deny", denied)
	shell := attrs(findEvent(events, "tool", "shell_exec"))
	check(t, "trace: the shell_exec call is recorded without a run", shell["output"] == nil)
}

// checkRewriteTrace asserts the rewrite and that the person approved it.
func checkRewriteTrace(t *testing.T, events []map[string]any) {
	t.Helper()
	var hooks []map[string]any
	for _, e := range events {
		if e["kind"] == "hook" && e["name"] == "PreToolUse" {
			hooks = append(hooks, e)
		}
	}
	check(t, "trace: PreToolUse hook event", len(hooks) >= 1)
	rewritten := false
	for _, e := range hooks {
		if attrs(e)["rewritten_args"] == true {
			rewritten = true
		}
	}
	check(t, "trace: rewritten_args", rewritten)
	shell := attrs(findEvent(events, "tool", "shell_exec"))
	check(t, "trace: approval=user after the rewrite", shell["approval"] == "user")
}
