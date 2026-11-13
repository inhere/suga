//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	t.Run("send", func(t *testing.T) { steerSend(t, binary) })
	t.Run("clear", func(t *testing.T) { steerClear(t, binary) })
	t.Run("withdraw", func(t *testing.T) { steerWithdraw(t, binary) })
	t.Run("approval", func(t *testing.T) { steerApproval(t, binary) })
	t.Run("off", func(t *testing.T) { steerOff(t, binary) })
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

// normalized is text with every run of whitespace collapsed to one space: what a
// message the fake model recorded reads as when its own line breaks are not the
// subject (the Python fake model collapsed them, the Go one keeps them).
func normalized(text string) string { return strings.Join(strings.Fields(text), " ") }

// normalizedList is list with every item normalized.
func normalizedList(list []string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, normalized(item))
	}
	return out
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

// The three messages the send scenario delivers through the CLI (steerSendText is
// the withdraw scenario's), and the text of the turn that finds chat idle.
const (
	steerSendText2 = "再改一处"
	steerSendText3 = "别改了"
	steerSendText4 = "空闲时的话"
)

// steerSendResult is what a `suga send` started in the background returned.
type steerSendResult struct {
	code int
	out  string
}

// steerSendAsync starts `suga send` in the background, the way a Gofer-style
// caller does: a scenario can check when it returns relative to the turn its
// message went into (a steered message is answered while the model still works on
// it, a queued one only once its own turn started).
func steerSendAsync(binary, work string, env []string, args ...string) <-chan steerSendResult {
	done := make(chan steerSendResult, 1)
	go func() {
		code, out := sugaSend(binary, work, env, 60*time.Second, args...)
		done <- steerSendResult{code: code, out: out}
	}()
	return done
}

// steerSendWait waits for a `suga send` started with steerSendAsync and reports
// its exit code and output (124 when it never returned).
func steerSendWait(t *testing.T, done <-chan steerSendResult) steerSendResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(60 * time.Second):
		t.Logf("send --wait did not return within 60s")
		return steerSendResult{code: 124, out: "timeout"}
	}
}

