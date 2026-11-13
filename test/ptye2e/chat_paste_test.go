//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChatPaste ports scripts/ptysmoke/chat_paste.py: a pasted multi-line text
// lands on the input line as one piece and only the person's Enter sends it, as
// ONE message with its internal newlines kept (suga-b1md).
//
// Every wait is an Expect on what the line editor draws or on the fake model's
// request log; the "no turn starts" checks wait on the request count only, so a
// paste that submits at its first line break fails them (suga-fksc: the Python
// script waited with fixed sleeps and failed 4 items now and then).
func TestChatPaste(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "text", Delay: 100 * time.Millisecond, Reply: "完成"}
	server := httptest.NewServer(fake)
	defer server.Close()
	// Defaults a person sees: history/spinner/title from the config template's
	// chat: section are off, markdown and the input line rendering are on.
	s, _ := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: t.TempDir(), URL: server.URL})

	mark := s.Mark()
	expect(t, s, `❯ `, mark)
	// The terminal is asked for bracketed pastes while the line is editable.
	expectRaw(t, s, `\x1b\[\?2004h`, 0)

	// A paste with an LF line break: both lines on the input line, no turn.
	rawMark := len(s.RawText())
	s.Send("\x1b[200~第一行\n第二行\x1b[201~")
	expectRaw(t, s, `第一行\r\n第二行`, rawMark)
	check(t, "LF paste: no turn starts", noTurnAfter(fake, 0, 500*time.Millisecond))
	// Only the person's Enter sends it, as one message with the newline kept.
	turnMark := s.Mark()
	s.Send("\r")
	expect(t, s, `▣ 完成`, turnMark)
	expect(t, s, `tokens · `, turnMark)
	user := waitUserRequestName(fake, "第一行\n第二行", 10*time.Second)
	check(t, "LF paste: the model got one message with both lines", user != "")
	check(t, "LF paste: exactly one user message carries the paste", countUserWith(fake, "第二行") == 1)
	// The message is the pasted text, not just its first line.
	check(t, "LF paste: the message keeps both lines", strings.Contains(user, "第一行\n第二行"))
	promptReady(t, s, turnMark)

	// The same with a CR line break, which is what a terminal inside a paste
	// usually hands over.
	rawMark = len(s.RawText())
	s.Send("\x1b[200~第三行\r第四行\x1b[201~")
	expectRaw(t, s, `第三行\r\n第四行`, rawMark)
	check(t, "CR paste: no turn starts", noTurnAfter(fake, len(fake.Requests()), 500*time.Millisecond))
	turnMark = s.Mark()
	s.Send("\r")
	expect(t, s, `▣ 完成`, turnMark)
	expect(t, s, `tokens · `, turnMark)
	check(t, "CR paste: the model got one message with both lines", waitUserRequest(fake, "第三行\n第四行", 10*time.Second))
	promptReady(t, s, turnMark)

	// A terminal without bracketed paste support hands the text over without the
	// markers: chat recognises the newline by the bytes that follow it.
	rawMark = len(s.RawText())
	s.Send("第五行\n第六行")
	expectRaw(t, s, `第五行\r\n第六行`, rawMark)
	check(t, "a terminal without markers: no turn starts", noTurnAfter(fake, len(fake.Requests()), 500*time.Millisecond))
	turnMark = s.Mark()
	s.Send("\r")
	expect(t, s, `▣ 完成`, turnMark)
	expect(t, s, `tokens · `, turnMark)
	check(t, "a terminal without markers: the model got one message", waitUserRequest(fake, "第五行\n第六行", 10*time.Second))
	promptReady(t, s, turnMark)

	// Multi-line input: Alt+Enter inserts a newline instead of submitting (cursor
	// movement inside a multi-line input is covered by lineedit's unit tests).
	rawMark = len(s.RawText())
	s.Send("第一行")
	s.Send("\x1b\r")
	s.Send("第二行")
	expectRaw(t, s, `第一行\r\n第二行`, rawMark)
	check(t, "alt+Enter: no turn starts", noTurnAfter(fake, len(fake.Requests()), 500*time.Millisecond))
	// Ctrl+U clears the line again; the bytes that follow arrive after it, so the
	// next line starts from an empty input.
	s.Send("\x15")

	// A typed Enter with text still submits.
	turnMark = s.Mark()
	s.Send("普通一行\r")
	expect(t, s, `▣ 完成`, turnMark)
	expect(t, s, `tokens · `, turnMark)
	user = waitUserRequestName(fake, "普通一行", 10*time.Second)
	check(t, "typed Enter still submits", user != "")
	check(t, "typed Enter: the model got the line alone", user == "普通一行")

	// Leaving chat names the command that continues this session.
	check(t, "exit: resume command", ctrlCTwice(t, s) != "")
}
