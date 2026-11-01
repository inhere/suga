//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	loopHint   = "换一种方法"
	loopGrayR1 = `↻ 重复调用 file_read ×3，已提醒模型`
	loopGrayR3 = `↻ shell_exec 已失败 ×3，已提醒模型`
)

// TestChatLoopGuard ports scripts/ptysmoke/chat_loop_guard.py: what the loop guard
// (tools.loop_guard, on by default) does against the fake model's loop modes. One
// chat, one turn per scenario; the fake logs every request (Step, and whether it
// carried a reminder: hintKind).
//
// Python check -> Go assertion (scenario name prefixes the check, as before):
//
//	on: gray line R1 / exactly two gray lines / >= 3 repeated calls / >= 4 requests /
//	    earlier requests carry no hint / the next request after the third result carries
//	    the "repeat" hint / trace loop_guard events: first is R1 count 3, last is R2 count 4
//	off (tools.loop_guard: false): no gray line / no hint text on screen / >= 4 requests /
//	    no hint reached the model / no loop_guard trace event
//	edit-shell: >= 7 requests / no gray line / no hint text / no hint reached the model /
//	    no trace event
//	fail-shell: gray line R3 / >= 3 requests / "r3" hint in a later request / trace has only
//	    R3 events, the first with count 3
func TestChatLoopGuard(t *testing.T) {
	binary := buildSuga(t)

	// run is one chat session with one turn; verify gets the screen text, the fake's
	// requests and the trace events.
	run := func(t *testing.T, fake *fakeLLM, toolsExtra string, verify func(screen string, reqs []fakeRequest, events []map[string]any)) {
		fake.Delay = 200 * time.Millisecond
		server := httptest.NewServer(fake)
		defer server.Close()
		work := t.TempDir()
		// loop mode keeps reading this file; loop-edit edits target.txt old -> new -> old.
		for name, body := range map[string]string{
			"loop_target.txt": "SMOKE_LOOP_TARGET\n", "target.txt": "line one\nSMOKE_TARGET old\nline three\n",
		} {
			if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// approval: auto lets the shell_exec and file_edit calls run without a question.
		s, env := startChatOpts(t, binary, chatOpts{
			Home: t.TempDir(), Work: work, URL: server.URL, ToolsExtra: "  approval: auto\n" + toolsExtra,
		})
		m := s.Mark()
		until(t, s, "prompt", `❯ `, m)
		s.SendLine("把规则跑一遍")
		until(t, s, "turn ends", budgetStatus, m)
		time.Sleep(500 * time.Millisecond) // the last tool results and the guard's line render
		exitChat(t, s)
		session := sessionFromText(s.Text())
		var events []map[string]any
		if check2(t, "session id", session != "") {
			events = traceEvents(t, binary, work, env, session)
		}
		verify(s.Text(), fake.Requests(), events)
	}
	guards := func(events []map[string]any) (out []map[string]any) {
		for _, e := range events {
			if e["kind"] == "loop_guard" {
				out = append(out, attrs(e))
			}
		}
		return out
	}
	anyHint := func(reqs []fakeRequest) bool {
		for _, r := range reqs {
			if r.hintKind() != "" {
				return true
			}
		}
		return false
	}

	t.Run("on", func(t *testing.T) {
		run(t, &fakeLLM{Mode: "loop"}, "", func(screen string, reqs []fakeRequest, events []map[string]any) {
			check(t, "chat gray line R1", regexp.MustCompile(loopGrayR1).MatchString(screen))
			check(t, "exactly two gray lines", strings.Count(screen, "↻ 重复调用 file_read") == 2)
			repeated := 0
			for _, r := range reqs {
				if r.Step < 3 {
					repeated++
				}
			}
			check(t, "three repeated calls ran", repeated >= 3)
			if !check2(t, "four loop-mode requests logged", len(reqs) >= 4) {
				return
			}
			check(t, "earlier requests carry no hint", reqs[0].hintKind() == "" && reqs[1].hintKind() == "")
			later := false
			for _, r := range reqs[2:] {
				later = later || r.hintKind() == "repeat"
			}
			check(t, "third repeated result carries the hint into the next request", later)
			g := guards(events)
			if !check2(t, "trace has loop_guard events", len(g) >= 2) {
				return
			}
			check(t, "trace R1 count", g[0]["rule"] == "R1" && g[0]["count"] == float64(3))
			check(t, "trace R2 count", g[len(g)-1]["rule"] == "R2" && g[len(g)-1]["count"] == float64(4))
		})
	})

	// The reverse: tools.loop_guard: false must silence all of it.
	t.Run("off", func(t *testing.T) {
		run(t, &fakeLLM{Mode: "loop"}, "  loop_guard: false\n", func(screen string, reqs []fakeRequest, events []map[string]any) {
			check(t, "no gray line while off", !strings.Contains(screen, "↻ 重复调用 file_read"))
			check(t, "no hint text on screen while off", !strings.Contains(screen, loopHint))
			check(t, "four loop-mode requests logged without the guard", len(reqs) >= 4)
			check(t, "no hint reached the model", !anyHint(reqs))
			check(t, "no trace loop_guard events", len(guards(events)) == 0)
		})
	})

	// The same successful command with a successful edit between two runs is a
	// changed workspace, not a loop.
	t.Run("edit-shell", func(t *testing.T) {
		run(t, &fakeLLM{Mode: "loop-edit"}, "", func(screen string, reqs []fakeRequest, events []map[string]any) {
			check(t, "edits and commands both ran", len(reqs) >= 7)
			check(t, "no gray line for edit/command alternation", !strings.Contains(screen, "↻ 重复调用 shell_exec"))
			check(t, "no hint text for edit/command alternation", !strings.Contains(screen, loopHint))
			check(t, "no hint reached the model", !anyHint(reqs))
			check(t, "no trace loop_guard events", len(guards(events)) == 0)
		})
	})

	// The third failure of one command is R3.
	t.Run("fail-shell", func(t *testing.T) {
		fake := &fakeLLM{Mode: "loop-fail", LoopFailCommand: osCommand("false", "exit 1")}
		run(t, fake, "", func(screen string, reqs []fakeRequest, events []map[string]any) {
			check(t, "chat gray line R3", regexp.MustCompile(loopGrayR3).MatchString(screen))
			check(t, "three loop-mode requests logged", len(reqs) >= 3)
			r3 := false
			for _, r := range reqs {
				r3 = r3 || r.hintKind() == "r3"
			}
			check(t, "the failing command's third result carries the hint into the next request", r3)
			g := guards(events)
			if !check2(t, "trace has an R3 event", len(g) >= 1) {
				return
			}
			allR3 := true
			for _, a := range g {
				allR3 = allR3 && a["rule"] == "R3"
			}
			check(t, "trace R3 count", allR3 && g[0]["count"] == float64(3))
		})
	})
}