// steerRemoveMarker removes the tool's marker file so the next phase can wait for
// its own tool to write it again.
func steerRemoveMarker(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// steerSend: `suga send --steer` from outside (the way gofer delivers) puts the
// message into the turn running right now, and what became of it decides the exit
// code of the sender (suga-fvnm).
//
//	❯ prompt / session id                         until(❯ ) / the startup session id
//	tool call shown / ↳ / tool started            until($ shell_exec) / (↳ ) / marker file
//	the first response is steered (no --wait)      send --steer exits 0 with "steered"
//	the message is shown as waiting                until([来自 cli] 只改 README（等待送达）)
//	tool result shown / shown as delivered         until(⎿ ok: TOOL_DONE) / (已送达)
//	the turn answers / the end-of-turn line        until(STEER_OK) / until(插话 1)
//	two requests, the first without it             2 requests; no 只改 README in request 1
//	the next request carries it after the result  last user == [来自 cli] 只改 README, [.., tool, user]
//	--wait exits 0                                 send --steer --wait exits 0
//	--wait returned before the turn ended          no 完成 line and no answer since the mark
//	shown as delivered in the running turn         the （已送达） notice is on screen
//	four requests, the second turn reached          4 requests; request 4's user is the second one
//	the second / third / fourth tool started        the marker file of each phase
//	a message of an aborted turn exits 3             send --wait exits 3 after Esc
//	esc ends this turn only / chat stays / back     until(已中断本轮) / no 续接 / until([来自 cli] 别改了)
//	eight messages fit the inbox                   8 send --steer exit 0 with "steered"
//	the ninth is refused with queue_full            exit 1 and queue_full in its output
//	an idle chat queues the message                 exit 0 with 未插入当前回合，已排队
//	the queued message runs as the next turn        until([来自 cli] 空闲时的话)
//	the trace records both deliveries               2 steer "delivered", source send:cli
//	the aborted turn's messages are traced back     a steer "returned" with disposition input_line
func steerSend(t *testing.T, binary string) {
	ws := t.TempDir()
	// The model's own delay keeps the turn running after the tool result: that is
	// what the --wait timing check needs.
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 3 * time.Second, Reply: steerReply, Command: steerToolCommand(6)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, env := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), 100, 40)

	m := s.Mark()
	until(t, s, "send: ❯ prompt", `❯ `, m)
	sid := ""
	if found := chatSessionID.FindStringSubmatch(s.Text()); found != nil {
		sid = found[1]
	}
	if !check2(t, "send: session id", sid != "") {
		return
	}
	marker := filepath.Join(ws, steerStarted)

	// --- steered into the turn running right now ---------------------------
	m = s.Mark()
	s.SendLine("跑一个长命令")
	until(t, s, "send: tool call shown", `\$ shell_exec`, m)
	until(t, s, "send: the prompt becomes ↳ while the turn runs", steerPrompt, m)
	if !check2(t, "send: tool started (marker file)", waitMarker(marker, 30*time.Second)) {
		return
	}

	// The notice of the inserted message is written before the response leaves, so
	// the mark goes up before the send runs.
	m = s.Mark()
	code, out := sugaSend(binary, ws, env, 60*time.Second, "--session", sid, "--steer", steerSendText)
	check(t, "send: the first response is steered (no --wait)", code == 0 && trimSpace(out) == "steered")
	until(t, s, "send: the message is shown as waiting", steerNotice(steerSendPrefix+steerSendText, steerWaiting), m)
	until(t, s, "send: tool result shown", `⎿ ok: TOOL_DONE`, m)
	until(t, s, "send: shown as delivered", steerNotice(steerSendPrefix+steerSendText, steerTaken), m)
	until(t, s, "send: the turn answers", regexp.QuoteMeta(steerReply), m)
	until(t, s, "send: the end-of-turn line counts it", `插话 1`, m)

	reqs := waitRequests(fake, 2, 30*time.Second)
	check(t, "send: two requests", len(reqs) >= 2)
	if len(reqs) >= 2 {
		check(t, "send: the first request has no inserted message",
			!strings.Contains(strings.Join(steerUserTexts(reqs[0]), " "), steerSendText))
		roles := steerRoles(reqs[1])
		check(t, "send: the turn's next request carries it after the tool result",
			normalized(reqs[1].User) == steerSendPrefix+steerSendText && hasRole(roles, "tool") && roles[len(roles)-1] == "user")
	}

	// --- --wait returns before that turn ends ------------------------------
	steerRemoveMarker(t, marker)
	m = s.Mark()
	s.SendLine("再跑一个长命令")
	until(t, s, "send: tool call shown again", `\$ shell_exec`, m)
	if !check2(t, "send: the second tool started", waitMarker(marker, 30*time.Second)) {
		return
	}

	done := steerSendAsync(binary, ws, env, "--session", sid, "--steer", "--wait", steerSendText2)
	res := steerSendWait(t, done)
	// The message was taken by this turn's next request, so the sender is answered
	// while the model is still working on it: neither the end-of-turn line nor the
	// answer is there yet. (A queued message would answer only once this turn had
	// ended and the message's own turn had started - which is the reverse check.)
	settleOutput(s, len(s.RawText()), 300*time.Millisecond)
	check(t, "send: --wait exits 0", res.code == 0)
	check(t, "send: --wait returned before the turn ended",
		!s.Contains(`tokens · `, m) && !s.Contains(steerReply, m))
	check(t, "send: shown as delivered in the running turn",
		s.Contains(steerNotice(steerSendPrefix+steerSendText2, steerTaken), m))
	until(t, s, "send: the turn answers", regexp.QuoteMeta(steerReply), m)
	reqs = waitRequests(fake, 4, 30*time.Second)
	check(t, "send: four requests", len(reqs) >= 4)
	if len(reqs) >= 4 {
		check(t, "send: the message reached the second turn's next request",
			normalized(reqs[3].User) == steerSendPrefix+steerSendText2)
	}

	// --- an aborted turn discards the message (exit 3) ----------------------
	steerRemoveMarker(t, marker)
	m = s.Mark()
	s.SendLine("再跑一个长命令")
	until(t, s, "send: tool call shown (third turn)", `\$ shell_exec`, m)
	if !check2(t, "send: the third tool started", waitMarker(marker, 30*time.Second)) {
		return
	}

	done = steerSendAsync(binary, ws, env, "--session", sid, "--steer", "--wait", steerSendText3)
	until(t, s, "send: the message is shown as waiting", steerNotice(steerSendPrefix+steerSendText3, steerWaiting), m)
	s.Send("\x1b") // Esc: the lone-Esc decision waits 50ms, so send nothing right after
	time.Sleep(300 * time.Millisecond)
	res = steerSendWait(t, done)
	check(t, "send: a message of an aborted turn exits 3", res.code == 3)
	untilWithin(t, s, "send: esc ends this turn only", `已中断本轮`, m, 15*time.Second)
	check(t, "send: chat stays after esc", !s.Contains(`续接：`, m))
	until(t, s, "send: the message is back in the input line", regexp.QuoteMeta(steerSendPrefix+steerSendText3), m)

	// --- a full inbox is refused with queue_full ---------------------------
	// The fourth turn's tool sleeps longer than the six seconds of the others: nine
	// `suga send` processes have to arrive while it runs, and a phase that outran
	// the tool left the ninth answered "queued" instead of queue_full.
	fake.SetCommand(steerToolCommand(30))
	steerRemoveMarker(t, marker)
	m = s.Mark()
	s.SendLine("再跑一个长命令")
	until(t, s, "send: tool call shown (fourth turn)", `\$ shell_exec`, m)
	if !check2(t, "send: the fourth tool started", waitMarker(marker, 30*time.Second)) {
		return
	}

	filled := true
	for i := 0; i < 8; i++ {
		code, out = sugaSend(binary, ws, env, 60*time.Second, "--session", sid, "--steer", fmt.Sprintf("插话 %d", i))
		if code != 0 || trimSpace(out) != "steered" {
			t.Logf("message %d: exit %d, output %q", i, code, out)
			filled = false
		}
	}
	check(t, "send: eight messages fit the inbox", filled)
	code, out = sugaSend(binary, ws, env, 60*time.Second, "--session", sid, "--steer", "第九条")
	check(t, "send: the ninth is refused with queue_full", code == 1 && strings.Contains(out, "queue_full"))
	s.Send("\x1b")
	untilWithin(t, s, "send: esc ends this turn only", `已中断本轮`, m, 15*time.Second)

	// --- an idle chat queues it --------------------------------------------
	promptReady(t, s, m)
	code, out = sugaSend(binary, ws, env, 60*time.Second, "--session", sid, "--steer", steerSendText4)
	check(t, "send: an idle chat queues the message instead",
		code == 0 && trimSpace(out) == "未插入当前回合，已排队")
	until(t, s, "send: the queued message runs as the next turn",
		regexp.QuoteMeta(steerSendPrefix+steerSendText4), m)

	events := steerExit(t, s, binary, ws, env)
	delivered := steerEvents(events, "steer", "delivered")
	check(t, "send: the trace records both deliveries", len(delivered) == 2)
	cpu := true
	for _, e := range delivered {
		if str(attrs(e)["source"]) != "send:cli" {
			cpu = false
		}
	}
	check(t, "send: they name the send source", cpu)
	returnedToLine := false
	for _, e := range steerEvents(events, "steer", "returned") {
		if str(attrs(e)["disposition"]) == "input_line" {
			returnedToLine = true
		}
	}
	check(t, "send: the aborted turn's messages are traced back to the input line", returnedToLine)
}

