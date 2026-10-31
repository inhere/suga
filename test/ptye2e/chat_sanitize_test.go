//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChatSanitize ports scripts/ptysmoke/chat_sanitize.py: model and tool text
// cannot emit terminal control sequences. The raw PTY output is checked, not
// only the ANSI-stripped text.
//
// Python check -> Go assertion:
//
//	prompt / tool result text survives / answer text survives
//	raw PTY has no ESC]52 / raw PTY has no ESC[2J
func TestChatSanitize(t *testing.T) {
	binary := buildSuga(t)
	reply := "ANSWER \x1b]52;c;PASTE\x07\x1b[2JVISIBLE"
	command := osCommand(`printf 'TOOL_OK \033]52;c;PASTE\007\033[2JVISIBLE'`,
		`Write-Host -NoNewline ("TOOL_OK " + [char]27 + "]52;c;PASTE" + [char]7 + [char]27 + "[2JVISIBLE")`)
	server := httptest.NewServer(&fakeLLM{Mode: "tool", Command: command, Reply: reply, Delay: 100 * time.Millisecond})
	defer server.Close()
	s, _ := startChatOpts(t, binary, chatOpts{
		Home: t.TempDir(), Work: t.TempDir(), URL: server.URL, ToolsExtra: "  approval: auto\n",
	})

	m := s.Mark()
	until(t, s, "prompt", `❯ `, m)
	m, rawMark := s.Mark(), len(s.RawText())
	s.SendLine("sanitize")
	until(t, s, "tool result text survives", `TOOL_OK .*VISIBLE`, m)
	until(t, s, "answer text survives", `ANSWER .*VISIBLE`, m)
	raw := s.RawText()[rawMark:]
	for _, sequence := range []string{"\x1b]52", "\x1b[2J"} {
		check(t, "raw PTY has no "+sequence, !strings.Contains(raw, sequence))
	}
}
