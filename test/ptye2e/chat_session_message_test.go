//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

const (
	// sessionMsgReply is what both chats answer with (the fake's text reply).
	sessionMsgReply = "SESSION_OK"
	// sessionMsgAsk is the question the person asks chat A to pass on.
	sessionMsgAsk = "你的工作目录里有什么文件？"
	// sessionMsgHeld is what the receiver says when it held a message at the hop
	// limit (chat.sessionQueuedHint).
	sessionMsgHeld = "已排队"
	// sessionMsgMaxRequests bounds the model requests of the whole exchange: A's
	// person turn, B's answer and A's answer are six, plus room for a retry.
	sessionMsgMaxRequests = 8
	// sessionMsgTimeout bounds every step of the exchange, so a loop that never
	// stops fails instead of hanging.
	sessionMsgTimeout = 90 * time.Second
)

// TestChatSessionMessage ports scripts/ptysmoke/chat_session_message.py: two
// chats messaging each other, stopped by the hop limit.
//
// Two chat processes share one SUGA_HOME (one fake model serves both, in the fake's
// session mode): the person asks chat A to ask chat B what its working directory
// holds. The model of A calls session_message send, which starts a turn in B; B's
// model answers with a session_message send of its own, which starts a turn in A,
// and so on. The chain has to stop by itself: a message at
// tools.session_message.max_hops (default 3) starts no turn in the receiver - it is
// only queued, the person sees 已排队 on that terminal, and the exchange ends.
//
// Python check -> Go assertion:
//
//	B is running / A is running              session id and short name from :session
//	A calls session_message                  Expect(✦ session_message)
//	B reads the message as A's               Expect("[来自会话 <a name>（<a id>）的消息]")
//	A reads B's answer as B's                Expect("[来自会话 <b name>（<b id>）的消息]")
//	the 3rd hop is queued, not answered      Expect(已排队)
//	B names the sender of the held message   Expect("来自会话 <a name> 的消息已排队")
//	the model was asked at most 8 times      len(fake.Requests()) <= 8
//	A ran two turns, B ran one turn          trace turn counts of both sessions
func TestChatSessionMessage(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "session", Delay: 200 * time.Millisecond, Reply: sessionMsgReply}
	server := httptest.NewServer(fake)
	defer server.Close()
	work := t.TempDir()
	// One home for both chats: they are two terminals of the same person, and the
	// short names the model resolves live in it. GOFER_JOB_ID is dropped because
	// these chats stand in for a person's terminals, not for a gofer job (a job
	// does not get session_message) - this test may well run inside one.
	o := chatOpts{Home: t.TempDir(), Work: work, URL: server.URL, DropEnv: []string{"GOFER_JOB_ID"}}
	writeChatHome(t, o)
	env := chatEnv(t, o)

	// B first: A's first line names it, and its process record is what the tool
	// resolves the short name against.
	b, _ := startChatIn(t, binary, work, chatCols, 40, []string{"chat"}, env)
	bID, bName := sessionInfo(t, b)
	check(t, "B is running", bID != "" && bName != "")
	a, _ := startChatIn(t, binary, work, chatCols, 40, []string{"chat"}, env)
	aID, aName := sessionInfo(t, a)
	if !check2(t, "A is running", aID != "" && aName != "" && bName != "") {
		exitChat(t, a)
		exitChat(t, b)
		return
	}

	// The person asks A to talk to B. Both marks are taken before A's line: B may
	// answer within a second, before the A-side check returns.
	m := a.Mark()
	mb := b.Mark()
	a.SendLine(fmt.Sprintf("发给 %s：%s", bName, sessionMsgAsk))
	checkWithin(t, a, "A calls session_message", `✦ session_message`, m, sessionMsgTimeout)
	// B receives it as a tagged user message and starts a turn of its own.
	checkWithin(t, b, "B reads the message as A's",
		regexp.QuoteMeta(fmt.Sprintf("[来自会话 %s（%s）的消息]", aName, aID)), mb, sessionMsgTimeout)
	// ...and answers: A reads B's message the same way.
	checkWithin(t, a, "A reads B's answer as B's",
		regexp.QuoteMeta(fmt.Sprintf("[来自会话 %s（%s）的消息]", bName, bID)), m, sessionMsgTimeout)
	// The third hop reaches B, which holds it instead of starting a turn.
	checkWithin(t, b, "the 3rd hop is queued, not answered", sessionMsgHeld, mb, sessionMsgTimeout)
	checkWithin(t, b, "B names the sender of the held message",
		regexp.QuoteMeta(fmt.Sprintf("来自会话 %s 的消息已排队", aName)), mb, sessionMsgTimeout)
	// Nothing may follow: the exchange is over, so the count stays bounded.
	requests := len(fake.Requests())
	check(t, fmt.Sprintf("the model was asked at most %d times (%d)", sessionMsgMaxRequests, requests),
		requests <= sessionMsgMaxRequests)

	// Leave both chats so their traces hold every turn.
	exitChat(t, a)
	exitChat(t, b)
	aTurns, bTurns := sessionTurns(t, binary, work, env, aID), sessionTurns(t, binary, work, env, bID)
	check(t, fmt.Sprintf("A ran two turns (%d)", aTurns), aTurns == 2)
	// B ran the turn A's message started; the held 3rd hop started none.
	check(t, fmt.Sprintf("B ran one turn (%d)", bTurns), bTurns == 1)
}

// sessionInfo is the session id and short name chat printed, waiting for both the
// banner's session id and the :session line that adds the short name ("" when a
// line is missing).
func sessionInfo(t *testing.T, s *ptytest.Session) (string, string) {
	t.Helper()
	m := s.Mark()
	until(t, s, "banner: session id", `session id: `, m)
	// The start-up line has the id only; :session adds the short name once the
	// session socket is registered.
	promptReady(t, s, m)
	m = s.Mark()
	s.SendLine(":session")
	if !s.Expect(`· 会话 \S+`, m, 15*time.Second) {
		return "", ""
	}
	found := deliverySessionLine.FindStringSubmatch(s.Since(m))
	if found == nil {
		return "", ""
	}
	return found[1], found[2]
}

// sessionTurns is the number of turns a session's trace records (0 without one).
func sessionTurns(t *testing.T, binary, work string, env []string, session string) int {
	t.Helper()
	if session == "" {
		return 0
	}
	n := 0
	for _, event := range traceEvents(t, binary, work, env, session) {
		if event["kind"] == "turn" {
			n++
		}
	}
	return n
}

// checkWithin is until with a timeout of its own: the exchange between two chats
// waits for turns the other terminal starts.
func checkWithin(t *testing.T, s *ptytest.Session, name, pattern string, mark int, timeout time.Duration) bool {
	t.Helper()
	ok := s.Expect(pattern, mark, timeout)
	check(t, name, ok)
	if !ok {
		t.Logf("%s: chat tail %q", name, tail(s.Text()))
	}
	return ok
}
