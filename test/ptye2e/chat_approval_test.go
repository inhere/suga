//go:build ptye2e

package ptye2e

import (
	"bytes"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/gopty"
	"github.com/gookit/gopty/ptytest"
)

// TestChatApproval ports scripts/ptysmoke/chat_approval.py: the approval
// question of an ordinary write turn (full command, y runs, n denies), what the
// person's answer is remembered as, Ctrl-C on the question versus on a non-empty
// input line, the pre-typed y of a running turn (once per chat.steering), the
// editor's width handling (a wide window, then a resize) and the piped
// (non-TTY) run that denies write tools.
//
// Python check -> Go assertion:
//
//	banner shows model / banner shows write tools on / ❯ prompt
//	                                      banner, 工具：可读写文件 and ❯ expects
//	wide window: 90 columns stay on one row        approvalEditorWidth: x{90}
//	wide window: no wide-rune filler at column 99   !Contains("好 \r\n")
//	resize: the editor lays out at the narrower width  Resize(60)+Contains("好 \r\n")
//	y: full command shown / executed / approved note shown / answer marked ▣
//	命令：echo SMOKE_OK, ⎿ ok: SMOKE_OK, 已经人工批准执行 note, ▣ 完成
//	y: turn ends / turn end shows context share and local time  approvalTurnEnd
//	no highlight without colors          !Contains("\x1b[48;") in RawText after the mark
//	y|n|a: prompt row is ❯ / typed text is a prompt line (ptydrive input_row)
//	                                      approvalAnsweredPrompt / approvalLineOfPrompt
//	ctrl-c: question / cancelled / not executed
//	                                      QUESTION, 已取消, !Contains(OK)
//	ctrl-c with text: question / cancelled / not executed   same with "abc" typed first
//	a: chat alive, question / persistence question / executed  QUESTION, 把这条规则写进规则文件, OK
//	a: approved note shown / executed again / remembered call: no note / no question again
//	                                      note, OK, !Contains(note), !Contains(QUESTION)
//	ctrl-c non-empty: no exit hint / chat alive / the input line is empty /
//	  the next text is a line of its own  !Contains(再按一次), !Contains(续接：),
//	                                      waitInputRow(❯) + waitInputRow(❯ def)
//	ctrl-c once: hint / chat alive / ctrl-c late: hint again  再按一次 Ctrl+C 退出, !Contains(续接：)
//	exit: usage summary / resume command  本次 N 轮 · in .. tokens, 续接：suga chat --resume
//	pre-typed y (both steering groups): inserted into the turn / question still
//	  asked / not executed / n: denied / the insert reached the turn / the turn
//	  answers / n: denied tools listed / n: turn ends / no insert at all (steering
//	  off)                          [插话] y（等待送达）, QUESTION, !OK, 用户拒绝执行 shell_exec,
//	                                [插话] y（已送达）, 完成, 本轮被拒绝的工具：shell_exec, tokens
//	non-tty: write tool denied with hint  标准输入不是终端 + tools.approval: auto
func TestChatApproval(t *testing.T) {
	binary := buildSuga(t)

	t.Run("editor width", func(t *testing.T) {
		approvalEditorWidth(t, binary)
	})

	t.Run("answer keys", func(t *testing.T) {
		server := httptest.NewServer(&fakeLLM{Mode: "tool", Command: "echo SMOKE_OK", Delay: time.Second})
		defer server.Close()
		s, _ := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: t.TempDir(), URL: server.URL})

		m := s.Mark()
		check(t, "banner shows model", s.Expect(`Suga chat · fake/m1`, m, 15*time.Second))
		check(t, "banner shows write tools on", s.Expect(`工具：可读写文件、执行 shell`, m, 15*time.Second))
		check(t, "❯ prompt", s.Expect(`❯ `, m, 15*time.Second))

		// y: the whole command is shown, the answer runs it, and the model sees the
		// person-approved note at the end of the result.
		m = s.Mark()
		rawMark := len(s.RawText())
		s.SendLine("跑一下")
		check(t, "y: full command shown",
			s.Expect(`命令：echo SMOKE_OK`, m, 15*time.Second) && s.Expect(approvalQuestion, m, 15*time.Second))
		s.SendLine("y")
		check(t, "y: executed", s.Expect(approvalOK, m, 15*time.Second) && s.Expect(`完成`, m, 15*time.Second))
		check(t, "y: approved note shown", s.Expect(`⎿ ok: SMOKE_OK[^\r\n]*已经人工批准执行`, m, 15*time.Second))
		check(t, "y: answer marked ▣", s.Contains(`▣ 完成`, m))
		check(t, "y: turn ends", s.Expect(`tokens · `, m, 15*time.Second))
		check(t, "turn end shows the context share and the local time", s.Expect(approvalTurnEnd, m, 15*time.Second))
		// NO_COLOR is set for these scenarios: the input line gets no background.
		check(t, "no highlight without colors", !strings.Contains(s.RawText()[rawMark:], "\x1b[48;"))
		approvalAnsweredPrompt(t, s, "y", m)

		// Ctrl-C on the question cancels this turn only: nothing runs, chat stays.
		m = s.Mark()
		s.SendLine("第二次")
		check(t, "ctrl-c: question", s.Expect(approvalQuestion, m, 15*time.Second))
		s.SendCtrlC()
		check(t, "ctrl-c: cancelled", s.Expect(`已取消`, m, 15*time.Second))
		check(t, "ctrl-c: not executed", !s.Contains(approvalOK, m))
		time.Sleep(500 * time.Millisecond)

		// Text already on the line does not change that: the question's own read is
		// the one read whose Ctrl-C stays "cancel" (wll1).
		m = s.Mark()
		s.SendLine("第三次")
		check(t, "ctrl-c with text: question", s.Expect(approvalQuestion, m, 15*time.Second))
		s.Send("abc")
		time.Sleep(400 * time.Millisecond)
		s.SendCtrlC()
		check(t, "ctrl-c with text: cancelled", s.Expect(`已取消`, m, 15*time.Second))
		check(t, "ctrl-c with text: not executed", !s.Contains(approvalOK, m))
		time.Sleep(500 * time.Millisecond)

		// "a" allows the tool for the session: the call runs, and the rule answers
		// the next one without a person (so its result carries no note).
		m = s.Mark()
		s.SendLine("第四次")
		check(t, "a: chat alive, question", s.Expect(approvalQuestion, m, 15*time.Second))
		s.SendLine("a")
		check(t, "a: persistence question", s.Expect(`把这条规则写进规则文件`, m, 15*time.Second))
		s.SendLine("")
		check(t, "a: executed", s.Expect(approvalOK, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
		check(t, "a: approved note shown", s.Contains(`⎿ ok: SMOKE_OK[^\r\n]*已经人工批准执行`, m))
		approvalAnsweredPrompt(t, s, "a", m)

		m = s.Mark()
		s.SendLine("又一次")
		check(t, "a: executed again", s.Expect(approvalOK, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
		check(t, "a: remembered call: no note", !s.Contains(`⎿ ok: SMOKE_OK[^\r\n]*已经人工批准执行`, m))
		check(t, "a: no question again", !s.Contains(approvalQuestion, m))
		exitAfterTurn(t, s, m)
	})

	t.Run("cancellation keys", func(t *testing.T) {
		server := httptest.NewServer(&fakeLLM{Mode: "tool", Command: "echo SMOKE_OK", Delay: time.Second})
		defer server.Close()
		s, _ := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: t.TempDir(), URL: server.URL})
		m := s.Mark()
		until(t, s, "❯ prompt", `❯ `, m)

		// Ctrl-C on a non-empty line only clears the line (wll1, the way Claude Code
		// does it): no exit confirmation, chat stays, and the row is left at the
		// prompt alone.
		m = s.Mark()
		time.Sleep(300 * time.Millisecond)
		s.Send("abc")
		time.Sleep(200 * time.Millisecond)
		s.SendCtrlC()
		time.Sleep(600 * time.Millisecond)
		check(t, "ctrl-c non-empty: no exit hint", !s.Contains(`再按一次 Ctrl\+C 退出`, m))
		check(t, "ctrl-c non-empty: chat alive", !s.Contains(`续接：`, m))
		// The cleared text is really gone: the row holds the prompt alone, and what is
		// typed next is a line of its own (ptydrive's input_row: "❯" then "❯ def").
		check(t, "ctrl-c non-empty: the input line is empty", waitInputRow(t, s, chatCols, approvalPrompt, 15*time.Second))
		s.Send("def")
		check(t, "ctrl-c non-empty: the next text is a line of its own", waitInputRow(t, s, chatCols, approvalPrompt+" def", 15*time.Second))
		s.Send("\x15") // Ctrl-U drops the line again
		time.Sleep(300 * time.Millisecond)

		// An empty line: one Ctrl-C only asks for another (the old meaning).
		m = s.Mark()
		s.SendCtrlC()
		check(t, "ctrl-c once: hint", s.Expect(`再按一次 Ctrl\+C 退出`, m, 15*time.Second))
		check(t, "ctrl-c once: chat alive", !s.Contains(`续接：`, m))
		// A second Ctrl-C after the window is again only a hint.
		m = s.Mark()
		time.Sleep(3500 * time.Millisecond)
		s.SendCtrlC()
		check(t, "ctrl-c late: hint again", s.Expect(`再按一次 Ctrl\+C 退出`, m, 15*time.Second))
		check(t, "ctrl-c late: chat alive", !s.Contains(`续接：`, m))

		m = s.Mark()
		time.Sleep(300 * time.Millisecond)
		s.SendCtrlC()
		check(t, "exit: usage summary", s.Expect(`本次 \d+ 轮 · in \d+ / out \d+ tokens`, m, 15*time.Second))
		check(t, "exit: resume command", s.Expect(`续接：suga chat --resume (?:sess_|sg_|s_)`, m, 15*time.Second))
		waitExit(t, s)
	})

	// The pre-typed y of an approval question: chat.steering decides what such a
	// line is, so it runs once per setting.
	t.Run("pre-typed y", func(t *testing.T) {
		t.Run("steering on", func(t *testing.T) {
			approvalPretyped(t, binary, true)
		})
		t.Run("steering off", func(t *testing.T) {
			approvalPretyped(t, binary, false)
		})
	})

	t.Run("non-tty", func(t *testing.T) {
		server := httptest.NewServer(&fakeLLM{Mode: "tool", Command: "echo SMOKE_OK", Delay: time.Second})
		defer server.Close()
		home, work := t.TempDir(), t.TempDir()
		o := chatOpts{Home: home, Work: work, URL: server.URL}
		writeChatHome(t, o)
		cmd := exec.Command(binary, "chat")
		cmd.Dir, cmd.Env = work, chatEnv(t, o)
		cmd.Stdin = strings.NewReader("跑一下\n:quit\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("piped chat: %v; output:\n%s", err, out)
		}
		check(t, "non-tty: write tool denied with hint",
			strings.Contains(string(out), "标准输入不是终端") && strings.Contains(string(out), "tools.approval: auto"))
	})
}

