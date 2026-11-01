//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// The rewind/fork scenarios (plan T8). Every scenario runs three questions that
// write the file w.txt as v1, v2, v3 (fakeLLM "writev" mode: the content comes from
// the question, not from the request count), then rewinds to before question 2.
// The requests of the model are the oracle: fake.Requests()[k].Msgs is exactly what
// the model received.
const (
	rwQ1 = "第一问 write v1"
	rwQ2 = "第二问 write v2"
	rwQ3 = "第三问 write v3"
	// rwNoticeDisk is the part of the rewind notice that only a conversation-only
	// rewind carries ("the disk files were NOT rewound").
	rwNoticeDisk = "磁盘文件没有回退"
	rwConfirm    = `执行？\[y\] 回退`
)

var rwForkID = regexp.MustCompile(`已分叉出新会话 ([A-Za-z0-9_]+)`)

type rewindChat struct {
	s      *ptytest.Session
	fake   *fakeLLM
	binary string
	home   string
	work   string
	target string // the file the questions write
	env    []string
	url    string // the fakeLLM server, kept for a second chat on the same home
}

// startRewindChat starts a chat on a fresh home and workspace against a writev
// fakeLLM. toolsExtra lines go under tools: after "approval: auto".
func startRewindChat(t *testing.T, binary string, delay time.Duration, toolsExtra string, args ...string) *rewindChat {
	t.Helper()
	fake := &fakeLLM{Mode: "writev", Delay: delay, WritePath: "w.txt"}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	rc := &rewindChat{fake: fake, binary: binary, home: t.TempDir(), work: t.TempDir(), url: server.URL}
	rc.target = filepath.Join(rc.work, "w.txt")
	rc.start(t, toolsExtra, args...)
	return rc
}

func (rc *rewindChat) start(t *testing.T, toolsExtra string, args ...string) {
	t.Helper()
	rc.s, rc.env = startChatOpts(t, rc.binary, chatOpts{
		Home: rc.home, Work: rc.work, URL: rc.url, ToolsExtra: "  approval: auto\n" + toolsExtra, Args: args,
	})
	m := rc.s.Mark()
	if !rc.s.Expect(`❯ `, m, 15*time.Second) {
		t.Fatalf("no prompt; terminal tail:\n%s", tail(rc.s.Text()))
	}
}

// ask sends one question and waits for its turn to end. The mark is taken after
// the previous turn's footer, so an old "tokens · " cannot satisfy this wait.
func (rc *rewindChat) ask(t *testing.T, line string) {
	t.Helper()
	m := rc.s.Mark()
	rc.s.SendLine(line)
	waitTurnEnd(t, rc.s, m)
}

// threeTurns runs the three writing questions; the file ends as v3.
func (rc *rewindChat) threeTurns(t *testing.T) {
	t.Helper()
	for i, q := range []string{rwQ1, rwQ2, rwQ3} {
		rc.ask(t, q)
		want := "v" + string(rune('1'+i))
		assert(t, "the file is "+want+" after question "+want, readFile(rc.target) == want)
	}
}

func assert(t *testing.T, name string, ok bool) {
	t.Helper()
	if !ok {
		t.Fatalf("FAIL %s", name)
	}
}

// rewind runs ":rewind <args>" and answers the confirmation with y. It waits for
// the result and returns what chat printed for the command.
func (rc *rewindChat) rewind(t *testing.T, args string) string {
	t.Helper()
	m := rc.s.Mark()
	rc.s.SendLine(":rewind " + args)
	expect(t, rc.s, `将回退到第 2 轮`, m)
	expect(t, rc.s, rwConfirm, m)
	rc.s.SendLine("y")
	expect(t, rc.s, `回退完成`, m)
	time.Sleep(300 * time.Millisecond)
	return rc.s.Since(m)
}

// exit leaves chat and returns the session id.
func (rc *rewindChat) exit(t *testing.T) string {
	t.Helper()
	m := rc.s.Mark()
	exitChat(t, rc.s)
	return sessionFromText(rc.s.Since(m))
}

