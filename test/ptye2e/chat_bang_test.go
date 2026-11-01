//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	bangOut    = "hi"
	bangCtx    = "SMOKE_BANG_CTX"
	bangSend   = "SMOKE_BANG_DELIVERED"
	bangReply  = "完成"
	bangNotice = "本地命令结果会随下一条消息发给模型"
	bangStop   = "已终止本地命令"
)

// TestChatBang ports scripts/ptysmoke/chat_bang.py: the local commands `!cmd` and
// `!!cmd` at the prompt (suga-9os5).
//
// Python check -> Go assertion:
//
//	the command's output is shown / prompt comes back     outputLine (whole line, not the echo)
//	no request for !echo hi / !! asks the model nothing   fake model request count unchanged
//	non-zero exit code reported                           本地命令退出码 3
//	!! says the result is for the model / output shown    notice + outputLine
//	next message carries the !! result                    the request's messages hold [本地命令] with the command
//	                                                      and marker, before the person's own text
//	model answers / Ctrl-C ends the command / chat alive  reply, 已终止本地命令, prompt, a later answer
//	delivered `!echo x`: reaches / shown as [来自 cli] / answered not run / model received it
//	trace: command list, exit codes/bytes/sent/cancelled  exact list and attrs per event
//	read-only chat: runs the command, no model request, exits
func TestChatBang(t *testing.T) {
	binary := buildSuga(t)
	t.Run("local commands", func(t *testing.T) {
		fake := &fakeLLM{Mode: "text", Delay: 300 * time.Millisecond, Reply: bangReply}
		server := httptest.NewServer(fake)
		defer server.Close()
		ws := t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: ws, URL: server.URL, ToolsExtra: "  approval: auto\n"})

		m := s.Mark()
		until(t, s, "❯ prompt", `❯ `, m)

		// 1. "!cmd": the command runs here, the model hears nothing of it.
		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine("!echo " + bangOut)
		outputLine(t, s, "the command's output is shown", bangOut, m)
		promptReady(t, s, m)
		time.Sleep(500 * time.Millisecond)
		check(t, "no request for !echo hi", len(fake.Requests()) == 0)

		// 2. A non-zero exit code is reported.
		m = s.Mark()
		s.SendLine("!exit 3")
		until(t, s, "the non-zero exit code is reported", `本地命令退出码 3`, m)

		// 3. "!!cmd": runs, and the result goes to the model with the next message.
		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine("!!echo " + bangCtx)
		until(t, s, "a !! command says the result is for the model", bangNotice, m)
		outputLine(t, s, "the !! command's output is shown", bangCtx, m)
		time.Sleep(500 * time.Millisecond)
		check(t, "the !! command asks the model nothing yet", len(fake.Requests()) == 0)
		promptReady(t, s, m)
		before := len(fake.Requests())
		m = s.Mark()
		s.SendLine("你好")
		req, ok := waitRequest(fake, before, func(r fakeRequest) bool { return strings.Contains(r.User, "你好") }, 15*time.Second)
		localAt, personAt := strings.Index(req.Messages, "[本地命令]"), strings.LastIndex(req.Messages, "你好")
		check(t, "the next message carries the !! result",
			ok && localAt >= 0 && localAt < personAt && strings.Contains(req.Messages[localAt:personAt], "echo "+bangCtx))
		if !ok || localAt < 0 {
			t.Logf("request messages: %q", req.Messages)
		}
		check(t, "the model answers the message", s.Expect(bangReply, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))

		// 4. Ctrl-C during a command ends the command, not the chat.
		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine("!sleep 30")
		time.Sleep(600 * time.Millisecond) // let the shell start, so the press ends a running command
		s.SendCtrlC()
		until(t, s, "Ctrl-C ends the command", bangStop, m)
		promptReady(t, s, m)
		check(t, "Ctrl-C keeps chat alive", s.Code() == nil)
		m = s.Mark()
		s.SendLine("你好")
		check(t, "chat still answers after the kill", s.Expect(bangReply, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))

		// 5. A delivered "!cmd" is an ordinary message: shown and answered, nothing runs.
		sid := chatSessionID.FindStringSubmatch(s.Text())
		if check2(t, "session id", sid != nil) {
			promptReady(t, s, m)
			m = s.Mark()
			before = len(fake.Requests())
			_, stderr, code := sugaRun(t, binary, ws, env, "send", "--session", sid[1], "!echo "+bangSend)
			check(t, "the delivered text reaches the chat", code == 0 && strings.TrimSpace(stderr) == "delivered")
			until(t, s, "the delivered text is shown as a message", `\[来自 cli\] !echo `+bangSend, m)
			check(t, "the delivered text is answered, not run", s.Expect(bangReply, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
			_, received := waitRequest(fake, before, func(r fakeRequest) bool { return strings.HasSuffix(r.User, "!echo "+bangSend) }, 10*time.Second)
			check(t, "the model received the delivered text", received)
			// Nothing ran: the marker only appears inside the shown message line,
			// never as a line of its own (a command's output).
			check(t, "the delivered text ran no command", !s.Contains(`(?m)^`+bangSend+`\r*$`, m))
		}

		exitChat(t, s)
		if sid == nil {
			return
		}
		events := traceEvents(t, binary, ws, env, sid[1])
		var cmds []string
		var local []map[string]any
		for _, e := range events {
			if e["kind"] == "local_command" {
				cmds = append(cmds, str(attrs(e)["command"]))
				local = append(local, attrs(e))
			}
		}
		want := []string{"echo " + bangOut, "exit 3", "echo " + bangCtx, "sleep 30"}
		if !check2(t, "trace: one local_command event per command that ran", strings.Join(cmds, "|") == strings.Join(want, "|")) {
			t.Logf("local_command events: %v", cmds)
			return
		}
		bytes0, _ := local[0]["bytes"].(float64)
		_, sent0 := local[0]["sent"]
		check(t, "trace: a plain command records its exit code and bytes", local[0]["exit_code"] == float64(0) && bytes0 > 0 && !sent0)
		check(t, "trace: the failed command records exit 3", local[1]["exit_code"] == float64(3))
		check(t, "trace: the !! command is marked as sent to the model", local[2]["sent"] == true)
		check(t, "trace: the killed command is marked cancelled", local[3]["cancelled"] == true && local[3]["exit_code"] != float64(0))
	})

	// A --read-only chat runs a local command too: the mode limits the model's
	// tools, not the person's own shell.
	t.Run("read-only chat", func(t *testing.T) {
		fake := &fakeLLM{Mode: "text", Delay: 300 * time.Millisecond, Reply: bangReply}
		server := httptest.NewServer(fake)
		defer server.Close()
		s, _ := startChatOpts(t, binary, chatOpts{
			Home: t.TempDir(), Work: t.TempDir(), URL: server.URL, ToolsExtra: "  approval: auto\n", Args: []string{"--read-only"},
		})
		m := s.Mark()
		until(t, s, "read-only chat: ❯ prompt", `❯ `, m)
		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine("!echo " + bangOut)
		outputLine(t, s, "read-only chat: the local command runs", bangOut, m)
		promptReady(t, s, m)
		time.Sleep(500 * time.Millisecond)
		check(t, "read-only chat: no model request", len(fake.Requests()) == 0)
		exitChat(t, s)
	})
}
