//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// Steering: inserting a message into a running turn, and ending one turn only.
// Ported from scripts/ptysmoke/chat_steer.py. chat.steering (on by default) keeps
// the input line available while a turn runs: Enter inserts a message into the
// running turn, Esc or a single Ctrl-C end this turn only.
const (
	// steerReply is the model's answer: short, one chunk, and not a word any
	// prompt of these scenarios uses.
	steerReply = "STEER_OK"
	// steerAuto answers every approval question, except in the approval scenario
	// (the question is the subject there, so its config keeps the default).
	steerAuto = "  approval: auto\n"
	// steerStarted is the file the long command writes first: a tool's output only
	// reaches chat when the tool ends, so the file is what tells it is running.
	steerStarted = "steer-tool-started"
	// The two prompts: the person's own (❯) and the one a running turn keeps
	// (↳, the steer prompt).
	steerPrompt = "↳ "
	// What chat says about a message inserted into the running turn (steering.go).
	steerWaiting   = "（等待送达）"
	steerTaken     = "（已送达）"
	steerWithdrawn = "（已撤回）"
)

// TestChatSteer ports the scenario scripts of scripts/ptysmoke/chat_steer.py; the
// doc comment of each subtest names the checks it carries.
func TestChatSteer(t *testing.T) {
	binary := buildSuga(t)
	t.Run("same-turn", func(t *testing.T) { steerSameTurn(t, binary) })
	t.Run("paste", func(t *testing.T) { steerPaste(t, binary) })
	t.Run("next-turn", func(t *testing.T) { steerNextTurn(t, binary) })
	t.Run("followup", func(t *testing.T) { steerFollowup(t, binary) })
	t.Run("abort", func(t *testing.T) { steerAbort(t, binary) })
}

// steerToolCommand is the fake model's shell_exec: write the marker the scenario
// waits for, sleep, then print the result the checks look for.
func steerToolCommand(seconds int) string {
	return osCommand(
		fmt.Sprintf("touch %s; sleep %d; echo TOOL_DONE", steerStarted, seconds),
		fmt.Sprintf("New-Item -ItemType File -Path %s | Out-Null; Start-Sleep %d; echo TOOL_DONE", steerStarted, seconds))
}

// steerChatOpts is the config the steer scenarios share: the fake provider as the
// only one, the approval override (or "" for the approval scenario) and the
// scenario's own extras.
func steerChatOpts(home, ws, url, extra string) chatOpts {
	return chatOpts{Home: home, Work: ws, URL: url, ToolsExtra: steerAuto, Extra: extra}
}

// startSteer writes the home and starts chat in a pty of an explicit size: the
// input line and the status row are read off a screen model of that width.
func startSteer(t *testing.T, binary string, o chatOpts, cols, rows int) (*ptytest.Session, []string) {
	t.Helper()
	writeChatHome(t, o)
	env := chatEnv(t, o)
	return startChatIn(t, binary, o.Work, cols, rows, []string{"chat"}, env)
}

// steerNotice is the line chat shows for an inserted message (steering.go), as a
// pattern: [插话] <text><suffix>.
func steerNotice(text, suffix string) string {
	return regexp.QuoteMeta("[插话] " + text + suffix)
}

// untilWithin is until with a timeout of its own (a tool that sleeps longer than
// the usual 15s check allows for).
func untilWithin(t *testing.T, s *ptytest.Session, name, pattern string, mark int, timeout time.Duration) bool {
	t.Helper()
	ok := s.Expect(pattern, mark, timeout)
	check(t, name, ok)
	if !ok {
		t.Logf("%s: text tail %q", name, tail(s.Since(mark)))
	}
	return ok
}