const (
	approvalOK       = `⎿ ok: SMOKE_OK`
	approvalQuestion = `允许执行 shell_exec`
	// approvalPrompt is the input prompt as the screen shows it (ptydrive's
	// input_row compared whole rows: a cleared line reads as the prompt alone, a
	// line holding text as the prompt, its own space and the text).
	approvalPrompt = `❯`
	// chatCols is the pty width startChatOpts starts a chat with: the screen model
	// (vtscreen_test.go) lays its rows out with it.
	chatCols = 100
	// approvalTurnEnd is the status line every turn ends with: the context share
	// and the local time.
	approvalTurnEnd = `tokens · ctx \d+% · [\d.]+m?s · \d{2}:\d{2}`
)

// approvalAnsweredPrompt is the Go form of the Python check_answered_prompt
// (issue suga-9ew7): the input row after a turn whose question was answered must
// look like the row of a turn without a question - a prompt of its own, with the
// next typed text on a line of that prompt. Without the repaint the row kept the
// steer prompt plus the question's width.
func approvalAnsweredPrompt(t *testing.T, s *ptytest.Session, name string, mark int) {
	t.Helper()
	if !s.Expect(`(?s)完成.*❯`, mark, 15*time.Second) {
		t.Fatalf("%s: no ❯ after the answer; terminal tail:\n%s", name, tail(s.Text()))
	}
	approvalLineOfPrompt(t, s, name)
}

