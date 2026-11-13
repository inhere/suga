//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestChatWin32Input ports scripts/ptysmoke/chat_win32input.py: with
// chat.win32_input_mode on, the terminal reports every key press as a
// win32-input-mode record (DECSET 9001) and chat decodes them - the mode is what
// makes Ctrl+Enter a newline without a terminal binding (suga-tcgw).
//
// The scenario feeds the records itself, so it stands in for Windows Terminal on
// every platform. The `!` local command check is the one that is really about a
// console: a child process reads plain keys there, so chat switches the mode off
// while it runs; a POSIX pty has no console mode to switch, but chat runs the same
// code path on both, so the check is kept everywhere.
func TestChatWin32Input(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "text", Delay: 100 * time.Millisecond, Reply: "完成"}
	server := httptest.NewServer(fake)
	defer server.Close()
	s, _ := startChatOpts(t, binary, chatOpts{
		Home: t.TempDir(), Work: t.TempDir(), URL: server.URL,
		Extra: "chat:\n  win32_input_mode: on\n",
	})

	// chat asks for the mode while the line is editable. Both marks are taken
	// BEFORE either wait: the mode request and the prompt are two writes that a pty
	// reads as one chunk (Linux), so a mark taken after waiting for the request can
	// already sit behind the prompt - the original script waited from one mark taken
	// before both, which does not depend on the order the two arrive in.
	mark, rawModeMark := s.Mark(), len(s.RawText())
	expectRaw(t, s, `\x1b\[\?9001h`, rawModeMark)
	expect(t, s, `❯ `, mark)

	// CJK text arrives through the records (one per UTF-16 code unit), then
	// Ctrl+Enter is a newline, not a submit.
	firstMark := s.Mark()
	s.Send(win32Text("第一行"))
	expect(t, s, `第一行`, firstMark)
	recordMark := s.Mark()
	s.Send(win32CtrlEnter)
	s.Send(win32Text("第二行"))
	// How the editor redraws a second line is its own business (it may or may
	// not repaint the first one): both lines are on screen since before the
	// second was typed, and the model check below proves they form one message.
	if !until(t, s, "Ctrl+Enter puts both lines on the input line", `第二行`, recordMark) {
		t.Logf("input line: %q", tail(s.Since(firstMark)))
	}
	check(t, "Ctrl+Enter keeps the first line on screen", strings.Contains(s.Since(firstMark), "第一行"))
	check(t, "Ctrl+Enter starts no turn", noTurnAfter(fake, 0, 500*time.Millisecond))

	// Enter (a record as well) sends ONE message with both lines.
	turnMark := s.Mark()
	s.Send(win32Enter)
	expect(t, s, `▣ 完成`, turnMark)
	expect(t, s, `tokens · `, turnMark)
	check(t, "the model got one message with both lines", waitUserRequest(fake, "第一行\n第二行", 10*time.Second))
	check(t, "exactly one user message carries it", countUserWith(fake, "第二行") == 1)
	promptReady(t, s, turnMark)

	// Backspace and a held key (a repeat count) through records: "abcd", one
	// Backspace, then a held Backspace that stands for two presses leaves "a".
	turnMark = s.Mark()
	s.Send(win32Text("abcd") + win32Backspace + win32Press(8, 8, 0, 0, 2) + win32Enter)
	expect(t, s, `▣ 完成`, turnMark)
	expect(t, s, `tokens · `, turnMark)
	got := waitUserRequestName(fake, "a", 10*time.Second)
	check(t, "backspace and a repeat count delete through records", got == "a")
	promptReady(t, s, turnMark)

	// A `!` local command: a child process reads plain keys, so the mode goes off
	// for as long as it runs and comes back afterwards.
	cmdMark := s.Mark()
	cmdRaw := len(s.RawText())
	s.SendLine("!echo 本地命令")
	outputLine(t, s, "the local command runs", `本地命令`, cmdMark)
	check(t, "the prompt comes back", s.Expect(freshPrompt, cmdMark, 15*time.Second))
	// A POSIX pty has no console input mode to switch, and ConPTY does not pass the
	// DECRST through to the driver the way a terminal does, so the sequence is only
	// read where a real console reports it (the Linux/macOS runs of this scenario in
	// the container); the mode being active again afterwards is checked below on
	// every platform.
	if runtime.GOOS != "windows" {
		check(t, "the mode was switched off for the child process",
			rawContains(s, `\x1b\[\?9001l`, cmdRaw, 15*time.Second))
	}

	// The mode is back: Ctrl+Enter inserts a newline again instead of submitting.
	rawMark := len(s.RawText())
	requests := len(fake.Requests())
	s.Send(win32Text("上") + win32CtrlEnter + win32Text("下"))
	expectRaw(t, s, `上\r\n下`, rawMark)
	check(t, "the mode is back: Ctrl+Enter is a newline again", noTurnAfter(fake, requests, 500*time.Millisecond))
	turnMark = s.Mark()
	s.Send(win32Enter)
	expect(t, s, `▣ 完成`, turnMark)
	check(t, "the model got the two lines after the local command", waitUserRequest(fake, "上\n下", 10*time.Second))

	// Leaving chat restores the terminal: the mode is switched off again.
	exitMark := s.Mark()
	exitRaw := len(s.RawText())
	s.Send(win32CtrlD)
	expect(t, s, `续接：suga chat --resume (?:sess_|sg_|s_)`, exitMark)
	check(t, "win32-input-mode is switched off on exit",
		rawContains(s, `\x1b\[\?9001l`, exitRaw, 15*time.Second))
	waitExit(t, s)
}
