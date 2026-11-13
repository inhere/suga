//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// nudgeReply is the answer of the nudge fake: a change was made, then the answer
// promises to continue without running any command, so both "stopped too early"
// rules hit (unverified-write and promise-zh).
const nudgeReply = "文件已写入。接下来我去跑测试。"

// TestChatNudge ports scripts/ptysmoke/chat_nudge.py: the "stopped too early" hint
// of chat (personal.CheckNudge).
//
// The fake (fakellm --mode nudge) writes a file and answers with a sentence that
// promises to continue, without running a command: the turn hits unverified-write
// (a change without a following shell_exec) and promise-zh. chat only shows one
// gray hint line under the answer, starts no turn of its own (the fake sees
// exactly the two requests of that turn), and the trace carries two nudge events
// with action=hinted.
//
// The second part runs against a workspace .suga/nudge-rules.yaml that turns the
// promise-* rules off: the same answer then only hits unverified-write, so the
// hint and the trace no longer carry the promised continuation.
//
// Python check -> Go assertion:
//
//	prompt                                             until(❯ )
//	turn completes                                     until(tokens ·)
//	hint line is shown / names the unchecked change     Contains(⚠ 最后修改 a.txt 之后没有运行 shell_exec)
//	hint quotes the promised continuation              Contains(结尾说接下来我去跑测试)
//	one turn only: the write and the answer            WaitArrived(2) then len(Requests()) == 2
//	no turn started on its own                         noTurnAfter(f, 2, 1.5s)
//	exit: resume command / session id                  exitChat + sessionFromText != ""
//	trace records both rules / kinds / hinted          attrs.rule set, kind in promise|unverified_write, action=hinted
//	rules file: (the second chat's checks)             until(❯ ), tokens ·, ⚠ 最后修改 b.txt …, promise off, resume line
//	trace: workspace rules file applied                attrs.rule set == [unverified-write]
func TestChatNudge(t *testing.T) {
	binary := buildSuga(t)
	ws := t.TempDir()

	fake := &fakeLLM{Mode: "nudge", Delay: 200 * time.Millisecond, WritePath: "a.txt", WriteBody: "alpha", Reply: nudgeReply}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: ws, URL: server.URL,
		ToolsExtra: "  approval: auto\n"})

	m := s.Mark()
	until(t, s, "prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("写个文件")
	until(t, s, "turn completes", `tokens · `, m)
	check(t, "hint line is shown", s.Contains(`⚠ `, m))
	check(t, "hint names the unchecked change", s.Contains(`最后修改 a.txt 之后没有运行 shell_exec`, m))
	check(t, "hint quotes the promised continuation", s.Contains(`结尾说接下来我去跑测试`, m))
	// No push in chat: the hint is all that happens, so the fake got exactly the
	// two requests of the one turn (write, final answer) and no third one arrives.
	if check2(t, "one turn only: the write and the answer", fake.WaitArrived(2, 15*time.Second)) {
		check(t, "one turn only: exactly two requests", len(fake.Requests()) == 2)
	}
	check(t, "no turn started on its own", noTurnAfter(fake, 2, 1500*time.Millisecond))
	exit := s.Mark()
	exitChat(t, s)
	session := sessionFromText(s.Since(exit))
	if check2(t, "session id", session != "") {
		nudges := nudgeEvents(traceEvents(t, binary, ws, env, session))
		check(t, "trace records both rules", rulesOf(nudges) == "promise-zh,unverified-write")
		check(t, "trace carries the kinds", kindsWithin(nudges, "promise", "unverified_write"))
		check(t, "trace says hinted", allHinted(nudges))
	}

	// Second part: a workspace nudge-rules.yaml that switches the promise rules
	// off. The same answer then only hits unverified-write (its own SUGA_HOME, so
	// the first session's config stays as it was).
	if err := os.MkdirAll(filepath.Join(ws, ".suga"), 0o700); err != nil {
		t.Fatal(err)
	}
	rules := "rules:\n  - name: promise-zh\n    enabled: false\n  - name: promise-en\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(ws, ".suga", "nudge-rules.yaml"), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	fake2 := &fakeLLM{Mode: "nudge", Delay: 200 * time.Millisecond, WritePath: "b.txt", WriteBody: "alpha", Reply: nudgeReply}
	server2 := httptest.NewServer(fake2)
	t.Cleanup(server2.Close)
	s2, env2 := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: ws, URL: server2.URL,
		ToolsExtra: "  approval: auto\n"})

	m = s2.Mark()
	until(t, s2, "rules file: prompt", `❯ `, m)
	m = s2.Mark()
	s2.SendLine("写个文件")
	until(t, s2, "rules file: turn completes", `tokens · `, m)
	check(t, "rules file: hint line is shown", s2.Contains(`⚠ `, m))
	check(t, "rules file: hint names the unchecked change", s2.Contains(`最后修改 b.txt 之后没有运行 shell_exec`, m))
	check(t, "rules file: promise check is switched off", !s2.Contains(`结尾说接下来我去跑测试`, m))
	exit = s2.Mark()
	exitChat(t, s2)
	session2 := sessionFromText(s2.Since(exit))
	if check2(t, "rules file: session id", session2 != "") {
		nudges := nudgeEvents(traceEvents(t, binary, ws, env2, session2))
		check(t, "workspace rules file applied", rulesOf(nudges) == "unverified-write")
	}
}

// nudgeEvents are the trace's nudge events.
func nudgeEvents(events []map[string]any) []map[string]any {
	var out []map[string]any
	for _, e := range events {
		if e["kind"] == "nudge" {
			out = append(out, e)
		}
	}
	return out
}

// rulesOf joins the sorted distinct rule names the nudge events name.
func rulesOf(nudges []map[string]any) string {
	seen := map[string]bool{}
	var rules []string
	for _, e := range nudges {
		rule := str(attrs(e)["rule"])
		if !seen[rule] {
			seen[rule] = true
			rules = append(rules, rule)
		}
	}
	sort.Strings(rules)
	return strings.Join(rules, ",")
}

// kindsWithin reports whether every nudge event's kind is one of kinds.
func kindsWithin(nudges []map[string]any, kinds ...string) bool {
	for _, e := range nudges {
		if !containsString(kinds, str(attrs(e)["kind"])) {
			return false
		}
	}
	return len(nudges) > 0
}

// allHinted reports whether every nudge event recorded action=hinted.
func allHinted(nudges []map[string]any) bool {
	for _, e := range nudges {
		if str(attrs(e)["action"]) != "hinted" {
			return false
		}
	}
	return len(nudges) > 0
}