const steerClearCols = 100

// steerClear: Ctrl-C on a non-empty input line drops that line and nothing else
// (wll1); an empty line keeps the old meaning and ends this turn only.
//
//	❯ prompt / tool call shown / ↳ / tool started   until(❯ ) / ($ shell_exec) / (↳ ) / marker file
//	the text is on the input line                    until(这行要被清掉)
//	ctrl-c with text: the turn is not ended          no 已中断本轮 after 0.8s
//	ctrl-c with text: nothing was inserted           no [插话]
//	ctrl-c with text: no request carried it          still 1 request after 0.3s
//	ctrl-c with text: the input line is empty        the input row is the ↳ prompt alone
//	the new message is shown as waiting              until([插话] 只改 README（等待送达）)
//	the dropped text did not come along              no [插话] 这行要被清掉只改 README
//	tool result / delivered / answer / counts one    until(⎿ ok: TOOL_DONE) / (已送达) / answer / 插话 1
//	the next request carries only the new message     last user == 只改 README, none carries the dropped text
//	ctrl-c on an empty line still ends this turn     until(已中断本轮) after the second turn's tool started
//	chat stays after it                              no 续接
func steerClear(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(20)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, _ := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), steerClearCols, 40)
	const (
		dropped = "这行要被清掉"
		clear   = "只改 README"
	)

	m := s.Mark()
	until(t, s, "clear: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("跑一个长命令")
	until(t, s, "clear: tool call shown", `\$ shell_exec`, m)
	until(t, s, "clear: the prompt becomes ↳ while the turn runs", steerPrompt, m)
	if !check2(t, "clear: tool started (marker file)", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}

	// A half-typed message, then Ctrl-C: the line goes away, the turn runs on.
	m = s.Mark()
	s.Send(dropped)
	until(t, s, "clear: the text is on the input line", regexp.QuoteMeta(dropped), m)
	s.SendCtrlC()
	settleOutput(s, len(s.RawText()), 800*time.Millisecond)
	check(t, "clear: ctrl-c with text: the turn is not ended", !s.Contains(`已中断本轮`, m))
	check(t, "clear: ctrl-c with text: nothing was inserted", !s.Contains(`\[插话\]`, m))
	time.Sleep(300 * time.Millisecond)
	check(t, "clear: ctrl-c with text: no request carried it", len(fake.Requests()) == 1)
	// The input line is empty again: the row holds the prompt and nothing else (a
	// dropped line left on the screen would still be there).
	check(t, "clear: ctrl-c with text: the input line is empty", waitInputRow(t, s, steerClearCols, strings.TrimSpace(steerPrompt), 3*time.Second))

	// What is typed next is a line of its own: the dropped text did not come along.
	m = s.Mark()
	s.SendLine(clear)
	until(t, s, "clear: the new message is shown as waiting", steerNotice(clear, steerWaiting), m)
	check(t, "clear: the dropped text did not come along",
		!s.Contains(steerNotice(dropped+clear, steerWaiting), m))
	// The tool ends and the model is asked again: the message reached this turn, so
	// the press dropped a line and did not end anything.
	untilWithin(t, s, "clear: tool result shown", `⎿ ok: TOOL_DONE`, m, 40*time.Second)
	until(t, s, "clear: shown as delivered", steerNotice(clear, steerTaken), m)
	untilScreen(t, s, steerClearCols, "clear: the turn answers", regexp.QuoteMeta(steerReply))
	until(t, s, "clear: the end-of-turn line counts one message", `插话 1`, m)

	reqs := waitRequests(fake, 2, 30*time.Second)
	check(t, "clear: two requests", len(reqs) >= 2)
	if len(reqs) >= 2 {
		carried := steerUserTexts(reqs[1])
		check(t, "clear: the next request carries only the message typed after the ctrl-c",
			reqs[1].User == clear && !strings.Contains(strings.Join(carried, " "), dropped))
	}

	// An empty line keeps the old meaning during a turn: Ctrl-C ends this turn.
	if err := os.Remove(filepath.Join(ws, steerStarted)); err != nil {
		t.Fatal(err)
	}
	m = s.Mark()
	s.SendLine("再跑一个长命令")
	until(t, s, "clear: tool call shown again", `\$ shell_exec`, m)
	if !check2(t, "clear: the second tool started", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}
	s.SendCtrlC()
	untilWithin(t, s, "clear: ctrl-c on an empty line still ends this turn", `已中断本轮`, m, 15*time.Second)
	check(t, "clear: chat stays after it", !s.Contains(`续接：`, m))
	exitChat(t, s)
}

