//go:build ptye2e

package ptye2e

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"

	"github.com/inhere/suga/internal/personal/store"
)

// Fork scenario lines, one place for the checks to read them from. The fork's
// task is labeled from its agent call ("smoke fork"); its notice is the generic
// background sub-agent one, holding the report whole.
const (
	// forkAsk is the line whose model call is the agent tool with type: fork.
	forkAsk = "调研一下 fork"
	// forkPlain is a plain turn (no fork call): its reply is text.
	forkPlain = "你好"
	// forkBg is the :bg command that starts the same read-only fork from the prompt.
	forkBg = ":bg 查一下 SMOKE"
	// forkOutput asks for the killed task's own output.
	forkOutput = "看输出"

	forkStarted = `background fork task t1 started: smoke fork`
	forkEnded   = `agent "smoke fork" finished \(stop=final\)`
	forkReport  = `SUBREPORT: SMOKE is in target\.txt`
	forkKilled  = `已终止任务 t1：`
	forkBgUsage = `用法：:bg <任务>`
	forkBgUp    = `分身 t1 已在后台启动`
	// forkTerminated is what a killed fork's task output holds (the report is
	// cancelled with the task; no failure notice follows).
	forkTerminated = `已终止`
)

// TestChatFork (background-fork design 0.5, plan T7) is the end-to-end chat
// scenario of a read-only fork, driven through the real binary and the Go fake
// model:
//
//	model: the model calls agent{type: fork}; the start line shows, the status
//	       line counts the running task, `:tasks` lists it, and when it ends the
//	       idle chat is woken with a notice holding its report. Then the session
//	       records: the fork's first request repeats the parent's request (system,
//	       tools, every message), the child session is agent_type=fork with a
//	       fork_base, its first model event says fork_reuse=prefix, the parent's
//	       task event has the step count, and the parent got a task notice.
//	bg:    ":bg 查一下 SMOKE" starts a fork from the prompt (its start line names
//	       the task and the description), ":bg" alone prints the usage, and a
//	       ":bg" typed while a turn runs goes back to the input line.
//	kill:  ":tasks -k t1" ends the fork: no notice follows, the task output reads
//	       已终止 and the fork's step ends stop=cancelled.
func TestChatFork(t *testing.T) {
	binary := buildSuga(t)

	t.Run("model", func(t *testing.T) {
		// Colors and steering off: the waiting line (which counts the running
		// fork) is only drawn then. ChildDelay keeps the fork running while the
		// parent's second model call is in flight.
		fake := &fakeLLM{Mode: "fork", Delay: 200 * time.Millisecond, ChildDelay: 8 * time.Second}
		s, work, home, env := forkChat(t, binary, fake, "chat:\n  steering: false\n")
		forkModelScenario(t, s, binary, work, home, env, fake)
	})

	t.Run("bg", func(t *testing.T) {
		// The delay keeps the plain turn of the mid-turn :bg running while the line
		// is typed into it.
		fake := &fakeLLM{Mode: "fork", Delay: time.Second}
		s, _, _, _ := forkChat(t, binary, fake, "")
		forkBgScenario(t, s)
	})

	t.Run("kill", func(t *testing.T) {
		fake := &fakeLLM{Mode: "fork", Delay: 200 * time.Millisecond, ChildDelay: 30 * time.Second}
		s, work, _, env := forkChat(t, binary, fake, "")
		forkKillScenario(t, s, binary, work, env)
	})
}

// forkChat starts one fork scenario's chat: a home and a workspace of its own, a
// target.txt the fork's grep finds, and the extra config of the scenario. It
// returns the session, the workspace, the home (to read the session records) and
// the child's environment.
func forkChat(t *testing.T, binary string, fake *fakeLLM, extra string) (*ptytest.Session, string, string, []string) {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	work, home := t.TempDir(), t.TempDir()
	writeTarget(t, work)
	s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, Extra: extra, Color: true})
	return s, work, home, env
}

