//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

const (
	ruleQuestion = `允许执行 shell_exec`
	rulesTools   = "  approval_rules:\n    persist_ask: true\n"
	noQuestionIn = 800 * time.Millisecond
)

// rulesRun is one scratch home + workspace + fake model of a rules scenario;
// start can be called again for a "new process" on the same home.
type rulesRun struct {
	t                       *testing.T
	binary, home, work, url string
	rulesExtra              string
	env                     []string
}

func newRulesRun(t *testing.T, binary string, fake *fakeLLM, rulesExtra string) *rulesRun {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return &rulesRun{t: t, binary: binary, home: t.TempDir(), work: t.TempDir(), url: server.URL, rulesExtra: rulesExtra}
}

func (r *rulesRun) start() *ptytest.Session {
	r.t.Helper()
	s, env := startChatOpts(r.t, r.binary, chatOpts{
		Home: r.home, Work: r.work, URL: r.url, ToolsExtra: rulesTools + r.rulesExtra,
	})
	r.env = env
	return s
}

func (r *rulesRun) writeRules(body string) {
	r.t.Helper()
	if err := os.MkdirAll(r.home, 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.home, "approval-rules.yaml"), []byte("rules:\n"+body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// answersWithoutQuestion waits for the closing reply and checks that no
// approval question came up on the way: a question left unanswered would keep
// the reply from ever arriving, so this is not weaker than waiting for silence.
func answersWithoutQuestion(t *testing.T, s *ptytest.Session, name string, mark int) {
	t.Helper()
	done := s.Expect(`完成`, mark, 15*time.Second)
	check(t, name+" does not ask", !s.Contains(ruleQuestion, mark))
	check(t, name+" completes", done)
}

// TestChatRules ports scripts/ptysmoke/chat_rules.py (scoped approval rules).
//
// Python check -> Go assertion (every item keeps its name in check/expect):
//
//	rule case does not ask / reports tool denial / turn completes  allow, deny, precedence subtests
//	exit: the turn has ended / first Ctrl-C hint / resume summary  exitAfterTurn (+ ctrlCTwice)
//	scoped command prompt, persistence prompt, first call runs, same prefix not asked, completes  session_scope
//	directory scope prompt .. file was written  path_scope
//	deny scope prompt .. denied second turn continues  deny_scope
//	user persistence asks .. new process user rule completes  user_persistence
//	project deny asks .. new process project denial continues  project_persistence
//	command/path control bytes cleaned, no allow-always, escape not written  control_text
//	disabled rules keep old prompt .. second turn completes  legacy_disabled
func TestChatRules(t *testing.T) {
	binary := buildSuga(t)

	rule := func(name, rules, command string, denied bool) {
		t.Run(name, func(t *testing.T) {
			r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: command, Delay: 100 * time.Millisecond}, "")
			r.writeRules(rules)
			s := r.start()
			mark := s.Mark()
			expect(t, s, `❯ `, mark)
			s.SendLine("run")
			if denied {
				check(t, "rule case reports tool denial", s.Expect(`规则 .*拒绝执行|用户拒绝执行`, mark, 15*time.Second))
			}
			answersWithoutQuestion(t, s, "rule case", mark)
			exitAfterTurn(t, s, mark)
		})
	}
	rule("allow", "  - tool: shell_exec\n    command: echo ALLOW\n    action: allow\n", "echo ALLOW", false)
	rule("deny", "  - tool: shell_exec\n    command: echo DENY\n    action: deny\n", "echo DENY", true)
	rule("precedence", "  - tool: shell_exec\n    command: echo PRECEDENCE\n    action: allow\n"+
		"  - tool: shell_exec\n    command: echo PRECEDENCE\n    action: deny\n", "echo PRECEDENCE", true)

	t.Run("session scope", func(t *testing.T) {
		r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: "echo SCOPED", Delay: 100 * time.Millisecond}, "")
		s := r.start()
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("first")
		check(t, "scoped command prompt", s.Expect(`\[a\]本会话总是允许“echo SCOPED”开头的命令`, mark, 15*time.Second))
		s.SendLine("a")
		check(t, "scoped allow persistence prompt", s.Expect(`把这条规则写进规则文件`, mark, 15*time.Second))
		s.SendLine("")
		check(t, "scoped first call runs", s.Expect(`完成`, mark, 15*time.Second))
		mark = s.Mark()
		s.SendLine("second")
		answersWithoutQuestion(t, s, "same command prefix", mark)
		exitAfterTurn(t, s, mark)
	})

	t.Run("path scope", func(t *testing.T) {
		r := newRulesRun(t, binary, &fakeLLM{Mode: "adddir", WritePath: "docs/one.txt", Delay: 100 * time.Millisecond}, "")
		if err := os.MkdirAll(filepath.Join(r.work, "docs"), 0o700); err != nil {
			t.Fatal(err)
		}
		s := r.start()
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("只写 first")
		check(t, "directory scope prompt", s.Expect(`\[a\]本会话总是允许写入 docs[/\\] 目录`, mark, 15*time.Second))
		s.SendLine("a")
		check(t, "directory scope persistence prompt", s.Expect(`把这条规则写进规则文件`, mark, 15*time.Second))
		s.SendLine("")
		check(t, "directory scoped write completes", s.Expect(`完成`, mark, 15*time.Second))
		mark = s.Mark()
		s.SendLine("只写 second")
		answersWithoutQuestion(t, s, "same directory", mark)
		_, err := os.Stat(filepath.Join(r.work, "docs", "one.txt"))
		check(t, "directory scoped file was written", err == nil)
		exitAfterTurn(t, s, mark)
	})

	t.Run("deny scope", func(t *testing.T) {
		r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: "echo DENY_SCOPE", Delay: 100 * time.Millisecond}, "")
		s := r.start()
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("first")
		check(t, "deny scope prompt", s.Expect(`\[d\]本会话总是拒绝`, mark, 15*time.Second))
		s.SendLine("d")
		check(t, "deny persistence prompt", s.Expect(`把这条规则写进规则文件`, mark, 15*time.Second))
		s.SendLine("")
		check(t, "deny first call reports tool error", s.Expect(`规则 .*拒绝执行`, mark, 15*time.Second))
		check(t, "deny first turn continues", s.Expect(`完成`, mark, 15*time.Second))
		mark = s.Mark()
		s.SendLine("second")
		answersWithoutQuestion(t, s, "same denied scope", mark)
		exitAfterTurn(t, s, mark)
	})

	t.Run("user persistence", func(t *testing.T) {
		r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: "echo USER_RULE", Delay: 100 * time.Millisecond}, "")
		s := r.start()
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("persist user")
		check(t, "user persistence asks", s.Expect(`\[a\]本会话总是允许`, mark, 15*time.Second))
		s.SendLine("a")
		check(t, "user persistence follow-up", s.Expect(`把这条规则写进规则文件`, mark, 15*time.Second))
		s.SendLine("u")
		check(t, "user rule first call completes", s.Expect(`完成`, mark, 15*time.Second))
		text, err := os.ReadFile(filepath.Join(r.home, "approval-rules.yaml"))
		check(t, "user rule file exists", err == nil)
		check(t, "user rule file contains allow",
			strings.Contains(string(text), "action: allow") && strings.Contains(string(text), "echo USER_RULE"))
		exitAfterTurn(t, s, mark)

		s2 := r.start()
		mark = s2.Mark()
		expect(t, s2, `❯ `, mark)
		s2.SendLine("new process")
		answersWithoutQuestion(t, s2, "new process user rule", mark)
		exitAfterTurn(t, s2, mark)
	})

	t.Run("project persistence and diagnostics", func(t *testing.T) {
		r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: "echo PROJECT_RULE", Delay: 100 * time.Millisecond}, "")
		s := r.start()
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("persist project")
		check(t, "project deny asks", s.Expect(`\[d\]本会话总是拒绝`, mark, 15*time.Second))
		s.SendLine("d")
		check(t, "project deny follow-up", s.Expect(`把这条规则写进规则文件`, mark, 15*time.Second))
		s.SendLine("p")
		check(t, "project deny first turn continues", s.Expect(`完成`, mark, 15*time.Second))
		text, err := os.ReadFile(filepath.Join(r.work, ".suga", "approval-rules.yaml"))
		check(t, "project rule file exists", err == nil)
		check(t, "project file contains deny only",
			strings.Contains(string(text), "action: deny") && !strings.Contains(string(text), "action: allow"))
		session := exitAfterTurn(t, s, mark)
		check(t, "session id", session != "")

		if session != "" {
			var denial map[string]any
			for _, e := range traceEvents(t, binary, r.work, r.env, session) { // "trace command succeeds"
				if e["name"] == "shell_exec" && (e["kind"] == "denial" || e["kind"] == "tool") {
					denial = e
					break
				}
			}
			a := attrs(denial)
			rule, _ := a["approval_rule"].(string)
			check(t, "trace exposes approval rule", strings.Contains(rule, "session#1") && strings.Contains(rule, "action=deny"))
			check(t, "trace exposes approval scope", a["approval_scope"] == "shell_exec:echo PROJECT_RULE")
		}

		out, _, code := sugaRun(t, binary, r.work, r.env, "inspect", "manifest", "--json")
		check(t, "manifest command succeeds", code == 0)
		var manifest map[string]any
		_ = json.Unmarshal([]byte(out), &manifest)
		rules, _ := manifest["approval_rules"].(map[string]any)
		project, _ := rules["project"].(map[string]any)
		effective, _ := project["effective_rules"].(float64)
		reason, _ := project["trust_reason"].(string)
		check(t, "manifest exposes project rule reason", effective >= 1 && strings.Contains(reason, "trust"))

		s2 := r.start()
		mark = s2.Mark()
		expect(t, s2, `❯ `, mark)
		s2.SendLine("new process")
		answersWithoutQuestion(t, s2, "new process project denial", mark)
		exitAfterTurn(t, s2, mark)
	})

	t.Run("control text", func(t *testing.T) {
		t.Run("command", func(t *testing.T) {
			r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: "echo\x1b[2KCONTROL", Delay: 100 * time.Millisecond}, "")
			s := r.start()
			mark := s.Mark()
			expect(t, s, `❯ `, mark)
			rawMark := len(s.RawText())
			s.SendLine("control")
			check(t, "command control bytes are cleaned", s.Expect(`命令：echoCONTROL`, mark, 15*time.Second))
			check(t, "unsafe shell has no allow-always option", !s.Contains(`\[a\]本会话总是允许`, mark))
			check(t, "command escape is not written", !regexp.MustCompile(regexp.QuoteMeta("命令：echo\x1b[2K")).MatchString(s.RawText()[rawMark:]))
			s.SendLine("n")
			s.Expect(`完成`, mark, 15*time.Second)
			exitAfterTurn(t, s, mark)
		})
		t.Run("path", func(t *testing.T) {
			r := newRulesRun(t, binary, &fakeLLM{Mode: "adddir", WritePath: "dir\x1b[2K/file.txt", Delay: 100 * time.Millisecond}, "")
			s := r.start()
			mark := s.Mark()
			expect(t, s, `❯ `, mark)
			rawMark := len(s.RawText())
			s.SendLine("只写")
			check(t, "path control bytes are cleaned", s.Expect(`路径：.*dir[/\\]file.txt`, mark, 15*time.Second))
			check(t, "path escape is not written", !regexp.MustCompile(regexp.QuoteMeta("dir\x1b[2K")).MatchString(s.RawText()[rawMark:]))
			s.SendLine("n")
			s.Expect(`完成`, mark, 15*time.Second)
			exitAfterTurn(t, s, mark)
		})
	})

	t.Run("legacy disabled", func(t *testing.T) {
		r := newRulesRun(t, binary, &fakeLLM{Mode: "tool", Command: "echo LEGACY", Delay: 100 * time.Millisecond}, "    enabled: false\n")
		r.writeRules("  - tool: shell_exec\n    command: echo LEGACY\n    action: deny\n")
		s := r.start()
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("legacy")
		check(t, "disabled rules keep old prompt", s.Expect(`允许执行 shell_exec？\[y\]是 \[N\]否 \[a\]本会话总是允许该工具`, mark, 15*time.Second))
		check(t, "disabled rules do not use scoped command prompt", !s.Contains(`开头的命令`, mark))
		s.SendLine("a")
		check(t, "disabled rules old always completes", s.Expect(`完成`, mark, 15*time.Second))
		check(t, "disabled rules have no persistence follow-up", !s.Contains(`把这条规则写进规则文件`, mark))
		mark = s.Mark()
		s.SendLine("legacy second")
		answersWithoutQuestion(t, s, "disabled rules old always", mark)
		exitAfterTurn(t, s, mark)
	})
}