// The two messages the withdraw scenario types into the running turn, the message
// `suga send --steer` adds to the same inbox, and the word added to the withdrawn
// ones before submitting them again.
const (
	steerWithdrawText  = "撤回这一句"
	steerWithdrawText2 = "还有这一句"
	steerWithdrawEdit  = "补充说明"
	// steerSendText is the text `suga send --steer` carries: chat labels every
	// message from elsewhere with its source (--source defaults to cli).
	steerSendText   = "只改 README"
	steerSendPrefix = "[来自 cli] "
)

// steerWithdraw: Up on the empty input line of a running turn takes the messages
// still waiting to be delivered back, while a `suga send --steer` message stays.
//
//	❯ prompt / session id / tool call shown / ↳ / tool started   until(❯ ) / the startup session id / ($ shell_exec) / (↳ ) / marker
//	the two typed messages are shown as waiting                  until([插话] 撤回这一句（等待送达）) / (还有这一句)
//	the sent message is steered into the turn and shown           send --steer exits 0 with "steered"; until([来自 cli] 只改 README（等待送达）)
//	no request carried them while they waited                    still 1 request after 0.3s
//	the first and second are shown as withdrawn                   until(...（已撤回）) for both
//	the sent one is not withdrawn                                no [来自 cli] 只改 README（已撤回）
//	both are back on the input line, in order                    raw output: 撤回这一句\r*\n还有这一句
//	nothing was submitted by the Up key                           no [插话] <edited>（等待送达）
//	the edited text is shown as waiting                           until([插话] 撤回这一句 还有这一句补充说明（等待送达）)
//	tool result / the sent one delivered / the edited delivered    until(⎿ ok: TOOL_DONE) / (已送达) both
//	the turn answers / counts the delivered two                  until(answer) / until(插话 2)
//	the withdrawn ones are not counted                            no 插话 4
//	the first request has none of the three                      1st request carries none of them
//	the withdrawn ones are not in the next request               no exact user text of them (nor in Seen)
//	the sent and the edited one are in the next request          the sent one is carried, the edited one is last
//	the trace records both withdrawals                           2 steer "withdrawn", source keyboard, count 2
//	the delivered ones are the edited and the sent one           delivered sources sorted == [keyboard send:cli]
func steerWithdraw(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(25)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, env := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), 100, 40)

	m := s.Mark()
	until(t, s, "withdraw: ❯ prompt", `❯ `, m)
	sid := ""
	if found := chatSessionID.FindStringSubmatch(s.Text()); found != nil {
		sid = found[1]
	}
	if !check2(t, "withdraw: session id", sid != "") {
		return
	}

	m = s.Mark()
	s.SendLine("跑一个长命令")
	until(t, s, "withdraw: tool call shown", `\$ shell_exec`, m)
	until(t, s, "withdraw: the prompt becomes ↳ while the turn runs", steerPrompt, m)
	if !check2(t, "withdraw: tool started (marker file)", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}

	// Two messages typed into the running turn: both wait for a model request.
	m = s.Mark()
	s.SendLine(steerWithdrawText)
	until(t, s, "withdraw: the first message is shown as waiting", steerNotice(steerWithdrawText, steerWaiting), m)
	s.SendLine(steerWithdrawText2)
	until(t, s, "withdraw: the second message is shown as waiting", steerNotice(steerWithdrawText2, steerWaiting), m)
	// And one sent from elsewhere: it waits in the same inbox.
	code, out := sugaSend(binary, ws, env, 60*time.Second, "--session", sid, "--steer", steerSendText)
	check(t, "withdraw: the sent message is steered into the turn", code == 0 && strings.TrimSpace(out) == "steered")
	until(t, s, "withdraw: the sent message is shown as waiting", steerNotice(steerSendPrefix+steerSendText, steerWaiting), m)
	time.Sleep(300 * time.Millisecond)
	check(t, "withdraw: no request carried them while they waited", len(fake.Requests()) == 1)

	// Up on the empty input line: the typed ones come back, the sent one stays (its
	// sender is waiting for it).
	m, rawMark := s.Mark(), len(s.RawText())
	s.Send("\x1b[A")
	until(t, s, "withdraw: the first is shown as withdrawn", steerNotice(steerWithdrawText, steerWithdrawn), m)
	until(t, s, "withdraw: the second is shown as withdrawn", steerNotice(steerWithdrawText2, steerWithdrawn), m)
	check(t, "withdraw: the sent one is not withdrawn",
		!s.Contains(steerNotice(steerSendPrefix+steerSendText, steerWithdrawn), m))
	// Both texts are on the input line, one line each, in the order they were typed.
	check(t, "withdraw: both are back on the input line, in order",
		rawContains(s, regexp.QuoteMeta(steerWithdrawText)+"\r*\n"+regexp.QuoteMeta(steerWithdrawText2), rawMark, 15*time.Second))
	edited := steerWithdrawText + " " + steerWithdrawText2 + steerWithdrawEdit
	check(t, "withdraw: nothing was submitted by the Up key", !s.Contains(steerNotice(edited, steerWaiting), m))

	// Edited at the end of the line and submitted again: one new message of the
	// same turn.
	m = s.Mark()
	s.SendLine(steerWithdrawEdit)
	until(t, s, "withdraw: the edited text is shown as waiting", steerNotice(edited, steerWaiting), m)

	// The tool ends and the model is asked again: it carries the sent message and
	// the edited one, not the withdrawn pair.
	untilWithin(t, s, "withdraw: tool result shown", `⎿ ok: TOOL_DONE`, m, 40*time.Second)
	until(t, s, "withdraw: the sent one is delivered", steerNotice(steerSendPrefix+steerSendText, steerTaken), m)
	until(t, s, "withdraw: the edited one is delivered", steerNotice(edited, steerTaken), m)
	untilScreen(t, s, 100, "withdraw: the turn answers", regexp.QuoteMeta(steerReply))
	until(t, s, "withdraw: the end-of-turn line counts the delivered two", `插话 2`, m)
	check(t, "withdraw: the withdrawn ones are not counted", !s.Contains(`插话 4`, m))

	reqs := waitRequests(fake, 2, 30*time.Second)
	check(t, "withdraw: two requests", len(reqs) >= 2)
	if len(reqs) >= 2 {
		first := strings.Join(steerUserTexts(reqs[0]), " ")
		check(t, "withdraw: the first request has no inserted message",
			!strings.Contains(first, steerWithdrawText) && !strings.Contains(first, steerWithdrawText2) &&
				!strings.Contains(first, steerSendPrefix+steerSendText))
		// Neither of the withdrawn texts is a message of the next request (exact
		// match: the edited one starts with the first of them), while the sent one
		// and the edited one are.
		carried := normalizedList(steerUserTexts(reqs[1]))
		seen := reqs[1].Seen
		check(t, "withdraw: the withdrawn ones are not in the next request",
			!containsString(carried, steerWithdrawText) && !containsString(carried, steerWithdrawText2) &&
				!containsString(seen, "user:"+steerWithdrawText) && !containsString(seen, "user:"+steerWithdrawText2))
		check(t, "withdraw: the sent one is in the next request", containsString(carried, steerSendPrefix+steerSendText))
		check(t, "withdraw: the edited one is what the next request carries last", normalized(reqs[1].User) == edited)
	}

	events := steerExit(t, s, binary, ws, env)
	withdrawn := steerEvents(events, "steer", "withdrawn")
	check(t, "withdraw: the trace records both withdrawals", len(withdrawn) == 2)
	counted := true
	for _, e := range withdrawn {
		n, _ := attrs(e)["count"].(float64)
		if str(attrs(e)["source"]) != "keyboard" || n != 2 {
			counted = false
		}
	}
	check(t, "withdraw: each names the keyboard source and the batch size", counted)
	delivered := steerEvents(events, "steer", "delivered")
	var sources []string
	for _, e := range delivered {
		sources = append(sources, str(attrs(e)["source"]))
	}
	slices.Sort(sources)
	check(t, "withdraw: the delivered ones are the edited and the sent one",
		strings.Join(sources, ",") == "keyboard,send:cli")
}

