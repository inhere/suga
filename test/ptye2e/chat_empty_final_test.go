//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestChatEmptyFinal ports scripts/ptysmoke/chat_empty_final.py: the fake model
// writes a file, then answers every resample with an empty streamed completion.
// Chat finishes normally without an empty "▣ " answer line, and the turn and
// final model trace events carry empty_final.
//
// Python check -> Go assertion:
//
//	prompt / empty final completes / empty final has no answer marker
//	exit: resume command / session id
//	trace model marks empty final / trace turn marks empty final
func TestChatEmptyFinal(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "empty-final", Delay: 200 * time.Millisecond, WritePath: "a.txt", WriteBody: "alpha"})
	defer server.Close()
	work := t.TempDir()
	s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: work, URL: server.URL, ToolsExtra: "  approval: auto\n"})

	m := s.Mark()
	until(t, s, "prompt", `❯ `, m)
	s.SendLine("只写文件")
	until(t, s, "empty final completes", `tokens · `, m)
	check(t, "empty final has no answer marker", !s.Contains(`▣ \r?\n`, m))
	time.Sleep(300 * time.Millisecond)
	session := ctrlCTwice(t, s)
	check(t, "session id", session != "")
	if session == "" {
		return
	}
	marked := func(kind string) bool {
		for _, e := range traceEvents(t, binary, work, env, session) {
			if e["kind"] == kind && attrs(e)["empty_final"] == true {
				return true
			}
		}
		return false
	}
	check(t, "trace model marks empty final", marked("model"))
	check(t, "trace turn marks empty final", marked("turn"))
}