// waitRequests waits until the fake model answered n requests and returns them
// (as many as arrived when the wait times out).
func waitRequests(f *fakeLLM, n int, timeout time.Duration) []fakeRequest {
	end := time.Now().Add(timeout)
	for {
		reqs := f.Requests()
		if len(reqs) >= n || time.Now().After(end) {
			return reqs
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// steerRoles is the roles of a request's messages, in order.
func steerRoles(req fakeRequest) []string {
	roles := make([]string, 0, len(req.Msgs))
	for _, m := range req.Msgs {
		roles = append(roles, m.Role)
	}
	return roles
}

// steerUserTexts is the text of every user message of a request, in order.
func steerUserTexts(req fakeRequest) []string {
	var texts []string
	for _, m := range req.Msgs {
		if m.Role == "user" {
			texts = append(texts, m.Text)
		}
	}
	return texts
}

// hasRole reports whether roles holds want.
func hasRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

// steerEvents lists the trace events of the kind (and of the name, when it is
// not empty).
func steerEvents(events []map[string]any, kind, name string) []map[string]any {
	var found []map[string]any
	for _, e := range events {
		if e["kind"] == kind && (name == "" || str(e["name"]) == name) {
			found = append(found, e)
		}
	}
	return found
}

// steerExit leaves chat, checks that its resume line named a session and returns
// that session's trace events (nil when it named none). Call it last: chat is
// gone afterwards.
func steerExit(t *testing.T, s *ptytest.Session, binary, ws string, env []string) []map[string]any {
	t.Helper()
	mark := s.Mark()
	exitChat(t, s)
	session := sessionFromText(s.Since(mark))
	if !check2(t, "session id", session != "") {
		return nil
	}
	return traceEvents(t, binary, ws, env, session)
}

// steerSameTurn: a message typed while a tool runs reaches the *same* turn.
//
//	❯ prompt / tool call shown / ↳ while the turn runs   until(❯ ) / until($ shell_exec) / until(↳ )
//	tool started (marker file)                           the marker file exists
//	the message is shown as waiting                      until([插话] 只改 README（等待送达）)
//	tool result shown / shown as delivered / answer      until(⎿ ok: TOOL_DONE) / (已送达) / STEER_OK
//	the end-of-turn line counts it                       until(插话 1)
//	two requests, the first without it                   2 requests; no 只改 README in request 1
//	the second carries it after the tool result          last user message is 只改 README, [.., tool, user]
//	one turn only / the trace records the delivery       1 turn event; 1 steer "delivered"
//	it names the keyboard source                         attrs.source == keyboard
func steerSameTurn(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(4)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, env := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), 100, 40)

	m := s.Mark()
	until(t, s, "same-turn: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("跑一个长命令")
	until(t, s, "same-turn: tool call shown", `\$ shell_exec`, m)
	until(t, s, "same-turn: the prompt becomes ↳ while the turn runs", steerPrompt, m)
	if !check2(t, "same-turn: tool started (marker file)", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}

	m = s.Mark()
	s.SendLine("只改 README")
	until(t, s, "same-turn: the message is shown as waiting", steerNotice("只改 README", steerWaiting), m)
	until(t, s, "same-turn: tool result shown", `⎿ ok: TOOL_DONE`, m)
	until(t, s, "same-turn: shown as delivered", steerNotice("只改 README", steerTaken), m)
	until(t, s, "same-turn: the turn answers", regexp.QuoteMeta(steerReply), m)
	until(t, s, "same-turn: the end-of-turn line counts it", `插话 1`, m)

	reqs := waitRequests(fake, 2, 30*time.Second)
	check(t, "same-turn: two requests", len(reqs) >= 2)
	if len(reqs) >= 2 {
		check(t, "same-turn: the first request has no inserted message",
			!strings.Contains(strings.Join(steerUserTexts(reqs[0]), " "), "只改 README"))
		roles := steerRoles(reqs[1])
		check(t, "same-turn: the second request carries it after the tool result",
			reqs[1].User == "只改 README" && hasRole(roles, "tool") && roles[len(roles)-1] == "user")
	}

	events := steerExit(t, s, binary, ws, env)
	check(t, "same-turn: one turn only", len(steerEvents(events, "turn", "")) == 1)
	delivered := steerEvents(events, "steer", "delivered")
	check(t, "same-turn: the trace records the delivery", len(delivered) == 1)
	check(t, "same-turn: it names the keyboard source",
		len(delivered) == 1 && str(attrs(delivered[0])["source"]) == "keyboard")
}

// steerPaste: two lines pasted into the running turn become ONE inserted message.
//
//	bracketed paste is on for the turn                  raw output after the line: ESC[?2004h
//	both lines are on the input line                     raw output: 第一行\r*\n第二行
//	the paste submits nothing / no request carried it     no [插话] ; still 1 request after 0.3s
//	the message is shown as waiting                      until([插话] 第一行 第二行（等待送达）)
//	tool result shown / delivered / answer / counts one  until(⎿ ok: TOOL_DONE) / (已送达) / answer / 插话 1
//	two requests, the first without it                   2 requests; no 第一行 in request 1
//	the second carries both lines as one message         last user == "第一行 第二行", [.., tool, user]
//	the trace records one delivery / keyboard source    1 steer "delivered", source keyboard
func steerPaste(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(10)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, env := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), 100, 40)

	m := s.Mark()
	until(t, s, "paste: ❯ prompt", `❯ `, m)
	m, rawMark := s.Mark(), len(s.RawText())
	s.SendLine("跑一个长命令")
	until(t, s, "paste: tool call shown", `\$ shell_exec`, m)
	until(t, s, "paste: the prompt becomes ↳ while the turn runs", steerPrompt, m)
	// The turn keeps its line editable and bracketed: the prompt read asked for the
	// paste markers and turned them off when it ended, so the marker after the line
	// above was submitted is the turn's own read asking again.
	check(t, "paste: bracketed paste is on for the turn",
		rawContains(s, "\\x1b\\[\\?2004h", rawMark, 15*time.Second))
	if !check2(t, "paste: tool started (marker file)", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}

	// A two-line paste into the running turn: one piece of text on the input line
	// and no submit at its line break.
	m, rawMark = s.Mark(), len(s.RawText())
	s.Send("\x1b[200~第一行\n第二行\x1b[201~")
	check(t, "paste: both lines are on the input line", rawContains(s, "第一行\r*\n第二行", rawMark, 15*time.Second))
	check(t, "paste: the paste submits nothing", !s.Contains(`\[插话\]`, m))
	time.Sleep(300 * time.Millisecond)
	check(t, "paste: no request carried it while it waited", len(fake.Requests()) == 1)

	// The person's own Enter sends both lines as ONE inserted message.
	m = s.Mark()
	s.Send("\r")
	until(t, s, "paste: the message is shown as waiting", steerNotice("第一行 第二行", steerWaiting), m)
	until(t, s, "paste: tool result shown", `⎿ ok: TOOL_DONE`, m)
	until(t, s, "paste: shown as delivered", steerNotice("第一行 第二行", steerTaken), m)
	until(t, s, "paste: the turn answers", regexp.QuoteMeta(steerReply), m)
	until(t, s, "paste: the end-of-turn line counts one message", `插话 1`, m)

	reqs := waitRequests(fake, 2, 30*time.Second)
	check(t, "paste: two requests", len(reqs) >= 2)
	if len(reqs) >= 2 {
		check(t, "paste: the first request has no inserted message",
			!strings.Contains(strings.Join(steerUserTexts(reqs[0]), " "), "第一行"))
		// The fake model keeps the pasted text as one message, its line break
		// included: the letters of both lines, one user message, after the tool.
		roles := steerRoles(reqs[1])
		check(t, "paste: the second request carries both lines as one message",
			strings.Join(strings.Fields(reqs[1].User), " ") == "第一行 第二行" && hasRole(roles, "tool") && roles[len(roles)-1] == "user")
	}

	events := steerExit(t, s, binary, ws, env)
	delivered := steerEvents(events, "steer", "delivered")
	check(t, "paste: the trace records one delivery", len(delivered) == 1)
	check(t, "paste: it names the keyboard source",
		len(delivered) == 1 && str(attrs(delivered[0])["source"]) == "keyboard")
}

