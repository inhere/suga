//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChatDanger ports scripts/ptysmoke/chat_danger.py: with tools.approval:
// auto a variable-led recursive delete is still confirmed, with the danger
// shown; "y" is not enough (refused, the scratch home survives), "yes" runs it.
// SCRATCH points at a scratch directory, the only thing the command can delete.
//
// Python check -> Go assertion:
//
//	❯ prompt                                     expect
//	auto mode still asks, danger shown           DANGER and PROMPT patterns
//	y is not enough: refused / scratch home kept 用户拒绝执行危险删除 + tokens, dir exists
//	asked again                                  PROMPT
//	yes: executed / scratch home removed         ⎿ ok + tokens, dir gone
//	exit: resume command / session id            ctrlCTwice
//	trace: two shell_exec events / refused call is a denial / danger and user_denied / run call danger and approval=user
func TestChatDanger(t *testing.T) {
	binary := buildSuga(t)
	// PowerShell runs shell_exec on Windows; its variable is $env:SCRATCH.
	command := osCommand(`rm -rf "$SCRATCH/"`, `Remove-Item -Recurse -Force "$env:SCRATCH/"`)
	variable := osCommand("$SCRATCH", "$env:SCRATCH")
	danger := osCommand(`⚠ 危险删除：rm -rf \$SCRATCH/：路径以变量 \$SCRATCH 开头`,
		`⚠ 危险删除：.+：路径以变量 \$env:SCRATCH 开头`)
	const prompt = `输入 yes 执行`

	server := httptest.NewServer(&fakeLLM{Mode: "tool", Delay: 200 * time.Millisecond, Command: command})
	defer server.Close()
	home, work, scratch := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "fakehome")
	if err := os.MkdirAll(filepath.Join(scratch, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, env := startChatOpts(t, binary, chatOpts{
		Home: home, Work: work, URL: server.URL, ToolsExtra: "  approval: auto\n",
		SetEnv: []string{"HOME=" + home, "SCRATCH=" + scratch},
	})

	m := s.Mark()
	until(t, s, "❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("删一下")
	check(t, "auto mode still asks, danger shown", s.Expect(danger, m, 15*time.Second) && s.Expect(prompt, m, 15*time.Second))
	s.SendLine("y")
	check(t, "y is not enough: refused", s.Expect(`用户拒绝执行危险删除`, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
	info, err := os.Stat(scratch)
	check(t, "y: scratch home kept", err == nil && info.IsDir())
	m = s.Mark()
	s.SendLine("再删一次")
	until(t, s, "asked again", prompt, m)
	s.SendLine("yes")
	check(t, "yes: executed", s.Expect(`⎿ ok`, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
	_, err = os.Stat(scratch)
	check(t, "yes: scratch home removed", os.IsNotExist(err))

	session := ctrlCTwice(t, s)
	check(t, "session id", session != "")
	if session == "" {
		return
	}
	var calls []map[string]any
	for _, e := range traceEvents(t, binary, work, env, session) {
		if e["name"] == "shell_exec" {
			calls = append(calls, e)
		}
	}
	check(t, "trace: two shell_exec events", len(calls) == 2)
	if len(calls) == 2 {
		denied, ran := attrs(calls[0]), attrs(calls[1])
		deniedDanger, _ := denied["danger"].(string)
		ranDanger, _ := ran["danger"].(string)
		check(t, "trace: refused call is a denial", calls[0]["kind"] == "denial")
		check(t, "trace: refused call has danger and user_denied",
			strings.Contains(deniedDanger, variable) && denied["approval"] == "user_denied")
		check(t, "trace: run call has danger and approval=user",
			strings.Contains(ranDanger, variable) && ran["approval"] == "user")
	}
}
