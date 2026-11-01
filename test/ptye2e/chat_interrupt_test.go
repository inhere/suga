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

const (
	interruptTarget = "line one\nSMOKE_TARGET old\nline three\n"
	interruptEdited = "line one\nSMOKE_TARGET new\nline three\n"
	// The head of the interruption note the failed turn writes (replay.go) and the
	// prefix of its token in the fake's "seen" digest.
	interruptNote      = "上一回合运行失败"
	interruptNoteToken = "user:[" + interruptNote
	interruptPrompt    = "继续"
)

// TestChatInterrupt ports scripts/ptysmoke/chat_interrupt.py (suga-y0sk): an
// interrupted turn's record replays into the next turn. The fake model (edit mode,
// FailFrom 3 / FailCount 3, retry.max_attempts 3) runs file_grep and file_edit
// (target.txt changes on disk), then three 503s fail the turn.
//
// Python check -> Go assertion:
//
//	turn 1: file_grep ran / file_edit applied / retry shown / failed after its attempts / prompt is back
//	turn 1: the edit is on disk                     file == edited content
//	turn 2: reply arrives / request was sent        完成 + status line; request with the prompt as last user message
//	turn 2 request: note, grep call, edit call, edit result   Seen has interruptNoteToken, a:file_grep, a:file_edit, tool:edited
//	turn 2 request keeps them in order              indices of the four tokens are all present and ascending
//	exit / session id
//	trace: the interrupted turn is failed / the one after it completed
//	export: the note is stored                      suga.turn_interrupted and the note text
func TestChatInterrupt(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "edit", Delay: 200 * time.Millisecond, FailFrom: 3, FailCount: 3}
	server := httptest.NewServer(fake)
	defer server.Close()
	work := t.TempDir()
	target := filepath.Join(work, "target.txt")
	if err := os.WriteFile(target, []byte(interruptTarget), 0o600); err != nil {
		t.Fatal(err)
	}
	s, env := startChatOpts(t, binary, chatOpts{
		Home: t.TempDir(), Work: work, URL: server.URL,
		ToolsExtra: "  approval: auto\n", Extra: "retry:\n  max_attempts: 3\n  base_delay: 200ms\n  max_delay: 400ms\n",
	})

	m := s.Mark()
	until(t, s, "❯ prompt", `❯ `, m)

	// Turn 1: two tool calls run, then the request that would answer fails with
	// 503s until the retry policy gives up.
	m = s.Mark()
	s.SendLine("把 old 改成 new")
	until(t, s, "turn 1: file_grep ran", `target.txt:2: SMOKE_TARGET old`, m)
	until(t, s, "turn 1: file_edit applied", `ok: edited `, m)
	until(t, s, "turn 1: the retry is shown", `↻ 模型请求失败（HTTP 503）`, m)
	until(t, s, "turn 1: failed after its attempts", `Error: .*request failed after 3 attempts`, m)
	promptReady(t, s, m)
	check(t, "turn 1: the edit is on disk", readFile(target) == interruptEdited)

	// Turn 2: the model sees the interrupted turn again.
	m = s.Mark()
	s.SendLine(interruptPrompt)
	req, sent := waitRequest(fake, 0, func(r fakeRequest) bool { return r.User == interruptPrompt }, 20*time.Second)
	check(t, "turn 2 request was sent", sent)
	check(t, "turn 2: reply arrives", s.Expect(`完成`, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
	seen := req.Seen
	has := func(prefix string) bool { return indexPrefix(seen, prefix) >= 0 }
	check(t, "turn 2 request carries the interruption note", has(interruptNoteToken))
	check(t, "turn 2 request replays the grep call", containsString(seen, "a:file_grep"))
	check(t, "turn 2 request replays the edit call", containsString(seen, "a:file_edit"))
	check(t, "turn 2 request replays the edit result", has("tool:edited "))
	order := []int{indexPrefix(seen, "a:file_grep"), indexPrefix(seen, "a:file_edit"), indexPrefix(seen, "tool:edited "), indexPrefix(seen, interruptNoteToken)}
	check(t, "turn 2 request keeps them in order", order[0] >= 0 && sort.IntsAreSorted(order) && !containsInt(order, -1))
	if !sent || containsInt(order, -1) {
		t.Logf("seen: %q", seen)
	}

	exitChat(t, s)
	session := sessionFromText(s.Text())
	if !check2(t, "session id", session != "") {
		return
	}
	// The store keeps both turns and the note the failed one appended.
	var failed, completed bool
	for _, e := range traceEvents(t, binary, work, env, session) {
		if e["kind"] == "turn" {
			failed = failed || e["status"] == "failed"
			completed = completed || e["status"] == "completed"
		}
	}
	check(t, "trace: the interrupted turn is recorded failed", failed)
	check(t, "trace: the turn after it completed", completed)
	out, _, _ := sugaRun(t, binary, work, env, "inspect", "export", session)
	check(t, "export: the interruption note is stored", strings.Contains(out, "suga.turn_interrupted") && strings.Contains(out, interruptNote))
}

// indexPrefix is the index of the first token that starts with prefix (-1: none).
func indexPrefix(tokens []string, prefix string) int {
	for i, tok := range tokens {
		if strings.HasPrefix(tok, prefix) {
			return i
		}
	}
	return -1
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
