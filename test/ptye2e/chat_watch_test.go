//go:build ptye2e

package ptye2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The watch/Stop-hook scenario runs its Stop hook as this test binary itself, the
// way TestHookHelper does, so the scenario needs no script.
const (
	watchHookProcessEnv = "PTYE2E_WATCH_HOOK"
	watchHookLogEnv     = "PTYE2E_WATCH_HOOK_LOG"
	// watchContinueReason is the Stop block reason: chat shows it as the task
	// notice that wakes the idle chat and runs the turn it asks for.
	watchContinueReason = "HOOK_CONTINUE please check again"
)

// TestWatchStopHookHelper is not a test: chat runs this test binary as the Stop
// hook, `-test.run=^TestWatchStopHookHelper$`. It appends one JSON line per event
// ({"name": ..., "active": stop_hook_active}) to the log, and on a first Stop
// (stop_hook_active false) sleeps 3s and then asks to continue — the slow block
// the scenario cuts short by typing the next line.
func TestWatchStopHookHelper(t *testing.T) {
	if os.Getenv(watchHookProcessEnv) != "1" {
		return
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	var event map[string]any
	_ = json.Unmarshal([]byte(line), &event)
	name, _ := event["hook_event_name"].(string)
	active, _ := event["stop_hook_active"].(bool)
	if path := os.Getenv(watchHookLogEnv); path != "" {
		entry, _ := json.Marshal(map[string]any{"name": name, "active": active})
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.WriteString(string(entry) + "\n")
			_ = f.Close()
		}
	}
	if name == "Stop" && !active {
		time.Sleep(3 * time.Second)
		out, _ := json.Marshal(map[string]any{"decision": "block", "reason": watchContinueReason})
		fmt.Println(string(out))
	}
	os.Exit(0)
}

// TestChatWatch ports scripts/ptysmoke/chat_watch.py: watch tasks and the
// background Stop hook in chat.
//
// watch: the fake (tool mode, background, watch_paths a.txt) starts a watch on a
// command that fails when a.txt says "bad".
//
//	watch: ❯ prompt                                   until(❯ )
//	watch: started / turn ends                        until(watch task t1 started) / until(tokens ·)
//	watch: idle chat woken by the change              until([background task notice] Background task updates)
//	watch: notice says exit 0 -> 3                    until(t1 (watch) reports: run 2 (files: a.txt): exit 0 -> 3)
//	watch: notice turn ends                           until(tokens ·)
//	watch: the same result leaves no notice           no "reports:" within 7s after writing the same content again
//	watch: session id                                 the resume line's session id is non-empty
//	watch trace: shell_exec carries task_id           attrs.task_id == "t1"
//	watch trace: task event of kind watch             attrs.kind == "watch"
//	watch trace: 3 runs, 1 change                     attrs.runs == 3 and attrs.changes == 1
//
// stop: hooks.stop_background with a Stop hook that sleeps 3s and then asks to
// continue (not when stop_hook_active). A second line is typed right after the
// first turn.
//
//	stop: ❯ prompt / first turn ends                   until(❯ ) / until(tokens ·)
//	stop: second turn ends                            until(tokens ·) after the second line
//	stop: block reply wakes chat as a notice           until(Stop hook asks to continue: HOOK_CONTINUE)
//	stop: the notice turn ends                        until(tokens ·)
//	stop: session id                                  the resume line's session id is non-empty
//	stop: no Interrupt sent to the hook               no Interrupt line in the hook log
//	stop: three Stops, the last one stop_hook_active  len(stops)==3, stops[2].active, !stops[0].active
//	stop trace: first Stop task killed, second ok     t1 killed, t2 ok in the hook task events
func TestChatWatch(t *testing.T) {
	binary := buildSuga(t)
	t.Run("watch", func(t *testing.T) { watchScenario(t, binary) })
	t.Run("stop hook", func(t *testing.T) { stopHookScenario(t, binary) })
}

// watchScenario starts a watch on a.txt, fails it by changing the file and checks
// that the second identical write runs the command again without a notice.
func watchScenario(t *testing.T, binary string) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.txt"), "good\n")
	fake := &fakeLLM{Mode: "tool", Delay: 200 * time.Millisecond, Background: true,
		WatchPaths: []string{"a.txt"},
		Command: osCommand("cat a.txt; if grep -q bad a.txt; then exit 3; fi",
			"Get-Content a.txt; if (Select-String -Path a.txt -Pattern bad -Quiet) { exit 3 }")}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: ws, URL: server.URL,
		ToolsExtra: "  approval: auto\n"})

	m := s.Mark()
	until(t, s, "watch: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("监听一下")
	until(t, s, "watch: started", `watch task t1 started`, m)
	until(t, s, "watch: turn ends", `tokens · `, m)
	// Pass one poll (2s) so the baseline stamps are taken: this waits for the
	// watch's own poll, not for a prompt or a turn end. Writing before it would
	// make the change part of the baseline run.
	settleOutput(s, len(s.RawText()), 2500*time.Millisecond)

	m = s.Mark()
	writeFile(t, filepath.Join(ws, "a.txt"), "bad\n")
	until(t, s, "watch: idle chat woken by the change", `\[background task notice\] Background task updates`, m)
	until(t, s, "watch: notice says exit 0 -> 3", `t1 \(watch\) reports: run 2 \(files: a\.txt\): exit 0 -> 3`, m)
	until(t, s, "watch: notice turn ends", `tokens · `, m)
	settleOutput(s, len(s.RawText()), 1500*time.Millisecond)

	m = s.Mark()
	writeFile(t, filepath.Join(ws, "a.txt"), "bad\n") // the same result: a run, no notice
	// The same result runs the command again but reports nothing: no notice line
	// may show up within one change's window (two polls plus the run).
	check(t, "watch: the same result leaves no notice", !s.Expect(`reports:`, m, 7*time.Second))
	exit := s.Mark()
	exitChat(t, s)
	session := sessionFromText(s.Since(exit))
	if !check2(t, "watch: session id", session != "") {
		return
	}
	// The watch is killed (and its end recorded) after the resume line.
	events := waitTraceEvent(t, binary, ws, env, session, "task")
	check(t, "watch trace: shell_exec carries task_id", str(attrs(findEvent(events, "tool", "shell_exec"))["task_id"]) == "t1")
	task := attrs(findEvent(events, "task", ""))
	runs, _ := task["runs"].(float64)
	changes, _ := task["changes"].(float64)
	check(t, "watch trace: task event of kind watch", str(task["kind"]) == "watch")
	check(t, "watch trace: 3 runs, 1 change", runs == 3 && changes == 1)
}