// steerNextTurn: a message that arrives while the model answers becomes the next
// turn (design D3).
//
//	❯ prompt / ↳ while the model answers    until(❯ ) / until(↳ )
//	the message is shown as waiting          until([插话] 改成 README（等待送达）)
//	the first answer                         until(STEER_OK)
//	two requests / the first without it      2 requests; no 改成 README in request 1
//	the next turn asks the message           request 2's user message == 改成 README
//	chat is back at the prompt               until(❯ )
//	two turns / the trace records the return 2 turn events; 1 steer "returned"
//	completed and untaken / handed on        attrs reason completed_untaken, disposition next_turn
func steerNextTurn(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "text", Delay: 3 * time.Second, Reply: steerReply, Command: steerToolCommand(4)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, env := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), 100, 40)

	m := s.Mark()
	until(t, s, "next-turn: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("先看看")
	until(t, s, "next-turn: the prompt becomes ↳ while the model answers", steerPrompt, m)
	// The model has been asked: the turn started and its inbox is open (a message
	// typed into the first instant is not taken, design m8). Waiting for the
	// request is what the sleep of the Python script stood for - the arrival, not
	// the answer, which would already be the end of the turn.
	check(t, "next-turn: the model is asked", fake.WaitArrived(1, 15*time.Second))
	s.SendLine("改成 README")
	until(t, s, "next-turn: the message is shown as waiting", steerNotice("改成 README", steerWaiting), m)
	until(t, s, "next-turn: the first answer", regexp.QuoteMeta(steerReply), m)

	reqs := waitRequests(fake, 2, 30*time.Second)
	check(t, "next-turn: two requests", len(reqs) >= 2)
	if len(reqs) >= 2 {
		check(t, "next-turn: the first request has no inserted message",
			!strings.Contains(strings.Join(steerUserTexts(reqs[0]), " "), "改成 README"))
		check(t, "next-turn: the next turn asks the message", reqs[1].User == "改成 README")
	}
	until(t, s, "next-turn: the second turn ends", `tokens · `, m)
	promptReady(t, s, m)

	events := steerExit(t, s, binary, ws, env)
	check(t, "next-turn: two turns", len(steerEvents(events, "turn", "")) == 2)
	returned := steerEvents(events, "steer", "returned")
	check(t, "next-turn: the trace records the return", len(returned) == 1)
	if len(returned) == 1 {
		check(t, "next-turn: completed and untaken", str(attrs(returned[0])["reason"]) == "completed_untaken")
		check(t, "next-turn: handed to the next turn", str(attrs(returned[0])["disposition"]) == "next_turn")
	}
}

