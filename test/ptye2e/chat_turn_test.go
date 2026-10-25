//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// resumeLine is the line chat prints when it leaves: the command that resumes the
// session it just wrote.
var resumeLine = regexp.MustCompile(`续接：suga chat --resume (\S+)`)

// TestChatPTYSmokeTurns covers the interactive paths that need a running turn (a
// question to answer, a long tool call, queued lines, a background task) or that
// leave chat. Every scenario is a subtest with its own fake model, home and
// workspace, so one of them cannot leave state for the next.
func TestChatPTYSmokeTurns(t *testing.T) {
	binary := buildSuga(t)

	t.Run("answered question restores the prompt", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "approval"})
		defer server.Close()
		work := t.TempDir()
		s := startChatSpec(t, binary, server.URL, t.TempDir(), work, nil, "")
		mark := s.Mark()
		expect(t, s, `❯ `, mark)

		// y, n and a end the turn at the very same prompt: the question runs on the
		// turn's borrowed read, and the next read has to repaint the block with its
		// own ❯ (issue suga-9ew7). The "a" answer asks a second question of its own
		// (the rules-file writeback), so both kinds are covered; it comes last
		// because it remembers the rule for the rest of the session.
		for _, answer := range []string{"y", "n", "a"} {
			// A rejected call writes its own path, so the accepted write of "y" and
			// "a" stays checkable by the content alone.
			text := "write it"
			if answer == "n" {
				text = "reject it"
			}
			mark = s.Mark()
			s.SendLine(text)
			expect(t, s, `允许执行 file_write`, mark)
			s.SendLine(answer)
			if answer == "a" {
				expect(t, s, `把这条规则写进规则文件`, mark)
				// An empty answer keeps the rule in this session only: no file write.
				s.SendLine("")
			}
			expect(t, s, `工具处理完成`, mark)
			expectAnsweredPrompt(t, s, answer, mark)
		}

		if body, err := os.ReadFile(filepath.Join(work, "approved.txt")); err != nil || string(body) != "APPROVED" {
			t.Fatalf("accepted write = %q, %v; terminal tail:\n%s", body, err, tail(s.Text()))
		}
		exitChat(t, s)
	})

	t.Run("ctrl-c cancels a long shell command", func(t *testing.T) {
		work := t.TempDir()
		command := longShellCommand("started.txt", "finished.txt")
		server := httptest.NewServer(&fakeChat{mode: "shell", command: command})
		defer server.Close()
		s := startChatSpec(t, binary, server.URL, t.TempDir(), work, nil, approvalAuto)
		mark := s.Mark()
		expect(t, s, `❯ `, mark)

		mark = s.Mark()
		s.SendLine("跑一个长命令")
		expect(t, s, `shell_exec`, mark)
		if !waitFile(filepath.Join(work, "started.txt"), 30*time.Second) {
			t.Fatalf("the long command did not start; terminal tail:\n%s", tail(s.Text()))
		}

		// The command is in its 30s wait: one Ctrl-C ends this turn only and takes
		// the tool's process tree with it, so chat is back at the prompt in seconds.
		press := time.Now()
		s.SendCtrlC()
		expect(t, s, `已中断本轮`, mark)
		expect(t, s, `❯`, mark)
		if took := time.Since(press); took > 10*time.Second {
			t.Fatalf("the turn took %s to end after Ctrl-C; terminal tail:\n%s", took, tail(s.Text()))
		}
		// The command's second marker is what tells a killed process tree from one
		// chat only stopped waiting for.
		time.Sleep(time.Second)
		if _, err := os.Stat(filepath.Join(work, "finished.txt")); err == nil {
			t.Fatalf("the long command ran to its end: its process tree survived the interrupt")
		}
		exitChat(t, s)
	})

	t.Run("queue lists and drops a queued message", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "text", delay: 6 * time.Second})
		defer server.Close()
		s := startChatSpec(t, binary, server.URL, t.TempDir(), t.TempDir(), nil, "")
		mark := s.Mark()
		expect(t, s, `❯ `, mark)

		mark = s.Mark()
		s.SendLine("first")
		expect(t, s, `↳ `, mark)
		// Tab on a line typed while the turn runs queues it for after the turn.
		s.Send("one\t")
		expect(t, s, `\[排队\] one（本轮结束后发送）`, mark)
		s.Send("two\t")
		expect(t, s, `\[排队\] two（本轮结束后发送）`, mark)

		// :queue is the one local command the running turn runs itself (it is what
		// fills the queue), so the listing arrives while the model is still asked.
		s.SendLine(":queue")
		expect(t, s, `排队的消息（2 条`, mark)
		expect(t, s, `1\. one`, mark)
		expect(t, s, `2\. two`, mark)

		s.SendLine(":queue -d 1")
		expect(t, s, `已删除第 1 条排队的消息：one`, mark)
		s.SendLine(":queue")
		expect(t, s, `排队的消息（1 条`, mark)
		expect(t, s, `1\. two`, mark)

		// The dropped line never runs; the one still queued runs once the turn ends.
		expect(t, s, `收到：first`, mark)
		expect(t, s, `收到：two`, mark)
		if s.Contains(`收到：one`, mark) {
			t.Fatalf("the dropped queued message ran; terminal tail:\n%s", tail(s.Text()))
		}
		exitChat(t, s)
	})

	t.Run("tasks lists and kills a background task", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{
			mode: "shell", command: longShellCommand("started.txt", "finished.txt"), background: true,
		})
		defer server.Close()
		s := startChatSpec(t, binary, server.URL, t.TempDir(), t.TempDir(), nil, approvalAuto)
		mark := s.Mark()
		expect(t, s, `❯ `, mark)

		mark = s.Mark()
		s.SendLine("后台跑一个长命令")
		expect(t, s, `background task t1 started`, mark)
		// The turn ends while the task runs: the background call returns at once.
		expect(t, s, `命令完成`, mark)
		expect(t, s, `tokens ·`, mark)

		s.SendLine(":tasks")
		expect(t, s, `t1 · running ·`, mark)

		mark = s.Mark()
		s.SendLine(":tasks -k t1")
		expect(t, s, `已终止任务 t1：`, mark)
		// A second kill of the same task reads differently: Kill on an ended task
		// returns its final snapshot, so chat answers from the state before it.
		mark = s.Mark()
		s.SendLine(":tasks -k t1")
		expect(t, s, `任务 t1 已结束（killed），无需终止`, mark)
		exitChat(t, s)
	})

	t.Run("leaving chat prints a working resume command", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "text"})
		defer server.Close()
		home, work := t.TempDir(), t.TempDir()
		s := startChatSpec(t, binary, server.URL, home, work, nil, "")
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("hello")
		expect(t, s, `收到：hello`, mark)

		mark = s.Mark()
		s.SendCtrlC()
		expect(t, s, `再按一次 Ctrl\+C 退出`, mark)
		s.SendCtrlC()
		expect(t, s, `续接：suga chat --resume`, mark)
		waitExit(t, s)

		found := resumeLine.FindStringSubmatch(s.Since(mark))
		if len(found) < 2 {
			t.Fatalf("no resume command in %q", s.Since(mark))
		}
		sessionID := found[1]
		if !regexp.MustCompile(`^(?:sess_|sg_|s_)`).MatchString(sessionID) {
			t.Fatalf("resume command names %q, not a session id", sessionID)
		}

		// What chat printed is what works: the same home and directory resume that
		// session, and the resumed chat is a prompt of its own.
		resumed := startChatSpec(t, binary, server.URL, home, work, []string{"--resume", sessionID}, "")
		mark = resumed.Mark()
		expect(t, resumed, `❯ `, mark)
		expect(t, resumed, `Suga chat`, mark)
		exitChat(t, resumed)
	})
}

