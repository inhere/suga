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
)

// "Smoke_WS" is cleaned to "smokews", so the banner proves the directory is the
// source of the name; the suffix is two characters of [a-z0-9].
const (
	shortNamePattern = `smokews-[a-z0-9]{2}`
	shortNameBanner  = `Suga chat · fake/m1[^\n]*会话 ` + shortNamePattern
	shortNameReply   = "收到啦"
)

// psRecord is the `suga ps --json` row of a session ("" fields when absent).
func psRecord(t *testing.T, binary, work string, env []string, session string) (map[string]any, bool) {
	t.Helper()
	out, _, _ := sugaRun(t, binary, work, env, "ps", "--json")
	var rows []map[string]any
	_ = json.Unmarshal([]byte(out), &rows)
	var match []map[string]any
	for _, row := range rows {
		if row["session_id"] == session {
			match = append(match, row)
		}
	}
	if len(match) != 1 {
		return nil, false
	}
	return match[0], true
}

// TestChatShortName ports scripts/ptysmoke/chat_shortname.py: a chat registers a
// short session name that `suga send` accepts.
//
// Python check -> Go assertion:
//
//	banner shows the short name / the same line shows the session id / registration produced a short name
//	ps --json lists the short name / ps lists the short name
//	send by short name succeeds / chat shows the delivered message / chat answers the delivered message
//	the typed prompt is answered / the first prompt is not a name (exit 3)
//	the record keeps the short name after the first prompt
//	:session shows id and name / exit summary names the session
//	resume shows the same short name / resume: prompt / resume exit summary keeps the name
//	a closed chat is no longer at that address (exit 3)
func TestChatShortName(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "text", Reply: shortNameReply, Delay: 300 * time.Millisecond})
	defer server.Close()
	home := t.TempDir()
	// A directory name with an underscore: the short name must be cleaned.
	work := filepath.Join(t.TempDir(), "Smoke_WS")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL})

	m := s.Mark()
	until(t, s, "banner shows the short name", shortNameBanner, m)
	nameFound := regexp.MustCompile(`会话 (` + shortNamePattern + `)`).FindStringSubmatch(s.Since(m))
	idFound := sessionIDLine.FindStringSubmatch(s.Since(m))
	check(t, "the same line shows the session id", idFound != nil)
	if nameFound == nil || idFound == nil {
		// Without an address the remaining checks cannot be meaningful (an empty
		// pattern would match anything).
		t.Fatal("registration produced a short name")
	}
	name, sid := nameFound[1], idFound[1]

	record, ok := psRecord(t, binary, work, env, sid)
	check(t, "ps --json lists the short name", ok && record["name"] == name)
	listed, _, _ := sugaRun(t, binary, work, env, "ps")
	check(t, "ps lists the short name", strings.Contains(listed, sid+"\t"+name+"\t"))

	// A message addressed by short name reaches the running chat and starts a turn.
	m = s.Mark()
	_, errOut, code := sugaRun(t, binary, work, env, "send", "--session", name, "来自短名称")
	check(t, "send by short name succeeds", code == 0 && strings.TrimSpace(errOut) == "delivered")
	until(t, s, "chat shows the delivered message", `\[来自 cli\] 来自短名称`, m)
	check(t, "chat answers the delivered message", s.Expect(shortNameReply, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))

	// A typed prompt becomes the session title, never an address: the record keeps
	// the short name and the prompt text is not resolvable.
	m = s.Mark()
	s.SendLine("第一句话")
	check(t, "the typed prompt is answered", s.Expect(shortNameReply, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
	_, _, code = sugaRun(t, binary, work, env, "send", "--session", "第一句话", "不要送到这里")
	check(t, "the first prompt is not a name", code == 3)
	record, ok = psRecord(t, binary, work, env, sid)
	check(t, "the record keeps the short name after the first prompt", ok && record["name"] == name)

	m = s.Mark()
	s.SendLine(":session")
	until(t, s, ":session shows id and name", `session id: `+regexp.QuoteMeta(sid)+` · 会话 `+regexp.QuoteMeta(name), m)

	summary := `会话 ` + regexp.QuoteMeta(name) + ` · 续接：suga chat --resume ` + regexp.QuoteMeta(sid) + `\r?\n`
	time.Sleep(300 * time.Millisecond)
	exitChatUntil(t, s, summary) // "exit summary names the session"

	// A resumed session registers under the name it was first given.
	resumed, _ := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, Args: []string{"--resume", sid}})
	m = resumed.Mark()
	until(t, resumed, "resume shows the same short name", `Suga chat · fake/m1[^\n]*会话 `+regexp.QuoteMeta(name), m)
	// Wait for the prompt (after the resumed reply preview): text typed ahead of
	// the line editor's raw mode keeps its characters but loses the Enter.
	until(t, resumed, "resume: ❯ prompt", `❯ `, m)
	time.Sleep(300 * time.Millisecond)
	m = resumed.Mark()
	resumed.SendLine(":quit")
	until(t, resumed, "resume exit summary keeps the name", summary, m)
	waitExit(t, resumed)
	_, _, code = sugaRun(t, binary, work, env, "send", "--session", name, "太晚了")
	check(t, "a closed chat is no longer at that address", code == 3)
}
