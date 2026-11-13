//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

const (
	// agentAsk is the line that makes the model call the agent tool.
	agentAsk = "调研一下"
	// agentBack asks for the agent call in the background, agentPlain is a plain
	// turn (the fake answers with text: no agent).
	agentBack  = "后台调研"
	agentPlain = "你好"

	// The lines the agent scenarios wait for.
	agentSummary = `子 agent smoke survey：1 次工具调用，耗时`
	agentResult  = `⎿ ok: SUBREPORT: SMOKE is in target\.txt`
	agentReport  = `SUBREPORT: SMOKE is in target\.txt`
	agentStarted = `background agent task t1 started: smoke survey`
	agentEnded   = `agent "smoke survey" finished \(stop=final\)`
	agentFailed  = `⎿ error: sub-agent failed`
	agentResume  = `续接：suga chat --resume (?:sess_|sg_|s_)`

	// agentProgressFull is the config of the scenarios that keep the per-call
	// sub-agent lines instead of the summary.
	agentProgressFull = "chat:\n  agent_progress: full\n"
)

// TestChatAgent ports scripts/ptysmoke/chat_agent.py: sub-agents (the agent tool)
// in chat.
//
// sync: by default the sub-agent's grep is summarized (no `  ·` line) with a
// count/duration line; the sync-full case keeps the old per-call progress line.
// The report comes back as the tool result, and the trace has the agent tool
// event naming the sub-agent session, which `suga session list` hides and
// `suga session list --agents` lists.
//
// cancel: the sub-agent is slow (ChildDelay); Ctrl-C during it ends chat cleanly
// (resume line), the interrupted turn reads 已中断本轮 (never the raw
// "context canceled"), and the sub-agent session is not left running.
//
// background: the agent call has background: true; the turn ends at once and
// the sub-agent's end wakes the idle chat with a notice holding its report.
//
// error: the sub-agent's model call fails for good (ChildFail): the status line
// must stop counting it when its error result comes back.
//
// cancel-status / kill-status: after a cancelled or killed sub-agent the next
// turn's status line must not count it.
//
// Python check -> Go assertion:
//
//	sync: ❯ prompt                                     until(❯ )
//	sync: agent call shown                             until(◆ agent)
//	sync full: sub-agent progress line                 until("  · file_grep") [sync-full]
//	sync summary: count and duration line              until(子 agent smoke survey：1 次工具调用，耗时)
//	sync summary: no per-call progress line            !Contains("  · file_grep") after the turn ended
//	sync: report is the result                         until(⎿ ok: SUBREPORT: SMOKE is in target.txt)
//	sync: turn ends                                    until(tokens ·)
//	sync: session id                                   the resume line's session id is non-empty
//	sync trace: agent event names the sub-agent session  attrs.child_session_id has a session prefix
//	sync trace: stop=final                             attrs.stop == "final"
//	sync: sessions hides the sub-agent session          session list --json --all holds the parent only
//	sync: sessions --agents lists it                   session list --agents holds the child
//	cancel: ❯ prompt / sub-agent at work               until(❯ ) / until("  · file_grep")
//	cancel: interrupted turn reads 已中断本轮          Expect(已中断本轮)
//	cancel: chat stays after one Ctrl-C                !Contains(续接：) after the interrupt
//	cancel: chat ends with the resume line             exitChat then Contains(续接：suga chat --resume)
//	cancel-off: chat ends with the resume line         Expect(resume line) after that one press
//	cancel: never shows the raw context canceled       !Contains(context canceled)
//	cancel: the sub-agent session is not left running  its row's status is not "running"
//	background: task started / turn ends               until(background agent task t1 started: smoke survey) / until(tokens ·)
//	background: summary / idle chat woken              until(子 agent …耗时) / until(agent "smoke survey" finished (stop=final))
//	background: report in the notice / notice turn ends until(SUBREPORT: …) / until(tokens ·)
//	error: agent call shown / failed sub-agent result  until(◆ agent) / until(⎿ error: sub-agent failed)
//	error: turn ends / no running count after it       until(tokens ·) / !Contains(子 agent：N 个运行中)
//	cancel-status: turn interrupted / chat takes next line  until(已中断本轮) / until(❯ )
//	cancel-status: no stale sub-agent count / turn ends     !Contains(子 agent：) / until(tokens ·)
//	kill-status: task started / turn ends              until(background agent task t1 started) / until(tokens ·)
//	kill-status: task listed as running                until(t1 · running)
//	kill-status: killed / task listed as killed        until(已终止任务 t1：) / until(t1 · killed)
//	kill-status: the kill's notice wakes a turn, then ends  until([background task notice]) / until(tokens ·)
//	kill-status: killing it again says it ended        until(任务 t1 已结束（killed），无需终止)
//	kill-status: no stale sub-agent count / turn ends  !Contains(子 agent：) / until(tokens ·)
func TestChatAgent(t *testing.T) {
	binary := buildSuga(t)

	t.Run("sync", func(t *testing.T) {
		fake := &fakeLLM{Mode: "agent", Agent: true, Delay: 200 * time.Millisecond}
		s, work, env := agentChat(t, binary, fake, "")
		agentSyncScenario(t, s, binary, work, env, false)
	})

	t.Run("sync-full", func(t *testing.T) {
		fake := &fakeLLM{Mode: "agent", Agent: true, Delay: 200 * time.Millisecond}
		s, work, env := agentChat(t, binary, fake, agentProgressFull)
		agentSyncScenario(t, s, binary, work, env, true)
	})

	// The two Ctrl-C groups: with steering on (the default) a press ends the turn
	// only, with chat.steering false it ends chat as before.
	t.Run("cancel", func(t *testing.T) {
		fake := &fakeLLM{Mode: "agent", Agent: true, Delay: 200 * time.Millisecond, ChildDelay: 30 * time.Second}
		s, work, env := agentChat(t, binary, fake, agentProgressFull)
		agentCancelScenario(t, s, binary, work, env, "cancel", true)
	})

	t.Run("cancel-off", func(t *testing.T) {
		fake := &fakeLLM{Mode: "agent", Agent: true, Delay: 200 * time.Millisecond, ChildDelay: 30 * time.Second}
		s, work, env := agentChat(t, binary, fake, agentProgressFull+"  steering: false\n")
		agentCancelScenario(t, s, binary, work, env, "cancel-off", false)
	})

	t.Run("background", func(t *testing.T) {
		fake := &fakeLLM{Mode: "agent", Agent: true, Background: true, Delay: 200 * time.Millisecond}
		s, _, _ := agentChat(t, binary, fake, "")
		agentBackgroundScenario(t, s)
	})

	t.Run("error", func(t *testing.T) {
		fake := &fakeLLM{Mode: "agent", Agent: true, AgentWhen: "调研", ChildFail: true, Delay: 400 * time.Millisecond}
		s, _, _ := agentChat(t, binary, fake, "")
		agentErrorScenario(t, s)
	})

	t.Run("cancel-status", func(t *testing.T) {
		fake := &fakeLLM{Mode: "agent", Agent: true, AgentWhen: "调研", ChildDelay: 30 * time.Second, Delay: 500 * time.Millisecond}
		s, _, _ := agentChat(t, binary, fake, agentProgressFull)
		agentCancelStatusScenario(t, s)
	})

	// ChildDelay keeps the sub-agent running, so :tasks has a task to kill.
	t.Run("kill-status", func(t *testing.T) {
		fake := &fakeLLM{Mode: "agent", Agent: true, AgentWhen: "调研", Background: true,
			ChildDelay: 30 * time.Second, Delay: 500 * time.Millisecond}
		s, _, _ := agentChat(t, binary, fake, "")
		agentKillStatusScenario(t, s)
	})
}