// approvalAuto is the tools: extra of the scenarios whose model calls a tool
// without a question: the subject there is the turn, not the approval.
const approvalAuto = "  approval: auto\n"

// expectAnsweredPrompt checks the input line of a turn that answered a question
// (issue suga-9ew7): the next read repaints the block with its own ❯ prompt, so
// the row is a plain prompt again and what is typed next is a line of its own.
// Without the repaint the row keeps the turn's ↳ prompt and the question's width,
// and nothing paints a ❯ after the turn's last line at all.
func expectAnsweredPrompt(t *testing.T, s *ptytest.Session, name string, mark int) {
	t.Helper()
	// The row is a plain prompt again: the next read repainted the block with its
	// own ❯ right after the turn's answer. Without that repaint (issue suga-9ew7)
	// the row keeps the turn's ↳ prompt and the question's width, and no ❯ follows
	// the answer at all. The anchor is the model's answer text, not the status line:
	// the end-of-turn line is a display detail, the answer is part of the turn.
	if !s.Expect(`(?s)工具处理完成.*❯`, mark, 15*time.Second) {
		t.Fatalf("answer %q: no ❯ after the turn's answer; terminal tail:\n%s", name, tail(s.Text()))
	}
	// What is typed next is a line of its own, starting right at that prompt. A paste
	// lays the text down in one write (typing rewrites the line once per key).
	s.Send("\x1b[200~abc\x1b[201~")
	if !s.Expect(`(?s)工具处理完成.*❯ ?abc`, mark, 15*time.Second) {
		t.Fatalf("answer %q: the pasted text is not a line of the prompt; terminal tail:\n%s", name, tail(s.Text()))
	}
	// Ctrl-U drops the line again: the next question starts on an empty one.
	s.Send("\x15")
	time.Sleep(200 * time.Millisecond)
}

// longShellCommand runs long enough that a turn waiting for it would overrun the
// test: the marker before the wait tells the scenario the command started, the one
// after it that the command still ran to its end (its process tree survived).
func longShellCommand(started, finished string) string {
	if runtime.GOOS == "windows" {
		// shell_exec runs PowerShell on Windows (native.ShellName).
		return fmt.Sprintf("Set-Content -Path %s -Value ok; Start-Sleep -Seconds 30; Set-Content -Path %s -Value ok",
			started, finished)
	}
	return fmt.Sprintf("touch %s; sleep 30; touch %s", started, finished)
}

// waitFile waits for a marker file of a shell command, the way the pty smoke
// scripts do: a tool's output only reaches chat when the tool ends, so the file is
// what tells that the command is running.
func waitFile(path string, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// exitChat leaves chat the way a person does: two Ctrl-C in a row print the resume
// command. A Ctrl-C on a non-empty input line only clears it and one during a turn
// only ends that turn, so up to four presses are sent, stopping as soon as chat has
// left (a press to a child that already exited would fail the session's write).
func exitChat(t *testing.T, s *ptytest.Session) {
	t.Helper()
	mark := s.Mark()
	for i := 0; i < 4; i++ {
		time.Sleep(300 * time.Millisecond)
		if s.Code() != nil || s.Contains(`续接：suga chat --resume`, mark) {
			break
		}
		s.SendCtrlC()
	}
	expect(t, s, `续接：suga chat --resume`, mark)
	waitExit(t, s)
}