// approvalLineOfPrompt reads the input row off the screen the way ptydrive's
// input_row did: the row must hold the prompt alone, and the text sent next must
// be a line of that prompt ("❯ abc"), then the line is cleared again.
//
// The row, not the byte stream, decides: whether the editor repaints the whole row
// (ConPTY) or only writes the new text (a Linux pty after Ctrl-C cleared the line),
// the screen shows the same row, while the bytes differ.
func approvalLineOfPrompt(t *testing.T, s *ptytest.Session, name string) {
	t.Helper()
	check(t, name+": prompt row is ❯", waitInputRow(t, s, chatCols, approvalPrompt, 15*time.Second))
	s.Send("abc")
	check(t, name+": typed text is a prompt line", waitInputRow(t, s, chatCols, approvalPrompt+" abc", 15*time.Second))
	s.Send("\x15") // Ctrl-U drops the line again
	time.Sleep(200 * time.Millisecond)
}

// approvalPretyped runs the pre-typed y scenario once: with steering on the turn
// keeps reading the input line, so the y is a steer inserted into the running
// turn and cannot answer the question that follows; with steering off the console
// stays cooked, nothing reads during the turn, and the question's own read must
// not take the waiting line as its answer (ask.go flushes it).
func approvalPretyped(t *testing.T, binary string, steering bool) {
	o := chatOpts{Home: t.TempDir(), Work: t.TempDir()}
	server := httptest.NewServer(&fakeLLM{Mode: "tool", Command: "echo SMOKE_OK", Delay: 2 * time.Second, ToolOnce: true})
	defer server.Close()
	o.URL = server.URL
	if !steering {
		o.Extra = "chat:\n  steering: false\n"
	}
	s, _ := startChatOpts(t, binary, o)

	m := s.Mark()
	check(t, "banner shows model", s.Expect(`Suga chat · fake/m1`, m, 15*time.Second))
	check(t, "❯ prompt", s.Expect(`❯ `, m, 15*time.Second))

	m = s.Mark()
	s.SendLine("跑一下")
	if steering {
		// Type the y once the turn's own read is up (its steer inbox is open by
		// then) and well before the model's answer, so it can never land at the
		// question that follows.
		check(t, "the prompt becomes ↳ while the turn runs", s.Expect(`↳ `, m, 15*time.Second))
		time.Sleep(500 * time.Millisecond)
	} else {
		time.Sleep(300 * time.Millisecond)
	}
	m = s.Mark()
	s.SendLine("y")
	if steering {
		check(t, "pre-typed y: inserted into the turn", s.Expect(`\[插话\] y（等待送达）`, m, 15*time.Second))
	}
	check(t, "pre-typed y: question still asked", s.Expect(approvalQuestion, m, 15*time.Second))
	check(t, "pre-typed y: not executed", !s.Contains(approvalOK, m))
	s.SendLine("n")
	check(t, "n: denied", s.Expect(`用户拒绝执行 shell_exec`, m, 15*time.Second))
	if steering {
		// The request after the denied result carried the insert: the model answers
		// with its text.
		check(t, "the insert reached the turn", s.Expect(`\[插话\] y（已送达）`, m, 15*time.Second))
		check(t, "the turn answers", s.Expect(`完成`, m, 15*time.Second))
	}
	check(t, "n: denied tools listed", s.Expect(`本轮被拒绝的工具：shell_exec`, m, 15*time.Second))
	check(t, "n: turn ends", s.Expect(`tokens · `, m, 15*time.Second))
	if !steering {
		// Nothing reads the keyboard during a cooked turn, so the y became no
		// inserted message at all - checked once the whole turn is over.
		check(t, "pre-typed y: no insert at all", !s.Contains(`\[插话\]`, m))
	}
	check(t, "turn end shows the context share and the local time", s.Expect(approvalTurnEnd, m, 15*time.Second))
	approvalAnsweredPrompt(t, s, "n", m)
	exitAfterTurn(t, s, m)
}

