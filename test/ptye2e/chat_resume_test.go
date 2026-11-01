//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

const (
	resumeTail  = "SMOKE_RESUME_TAIL"
	resumeNum   = 50
	resumeTotal = resumeNum + 1
	// Lines of the reply chat does not show: the preview is the last 40.
	resumeOmitted    = resumeTotal - 40
	resumeLabel      = "上一次回复："
	resumeIncomplete = "上一回合未正常结束"
	resumeRecovered  = "已恢复会话"
)

func resumeReply() string {
	lines := make([]string, 0, resumeTotal)
	for i := 1; i <= resumeNum; i++ {
		lines = append(lines, fmt.Sprintf("SMOKE line %02d", i))
	}
	return strings.Join(append(lines, resumeTail), "\n")
}

// TestChatResume ports scripts/ptysmoke/chat_resume.py: a chat resumed after the
// process that ran its last turn was killed. The fake model (text mode, a 51-line
// reply, 1s delay) answers once; a second turn is killed while the model is asked
// for it, leaving the session running. Chat runs with colors ON (the label is dim,
// the reply goes through the markdown renderer).
//
// Python check -> Go assertion:
//
//	first chat: banner, prompt, no last reply on a new session, the turn answers, the second
//	  turn reached the model (fake Arrived, counted before the delay so it is deterministic)
//	killed chat (Session.Close) -> session id
//	for `--resume <id>` and `--session-id <id>` (preview, text up to the first prompt):
//	  label above the first prompt / before the reply / reply keeps ▣ before it / last lines
//	  shown / head not shown / truncation hint "已截断前 N 行，完整记录见 suga inspect trace <id>" /
//	  killed turn flagged
//	--resume: no model call (Arrived unchanged), :exit alias prints the summary
//	--session-id: first prompt recovers the session (notice above the answer), answers, one more
//	  model request; a fourth turn is killed again
//	suga run --resume: exit 0, notice on stderr, answer on stdout
//	trace: 3 model events, 3 turn events, 2 session recovered events (previous_status running,
//	  turn_status cancelled, a turn_id each, different turns)
func TestChatResume(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "text", Delay: time.Second, Reply: resumeReply()}
	server := httptest.NewServer(fake)
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	// Colors on: the label is dim and the reply is rendered as markdown.
	var env []string
	start := func(args ...string) *ptytest.Session {
		s, e := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, Color: true, Args: args})
		env = e
		return s
	}

	first := start()
	m := first.Mark()
	until(t, first, "banner shows model", `Suga chat · fake/m1`, m)
	until(t, first, "❯ prompt", `❯ `, m)
	check(t, "a new session shows no last reply", !strings.Contains(first.Since(m), resumeLabel))
	m = first.Mark()
	first.SendLine("第一句")
	check(t, "the turn answers", first.Expect(resumeTail, m, 15*time.Second) && first.Expect(`tokens · `, m, 15*time.Second))
	time.Sleep(300 * time.Millisecond)
	first.SendLine("第二句")
	check(t, "the second turn reached the model", fake.WaitArrived(2, 15*time.Second))
	_ = first.Close() // killed mid-turn: nothing resets the running status

	listing, _, _ := sugaRun(t, binary, work, env, "session", "list", "--json", "-n", "1")
	sid := ""
	if m := sessionJSONID.FindStringSubmatch(listing); m != nil {
		sid = m[1]
	}
	if !check2(t, "session id", sid != "") {
		return
	}

	// previewChecks is what a chat continuing the session shows above its first prompt.
	previewChecks := func(s *ptytest.Session, name string) {
		until(t, s, name+": ❯ prompt", `❯ `, 0)
		t.Logf("DBG3 %q", tail(s.Text()))
		text := s.Text()
		before := text
		if cut := strings.Index(text, "❯ "); cut >= 0 {
			before = text[:cut]
		}
		marker, reply := strings.Index(before, "▣"), strings.Index(before, fmt.Sprintf("SMOKE line %02d", resumeOmitted+1))
		check(t, name+": the label is above the first prompt", strings.Contains(before, resumeLabel))
		check(t, name+": the label comes before the reply", reply >= 0 && strings.Index(before, resumeLabel) >= 0 && strings.Index(before, resumeLabel) < reply)
		check(t, name+": the reply keeps the ▣ marker", marker >= 0 && marker < reply)
		check(t, name+": the last lines of the reply are shown", strings.Contains(before, resumeTail) && reply >= 0)
		check(t, name+": the head of the long reply is not shown", !strings.Contains(before, fmt.Sprintf("SMOKE line %02d", resumeOmitted)))
		check(t, name+": the truncation hint names the trace command",
			strings.Contains(before, fmt.Sprintf("已截断前 %d 行，完整记录见 suga inspect trace %s", resumeOmitted, sid)))
		check(t, name+": the killed turn is flagged", strings.Contains(before, resumeIncomplete))
	}

	// Continuing only reads the session: the preview, no model call and nothing
	// recovered yet (the session is still running afterwards).
	resumed := start("--resume", sid)
	previewChecks(resumed, "resume")
	check(t, "resume: no model call", fake.Arrived() == 2)
	m = resumed.Mark()
	// :exit is the same command as :quit; leaving here proves the alias works.
	resumed.SendLine(":exit")
	until(t, resumed, "resume: exit summary", `续接：suga chat --resume `, m)
	waitExit(t, resumed)

	byID := start("--session-id", sid)
	previewChecks(byID, "--session-id")
	// The session is running with no live process behind it: the first prompt
	// recovers it and the turn answers.
	promptReady(t, byID, 0)
	m = byID.Mark()
	byID.SendLine("第三句")
	until(t, byID, "--session-id: the stale session is recovered", resumeRecovered, m)
	check(t, "--session-id: the new turn answers", byID.Expect(resumeTail, m, 15*time.Second) && byID.Expect(`tokens · `, m, 15*time.Second))
	text := byID.Since(m)
	check(t, "--session-id: the notice is above the answer", strings.Index(text, resumeRecovered) >= 0 && strings.Index(text, resumeRecovered) < strings.Index(text, resumeTail))
	check(t, "--session-id: the model was asked once more", fake.Arrived() == 3)
	// Leave it running again, for the `run --resume` path below.
	time.Sleep(300 * time.Millisecond)
	byID.SendLine("第四句")
	check(t, "the killed turn reached the model", fake.WaitArrived(4, 15*time.Second))
	_ = byID.Close()

	// `suga run --resume` takes the session lock itself, so it recovers the session
	// too; the notice goes to stderr and stdout keeps the answer.
	stdout, stderr, code := sugaRun(t, binary, work, env, "run", "--read-only", "--resume", sid, "--prompt", "第五句")
	check(t, "run --resume: exits 0", code == 0)
	check(t, "run --resume: the stale session is recovered", strings.Contains(stderr, resumeRecovered))
	check(t, "run --resume: the answer is on stdout", strings.Contains(stdout, resumeTail))

	// The whole scenario: three turns answered, two stale sessions recovered.
	kinds := map[any]int{}
	var recovered []map[string]any
	for _, e := range traceEvents(t, binary, work, env, sid) {
		kinds[e["kind"]]++
		if e["kind"] == "session" && e["name"] == "recovered" {
			recovered = append(recovered, e)
		}
	}
	check(t, "trace: one model call per answered turn (the killed ones wrote none)", kinds["model"] == 3)
	check(t, "trace: one turn event per answered turn (the killed ones wrote none)", kinds["turn"] == 3)
	check(t, "trace: one recovered event per recovery", len(recovered) == 2)
	turns, named := map[any]bool{}, true
	for _, e := range recovered {
		a := attrs(e)
		named = named && a["previous_status"] == "running" && a["turn_status"] == "cancelled" && e["turn_id"] != nil && e["turn_id"] != ""
		turns[e["turn_id"]] = true
	}
	check(t, "trace: each recovery names its killed turn and the old status", named && len(recovered) > 0)
	check(t, "trace: the two recoveries belong to different turns", len(turns) == len(recovered))
}