// steerFollowup: Tab on the line typed during a turn queues it for the turn after
// it (suga-8pug).
//
//	❯ prompt / ↳ while the model answers    until(❯ ) / until(↳ )
//	the queued line is shown                 until([排队] 改成 README（本轮结束后发送）)
//	nothing is inserted                      no [插话] on screen
//	the first answer / shown as sent         until(STEER_OK) / until([排队] 改成 README（已发送）)
//	two requests / the first without it      2 requests; no 改成 README in request 1
//	the next turn asks the queued line       request 2's user message == 改成 README
//	chat is back at the prompt               until(❯ )
//	two turns / no steer in the trace        2 turn events; no steer event at all
func steerFollowup(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "text", Delay: 3 * time.Second, Reply: steerReply, Command: steerToolCommand(4)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, env := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), 100, 40)

	m := s.Mark()
	until(t, s, "followup: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("先看看")
	until(t, s, "followup: the prompt becomes ↳ while the model answers", steerPrompt, m)
	check(t, "followup: the model is asked", fake.WaitArrived(1, 15*time.Second))

	m = s.Mark()
	s.Send("改成 README")
	s.Send("\t")
	until(t, s, "followup: the queued line is shown", regexp.QuoteMeta("[排队] 改成 README（本轮结束后发送）"), m)
	check(t, "followup: nothing is inserted", !s.Contains(`\[插话\]`, m))
	until(t, s, "followup: the first answer", regexp.QuoteMeta(steerReply), m)
	until(t, s, "followup: the queued line is shown as sent", regexp.QuoteMeta("[排队] 改成 README（已发送）"), m)

	reqs := waitRequests(fake, 2, 30*time.Second)
	check(t, "followup: two requests", len(reqs) >= 2)
	if len(reqs) >= 2 {
		check(t, "followup: the first request has no queued message",
			!strings.Contains(strings.Join(steerUserTexts(reqs[0]), " "), "改成 README"))
		check(t, "followup: the next turn asks the queued line", reqs[1].User == "改成 README")
	}
	until(t, s, "followup: the queued turn ends", `tokens · `, m)
	promptReady(t, s, m)

	events := steerExit(t, s, binary, ws, env)
	check(t, "followup: two turns", len(steerEvents(events, "turn", "")) == 2)
	check(t, "followup: a queued line is no inserted message", len(steerEvents(events, "steer", "")) == 0)
}