// approvalEditorWidth covers the wide window and the resize: at 100 columns a
// 90-column line stays on one row and no wide-rune filler appears, while at 60
// columns the same line hits the last column and the editor writes the filler
// space a wide rune needs. ptytest.Session does not expose Resize, so this
// scenario drives the pty itself.
func approvalEditorWidth(t *testing.T, binary string) {
	o := chatOpts{Home: t.TempDir(), Work: t.TempDir()}
	server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 100 * time.Millisecond})
	defer server.Close()
	o.URL = server.URL
	writeChatHome(t, o)

	spec := gopty.Spec{Command: binary, Args: []string{"chat"}, Dir: o.Work, Env: chatEnv(t, o), Cols: 100, Rows: 30}
	s := startRawChat(t, spec)

	m := s.Mark()
	check(t, "❯ prompt", s.Expect(`❯ `, m, 15*time.Second))

	// A wide window (100 columns): the editor wraps at the terminal's width, not at
	// a fixed 80, so a 90-column line stays on one row.
	m = s.Mark()
	s.Send(strings.Repeat("x", 90))
	check(t, "wide window: 90 columns stay on one row", s.Expect(`x{90}`, m, 15*time.Second))
	s.Send("\x15")
	time.Sleep(200 * time.Millisecond)

	// A wide rune never straddles two rows: at the last column the editor writes the
	// filler space first. "a" + 29 wide runes reach column 59, so the filler shows
	// only when the editor lays the line out 60 columns wide - which this control
	// run (100 columns) must not do.
	m = s.Mark()
	s.Send("a" + strings.Repeat("好", 29))
	time.Sleep(800 * time.Millisecond)
	check(t, "wide window: no wide-rune filler at column 99", !s.Contains(`好 \r\n`, m))
	s.Send("\x15")
	time.Sleep(200 * time.Millisecond)

	// A window resized while chat runs reaches the editor (SIGWINCH; Windows polls).
	s.Resize(60, 30)
	time.Sleep(500 * time.Millisecond)
	m = s.Mark()
	s.Send("a" + strings.Repeat("好", 29))
	check(t, "resize: the editor lays out at the narrower width", s.Expect(`好 \r\n`, m, 2*time.Second))
	s.Send("\x15")
	s.Resize(100, 30)
}

