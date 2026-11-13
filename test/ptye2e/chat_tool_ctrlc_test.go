//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// Marker files the long command writes: the scenario waits for the first one (a
// tool's output only reaches chat when the tool ends) and checks the second one
// never appears.
const (
	toolStarted  = "tool-started"
	toolFinished = "tool-finished"
)

// TestChatToolCtrlC ports scripts/ptysmoke/chat_tool_ctrlc.py: what Ctrl-C and Esc
// do while a foreground shell_exec runs depends on chat.steering.
//
//	steering on (the default): the turn keeps the input line open, so Ctrl-C
//	  arrives as the byte 0x03 and a lone Esc as its own sequence. One press ends
//	  this turn only ("已中断本轮", chat stays at the prompt); two presses within
//	  the confirmation window leave chat.
//	steering off: the console stays cooked, Ctrl-C raises SIGINT, the turn is
//	  interrupted and chat exits right away.
//
// The command writes a marker, sleeps and writes a second one; once the first is
// there the scenario interrupts and checks the second never appears - the whole
// process tree died with the turn.
func TestChatToolCtrlC(t *testing.T) {
	binary := buildSuga(t)
	t.Run("steering on", func(t *testing.T) { runToolInterrupt(t, binary, true) })
	t.Run("steering off", func(t *testing.T) { runToolInterrupt(t, binary, false) })
}

func runToolInterrupt(t *testing.T, binary string, steering bool) {
	t.Helper()
	work := t.TempDir()
	started, finished := filepath.Join(work, toolStarted), filepath.Join(work, toolFinished)
	command := osCommand(
		"touch "+toolStarted+"; sleep 60; touch "+toolFinished,
		`New-Item -ItemType File -Path `+toolStarted+` | Out-Null; Start-Sleep 60; New-Item -ItemType File -Path `+toolFinished,
	)
	fake := &fakeLLM{Mode: "tool", Delay: 200 * time.Millisecond, Command: command}
	server := httptest.NewServer(fake)
	defer server.Close()
	extra := ""
	if !steering {
		extra = "chat:\n  steering: false\n"
	}
	// No approval question: the tool must start on its own, the subject here is
	// the interrupt, not the approval.
	s, _ := startChatOpts(t, binary, chatOpts{
		Home: t.TempDir(), Work: work, URL: server.URL, ToolsExtra: "  approval: auto\n", Extra: extra,
	})

	mark := s.Mark()
	expect(t, s, `Suga chat · fake/m1`, mark)
	expect(t, s, `❯ `, mark)

	mark = s.Mark()
	s.SendLine("跑一个长命令")
	expect(t, s, `shell_exec`, mark)
	if !waitMarker(started, 30*time.Second) {
		t.Fatalf("the tool did not start; terminal tail:\n%s", tail(s.Text()))
	}

	// The tool is running and chat is in its turn: Ctrl-C must end it.
	time.Sleep(500 * time.Millisecond)
	s.SendCtrlC()
	if !steering {
		// Cooked console: the turn is interrupted and chat itself ends with its
		// summary right away (the tool process tree dies with it).
		expect(t, s, `已中断`, mark)
		expect(t, s, `续接：suga chat --resume (?:sess_|sg_|s_)`, mark)
		// Cooked console: Ctrl-C is SIGINT, so the process ends with the signal's
		// status; what matters is that it ended.
		waitGone(t, s)
		time.Sleep(time.Second)
		check(t, "ctrl-c: the tool's command was cut short", !fileExists(finished))
		return
	}

	check(t, "tool result not yet shown", !s.Contains(`⎿`, mark))
	expect(t, s, `已中断本轮`, mark)
	// The prompt after the interrupt: searched from the mark before it, so a
	// prompt that already arrived while waiting above is not missed.
	expect(t, s, `(?s)已中断本轮.*?❯`, mark)
	check(t, "ctrl-c: chat stays after one press", !s.Contains(`续接：`, mark))
	time.Sleep(500 * time.Millisecond)
	check(t, "ctrl-c: the tool's command was cut short", !fileExists(finished))

	// Esc ends the next turn the same way (no Ctrl-C event involved at all).
	mark = s.Mark()
	s.SendLine("再来一个长命令")
	expect(t, s, `shell_exec`, mark)
	time.Sleep(time.Second)
	s.Send("\x1b")
	expect(t, s, `已中断本轮`, mark)
	expect(t, s, `(?s)已中断本轮.*?❯`, mark)
	check(t, "esc: the tool's command was cut short", !fileExists(finished))

	// Two Ctrl-C within the confirmation window leave chat (the first one ended
	// the turn, the second lands at the prompt).
	mark = s.Mark()
	s.SendLine("第三次")
	expect(t, s, `shell_exec`, mark)
	time.Sleep(500 * time.Millisecond)
	s.SendCtrlC()
	time.Sleep(300 * time.Millisecond)
	s.SendCtrlC()
	expectWithin(t, s, `续接：suga chat --resume (?:sess_|sg_|s_)`, mark, 20*time.Second)
	waitGone(t, s)
	time.Sleep(time.Second)
	check(t, "exit: the tool's command was cut short", !fileExists(finished))
}

// waitGone waits for the chat process to end whatever its exit status (a cooked
// console ends it through SIGINT, which a shell reports as 130).
func waitGone(t *testing.T, s *ptytest.Session) {
	t.Helper()
	if _, err := s.WaitExit(20 * time.Second); err != nil {
		t.Fatalf("chat did not end: %v; terminal tail:\n%s", err, tail(s.Text()))
	}
}

// waitMarker waits until path exists (a tool's marker), false on a timeout.
func waitMarker(path string, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if fileExists(path) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