// agentChat starts one agent scenario's chat: a home and a workspace of its own,
// a target.txt the sub-agent's grep finds, and the extra config of the scenario.
// It returns the session, the workspace and the child's environment.
func agentChat(t *testing.T, binary string, fake *fakeLLM, extra string) (*ptytest.Session, string, []string) {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "target.txt"), []byte("SMOKE_TARGET here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: work, URL: server.URL, Extra: extra})
	return s, work, env
}

// agentSyncScenario runs one agent turn and checks the summary/full rendering, the
// report as the tool result and the session records of the sub-agent.
func agentSyncScenario(t *testing.T, s *ptytest.Session, binary, work string, env []string, full bool) {
	m := s.Mark()
	until(t, s, "sync: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine(agentAsk)
	until(t, s, "sync: agent call shown", `◆ agent`, m)
	if full {
		until(t, s, "sync full: sub-agent progress line", `  · file_grep`, m)
	} else {
		until(t, s, "sync summary: count and duration line", agentSummary, m)
	}
	until(t, s, "sync: report is the result", agentResult, m)
	until(t, s, "sync: turn ends", `tokens · `, m)
	if !full {
		// Checked once the turn has ended: right after the agent call the sub-agent
		// has not run its grep yet, so an early check proves nothing.
		check(t, "sync summary: no per-call progress line", !s.Contains(`  · file_grep`, m))
	}
	exit := s.Mark()
	exitChat(t, s)
	session := sessionFromText(s.Since(exit))
	if !check2(t, "sync: session id", session != "") {
		return
	}
	events := traceEvents(t, binary, work, env, session)
	agentAttrs := attrs(findEvent(events, "tool", "agent"))
	child := str(agentAttrs["child_session_id"])
	check(t, "sync trace: agent event names the sub-agent session",
		strings.HasPrefix(child, "sess_") || strings.HasPrefix(child, "sg_") || strings.HasPrefix(child, "s_"))
	check(t, "sync trace: stop=final", str(agentAttrs["stop"]) == "final")
	listed := agentSessionIDs(t, binary, work, env)
	check(t, "sync: sessions hides the sub-agent session",
		containsString(listed, session) && !containsString(listed, child))
	check(t, "sync: sessions --agents lists it", containsString(agentSessionIDs(t, binary, work, env, "--agents"), child))
}

// agentCancelScenario is Ctrl-C while a slow sub-agent runs: with steering the
// press ends this turn only (chat asks for the next line again), without it the
// press ends chat. Both ways the sub-agent session must not stay running.
func agentCancelScenario(t *testing.T, s *ptytest.Session, binary, work string, env []string, label string, steering bool) {
	m := s.Mark()
	until(t, s, label+": ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine(agentAsk)
	until(t, s, label+": sub-agent at work", `  · file_grep`, m)
	m = s.Mark()
	s.SendCtrlC() // while its report is slow to come
	// The interrupted turn is reported as 已中断本轮: the runner's raw
	// "context canceled" must not reach the terminal.
	check(t, label+": interrupted turn reads 已中断本轮", s.Expect(`已中断本轮`, m, 20*time.Second))
	if steering {
		// Only the turn ended: chat is still there and takes the next line. A
		// second press leaves (the confirmation window of the first one counts).
		check(t, label+": chat stays after one Ctrl-C", !s.Contains(`续接：`, m))
		left := s.Mark()
		exitChat(t, s)
		check(t, label+": chat ends with the resume line",
			regexpMatch(agentResume, s.Since(left)))
	} else {
		check(t, label+": chat ends with the resume line", s.Expect(agentResume, m, 20*time.Second))
		// The press that ends chat leaves it interrupted (exit 130), not idle: only
		// the exit itself is what this scenario waits for.
		if _, err := s.WaitExit(20 * time.Second); err != nil {
			t.Fatalf("%s: chat did not exit: %v", label, err)
		}
	}
	check(t, label+": never shows the raw context canceled", !s.Contains(`context canceled`, m))
	// The sub-agent is not left running (its turn was cancelled with the chat's).
	var child map[string]any
	for _, row := range agentSessionRows(t, binary, work, env, "--agents") {
		if str(row["parent"]) != "" {
			child = row
			break
		}
	}
	check(t, label+": the sub-agent session is not left running", child != nil && str(child["status"]) != "running")
}

// agentBackgroundScenario checks a background agent call: the turn ends at once,
// and the sub-agent's end wakes the idle chat with a notice holding its report.
func agentBackgroundScenario(t *testing.T, s *ptytest.Session) {
	m := s.Mark()
	until(t, s, "background: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine(agentBack)
	until(t, s, "background: task started", agentStarted, m)
	until(t, s, "background: turn ends", `tokens · `, m)
	m = s.Mark()
	until(t, s, "background: summary", agentSummary, m)
	until(t, s, "background: idle chat woken by its report", agentEnded, m)
	until(t, s, "background: report in the notice", agentReport, m)
	until(t, s, "background: notice turn ends", `tokens · `, m)
	exitChat(t, s)
}

// agentErrorScenario is a sub-agent that fails for good: its entry ends with the
// error result, so the waiting line goes back to 思考中… (it used to keep
// 子 agent：1 个运行中).
//
// NOTE: the waiting line is drawn by the spinner, off under NO_COLOR, so the "no
// running count" checks here guard the turn flow only; the label's lifecycle is
// covered by TestRendererAgentStatusLifecycle (internal/chat/render_test.go).
func agentErrorScenario(t *testing.T, s *ptytest.Session) {
	m := s.Mark()
	until(t, s, "error: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine(agentAsk)
	until(t, s, "error: agent call shown", `◆ agent`, m)
	until(t, s, "error: failed sub-agent result", agentFailed, m)
	r := s.Mark()
	until(t, s, "error: turn ends", `tokens · `, m)
	check(t, "error: no running count after the result", !s.Contains(`子 agent：\d+ 个运行中`, r))
	exitChat(t, s)
}

// agentCancelStatusScenario cancels a slow sub-agent and checks that the next
// turn's status line starts clean (no count of the cancelled sub-agent).
func agentCancelStatusScenario(t *testing.T, s *ptytest.Session) {
	m := s.Mark()
	until(t, s, "cancel-status: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine(agentAsk)
	until(t, s, "cancel-status: sub-agent at work", `  · file_grep`, m)
	m = s.Mark()
	s.SendCtrlC()
	until(t, s, "cancel-status: turn interrupted", `已中断本轮`, m)
	// The prompt is redrawn with cursor moves (which Text strips): wait for a
	// line-start prompt rather than the prompt's trailing space.
	check(t, "cancel-status: chat takes the next line", s.Expect(freshPrompt, m, 15*time.Second))
	// A plain turn (no 调研 in the message: the fake answers with text): its
	// waiting line must not count the cancelled sub-agent.
	m = s.Mark()
	s.SendLine(agentPlain)
	check(t, "cancel-status: no stale sub-agent count", !s.Contains(`子 agent：`, m))
	until(t, s, "cancel-status: turn ends", `tokens · `, m)
	exitChat(t, s)
}

// agentKillStatusScenario kills a running background agent with :tasks -k: the
// status line counts a background agent by its task, so the next turn must not
// show it either (Kill suppresses the notice that would drop the entry).
func agentKillStatusScenario(t *testing.T, s *ptytest.Session) {
	m := s.Mark()
	until(t, s, "kill-status: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine(agentBack)
	until(t, s, "kill-status: task started", `background agent task t1 started`, m)
	until(t, s, "kill-status: turn ends", `tokens · `, m)
	m = agentStep(t, s, m, ":tasks", "kill-status: task listed as running", `t1 · running`)
	m = agentStep(t, s, m, ":tasks -k t1", "kill-status: killed", `已终止任务 t1：`)
	// The kill cancels the sub-agent, whose run reports the failure as a task
	// notice (agent.go's startBackground Notify): that notice wakes a turn of its
	// own right after the kill. On Linux it arrives before the next line can be
	// typed, which then counts as a steer and never runs as a local command, so
	// wait for the notice's turn to end before asking for the listing again.
	until(t, s, "kill-status: the killed agent's notice wakes a turn", `\[background task notice\]`, m)
	until(t, s, "kill-status: notice turn ends", `tokens · `, m)
	// The listing itself is read off the screen: chat's write of the row can be
	// split by a repaint, which the terminal applies while the byte stream, ANSI
	// stripped, no longer spells the row out.
	m = agentRowStep(t, s, m, ":tasks", "kill-status: task listed as killed", `t1 · killed`)
	m = agentStep(t, s, m, ":tasks -k t1", "kill-status: killing it again says it ended",
		`任务 t1 已结束（killed），无需终止`)
	m = agentStep(t, s, m, agentPlain, "kill-status: turn ends", `tokens · `)
	check(t, "kill-status: no stale sub-agent count", !s.Contains(`子 agent：`, m))
	exitChat(t, s)
}

// agentRowStep is agentStep for a check on a line: it waits for a fresh prompt
// after mark, sends one line and reads the answer off the screen (untilRow).
func agentRowStep(t *testing.T, s *ptytest.Session, mark int, line, name, pattern string) int {
	t.Helper()
	promptReady(t, s, mark)
	m := s.Mark()
	s.SendLine(line)
	untilScreen(t, s, chatCols, name, pattern)
	return m
}

// agentStep waits for a fresh prompt after mark, sends one line and waits for its
// own output (the check named name); it returns the mark of that wait.
func agentStep(t *testing.T, s *ptytest.Session, mark int, line, name, pattern string) int {
	t.Helper()
	promptReady(t, s, mark)
	m := s.Mark()
	s.SendLine(line)
	until(t, s, name, pattern, m)
	return m
}

// agentSessionRows lists sessions the way `suga session list --json --all`
// reports them (--agents adds the sub-agent ones); it is empty on a bad listing.
func agentSessionRows(t *testing.T, binary, work string, env []string, extra ...string) []map[string]any {
	t.Helper()
	args := append([]string{"session", "list", "--json", "-n", "0", "--all"}, extra...)
	out, _, _ := sugaRun(t, binary, work, env, args...)
	var rows []map[string]any
	_ = json.Unmarshal([]byte(out), &rows)
	return rows
}

// agentSessionIDs is the id of every row agentSessionRows reports.
func agentSessionIDs(t *testing.T, binary, work string, env []string, extra ...string) []string {
	t.Helper()
	var ids []string
	for _, row := range agentSessionRows(t, binary, work, env, extra...) {
		ids = append(ids, str(row["id"]))
	}
	return ids
}