// TestChatRewind covers the five e2e scenarios of the rewind/fork plan (T8):
//
//	scope          files / conversation / both after three file-writing turns
//	compaction     a rewind to before a :compact: the request has none of the rewound turns
//	cleaned        snapshots already cleaned: the preview lists the unrecoverable file
//	fork           :fork N, --resume the new session, the source stays unchanged
//	mid-turn       :rewind typed while a turn runs goes back to the input line
func TestChatRewind(t *testing.T) {
	binary := buildSuga(t)
	t.Run("scope", func(t *testing.T) {
		cases := []struct {
			scope     string
			fileWant  string
			hasQ2Q3   bool // the next request still has questions 2 and 3
			diskNotes bool // ... and the "disk not rewound" notice
		}{
			{"files", "v1", true, false},
			{"conversation", "v3", false, true},
			{"both", "v1", false, false},
		}
		for _, c := range cases {
			t.Run(c.scope, func(t *testing.T) {
				rc := startRewindChat(t, binary, 100*time.Millisecond, "")
				rc.threeTurns(t)
				before := len(rc.fake.Requests())
				out := rc.rewind(t, "2 "+c.scope)
				if c.scope != "conversation" {
					check(t, "the result lists the restored file", regexp.MustCompile(`恢复 .*w\.txt`).MatchString(out))
				}
				check(t, "file on disk", readFile(rc.target) == c.fileWant)
				rc.ask(t, "第四问 hello")
				next := before + 1
				assert(t, "the fourth question reached the model", rc.fake.RequestHas(next, "第四问 hello"))
				requireRequestText(t, rc.fake, next, rwQ1, true)
				requireRequestText(t, rc.fake, next, rwQ2, c.hasQ2Q3)
				requireRequestText(t, rc.fake, next, rwQ3, c.hasQ2Q3)
				requireRequestText(t, rc.fake, next, rwNoticeDisk, c.diskNotes)
				check(t, "the fourth question does not write", readFile(rc.target) == c.fileWant)
				rc.exit(t)
			})
		}
	})
	t.Run("compaction", func(t *testing.T) {
		rc := startRewindChat(t, binary, 100*time.Millisecond, "")
		rc.threeTurns(t)
		rc.compact(t)
		rc.ask(t, "第四问 write v4")
		rc.rewind(t, "2 both") // back to before :compact
		check(t, "file is v1", readFile(rc.target) == "v1")
		// A new :compact builds a fresh handoff package from what is still visible.
		n := len(rc.fake.Requests())
		rc.compact(t)
		rc.ask(t, "第五问 hello")
		assert(t, "the compaction request and the next turn were answered", len(rc.fake.Requests()) == n+2)
		for _, k := range []int{n + 1, n + 2} { // the handoff request, then the turn that carries the package
			for _, gone := range []string{rwQ2, rwQ3, "第四问"} {
				requireRequestText(t, rc.fake, k, gone, false)
			}
			requireRequestText(t, rc.fake, k, rwQ1, true)
		}
		requireRequestText(t, rc.fake, n+2, "最近的用户请求", true) // the package is really there
		rc.exit(t)
	})

	t.Run("snapshots cleaned", func(t *testing.T) {
		// keep_turns 1: opening turn 3 cleans the checkpoints of turns 1 and 2.
		rc := startRewindChat(t, binary, 100*time.Millisecond, "  snapshots:\n    keep_turns: 1\n")
		rc.threeTurns(t)
		out := rc.rewind(t, "2 both")
		check(t, "the preview lists the cleaned file as unrecoverable", regexp.MustCompile(`无法恢复 w\.txt`).MatchString(out))
		check(t, "the recoverable part is still restored", regexp.MustCompile(`恢复 .*w\.txt`).MatchString(out))
		check(t, "the conversation is rewound anyway", strings.Contains(out, "对话已回退"))
		rc.ask(t, "第四问 hello")
		requireRequestText(t, rc.fake, len(rc.fake.Requests()), rwQ3, false)
		rc.exit(t)
	})

	t.Run("fork", func(t *testing.T) {
		rc := startRewindChat(t, binary, 100*time.Millisecond, "")
		rc.threeTurns(t)
		source := len(rc.fake.Requests()) // request 5 is question 3's first one
		m := rc.s.Mark()
		rc.s.SendLine(":fork 3")
		expect(t, rc.s, `已分叉出新会话 [A-Za-z0-9_]+`, m)
		forked := ""
		if f := rwForkID.FindStringSubmatch(rc.s.Since(m)); f != nil {
			forked = f[1]
		}
		assert(t, "the fork prints the new session id", forked != "")
		time.Sleep(300 * time.Millisecond)
		// The source is untouched: the chat did not switch and the next question
		// still sees all three earlier ones.
		rc.ask(t, "第四问 hello")
		for _, q := range []string{rwQ1, rwQ2, rwQ3} {
			requireRequestText(t, rc.fake, source+1, q, true)
		}
		check(t, "the file is still v3", readFile(rc.target) == "v3")
		sourceID := rc.exit(t)
		assert(t, "the source session id differs from the fork", sourceID != "" && sourceID != forked)

		// Resume the fork and talk on: it carries questions 1 and 2 only.
		rc.start(t, "", "--resume", forked)
		rc.ask(t, "续聊 hello")
		resumed := rc.fake.Requests()[source+1]
		want := rc.fake.Requests()[4] // question 3's request in the source
		assert(t, "the fork's request ends with the new question", resumed.Msgs[len(resumed.Msgs)-1].Text == "续聊 hello")
		assert(t, "the fork has as many messages as the source before question 3", len(resumed.Msgs) == len(want.Msgs))
		for i := 0; i < len(want.Msgs)-1; i++ {
			if resumed.Msgs[i].Role != want.Msgs[i].Role || resumed.Msgs[i].Text != want.Msgs[i].Text {
				t.Fatalf("message %d of the fork differs from the source before question 3:\nfork:   %s %q\nsource: %s %q",
					i, resumed.Msgs[i].Role, resumed.Msgs[i].Text, want.Msgs[i].Role, want.Msgs[i].Text)
			}
		}
		requireRequestText(t, rc.fake, source+2, rwQ2, true)
		requireRequestText(t, rc.fake, source+2, rwQ3, false)
		rc.exit(t)
	})

	t.Run("mid-turn", func(t *testing.T) {
		rc := startRewindChat(t, binary, 2*time.Second, "")
		m := rc.s.Mark()
		rc.s.SendLine("第一问 hello")
		assert(t, "the turn reached the model", rc.fake.WaitArrived(1, 15*time.Second))
		rc.s.SendLine(":rewind 1")
		expect(t, rc.s, `回合进行中，等它结束再执行本地命令`, m)
		waitTurnEnd(t, rc.s, m)
		// The line went back to the input line: Enter alone runs it, without retyping.
		m = rc.s.Mark()
		rc.s.SendLine("")
		expect(t, rc.s, `将回退到第 1 轮`, m)
		expect(t, rc.s, rwConfirm, m)
		rc.s.SendLine("n") // really answer the confirmation: no
		expect(t, rc.s, `已取消`, m)
		time.Sleep(300 * time.Millisecond)
		for _, r := range rc.fake.Requests() {
			check(t, "the command was not sent to the model as a steer", !strings.Contains(r.Messages, ":rewind"))
		}
		check(t, "only the one turn ran", len(rc.fake.Requests()) == 1)
		rc.exit(t)
	})
}

// compact runs :compact and waits for its report.
func (rc *rewindChat) compact(t *testing.T) {
	t.Helper()
	m := rc.s.Mark()
	rc.s.SendLine(":compact")
	expect(t, rc.s, `Compacted context`, m)
	time.Sleep(300 * time.Millisecond)
}
