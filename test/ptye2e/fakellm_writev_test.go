//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestFakeLLMWriteByContent is the self-test of the rewind plan T0 test
// facilities: "write v1" / "write v2" turns write the file by the content of the
// question, and the request log shows the earlier question in the second request.
func TestFakeLLMWriteByContent(t *testing.T) {
	binary := buildSuga(t)
	work := t.TempDir()
	server := httptest.NewServer(&fakeLLM{Mode: "writev", Delay: 100 * time.Millisecond, WritePath: "w.txt"})
	defer server.Close()
	fake := server.Config.Handler.(*fakeLLM)
	s, _ := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: work, URL: server.URL, ToolsExtra: "  approval: auto\n"})
	target := filepath.Join(work, "w.txt")

	m := s.Mark()
	until(t, s, "prompt", `❯ `, m)
	s.SendLine("please write v1")
	until(t, s, "turn 1 done", `tokens · `, m)
	check(t, "file is v1", readFile(target) == "v1")

	m2 := s.Mark() // after turn 1's footer, so turn 2 has its own
	s.SendLine("please write v2")
	until(t, s, "turn 2 done", `tokens · `, m2)
	check(t, "file is v2", readFile(target) == "v2")

	// Requests per turn: the question, then the one after the tool result.
	check(t, "four requests", len(fake.Requests()) == 4)
	requireRequestText(t, fake, 1, "write v1", true)
	requireRequestText(t, fake, 1, "write v2", false)
	requireRequestText(t, fake, 3, "write v1", true) // the 2nd turn's first request carries turn 1
	requireRequestText(t, fake, 3, "write v2", true)
	requireRequestText(t, fake, 9, "write v1", false)
}
