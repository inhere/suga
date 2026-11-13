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

const (
	// memoryQuestion is the ask a memory write raises in a tainted session.
	memoryQuestion = `写入记忆？\[y\]是 \[N\]否 \[a\]本会话不再问`
	// memorySaved is the note a written entry leaves in the transcript.
	memorySaved = `已记住：Smoke rule（project）`
)

// TestChatMemory ports scripts/ptysmoke/chat_memory.py: memory writes in chat.
// The session runs with tools.approval: auto and memory.taint_tools: [shell_exec],
// so a shell call taints it and the next write asks first.
//
// Python check -> Go assertion:
//
//	banner                                  until "Suga chat · fake/m1"
//	clean: written                          until 已记住：Smoke rule（project）
//	clean: no question                      !Contains the question
//	tainted: shell ran                      until ⎿ ok: SMOKE_OK
//	tainted: question / tainted: details    until the question; Contains the taint,
//	                                        scope, kind, title and body lines
//	y: written                              until 已记住…; y: turn ends
//	y|n|a: prompt row is ❯ / typed text is
//	  a prompt line                         answeredPrompt (ptydrive check_answered_prompt)
//	n: refused / n: nothing remembered      until 用户拒绝写入记忆; !Contains 已记住…
//	a: written / a: written again            until 已记住… twice; a: no question again
//	ctrl-c once: hint / twice: exit          ctrlCTwice
//	disk: entries in SUGA_HOME              MEMORY.md carries "Smoke rule" four times
func TestChatMemory(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "memory", Delay: 300 * time.Millisecond})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	o := chatOpts{
		Home: home, Work: work, URL: server.URL,
		ToolsExtra:  "  approval: auto\n",
		MemoryExtra: "  taint_tools: [shell_exec]\n",
	}
	s, _ := startChatOpts(t, binary, o)

	m := s.Mark()
	until(t, s, "banner", `Suga chat · fake/m1`, m)
	until(t, s, "❯ prompt", `❯ `, m)

	m = memoryStep(t, s, "直接记一下", m)
	until(t, s, "clean: written", memorySaved, m)
	until(t, s, "clean: turn ends", `tokens · `, m)
	check(t, "clean: no question", !s.Contains(memoryQuestion, m))

	m = memoryStep(t, s, "跑一下再记", m)
	until(t, s, "tainted: shell ran", `⎿ ok: SMOKE_OK`, m)
	until(t, s, "tainted: question", memoryQuestion, m)
	check(t, "tainted: details", s.Contains(`会话读过外部内容`, m) &&
		s.Contains(`范围：project:`, m) && s.Contains(`类型：feedback`, m) &&
		s.Contains(`标题：Smoke rule`, m) && s.Contains(`正文：smoke memory body`, m))
	s.SendLine("y")
	until(t, s, "y: written", memorySaved, m)
	until(t, s, "y: turn ends", `tokens · `, m)
	// The answered question's turn ends like a no-question one (suga-9ew7).
	answeredPrompt(t, s, "y")

	m = memoryStep(t, s, "跑一下再记", m)
	until(t, s, "n: question", memoryQuestion, m)
	s.SendLine("n")
	// The refusal is a long line the terminal wraps, so it is read off the screen
	// with the wrap joined again.
	untilScreen(t, s, chatCols, "n: refused", `用户拒绝写入记忆`)
	until(t, s, "n: turn ends", `tokens · `, m)
	check(t, "n: nothing remembered", !s.Contains(memorySaved, m))
	// Same after the refused one (suga-9ew7).
	answeredPrompt(t, s, "n")

	m = memoryStep(t, s, "跑一下再记", m)
	until(t, s, "a: question", memoryQuestion, m)
	s.SendLine("a")
	until(t, s, "a: written", memorySaved, m)
	until(t, s, "a: turn ends", `tokens · `, m)
	// Same after the "a" answer (suga-9ew7).
	answeredPrompt(t, s, "a")
	m = memoryStep(t, s, "跑一下再记", m)
	until(t, s, "a: written again", memorySaved, m)
	until(t, s, "a: turn ends again", `tokens · `, m)
	check(t, "a: no question again", !s.Contains(memoryQuestion, m))

	session := ctrlCTwice(t, s)
	check(t, "session id", session != "")

	// Four writes reached the files backend in SUGA_HOME (clean, y, a and the one
	// after a); the refused one did not.
	index, err := filepath.Glob(filepath.Join(home, "projects", "*", "memory", "MEMORY.md"))
	body := ""
	if err == nil && len(index) > 0 {
		raw, _ := os.ReadFile(index[0])
		body = string(raw)
	}
	check(t, "disk: entries in SUGA_HOME", strings.Count(body, "Smoke rule") == 4)
}

// memoryStep waits for the prompt after prev, sends one line and returns a mark
// taken before it: the checks then only see the output of this line.
func memoryStep(t *testing.T, s *ptytest.Session, line string, prev int) int {
	t.Helper()
	promptReady(t, s, prev)
	m := s.Mark()
	s.SendLine(line)
	return m
}

// answeredPrompt checks the input row after a turn whose question was answered
// (suga-9ew7): the row must hold the prompt alone, and the text typed next is a
// line of its own - without the repaint it kept the question's width. It is
// ptydrive.check_answered_prompt, read off the screen (waitInputRow).
func answeredPrompt(t *testing.T, s *ptytest.Session, name string) {
	t.Helper()
	check(t, name+": prompt row is ❯", waitInputRow(t, s, chatCols, approvalPrompt, 15*time.Second))
	s.Send("abc")
	check(t, name+": typed text is a prompt line",
		waitInputRow(t, s, chatCols, approvalPrompt+" abc", 15*time.Second))
	s.Send("\x15") // Ctrl-U drops the line again
	time.Sleep(200 * time.Millisecond)
}
