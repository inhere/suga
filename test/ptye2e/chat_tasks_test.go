//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChatTasks ports scripts/ptysmoke/chat_tasks.py: background tasks in chat
// and in `suga run`.
//
// chat: the model starts `sleep 2; echo TASK_DONE` as a background task and
// answers at once. Python check -> Go assertion:
//
//	❯ prompt / task started / turn ends while the task runs   expect, TASK_DONE only as the shown command
//	idle chat woken by the task end / notice: exit and output / notice turn ends
//	exit: resume command / session id                         ctrlCTwice
//	trace: shell_exec carries task_id / task event / the notice turn is injected
//
// run: `sleep 30` in the background (a 2s sleep stays within the same shell
// syntax on sh and PowerShell, which both have `sleep`).
//
//	run: exit 0 / task killed at exit, named on stderr / did not wait for the 30s task
//	run: reminded once (two turns) / task event killed
func TestChatTasks(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "tool", Background: true, Delay: 200 * time.Millisecond, Command: "sleep 2; echo TASK_DONE"}
	server := httptest.NewServer(fake)
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, ToolsExtra: "  approval: auto\n"})

	m := s.Mark()
	until(t, s, "❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("后台跑一下")
	until(t, s, "task started", `background task t1 started`, m)
	// TASK_DONE alone is the task's output; "echo TASK_DONE" is the command shown.
	ended := s.Expect(`tokens · `, m, 15*time.Second)
	check(t, "turn ends while the task runs", ended && s.Count(`TASK_DONE`, m) == s.Count(`echo TASK_DONE`, m))
	m = s.Mark()
	until(t, s, "idle chat woken by the task end", `\[background task notice\]`, m)
	check(t, "notice: exit and output", s.Expect(`t1 exited \(exit 0\)`, m, 15*time.Second) && s.Expect(`TASK_DONE`, m, 15*time.Second))
	until(t, s, "notice turn ends", `tokens · `, m)

	session := ctrlCTwice(t, s)
	check(t, "session id", session != "")
	if session != "" {
		events := traceEvents(t, binary, work, env, session)
		shell := attrs(findEvent(events, "tool", "shell_exec"))
		check(t, "trace: shell_exec carries task_id", shell["task_id"] == "t1")
		task := findEvent(events, "task", "")
		lines, _ := attrs(task)["lines"].(float64)
		check(t, "trace: task event", task != nil && task["name"] == "t1" && task["status"] == "ok" && lines >= 1)
		var turns []map[string]any
		for _, e := range events {
			if e["kind"] == "turn" {
				turns = append(turns, e)
			}
		}
		check(t, "trace: the notice turn is injected", len(turns) >= 2 && attrs(turns[len(turns)-1])["injected"] == true)
	}

	// run: the same fake model starts `sleep 30` in the background.
	fake.SetCommand("sleep 30")
	start := time.Now()
	_, stderr, code := sugaRun(t, binary, work, env, "run", "--allow-write", "--prompt", "后台跑")
	took := time.Since(start)
	check(t, "run: exit 0", code == 0)
	check(t, "run: task killed at exit, named on stderr", strings.Contains(stderr, "killed background task at exit: t1 killed"))
	check(t, "run: did not wait for the 30s task", took < 20*time.Second)
	// sessions lists the current directory's sessions by default: ask from the
	// directory the run worked in.
	listing, _, _ := sugaRun(t, binary, work, env, "session", "list", "--json", "-n", "1")
	id := ""
	if m := sessionJSONID.FindStringSubmatch(listing); m != nil {
		id = m[1]
	}
	if !check2(t, "run: session listed", id != "") {
		return
	}
	events := traceEvents(t, binary, work, env, id)
	turns := 0
	for _, e := range events {
		if e["kind"] == "turn" {
			turns++
		}
	}
	check(t, "run: reminded once (two turns)", turns == 2)
	check(t, "run: task event killed", findEvent(events, "task", "")["status"] == "killed")
}
