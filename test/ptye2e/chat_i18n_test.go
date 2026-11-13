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

// The English UI texts of scripts/ptysmoke/chat_i18n_en.py: the fake model's own
// answer text is English too, so a negative check ("not the Chinese one") never
// sees model output.
const (
	i18nReply = "SMOKE_EN_DONE"
	// i18nQuestion is the approval question of a shell_exec call, shared by the
	// prompt variants (with and without a scope prefix).
	i18nQuestion = `Allow shell_exec\? \[y\] Yes \[N\] No `
	// i18nTurnState is the end-of-turn status line: the same words in both
	// languages, the numbers are the localized part.
	i18nTurnState = `tokens · ctx \d+% · [\d.]+m?s · \d{2}:\d{2}`
	// i18nExitSummary is the exit summary line.
	i18nExitSummary = `turns this chat: \d+ · in \d+ / out \d+ tokens`
)

// TestChatPTYEnglishUI exercises the English catalog through the real binary,
// PTY input path and approval wiring rather than calling a renderer directly: a
// string left untranslated - or a configured language that is ignored - fails
// here. The home config carries `language: en` and the child gets no
// SUGA_LANGUAGE (see chatEnv), so the file is what drives the UI.
func TestChatPTYEnglishUI(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "tool", Delay: 500 * time.Millisecond, Reply: i18nReply,
		Command: "echo SMOKE_OK"}
	server := httptest.NewServer(fake)
	defer server.Close()

	t.Run("english home", func(t *testing.T) { englishScenario(t, binary, server.URL) })
	t.Run("config set language", func(t *testing.T) { configSetScenario(t, binary, server.URL) })
}

// englishScenario is one English chat, from the banner to the exit summary.
func englishScenario(t *testing.T, binary, url string) {
	opts := chatOpts{Home: t.TempDir(), Work: t.TempDir(), URL: url,
		ToolsExtra: "  approval: ask\n", Language: "en"}
	s, _ := startChatOpts(t, binary, opts)

	mark := s.Mark()
	until(t, s, "banner title and model in English", `Suga chat · fake/m1`, mark)
	until(t, s, "banner help line in English", `Type a task to start`, mark)
	until(t, s, "banner tool line in English", `Tools: read/write files and run shell commands`, mark)
	until(t, s, "❯ prompt", `❯ `, mark)

	// A tool call that needs a person: the question, its options and the detail
	// line are UI text.
	mark = s.Mark()
	s.SendLine("run it")
	until(t, s, "approval question in English", i18nQuestion, mark)
	until(t, s, "approval detail line in English", `Command: echo SMOKE_OK`, mark)
	check(t, "approval options are not the Chinese ones", !s.Contains(`\[y\]是`, mark))

	// A denial: the turn reports it and the tool never ran.
	mark = s.Mark()
	s.SendLine("n")
	until(t, s, "denial reported in English", `Tools denied this turn: shell_exec`, mark)
	check(t, "denial did not run the tool", !s.Contains(`⎿ ok: SMOKE_OK`, mark))
	until(t, s, "turn ends with the status line", i18nTurnState, mark)
	// The status line is the end of that turn: the next line goes in once it is
	// there, so it cannot be swallowed by the drawing of the reply.
	promptReady(t, s, mark)

	// The approving answer runs the tool and the turn answers with the fake's.
	mark = s.Mark()
	s.SendLine("run it again")
	until(t, s, "approval question asked again", i18nQuestion, mark)
	mark = s.Mark()
	s.SendLine("y")
	until(t, s, "approval runs the tool", `⎿ ok: SMOKE_OK`, mark)
	until(t, s, "turn answers", `▣ `+i18nReply, mark)
	until(t, s, "approved turn ends with the status line", i18nTurnState, mark)
	promptReady(t, s, mark)

	mark = s.Mark()
	s.SendLine(":help")
	until(t, s, "help lists the commands", `Show this session`, mark)
	until(t, s, "help summarises :usage", `Show this session's usage and cost`, mark)
	until(t, s, "help points at the per-command usage", `See a command's details`, mark)
	check(t, "help tail is not the Chinese one", !s.Contains(`查看命令详情`, mark))

	mark = s.Mark()
	s.SendLine(":context")
	until(t, s, "context header in English", `Context composition`, mark)
	until(t, s, "context tool schema line in English", `Tool schemas:`, mark)

	mark = s.Mark()
	s.SendLine(":nope")
	until(t, s, "unknown command answered in English", `Unknown command :nope`, mark)
	check(t, "unknown command is not the Chinese answer", !s.Contains(`未知命令`, mark))

	mark = s.Mark()
	s.SendLine(":quit")
	until(t, s, "exit summary in English", i18nExitSummary, mark)
	until(t, s, "exit summary shows the running time", `ran [\d.]+m?s`, mark)
	until(t, s, "resume command in English", `resume: suga chat --resume (?:sess_|sg_|s_)`, mark)
	check(t, "exit summary is not the Chinese one", !s.Expect(`本次 \d+ 轮`, mark, time.Second))
	waitExit(t, s)
}

// configSetScenario runs `suga config set language en_US` on a Chinese home: the
// CLI writes the canonical value and the next chat of that home speaks English,
// which is what a person changing the setting sees.
func configSetScenario(t *testing.T, binary, url string) {
	opts := chatOpts{Home: t.TempDir(), Work: t.TempDir(), URL: url,
		ToolsExtra: "  approval: ask\n", Language: "zh"}
	writeChatHome(t, opts)
	env := chatEnv(t, opts)

	out, errOut, code := sugaRun(t, binary, opts.Work, env, "config", "set", "language", "en_US")
	text := out + errOut
	check(t, "config set exits 0", code == 0)
	// The home was Chinese, so the CLI speaks Chinese up to this write.
	check(t, "config set reports the write in the configured language", strings.Contains(text, "已写入"))
	check(t, "config set reads the canonical value back",
		strings.Contains(text, "language = en") && strings.Contains(text, "source = config:"))
	written, err := os.ReadFile(filepath.Join(opts.Home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	check(t, "config set wrote the canonical value, not the alias",
		strings.Contains(string(written), "language: en\n") && !strings.Contains(string(written), "en_US"))

	// The alias took effect: the next chat of that home is English.
	s, _ := startChatIn(t, binary, opts.Work, 100, 30, []string{"chat"}, env)
	mark := s.Mark()
	until(t, s, "config set: the next chat speaks English", `Type a task to start`, mark)
	mark = s.Mark()
	s.SendLine(":help")
	until(t, s, "config set: :help is English", `See a command's details`, mark)
	mark = s.Mark()
	s.SendLine(":quit")
	until(t, s, "config set: exit summary is English", i18nExitSummary, mark)
	waitExit(t, s)
}