// steerApproval: typing at an approval question is the answer, never an inserted
// message.
//
//	the question is asked                   until(允许执行 shell_exec)
//	the answer is used as the answer        until(用户拒绝执行 shell_exec)
//	no inserted message was shown          no [插话]
//	the turn ends                           until(tokens ·)
//	no request carried that text           no request whose user message is 插一句
//	no steer in the trace                  no steer event at all
func steerApproval(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(4)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	// No approval override: the default is asking, and the question is the subject.
	s, env := startSteer(t, binary, chatOpts{Home: t.TempDir(), Work: ws, URL: server.URL}, 100, 40)

	m := s.Mark()
	until(t, s, "approval: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("跑一下")
	until(t, s, "approval: the question is asked", `允许执行 shell_exec`, m)
	s.SendLine("插一句") // not y/a: the answer, not a steer
	until(t, s, "approval: the answer is used as the answer", `用户拒绝执行 shell_exec`, m)
	check(t, "approval: no inserted message was shown", !s.Contains(`\[插话\]`, m))
	until(t, s, "approval: the turn ends", `tokens · `, m)
	time.Sleep(500 * time.Millisecond)
	carried := false
	for _, req := range fake.Requests() {
		if req.User == "插一句" {
			carried = true
		}
	}
	check(t, "approval: no request carried that text", !carried)
	events := steerExit(t, s, binary, ws, env)
	check(t, "approval: no steer in the trace", len(steerEvents(events, "steer", "")) == 0)
}

// steerOff: chat.steering: false keeps the old behaviour.
//
//	❯ prompt / tool call shown / tool started   until(❯ ) / ($ shell_exec) / marker file
//	no ↳ prompt during the turn                 no ↳ on screen
//	typing during the turn inserts nothing      no [插话] after 0.5s
//	one ctrl-c still ends the chat              until(续接：suga chat --resume …)
func steerOff(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(60)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, _ := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, "chat:\n  steering: false\n"), 100, 40)

	m := s.Mark()
	until(t, s, "off: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("跑一个长命令")
	until(t, s, "off: tool call shown", `\$ shell_exec`, m)
	if !check2(t, "off: tool started (marker file)", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}
	check(t, "off: no ↳ prompt during the turn", !s.Contains(steerPrompt, m))
	// Cooked mode: the bytes wait in the tty, nothing reads them, so nothing is
	// inserted into the turn.
	s.Send("插一句")
	time.Sleep(500 * time.Millisecond)
	check(t, "off: typing during the turn inserts nothing", !s.Contains(`\[插话\]`, m))
	m = s.Mark()
	s.SendCtrlC()
	expectWithin(t, s, `续接：suga chat --resume (?:sess_|sg_|s_)`, m, 20*time.Second)
	waitGone(t, s)
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
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(4)}
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
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(10)}
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
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 200 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(60)}
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
