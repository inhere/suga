//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// Spinner artifacts the checks look for in the raw output (Text strips the
// escapes). The terminal title is OSC 0 with the program, the session's short
// name ("ws-<two chars>": the workspace directory) and the session title.
const (
	spinnerHint  = `思考中… \d+s`
	spinnerTitle = `\x1b\]0;suga · `
	titleSave    = `\x1b\[22;0t`
	titleRestore = `\x1b\[23;0t`
	titleClear   = `\x1b\]0;\x07`
	// The background chat paints the submitted line with (chat.highlight_input).
	inputBg = `\x1b\[48;5;236m`
)

// TestChatSpinner ports scripts/ptysmoke/chat_spinner.py: chat shows the
// "思考中…" waiting line while the model thinks, erases it before the next
// output, paints the submitted line on a background of its own and names the
// terminal tab after the session. A second chat with chat.spinner,
// chat.highlight_input and chat.terminal_title off shows none of them.
//
// Both runs need colors (NO_COLOR is dropped): neither the waiting line, the
// input background nor the terminal title shows on a terminal without them. The
// frame checks run with chat.steering: false - while chat keeps the input line on
// screen for the whole turn (the default) the waiting line stays off.
func TestChatSpinner(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "tool", Delay: 2 * time.Second, Command: "echo SMOKE_SPINNER_OK", Reply: "SMOKE_SPINNER_REPLY"}
	server := httptest.NewServer(fake)
	defer server.Close()
	// The workspace is named "ws" so the short name is "ws-<two chars>", as the
	// title checks read it.
	work := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("waiting line, highlighted input and terminal title", func(t *testing.T) {
		s, _ := startChatOpts(t, binary, chatOpts{
			Home: t.TempDir(), Work: work, URL: server.URL, Color: true,
			ToolsExtra: "  approval: auto\n", Extra: "chat:\n  steering: false\n",
		})
		mark := s.Mark()
		expect(t, s, `Suga chat · fake/m1`, mark)
		expect(t, s, `❯ `, mark)
		check(t, "nothing before the first turn", !s.Contains("思考中", mark))
		// The terminal title is set before the banner: the terminal's own title is
		// saved first, then the session named (no session title before the first
		// message).
		expectRaw(t, s, titleSave, 0)
		expectRaw(t, s, spinnerTitle+`ws-[a-z0-9]{2}\x07`, 0)
		check(t, "title: no session title before the first message",
			!regexpMatch(spinnerTitle+`ws-[a-z0-9]{2} · `, s.RawText()))

		turnMark := s.Mark()
		rawMark := len(s.RawText())
		s.SendLine("你好")
		expect(t, s, spinnerHint, turnMark)
		// The first message is the session title, so the terminal title changes with
		// it (the same title the session stores).
		expectRaw(t, s, spinnerTitle+`ws-[a-z0-9]{2} · 你好\x07`, rawMark)
		// The repaint: the background style, the prompt, the typed text on it.
		expectRaw(t, s, inputBg, rawMark)
		expectRaw(t, s, inputBg+`[^\n]*❯ [^\n]*你好`, rawMark)
		// The background reaches the row end: padded with spaces, or ended with an
		// erase-to-end-of-line (ConPTY rewrites the padding that way).
		expectRaw(t, s, `你好(?: +\x1b\[0m| *\x1b\[K)`, rawMark)
		expect(t, s, `\$ shell_exec`, turnMark)
		expect(t, s, `⎿ ok: SMOKE_SPINNER_OK`, turnMark)

		readMark := s.Mark()
		expect(t, s, spinnerHint, readMark)
		expect(t, s, `▣ SMOKE_SPINNER_REPLY`, readMark)
		expect(t, s, `tokens · `, readMark)

		raw := s.RawText()[rawMark:]
		hint := strings.LastIndex(raw, "思考中")
		answer := strings.LastIndex(raw, "SMOKE_SPINNER_REPLY")
		check(t, "the last hint was erased before the answer",
			hint >= 0 && answer > hint && regexpMatch(`\x1b\[(?:2K|K)`, raw[hint:answer]))

		// Ctrl-D at the prompt leaves chat: the title is emptied (a terminal that
		// keeps no title stack then shows its own default) and the saved title
		// popped after it.
		exitRaw := len(s.RawText())
		s.Send(ptytest.CtrlD)
		expectRaw(t, s, titleClear, exitRaw)
		expectRaw(t, s, titleRestore, exitRaw)
		waitExit(t, s)
	})

	t.Run("every switch off shows none of them", func(t *testing.T) {
		s, _ := startChatOpts(t, binary, chatOpts{
			Home: t.TempDir(), Work: work, URL: server.URL, Color: true,
			ToolsExtra: "  approval: auto\n",
			Extra:      "chat:\n  spinner: false\n  highlight_input: false\n  terminal_title: false\n",
		})
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		turnMark := s.Mark()
		rawMark := len(s.RawText())
		s.SendLine("你好")
		expect(t, s, `▣ SMOKE_SPINNER_REPLY`, turnMark)
		expect(t, s, `tokens · `, turnMark)
		raw := s.RawText()[rawMark:]
		check(t, "off: no waiting line anywhere", !regexpMatch(`思考中`, raw))
		check(t, "off: the submitted line has no background", !regexpMatch(inputBg, raw))
		check(t, "off: no terminal title anywhere", !regexpMatch(spinnerTitle, s.RawText()))
		check(t, "off: no title save or restore either", !regexpMatch(`\x1b\[2[23];0t`, s.RawText()))
		ctrlCTwice(t, s)
	})
}
