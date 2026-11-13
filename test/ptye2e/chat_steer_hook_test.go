//go:build ptye2e

package ptye2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The hook scenario of scripts/ptysmoke/chat_steer.py: a PostToolUse hook writes a
// line straight to the terminal and one to stderr (chat prints that as a notice)
// while a message is typed - the input line keeps its place (task 4/N4, r3 M1).
const (
	steerHookTTY    = "HOOK_TTY_LINE"
	steerHookTTY2   = "HOOK_TTY_NEXT"
	steerHookNotice = "HOOK_NOTICE_LINE"
	steerHookEnv    = "PTYE2E_STEER_HOOK"
)

// TestSteerHookHelper is not a test: chat runs this test binary as the PostToolUse
// hook (`-test.run=^TestSteerHookHelper$`), the way chat_steer.py wrote a small
// python hook. It writes two lines straight to the terminal (a bare newline: the
// chat's raw input mode has to keep the output post-processing on, or the second
// line starts where the first ended and the input line is repainted into it) and
// one to stderr, which chat prints as a notice.
func TestSteerHookHelper(t *testing.T) {
	if os.Getenv(steerHookEnv) != "1" {
		return
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	var event map[string]any
	_ = json.Unmarshal([]byte(line), &event)
	for _, key := range []string{"hook_event_name", "name", "Name"} {
		if event[key] != "PostToolUse" {
			continue
		}
		steerHookWriteTTY(steerHookTTY + "\n" + steerHookTTY2 + "\n")
		fmt.Fprintln(os.Stderr, steerHookNotice)
		out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"additionalContext": "SMOKE hook context"}})
		fmt.Println(string(out))
		break
	}
	os.Exit(0)
}

// steerHookWriteTTY writes text to the terminal the chat runs in, bypassing the
// line editor: /dev/tty on Unix, CONOUT$ on Windows.
func steerHookWriteTTY(text string) {
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONOUT$"
	}
	tty, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	_, _ = tty.WriteString(text)
	_ = tty.Close()
}

// steerHook: the hook's notice and the line it writes straight to the terminal
// arrive while a message is typed: the input line keeps its place.
//
//	❯ prompt / tool call shown                until(❯ ) / until($ shell_exec)
//	the hook wrote to the terminal itself     until(HOOK_TTY_LINE)
//	the hook's notice reached chat            until(HOOK_NOTICE_LINE)
//	the half line is echoed                   until(插一句)
//	no status line without colors             no 思考中 on screen
//	the input line is the last row            it is the last row of the screen
//	the input line carries its prompt         it is ↳+typed or ❯+typed
//	a second hook line starts at column 0     a row reads HOOK_TTY_NEXT
//	the hook's own line stayed a row of its own   no row holds the line and the notice
//	esc ends this turn only                   until(已中断本轮)
func steerHook(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 4 * time.Second, Reply: steerReply,
		Command: osCommand("sleep 2; echo SMOKE_OK", "Start-Sleep 2; echo SMOKE_OK")}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal([]string{self, "-test.run=^TestSteerHookHelper$"})
	extra := fmt.Sprintf("hooks:\n  command: %s\n  post_tool_use:\n    tools: ['*']\n", command)
	o := steerChatOpts(t.TempDir(), ws, server.URL, extra)
	o.SetEnv = []string{"SUGA_HOOKS=on", steerHookEnv + "=1"}
	s, _ := startSteer(t, binary, o, steerScreenCols, 24)

	m := s.Mark()
	until(t, s, "hook: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("跑一下")
	until(t, s, "hook: tool call shown", `\$ shell_exec`, m)
	// Type while the hook runs (PostToolUse fires right after the tool result).
	s.Send("插")
	s.Send("一句")
	typed := "插一句"
	untilWithin(t, s, "hook: the hook wrote to the terminal itself", steerHookTTY, m, 30*time.Second)
	untilWithin(t, s, "hook: the hook's notice reached chat", steerHookNotice, m, 30*time.Second)
	until(t, s, "hook: the half line is echoed", regexp.QuoteMeta(typed), m)
	check(t, "hook: no status line without colors", !s.Contains(`思考中`, m))

	// The hook writes to the terminal itself and chat repaints its input line
	// afterwards, asynchronously: read the screen until the repaint has landed, so
	// a busy machine does not catch it half way.
	rows := screenRows(s, steerScreenCols)
	lastRowOf := func() []int { return steerRowIndexes(screenRows(s, steerScreenCols), typed) }
	check(t, "hook: the input line is the last row",
		waitSteerRows(t, s, steerScreenCols, func(rows []string) bool {
			found := steerRowIndexes(rows, typed)
			return len(found) > 0 && found[len(found)-1] == len(rows)-1
		}))
	rows = screenRows(s, steerScreenCols)
	found := lastRowOf()
	if len(found) > 0 {
		last := rows[found[len(found)-1]]
		check(t, "hook: the input line carries its prompt", last == steerPrompt+typed || last == "❯ "+typed)
	}
	// A process writing to the terminal itself (the hook) moves the cursor without
	// the line editor knowing, so the input line can be repainted a row lower: what
	// must hold is that it is there, whole, as the last row - and that the hook's own
	// line did not end up glued to chat's notice of it, which is what happens when
	// the raw output has no newline processing left.
	secondLine := false
	glued := false
	for _, row := range rows {
		if strings.TrimRight(row, " ") == steerHookTTY2 {
			secondLine = true
		}
		if strings.Contains(row, steerHookTTY) && strings.Contains(row, steerHookNotice) {
			glued = true
		}
	}
	if !secondLine || glued {
		t.Logf("hook: screen rows %q", rows)
	}
	check(t, "hook: a second line the hook wrote starts at column 0", secondLine)
	check(t, "hook: the hook's own line stayed a row of its own", !glued)
	// End the turn only, then leave chat.
	s.Send("\x1b")
	untilWithin(t, s, "hook: esc ends this turn only", `已中断本轮`, m, 15*time.Second)
	exitChat(t, s)
}
