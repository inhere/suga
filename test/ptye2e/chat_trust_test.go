//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestChatTrust covers `suga chat --approve trust` end to end in a pty: the
// banner names the tier, an explicit ask rule is released without a question
// and traced as approval=trust, a variable-led delete is not confirmed with
// "yes" any more, a protected path (.git) is still asked about, and the trace
// records approval_mode on the turn_context event.
//
//	banner / ask rule released / approval=trust / danger not asked / .git asked / approval_mode attr
func TestChatTrust(t *testing.T) {
	binary := buildSuga(t)

	t.Run("banner and released ask rule", func(t *testing.T) {
		r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: "echo TRUST_ASK", Delay: 100 * time.Millisecond}, "")
		r.writeRules("  - tool: shell_exec\n    command: echo TRUST_ASK\n    action: ask\n")
		s, env := startChatOpts(t, binary, chatOpts{
			Home: r.home, Work: r.work, URL: r.url, ToolsExtra: rulesTools, Args: []string{"--approve", "trust"},
		})
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		check(t, "banner names the trust tier", s.Contains(`审批档位：trust`, mark))
		s.SendLine("run")
		answersWithoutQuestion(t, s, "released ask rule", mark)
		session := exitAfterTurn(t, s, mark)
		check(t, "session id", session != "")
		if session == "" {
			return
		}
		events := traceEvents(t, binary, r.work, env, session)
		call := findEvent(events, "tool", "shell_exec")
		check(t, "trace: shell_exec ran with approval=trust", call != nil && attrs(call)["approval"] == "trust")
		ctx := findEvent(events, "turn_context", "")
		check(t, "trace: turn_context has approval_mode=trust", ctx != nil && attrs(ctx)["approval_mode"] == "trust")
	})

	t.Run("a default-allowed write is traced as approval=trust", func(t *testing.T) {
		server := httptest.NewServer(&fakeLLM{Mode: "adddir", WritePath: "trust-note.txt", Delay: 100 * time.Millisecond})
		defer server.Close()
		home, work := t.TempDir(), t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, Args: []string{"--approve", "trust"}})
		m := s.Mark()
		until(t, s, "❯ prompt", `❯ `, m)
		s.SendLine("写文件")
		check(t, "default write completes without a question", s.Expect(`tokens · `, m, 30*time.Second) && !s.Contains(`允许执行 file_write`, m))
		session := ctrlCTwice(t, s)
		check(t, "session id", session != "")
		if session == "" {
			return
		}
		call := findEvent(traceEvents(t, binary, work, env, session), "tool", "file_write")
		check(t, "trace: file_write has approval=trust", call != nil && attrs(call)["approval"] == "trust")
	})

	t.Run("the same ask rule asks without the flag", func(t *testing.T) {
		r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: "echo TRUST_ASK", Delay: 100 * time.Millisecond}, "")
		r.writeRules("  - tool: shell_exec\n    command: echo TRUST_ASK\n    action: ask\n")
		s := r.start()
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		check(t, "no trust banner line", !s.Contains(`审批档位`, mark))
		s.SendLine("run")
		check(t, "ask rule asks", s.Expect(ruleQuestion, mark, 15*time.Second))
		s.SendLine("n")
		exitAfterTurn(t, s, mark)
	})

	t.Run("dangerous delete is not confirmed with yes", func(t *testing.T) {
		command := osCommand(`rm -rf "$SCRATCH/"`, `Remove-Item -Recurse -Force "$env:SCRATCH/"`)
		server := httptest.NewServer(&fakeLLM{Mode: "tool", Delay: 200 * time.Millisecond, Command: command})
		defer server.Close()
		home, work, scratch := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "fakehome")
		if err := os.MkdirAll(filepath.Join(scratch, "keep"), 0o700); err != nil {
			t.Fatal(err)
		}
		s, _ := startChatOpts(t, binary, chatOpts{
			Home: home, Work: work, URL: server.URL, Args: []string{"--yolo"},
			SetEnv: []string{"HOME=" + home, "SCRATCH=" + scratch},
		})
		m := s.Mark()
		until(t, s, "❯ prompt", `❯ `, m)
		s.SendLine("删一下")
		check(t, "executed without the yes prompt", s.Expect(`⎿ ok`, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
		check(t, "no yes prompt was shown", !s.Contains(`输入 yes 执行`, m))
		_, err := os.Stat(scratch)
		check(t, "scratch home removed", os.IsNotExist(err))
		ctrlCTwice(t, s)
	})

	t.Run("a protected path is still asked", func(t *testing.T) {
		server := httptest.NewServer(&fakeLLM{Mode: "adddir", WritePath: ".git/trust-probe", Delay: 100 * time.Millisecond})
		defer server.Close()
		home, work := t.TempDir(), t.TempDir()
		s, _ := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, Args: []string{"--approve", "trust"}})
		m := s.Mark()
		until(t, s, "❯ prompt", `❯ `, m)
		s.SendLine("写 .git")
		check(t, "protected path asks", s.Expect(`允许执行 file_write`, m, 15*time.Second))
		s.SendLine("n")
		check(t, "refused: the file does not exist", s.Expect(`tokens · `, m, 30*time.Second))
		_, err := os.Stat(filepath.Join(work, ".git", "trust-probe"))
		check(t, "nothing was written", os.IsNotExist(err))
		ctrlCTwice(t, s)
	})
}
