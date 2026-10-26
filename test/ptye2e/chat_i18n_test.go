//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestChatPTYEnglishUI exercises the English catalog through the real binary,
// PTY input path and approval wiring rather than calling a renderer directly.
func TestChatPTYEnglishUI(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeChat{mode: "approval"})
	defer server.Close()

	work := t.TempDir()
	s := startChatLanguage(t, binary, server.URL, t.TempDir(), work, nil, "", "en")
	mark := s.Mark()
	expect(t, s, `Suga chat`, mark)
	expect(t, s, `Type a task to start`, mark)
	expect(t, s, `Tools: read/write files`, mark)

	mark = s.Mark()
	s.SendLine("write it")
	expect(t, s, `Allow file_write\?`, mark)
	s.SendLine("y")
	waitTurnEnd(t, s, mark)
	if body, err := os.ReadFile(filepath.Join(work, "approved.txt")); err != nil || string(body) != "APPROVED" {
		t.Fatalf("accepted write = %q, %v; terminal tail:\n%s", body, err, tail(s.Text()))
	}
	expect(t, s, `in \d+ / out \d+ tokens`, mark)

	mark = s.Mark()
	s.SendLine(":help")
	expect(t, s, `Show this help`, mark)
	expect(t, s, `Keys: Enter`, mark)

	mark = s.Mark()
	exitChatUntil(t, s, `resume: suga chat --resume`)
	expect(t, s, `turns this chat: \d+`, mark)
	expect(t, s, `resume: suga chat --resume`, mark)
}