// steerAbort: Esc (and a single Ctrl-C) end this turn only; the inserted message
// goes back to the input line.
//
//	❯ prompt / tool call shown / tool started   until(❯ ) / until($ shell_exec) / marker file
//	the message is shown as waiting             until([插话] 别改了（等待送达）)
//	esc ends this turn only                     until(已中断本轮，1 条插话已放回输入行)
//	chat stays after esc / message is back      no 续接 within the mark; until(别改了)
//	nothing was run on its own                   still 1 request after 0.5s
//	Enter runs the message                      request 2's user message == 别改了
//	the next turn calls the tool again          until($ shell_exec)
//	one ctrl-c ends this turn only              until(已中断本轮); no 续接
func steerAbort(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(60)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, _ := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), 100, 40)

	m := s.Mark()
	until(t, s, "abort: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("跑一个长命令")
	until(t, s, "abort: tool call shown", `\$ shell_exec`, m)
	if !check2(t, "abort: tool started (marker file)", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}
	s.SendLine("别改了")
	until(t, s, "abort: the message is shown as waiting", steerNotice("别改了", steerWaiting), m)

	m = s.Mark()
	s.Send("\x1b") // Esc: the lone-Esc decision waits 50ms, so send nothing right after
	time.Sleep(300 * time.Millisecond)
	untilWithin(t, s, "abort: esc ends this turn only", `已中断本轮，1 条插话已放回输入行`, m, 15*time.Second)
	check(t, "abort: chat stays after esc", !s.Contains(`续接：`, m))
	until(t, s, "abort: the message is back in the input line", `别改了`, m)
	time.Sleep(500 * time.Millisecond)
	check(t, "abort: nothing was run on its own", len(fake.Requests()) == 1)

	// The put-back line is not submitted by itself: Enter runs it as the next turn.
	m = s.Mark()
	s.Send("\r")
	reqs := waitRequests(fake, 2, 30*time.Second)
	check(t, "abort: Enter runs the message", len(reqs) >= 2 && reqs[1].User == "别改了")
	until(t, s, "abort: the next turn calls the tool again", `\$ shell_exec`, m)
	time.Sleep(500 * time.Millisecond)
	s.SendCtrlC()
	untilWithin(t, s, "abort: one ctrl-c ends this turn only", `已中断本轮`, m, 15*time.Second)
	check(t, "abort: chat stays after one ctrl-c", !s.Contains(`续接：`, m))
	exitChat(t, s)
}