// forkModelScenario is the model-triggered fork: the start line, the status
// line's count, the :tasks row, the notice that wakes the idle chat, and the
// records the session keeps of it.
func forkModelScenario(t *testing.T, s *ptytest.Session, binary, work, home string, env []string, fake *fakeLLM) {
	m := s.Mark()
	until(t, s, "model: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine(forkAsk)
	until(t, s, "model: agent call shown", `◆ agent`, m)
	until(t, s, "model: fork task started", forkStarted, m)
	// The status line counts a running task of kind agent: the fork shows there
	// while the parent's own turn goes on.
	untilScreen(t, s, chatCols, "model: status line counts the fork", `子 agent：1 个运行中`)
	until(t, s, "model: turn ends", `tokens · `, m)
	m = promptMark(t, s, m)
	s.SendLine(":tasks")
	untilScreen(t, s, chatCols, "model: :tasks lists the fork", `t1 · running`)
	untilScreen(t, s, chatCols, "model: :tasks names the fork task", `agent: smoke fork`)
	m = promptMark(t, s, m)
	// The fork's report is slow (ChildDelay): the idle chat is woken by its end.
	until(t, s, "model: idle chat woken by the fork's end", forkEnded, m)
	until(t, s, "model: the notice carries the report", forkReport, m)
	until(t, s, "model: notice turn ends", `tokens · `, m)
	exit := s.Mark()
	exitChat(t, s)
	session := sessionFromText(s.Since(exit))
	if !check2(t, "model: session id", session != "") {
		return
	}

	// The fork starts from the parent's request: same system prompt, same tools,
	// same model, and the parent's messages as the prefix of its own.
	parent, fork, ok := forkRequests(fake)
	if check2(t, "model: a fork request and the parent request before it", ok) {
		forkPrefixChecks(t, "model", parent, fork)
	}

	events := traceEvents(t, binary, work, env, session)
	task := findEvent(events, "task", "t1")
	if !check2(t, "model: the parent trace has the fork's task event", task != nil) {
		return
	}
	a := attrs(task)
	check(t, "model: the task is an agent task", str(a["kind"]) == "agent")
	check(t, "model: the task row says agent_type=fork", str(a["agent_type"]) == "fork")
	check(t, "model: the task row has the step count", attrNumber(a["steps"]) > 0)
	check(t, "model: the fork ended stop=final", str(a["stop"]) == "final")
	child := str(a["child_session_id"])
	if !check2(t, "model: the parent trace names the fork's session", child != "") {
		return
	}

	childEvents := traceEvents(t, binary, work, env, child)
	first := firstModelEvent(childEvents)
	if check2(t, "model: the fork session called the model", first != nil) {
		check(t, "model: its first model event says fork_reuse=prefix", str(attrs(first)["fork_reuse"]) == "prefix")
		check(t, "model: only that event carries fork_reuse", countAttr(childEvents, "fork_reuse") == 1)
	}

	// The notice turn is the parent's: chat injected it into a turn of its own.
	check(t, "model: the parent got a task notice", noticeTurn(events))

	meta := forkChildMeta(t, home, child)
	check(t, "model: the fork session is agent_type=fork", str(meta["agent_type"]) == "fork")
	check(t, "model: the fork session records fork_base=<turn>|call", strings.HasSuffix(str(meta["fork_base"]), "|call"))
}

// forkBgScenario is the prompt's own fork: :bg starts it (no model trigger), an
// empty :bg prints the usage, and a :bg typed during a turn is put back into the
// input line instead of being run.
func forkBgScenario(t *testing.T, s *ptytest.Session) {
	m := s.Mark()
	until(t, s, "bg: ❯ prompt", `❯ `, m)
	// A completed turn first: a :bg forks the session's last request, and a
	// session without one refuses the start (no_request).
	m = promptMark(t, s, m)
	s.SendLine(forkPlain)
	until(t, s, "bg: a plain turn ends", `tokens · `, m)
	m = promptMark(t, s, m)
	s.SendLine(":bg")
	untilScreen(t, s, chatCols, "bg: an empty task prints the usage", forkBgUsage)
	m = promptMark(t, s, m)
	s.SendLine(forkBg)
	untilScreen(t, s, chatCols, "bg: the fork is started", forkBgUp)
	// It runs in the background: its notice wakes the idle chat with its report.
	until(t, s, "bg: the notice carries the report", forkReport, m)
	until(t, s, "bg: notice turn ends", `tokens · `, m)

	// A :bg typed while a turn runs is not steering: chat says so and puts the line
	// back into the input line, and Enter alone runs it after the turn.
	m = promptMark(t, s, m)
	s.SendLine(forkPlain)
	s.SendLine(":bg 回合中")
	until(t, s, "bg: a :bg during a turn is refused as steering", `回合进行中，等它结束再执行本地命令`, m)
	waitTurnEnd(t, s, m)
	m = promptMark(t, s, m)
	s.SendLine("")
	untilScreen(t, s, chatCols, "bg: the put-back line runs after the turn", `已在后台启动`)
	exitChat(t, s)
}

// forkKillScenario kills a slow fork with :tasks -k: the task ends without a
// notice, its output reads 已终止, and the fork's step is recorded cancelled.
func forkKillScenario(t *testing.T, s *ptytest.Session, binary, work string, env []string) {
	m := s.Mark()
	until(t, s, "kill: ❯ prompt", `❯ `, m)
	m = promptMark(t, s, m)
	s.SendLine(forkAsk)
	until(t, s, "kill: fork task started", forkStarted, m)
	until(t, s, "kill: turn ends", `tokens · `, m)
	m = promptMark(t, s, m)
	s.SendLine(":tasks -k t1")
	until(t, s, "kill: killed", forkKilled, m)
	// A killed fork leaves no notice (its report is cancelled with it): the next
	// turn is a local command, and the model's output read shows 已终止.
	killMark := m
	m = promptMark(t, s, m)
	s.SendLine(":tasks")
	untilScreen(t, s, chatCols, "kill: :tasks lists it as killed", `t1 · killed`)
	check(t, "kill: the kill leaves no notice turn", !s.Contains(`\[background task notice\]`, killMark))
	m = promptMark(t, s, m)
	s.SendLine(forkOutput)
	until(t, s, "kill: turn ends", `tokens · `, m)
	exit := s.Mark()
	exitChat(t, s)
	session := sessionFromText(s.Since(exit))
	if !check2(t, "kill: session id", session != "") {
		return
	}
	events := traceEvents(t, binary, work, env, session)
	a := attrs(findEvent(events, "task", "t1"))
	check(t, "kill: the task event is still a fork", str(a["agent_type"]) == "fork")
	check(t, "kill: the fork stopped cancelled", str(a["stop"]) == "cancelled")
	// The task output the model read is what a killed fork leaves: the report is
	// cancelled with it. The export carries every tool result whole.
	exported, _, _ := sugaRun(t, binary, work, env, "inspect", "export", session)
	check(t, "kill: the task output reads 已终止", strings.Contains(exported, forkTerminated))
}

// promptMark waits for a fresh prompt after mark and returns the mark of that
// wait, so the next line is only sent when chat is ready for it.
func promptMark(t *testing.T, s *ptytest.Session, mark int) int {
	t.Helper()
	promptReady(t, s, mark)
	return s.Mark()
}

// writeTarget writes the target.txt the fake fork's grep finds.
func writeTarget(t *testing.T, work string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(work, "target.txt"), []byte("SMOKE_TARGET here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// forkRequests finds the first fork request in the model's log and the parent
// request it forked from: the last parent request before it that carries no fork
// start line (the started result is what the parent's later requests hold, so
// this is the request whose step started the fork).
func forkRequests(fake *fakeLLM) (parent, fork fakeRequest, ok bool) {
	reqs := fake.Requests()
	for i, req := range reqs {
		if !isForkMsgs(req.Msgs) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if !isForkMsgs(reqs[j].Msgs) && !hasForkStartedLine(reqs[j]) {
				return reqs[j], req, true
			}
		}
		return fakeRequest{}, fakeRequest{}, false
	}
	return fakeRequest{}, fakeRequest{}, false
}

// hasForkStartedLine reports whether the request already carries the fork's start
// tool result (so it was made after the fork started).
func hasForkStartedLine(req fakeRequest) bool {
	for _, msg := range req.Msgs {
		if strings.Contains(msg.Text, "[background fork task") {
			return true
		}
	}
	return false
}

// forkPrefixChecks asserts design F1/F2 on the request log: the fork's first
// request carries the parent's system prompt, tools and model unchanged, and its
// messages start with the parent's own messages (the only additions are the
// assistant step and the directive tail).
func forkPrefixChecks(t *testing.T, label string, parent, fork fakeRequest) {
	t.Helper()
	check(t, label+": the fork runs on the parent's model", fork.Model == parent.Model)
	check(t, label+": the fork is offered the parent's tools",
		strings.Join(fork.Tools, ",") == strings.Join(parent.Tools, ","))
	system := len(fork.Msgs) > 0 && len(parent.Msgs) > 0 &&
		fork.Msgs[0].Role == "system" && parent.Msgs[0].Role == "system" && fork.Msgs[0].Text == parent.Msgs[0].Text
	check(t, label+": the fork's system prompt is the parent's", system)
	check(t, label+": the fork's messages start with the parent's", prefixOf(parent.Msgs, fork.Msgs))
	check(t, label+": the fork's tail carries the directive", isForkMsgs(fork.Msgs[len(parent.Msgs):]))
}

// prefixOf reports whether candidate starts with prefix, message by message: role,
// text and tool calls (a fork replays the parent's messages unchanged).
func prefixOf(prefix, candidate []fakeMsg) bool {
	if len(prefix) == 0 || len(candidate) <= len(prefix) {
		return false
	}
	for i, want := range prefix {
		got := candidate[i]
		if got.Role != want.Role || got.Text != want.Text || strings.Join(got.Tool, ",") != strings.Join(want.Tool, ",") {
			return false
		}
	}
	return true
}

// noticeTurn reports whether the session's trace holds a turn a background task
// notice was injected into (the run's own signal for it, as TestChatTasks reads
// it).
func noticeTurn(events []map[string]any) bool {
	for _, e := range events {
		if e["kind"] != "turn" {
			continue
		}
		if injected, _ := attrs(e)["injected"].(bool); injected {
			return true
		}
	}
	return false
}

// firstModelEvent is the first model event of a trace without a purpose: the
// session's own loop call (a compaction or handoff call carries one).
func firstModelEvent(events []map[string]any) map[string]any {
	for _, e := range events {
		if e["kind"] != "model" {
			continue
		}
		if _, purpose := attrs(e)["purpose"]; purpose {
			continue
		}
		return e
	}
	return nil
}

// countAttr counts the trace events carrying attr key.
func countAttr(events []map[string]any, key string) int {
	n := 0
	for _, e := range events {
		if _, ok := attrs(e)[key]; ok {
			n++
		}
	}
	return n
}

// attrNumber is a trace attr as an int (0 when it is not a number): an attr read
// back from the store lost the Go type it was written with.
func attrNumber(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// forkChildMeta is the meta of the fork's session, read from the store of the
// scenario's home. No CLI output carries session meta, so the record has to be
// opened directly (TestRunWithSubAgent reads it the same way).
func forkChildMeta(t *testing.T, home, session string) map[string]any {
	t.Helper()
	dbPath := filepath.Join(home, "db", "sessions.db")
	repo, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store %s: %v", dbPath, err)
	}
	defer func() { _ = repo.Close() }()
	sess, err := repo.GetSession(context.Background(), session)
	if err != nil {
		t.Fatalf("read session %s: %v", session, err)
	}
	return sess.Meta
}