// stopHookScenario runs chat with a background Stop hook and checks that typing
// does not interrupt it, that the second turn's Stop kills the first, and that the
// block reply wakes chat as a notice whose turn's Stop carries stop_hook_active.
func stopHookScenario(t *testing.T, binary string) {
	home, ws := t.TempDir(), t.TempDir()
	logPath := filepath.Join(home, "hook.log")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal([]string{self, "-test.run=^TestWatchStopHookHelper$"})
	extra := fmt.Sprintf("hooks:\n  command: %s\n  stop_background: true\n  stop_wait_sec: 10\n", command)
	server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 200 * time.Millisecond})
	t.Cleanup(server.Close)
	s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: ws, URL: server.URL, Extra: extra,
		SetEnv: []string{"SUGA_HOOKS=on", watchHookProcessEnv + "=1", watchHookLogEnv + "=" + logPath}})

	m := s.Mark()
	until(t, s, "stop: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("你好")
	until(t, s, "stop: first turn ends", `tokens · `, m)
	settleOutput(s, len(s.RawText()), 500*time.Millisecond) // back at the prompt
	m = s.Mark()
	s.SendLine("第二句") // while the first Stop still runs (it sleeps 3s)
	until(t, s, "stop: second turn ends", `tokens · `, m)
	m = s.Mark()
	until(t, s, "stop: block reply wakes chat as a notice", `Stop hook asks to continue: HOOK_CONTINUE`, m)
	until(t, s, "stop: the notice turn ends", `tokens · `, m)
	settleOutput(s, len(s.RawText()), 1500*time.Millisecond) // its Stop (stop_hook_active) answers at once
	exit := s.Mark()
	exitChat(t, s)
	session := sessionFromText(s.Since(exit))
	if !check2(t, "stop: session id", session != "") {
		return
	}
	log := readHookEntries(t, logPath)
	check(t, "stop: no Interrupt sent to the hook", !hookNameSeen(log, "Interrupt"))
	var stops []hookEntry
	for _, e := range log {
		if e.Name == "Stop" {
			stops = append(stops, e)
		}
	}
	check(t, "stop: three Stops, the last one stop_hook_active",
		len(stops) == 3 && stops[2].Active && !stops[0].Active)
	// Wait for the hook tasks' end records (two Stops as background tasks).
	events := waitTraceTaskCount(t, binary, ws, env, session, "hook", 2)
	status := map[string]string{}
	for _, e := range events {
		if e["kind"] == "task" && str(attrs(e)["kind"]) == "hook" {
			status[str(e["name"])] = str(e["status"])
		}
	}
	check(t, "stop trace: first Stop task killed, second ok", status["t1"] == "killed" && status["t2"] == "ok")
}

// hookEntry is one line of the Stop hook's log.
type hookEntry struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// readHookEntries parses the hook log (empty when the file does not exist).
func readHookEntries(t *testing.T, path string) []hookEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck // read-only
	var out []hookEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e hookEntry
		if json.Unmarshal(scanner.Bytes(), &e) == nil && e.Name != "" {
			out = append(out, e)
		}
	}
	return out
}

// hookNameSeen reports whether the log holds an event of that name.
func hookNameSeen(log []hookEntry, name string) bool {
	for _, e := range log {
		if e.Name == name {
			return true
		}
	}
	return false
}

// waitTraceEvent polls `inspect trace` until an event of the kind is recorded,
// then returns the events: the watch is killed (and its end written) as chat
// exits, so it may arrive just after the resume line.
func waitTraceEvent(t *testing.T, binary, work string, env []string, session, kind string) []map[string]any {
	t.Helper()
	return waitTrace(t, binary, work, env, session, func(events []map[string]any) bool {
		return findEvent(events, kind, "") != nil
	})
}

// waitTraceTaskCount polls `inspect trace` until n task events of the task kind
// are recorded (kind "" for any task event).
func waitTraceTaskCount(t *testing.T, binary, work string, env []string, session, kind string, n int) []map[string]any {
	t.Helper()
	return waitTrace(t, binary, work, env, session, func(events []map[string]any) bool {
		found := 0
		for _, e := range events {
			if e["kind"] == "task" && (kind == "" || str(attrs(e)["kind"]) == kind) {
				found++
			}
		}
		return found >= n
	})
}

// waitTrace polls the session's trace until ok accepts the events, then returns
// the last read (also on a timeout, so the checks that follow report the cause).
func waitTrace(t *testing.T, binary, work string, env []string, session string, ok func([]map[string]any) bool) []map[string]any {
	t.Helper()
	var events []map[string]any
	for i := 0; i < 25; i++ {
		events = traceEvents(t, binary, work, env, session)
		if ok(events) {
			return events
		}
		time.Sleep(200 * time.Millisecond)
	}
	return events
}

// writeFile writes body to path or fails the test.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
