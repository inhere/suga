//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

// TestChatIdle ports scripts/ptysmoke/chat_idle.py: each model response takes
// longer than context.idle_after and reports a prompt above half the context
// threshold. A turn longer than idle_after does not trigger consolidation; a
// moment after the turn "空闲整理中…" then "已整理上下文" appear; input afterwards
// works; typing while a consolidation runs preempts it; the trace records the
// schedule events and the idle segment.
func TestChatIdle(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 2500 * time.Millisecond, PromptTokens: 8000})
	defer server.Close()
	work := t.TempDir()
	// Window 16000 -> threshold 12800; the fake reports 8000 prompt tokens (0.62).
	s, env := startChatOpts(t, binary, chatOpts{
		Home: t.TempDir(), Work: work, URL: server.URL,
		ProviderExtra: "    models:\n      m1:\n        context_window: 16000\n",
		Extra:         "context:\n  idle_after: 2s\n  idle_min_ratio: 0.5\n",
	})
	const running, done = `✦ 空闲整理中…`, `✦ 已整理上下文：\d+ → \d+ token`

	mark := s.Mark()
	expect(t, s, `Suga chat · fake/m1`, mark)

	// Each response takes 2.5s, longer than idle_after (2s): the turn pauses it.
	mark = s.Mark()
	s.SendLine("你好")
	expect(t, s, `完成`, mark)
	expect(t, s, `tokens · `, mark)
	check(t, "turn 1: not during the turn", !s.Contains(`空闲整理`, mark))

	mark = s.Mark()
	expectWithin(t, s, running, mark, 10*time.Second)
	expectWithin(t, s, done, mark, 15*time.Second)

	mark = s.Mark()
	s.SendLine("再来")
	expect(t, s, `完成`, mark)
	expect(t, s, `tokens · `, mark)

	// Next idle run: type while it waits for the summary (2.5s) -> preempted.
	mark = s.Mark()
	expectWithin(t, s, running, mark, 10*time.Second)
	mark = s.Mark()
	s.SendLine("第三轮")
	expect(t, s, `完成`, mark)
	expect(t, s, `tokens · `, mark)
	check(t, "preempt: no success line", !s.Contains(`已整理上下文`, mark))

	// Wait for the prompt: input typed before it stays in the line unsubmitted.
	time.Sleep(500 * time.Millisecond)
	mark = s.Mark()
	s.SendLine(":quit")
	expect(t, s, `续接：suga chat --resume (?:sess_|sg_|s_)`, mark)
	waitExit(t, s)

	sid := resumeID.FindStringSubmatch(s.Text())
	if sid == nil {
		t.Fatalf("no session id in the output:\n%s", tail(s.Text()))
	}
	cmd := exec.Command(binary, "inspect", "trace", sid[1])
	cmd.Dir, cmd.Env = work, env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("inspect trace: %v", err)
	}
	trace := string(out)
	check(t, "trace: schedule fired", regexp.MustCompile(`schedule idle-consolidate fired`).MatchString(trace))
	check(t, "trace: preempted run cancelled", regexp.MustCompile(`schedule idle-consolidate cancelled`).MatchString(trace))
	check(t, "trace: idle segment", regexp.MustCompile(`compaction compact ok .*reason=idle`).MatchString(trace))
}

// TestChatRetry ports scripts/ptysmoke/chat_retry.py: chat shows model retries
// and the trace counts them. The fake answers an HTTP 503, then a stream that
// carries only an overload error, then a stream that breaks after half the
// reply, then the whole reply.
func TestChatRetry(t *testing.T) {
	binary := buildSuga(t)
	const reply = "SMOKE_RETRY_REPLY"
	server := httptest.NewServer(&fakeLLM{
		Mode: "text", Delay: 200 * time.Millisecond, Reply: reply,
		FailFirst: 1, OverloadFirst: 1, BreakFirst: 1,
	})
	defer server.Close()
	work := t.TempDir()
	s, env := startChatOpts(t, binary, chatOpts{
		Home: t.TempDir(), Work: work, URL: server.URL,
		Extra: "retry:\n  base_delay: 200ms\n  max_delay: 400ms\n",
	})

	mark := s.Mark()
	expect(t, s, `❯ `, mark)
	mark = s.Mark()
	s.SendLine("你好")
	expect(t, s, `↻ 模型请求失败（HTTP 503）.*第 1 次`, mark)
	// The overload arrived as an error event inside the stream: the turn is
	// requested again instead of failing, and nothing was streamed yet, so the
	// notice carries no "将重新输出".
	expect(t, s, `↻ 模型请求失败（服务端过载/限流）.*第 1 次`, mark)
	expect(t, s, `↻ 模型请求失败（stream broken）.*将重新输出`, mark)
	expect(t, s, `▣ `+reply, mark)
	expect(t, s, `tokens · `, mark)

	session := ctrlCTwice(t, s)
	check(t, "session id", session != "")
	if session == "" {
		return
	}
	model := attrs(findEvent(traceEvents(t, binary, work, env, session), "model", ""))
	retries, _ := model["retries"].(float64)
	wait, _ := model["retry_wait_ms"].(float64)
	check(t, "trace: model event counts 3 retries", retries == 3)
	check(t, "trace: last retry reason", model["retry_reason"] == "stream broken")
	check(t, "trace: retry wait recorded", wait > 0)
}