// rawChat drives a chat through gopty directly, with the output collection of a
// ptytest.Session plus the window resize ptytest does not expose.
type rawChat struct {
	p  gopty.Pty
	mu sync.Mutex
	// buf holds the output with the CSI sequences a terminal interprets cut out
	// (the same filter ptytest applies to Session.Text).
	buf bytes.Buffer
}

var rawANSI = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

func startRawChat(t *testing.T, spec gopty.Spec) *rawChat {
	t.Helper()
	if !gopty.IsAvailable() {
		t.Skipf("no pty backend on this host")
	}
	p, err := gopty.Start(spec)
	if err != nil {
		if gopty.IsHostBlocked(err) {
			t.Skipf("this process context cannot host a pty child: %v", err)
		}
		t.Fatalf("start %s: %v", spec.Command, err)
	}
	c := &rawChat{p: p}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.buf.WriteString(rawANSI.ReplaceAllString(string(buf[:n]), ""))
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = p.Close() })
	return c
}

func (c *rawChat) Send(text string) { _, _ = c.p.Write([]byte(text)) }

func (c *rawChat) Resize(cols, rows int) {
	if err := c.p.Resize(cols, rows); err != nil {
		// A host that cannot resize is reported by the check that needs it.
		return
	}
}

func (c *rawChat) Mark() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Len()
}

func (c *rawChat) Since(mark int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.buf.Bytes()
	if mark < 0 || mark > len(b) {
		mark = 0
	}
	return string(b[mark:])
}

// Expect waits until the output after mark matches pattern.
func (c *rawChat) Expect(pattern string, mark int, timeout time.Duration) bool {
	re := regexp.MustCompile(pattern)
	end := time.Now().Add(timeout)
	for {
		if re.MatchString(c.Since(mark)) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *rawChat) Contains(pattern string, mark int) bool {
	return regexp.MustCompile(pattern).MatchString(c.Since(mark))
}
