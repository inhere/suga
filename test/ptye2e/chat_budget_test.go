//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

const budgetStatus = `tokens · `

// budgetEvent is the attrs of the last budget trace event (nil when none).
func budgetEvent(events []map[string]any) map[string]any {
	var last map[string]any
	for _, e := range events {
		if e["kind"] == "budget" {
			last = attrs(e)
		}
	}
	return last
}

// TestChatBudget ports scripts/ptysmoke/chat_budget.py: token-budget status, the
// reminder and soft wrap-up, session short-circuit, estimated usage, estimate off,
// a returned steer starting a fresh budget turn, and the no-budget path. The trace
// is checked after every budgeted session. Each group has its own fake model.
//
// Python check -> Go assertion (same names, prefixed by the group as before):
//
//	turn and steer:    prompt / turn budget reaches 100% / wrap-up shown / first turn ends /
//	                   turn input stays available / steer starts a new budget turn (a second
//	                   "budget 100%" after the steer is sent) / second turn ends / returned
//	                   steer runs as the next turn / steer was sent to the fake model /
//	                   trace budget: event, reason max_tokens, scope turn, estimated false
//	session short-circuit: session budget 100% / wrap-up / returns to the prompt (a prompt
//	                   drawn after the line, not its echo) / no second wrap-up (no ▣) /
//	                   <= 2 model requests / trace scope session
//	estimate on/off:   budget ~100% and wrap-up (off: budget 0% and no wrap-up before the
//	                   turn ends) / turn ends / trace has estimated usage (on) / fake served
//	no budget:         the status line never mentions a budget
func TestChatBudget(t *testing.T) {
	binary := buildSuga(t)
	start := func(t *testing.T, fake *fakeLLM, args []string, setEnv ...string) (*ptytest.Session, []string, string) {
		server := httptest.NewServer(fake)
		t.Cleanup(server.Close)
		work := t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: work, URL: server.URL, Args: args, SetEnv: setEnv})
		return s, env, work
	}

	t.Run("turn and steer", func(t *testing.T) {
		fake := &fakeLLM{Mode: "text", Delay: 800 * time.Millisecond, PromptTokens: 10}
		s, env, work := start(t, fake, []string{"--budget-tokens", "12"})
		m := s.Mark()
		until(t, s, "prompt", `❯ `, m)
		s.SendLine("第一轮")
		until(t, s, "turn budget reaches 100%", `budget 100%`, m)
		until(t, s, "token budget wrap-up is shown", `本轮因 token 预算用尽收尾`, m)
		until(t, s, "first turn ends", budgetStatus, m)
		time.Sleep(300 * time.Millisecond)

		// A steer is inserted while the first request is waiting. The completed
		// budget turn hands it back as the next turn.
		m = s.Mark()
		s.SendLine("主任务")
		until(t, s, "turn input stays available", `↳ `, m)
		time.Sleep(200 * time.Millisecond)
		s.SendLine("插话")
		// The turn "主任务" reaches 100% on its own, so a new budget turn shows as
		// a second "budget 100%" after the steer was sent.
		until(t, s, "steer starts a new budget turn", `(?s)budget 100%.*budget 100%`, m)
		// Status lines since "主任务": its own turn, then the turn the steer was
		// handed back to. Wait for the latter before leaving, or chat exits before
		// the steer reaches the model.
		until(t, s, "second turn ends", budgetStatus, m)
		until(t, s, "returned steer runs as the next turn", `(?s)`+budgetStatus+`.*`+budgetStatus, m)
		exitChat(t, s)
		session := sessionFromText(s.Text())
		_, steered := waitRequest(fake, 0, func(r fakeRequest) bool { return strings.Contains(r.User, "插话") }, 5*time.Second)
		check(t, "steer was sent to fake model", steered)
		if check2(t, "session id", session != "") {
			ev := budgetEvent(traceEvents(t, binary, work, env, session))
			check(t, "trace has budget event", ev != nil)
			check(t, "trace budget reason", ev["reason"] == "max_tokens")
			check(t, "trace budget scope", ev["scope"] == "turn")
			check(t, "trace budget is not estimated", ev["estimated"] == false)
		}
	})

	t.Run("session short-circuit", func(t *testing.T) {
		fake := &fakeLLM{Mode: "text", Delay: 800 * time.Millisecond, PromptTokens: 10}
		s, env, work := start(t, fake, []string{"--budget-tokens", "12", "--budget-scope", "session"})
		m := s.Mark()
		until(t, s, "prompt", `❯ `, m)
		s.SendLine("耗尽会话")
		until(t, s, "session budget wraps up", `session budget 100%`, m)
		until(t, s, "session wrap-up reason", `本轮因 token 预算用尽收尾`, m)
		t.Logf("DBG2 %q", tail(s.Since(m)))
		until(t, s, "session wrap-up turn ends", budgetStatus, m)
		time.Sleep(300 * time.Millisecond)
		requests := len(fake.Requests())
		before := s.Mark()
		s.SendLine("不应调用模型")
		// The prompt drawn after the line (not the echo of the line itself).
		until(t, s, "spent session still returns to prompt", `(?:\n|↳ )❯`, before)
		time.Sleep(500 * time.Millisecond)
		check(t, "short-circuit has no second wrap-up request", !s.Contains(`▣`, before))
		check(t, "session short-circuit made no extra model request", len(fake.Requests()) == requests && requests <= 2)
		exitChat(t, s)
		if session := sessionFromText(s.Text()); check2(t, "session id", session != "") {
			ev := budgetEvent(traceEvents(t, binary, work, env, session))
			check(t, "short-circuit trace is session scoped", ev["scope"] == "session")
		}
	})

	for _, off := range []bool{false, true} {
		name := "estimate on"
		var setEnv []string
		if off {
			name, setEnv = "estimate off", []string{"SUGA_BUDGET_ESTIMATE=off"}
		}
		t.Run(name, func(t *testing.T) {
			fake := &fakeLLM{Mode: "text", Delay: 800 * time.Millisecond, PromptTokens: 10, NoUsage: true}
			s, env, work := start(t, fake, []string{"--budget-tokens", "5"}, setEnv...)
			m := s.Mark()
			until(t, s, "prompt", `❯ `, m)
			s.SendLine("无用量")
			if off {
				until(t, s, "estimate off shows zero budget", `budget 0%`, m)
				until(t, s, "estimate turn ends", budgetStatus, m)
				// Wrap-up text would come before the status line of the turn.
				check(t, "estimate off does not wrap up", !s.Contains(`本轮因 token 预算用尽收尾`, m))
			} else {
				until(t, s, "estimated budget is marked", `budget ~100%`, m)
				until(t, s, "estimated budget wraps up", `本轮因 token 预算用尽收尾`, m)
				until(t, s, "estimate turn ends", budgetStatus, m)
			}
			exitChat(t, s)
			session := sessionFromText(s.Text())
			if !off && check2(t, "session id", session != "") {
				estimated := false
				for _, e := range traceEvents(t, binary, work, env, session) {
					estimated = estimated || attrs(e)["estimated"] == true
				}
				check(t, "trace marks estimated usage", estimated)
			}
			check(t, "fake accepted no-usage mode", len(fake.Requests()) > 0)
		})
	}

	t.Run("no budget", func(t *testing.T) {
		fake := &fakeLLM{Mode: "text", Delay: 800 * time.Millisecond, PromptTokens: 10}
		s, _, _ := start(t, fake, nil)
		m := s.Mark()
		until(t, s, "prompt", `❯ `, m)
		s.SendLine("旧行为")
		ended := until(t, s, "turn ends", budgetStatus, m)
		// Only the status line may not mention a budget: the screen can also show
		// the work directory (suga-yk7d).
		mentions := false
		for _, line := range strings.Split(s.Since(m), "\n") {
			mentions = mentions || regexp.MustCompile(budgetStatus).MatchString(line) && strings.Contains(line, "budget")
		}
		check(t, "unconfigured status remains unchanged", ended && !mentions)
		exitChat(t, s)
	})
}
