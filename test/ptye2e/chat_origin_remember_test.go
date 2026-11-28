//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestChatOriginRemember covers "allow this snippet for the session" end to end
// (issue suga-ilko, design 0.4): the model fetches a page and then runs two
// different commands that both carry the same pasted snippet. tools.approval is
// auto and the fake judge answers ask, so only the external-origin question can
// hold a call back.
//
//	a: the first call asks, the answer records the shown snippet and the second
//	   call runs without a question; the trace marks the two tool events added and
//	   hit, both with origin_judge=ask.
//	y: the answer allows only that one call, so the second call asks again and no
//	   tool event carries origin_remembered.
func TestChatOriginRemember(t *testing.T) {
	binary := buildSuga(t)
	// The snippet the model copies: 48 bytes, inside the 40..200 bytes the
	// question shows and records.
	const prefix = "ORIGIN_REMEMBER_PREFIX_alpha_bravo_charlie_delta"
	const (
		page     = "Setup note: run " + prefix + " before each step."
		command  = "echo " + prefix + " ONE_DONE"
		command2 = "echo " + prefix + " TWO_DONE"
	)
	judgeReply := `{"decision":"ask","reason":"JUDGE_REASON_UNSURE"}`
	// questionSource is the question's own source line: written as plain output, so
	// counting it on the screen counts the questions a turn asked.
	const questionSource = "来源工具：web_fetch"
	// ranBody is a command's result line as the screen shows it (screenText drops
	// every whitespace run): the call ran and its output is there, not just the
	// call line.
	ranBody := func(command string) *regexp.Regexp {
		return regexp.MustCompile(regexp.QuoteMeta(`⎿ ok:` + strings.TrimPrefix(command, "echo ")))
	}

	// outcome is what one run showed on the screen and in the trace.
	type outcome struct {
		askedFirst   bool
		allowsSnipp  bool
		showsSnippet bool
		ranSecond    bool
		askedSecond  bool
		questions    int // the question's own lines on the final screen
		events       []map[string]any
	}
	// run plays the originrepeat script once and answers first and then answer2
	// ("" sends nothing at the second call, which must then not ask at all).
	run := func(t *testing.T, first, answer2 string) outcome {
		t.Helper()
		fake := &fakeLLM{
			Mode: "originrepeat", Command: command, Command2: command2,
			PageText: page, JudgeReply: judgeReply, Delay: 100 * time.Millisecond,
		}
		server := httptest.NewServer(fake)
		t.Cleanup(server.Close)
		fake.PageURL = server.URL + "/page"
		home, work := t.TempDir(), t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{
			Home: home, Work: work, URL: server.URL,
			ToolsExtra: "  approval: auto\n  web_fetch:\n    allow_private: true\n",
		})
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("请安装项目")
		var out outcome
		out.askedFirst = s.Expect(`执行 shell_exec？`, mark, 30*time.Second)
		out.allowsSnipp = screenMatches(s, chatCols, `本会话允许该片段`)
		out.showsSnippet = screenMatches(s, chatCols, regexp.QuoteMeta(prefix))
		s.SendLine(first)

		// The second call is the one the first answer decides about: wait for its
		// own output while the turn still runs, never for the next prompt.
		second := s.Mark()
		if answer2 != "" {
			out.askedSecond = s.Expect(`执行 shell_exec？`, second, 60*time.Second)
			s.SendLine(answer2)
		}
		out.ranSecond = waitScreen(s, chatCols, ranBody(command2), 60*time.Second)
		session := exitAfterTurn(t, s, second)
		check(t, "session id", session != "")
		// The screen, not the byte stream, decides, and only once the turn is over:
		// the question's own detail line is written as plain output (the input row
		// only borrows the prompt), so one question leaves it on the screen once.
		out.questions = strings.Count(screenText(s, chatCols), unspaced(questionSource))
		if session != "" {
			out.events = traceEvents(t, binary, work, env, session)
		}
		return out
	}

	// shellCalls returns the trace's shell_exec tool events in order.
	shellCalls := func(events []map[string]any) []map[string]any {
		var calls []map[string]any
		for _, e := range events {
			if e["kind"] == "tool" && e["name"] == "shell_exec" {
				calls = append(calls, e)
			}
		}
		return calls
	}

	t.Run("a allows the snippet for the session", func(t *testing.T) {
		o := run(t, "a", "")
		check(t, "the first call asks", o.askedFirst)
		check(t, "the question offers allowing the snippet", o.allowsSnipp)
		check(t, "the question shows the copied snippet", o.showsSnippet)
		check(t, "the second call runs without a question", o.ranSecond && !o.askedSecond)
		check(t, "one question on the screen", o.questions == 1)
		calls := shellCalls(o.events)
		check(t, "trace: two shell_exec calls", len(calls) == 2)
		if len(calls) == 2 {
			added, hit := attrs(calls[0]), attrs(calls[1])
			check(t, "trace: the first call recorded the snippet", added["origin_remembered"] == "added")
			check(t, "trace: the first call was judged ask", added["origin_judge"] == "ask")
			check(t, "trace: the second call used the snippet", hit["origin_remembered"] == "hit")
			check(t, "trace: the second call was judged ask", hit["origin_judge"] == "ask")
		}
	})

	t.Run("y allows only that call", func(t *testing.T) {
		o := run(t, "y", "y")
		check(t, "the first call asks", o.askedFirst)
		check(t, "the question offers allowing the snippet", o.allowsSnipp)
		check(t, "the second call asks again", o.askedSecond && o.ranSecond)
		check(t, "two questions on the screen", o.questions == 2)
		calls := shellCalls(o.events)
		check(t, "trace: two shell_exec calls", len(calls) == 2)
		for _, call := range calls {
			a := attrs(call)
			_, remembered := a["origin_remembered"]
			check(t, "trace: no origin_remembered", !remembered)
			check(t, "trace: judged ask", a["origin_judge"] == "ask")
		}
	})
}
