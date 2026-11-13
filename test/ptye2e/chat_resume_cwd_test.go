//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

const (
	// resumeCwdMark is the content of the file only directory A has, so where the
	// read succeeds is where the session's file tools point.
	resumeCwdMark = "SMOKE_RESUME_CWD_A"
	// resumeCwdReadPath is the relative path the model reads.
	resumeCwdReadPath = "cwd_smoke.txt"
	// resumeCwdPrompt names the relative path, so the model's read proves the
	// directory the turn runs in.
	resumeCwdPrompt = "只读：读一下 " + resumeCwdReadPath
	// resumeCwdQuestion is the three-choice question a resume in another directory
	// asks before anything else happens.
	resumeCwdQuestion = `请选择 \[1/2/3\]`
)

// TestChatResumeCwd ports scripts/ptysmoke/chat_resume_cwd.py: a session records
// the directory it was created in, and continuing it elsewhere asks where to go on
// instead of silently working in the new directory.
//
// Python check -> Go assertion:
//
//	create: banner shows the model / no cwd question for a new session /
//	  exit summary names the resume command
//	                                  Expect(Suga chat · fake/m1), !Contains(QUESTION),
//	                                  Expect(续接：suga chat --resume )
//	create: the session records directory A   `session list --json` row's cwd == A
//	same-dir: ❯ prompt / no cwd question / exit summary
//	                                  Expect(❯ ), !Contains(QUESTION), Expect(续接：…)
//	cancel: the cwd question is asked before anything else / names the session's
//	  directory / names the current directory / offers going back, staying and
//	  cancelling / cancelling reports it and leaves / no chat was opened
//	                                  Expect(QUESTION), Since(mark) holds A, B,
//	                                  "1) 切到 A", "2) 在 B", "3) 取消"; then 已取消 and
//	                                  !Contains(❯ )
//	cancel: sessions.cwd is unchanged / nothing was recorded
//	                                  session row cwd == A, no session_cwd_changed event
//	back-to-a: chat starts / the relative file of directory A is found / the turn
//	  ends / sessions.cwd is unchanged
//	                                  Expect(❯ ), Expect(SMOKE_RESUME_CWD_A),
//	                                  Expect(tokens ·), session row cwd == A
//	stay-in-b: chat starts / exit summary / sessions.cwd moved to B
//	                                  Expect(❯ ), Expect(续接：…), session row cwd == B
//	stay-in-b: the trace records one move from A to B / `suga inspect trace` shows
//	  the move in one line
//	                                  one session_cwd_changed event with attrs from=A
//	                                  to=B; the text trace names both in one line
//	run-mismatch: exits non-zero / the error names the session's directory and this
//	  one / offers cd and --allow-cwd-change / no turn ran
//	                                  `run --read-only --output-format stream-json
//	                                  --resume` exit != 0, stderr holds A, B, "cd ",
//	                                  --allow-cwd-change, stdout holds no MARK
//	run-allow: exits 0 / the turn read the file of directory A / sessions.cwd moved
//	  back to A / the second move is in the trace
//	                                  the same command with --allow-cwd-change: exit 0,
//	                                  MARK in stdout, session row cwd == A, two moves
//	chat-piped: exits non-zero / the error names both directories / offers
//	  --allow-cwd-change / sessions.cwd is unchanged
//	                                  `chat --resume` with a piped stdin: exit != 0,
//	                                  stderr holds A, B, --allow-cwd-change; cwd == A
func TestChatResumeCwd(t *testing.T) {
	binary := buildSuga(t)
	work := resolvedTempDir(t)
	home, wsA, wsB := filepath.Join(work, "home"), filepath.Join(work, "a"), filepath.Join(work, "b")
	for _, dir := range []string{home, wsA, wsB} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The file the model reads: only A has it (the relative path must be missing
	// in B, which proves where the file tools point).
	if err := os.WriteFile(filepath.Join(wsA, resumeCwdReadPath), []byte(resumeCwdMark+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{Mode: "adddir", Delay: 300 * time.Millisecond, ReadPath: resumeCwdReadPath, Reply: "读完了"}
	server := httptest.NewServer(fake)
	defer server.Close()

	// approval: auto, so the file_read runs without a question of its own.
	o := chatOpts{Home: home, Work: wsA, URL: server.URL, ToolsExtra: "  approval: auto\n"}
	writeChatHome(t, o)
	env := chatEnv(t, o)

	// A first chat in A: it creates the session (recording A) without a turn.
	first, _ := startChatIn(t, binary, wsA, chatCols, 40, []string{"chat"}, env)
	mark := first.Mark()
	check(t, "create: banner shows the model", first.Expect(`Suga chat · fake/m1`, mark, 15*time.Second))
	check(t, "create: no cwd question for a new session", !first.Contains(resumeCwdQuestion, mark))
	mark = first.Mark()
	first.SendLine(":exit")
	check(t, "create: exit summary names the resume command", first.Expect(`续接：suga chat --resume `, mark, 15*time.Second))
	id := ""
	if m := resumeID.FindStringSubmatch(first.Since(mark)); m != nil {
		id = m[1]
	}
	check(t, "create: session id", id != "")
	waitExit(t, first)
	if id == "" {
		return
	}
	check(t, "create: the session records directory A", resumeSessionCwd(t, binary, wsA, env, id) == wsA)

	// 1) The session's own directory: nothing to ask.
	same, _ := startChatIn(t, binary, wsA, chatCols, 40, []string{"chat", "--resume", id}, env)
	mark = same.Mark()
	check(t, "same-dir: ❯ prompt", same.Expect(`❯ `, mark, 15*time.Second))
	check(t, "same-dir: no cwd question", !same.Contains(resumeCwdQuestion, mark))
	mark = same.Mark()
	same.SendLine(":exit")
	check(t, "same-dir: exit summary", same.Expect(`续接：suga chat --resume `, mark, 15*time.Second))
	waitExit(t, same)

	// 2) Choice 3: cancel, nothing is opened and nothing changes.
	resumeCwdQuestionScenario(t, binary, wsA, wsB, env, id, "3", "cancel", false)
	check(t, "cancel: sessions.cwd is unchanged", resumeSessionCwd(t, binary, wsA, env, id) == wsA)
	check(t, "cancel: nothing was recorded", len(resumeCwdMoves(t, binary, wsA, env, id)) == 0)

	// 3) Choice 1: the process moves to A, which the relative read proves.
	resumeCwdQuestionScenario(t, binary, wsA, wsB, env, id, "1", "back-to-a", true)
	check(t, "back-to-a: sessions.cwd is unchanged", resumeSessionCwd(t, binary, wsA, env, id) == wsA)

	// 4) Choice 2: continue in B, and record the move.
	resumeCwdQuestionScenario(t, binary, wsA, wsB, env, id, "2", "stay-in-b", false)
	check(t, "stay-in-b: sessions.cwd moved to B", resumeSessionCwd(t, binary, wsA, env, id) == wsB)
	moved := resumeCwdMoves(t, binary, wsB, env, id)
	oneMove := len(moved) == 1 && attrs(moved[0])["from"] == wsA && attrs(moved[0])["to"] == wsB
	check(t, "stay-in-b: the trace records one move from A to B", oneMove)
	traceText, _, _ := sugaRun(t, binary, wsB, env, "inspect", "trace", id)
	showsLine := false
	for _, line := range strings.Split(traceText, "\n") {
		if strings.Contains(line, "session_cwd_changed") && strings.Contains(line, wsA) && strings.Contains(line, wsB) {
			showsLine = true
		}
	}
	check(t, "stay-in-b: `suga inspect trace` shows the move in one line", showsLine)

	// 5) run --resume from A (the session lives in B now) is refused.
	stdout, stderr, code := resumeRun(t, binary, wsA, env, id)
	t.Logf("run --resume in A: exit %d, stdout: %.120s, stderr: %.300s", code, stdout, stderr)
	check(t, "run-mismatch: exits non-zero", code != 0)
	check(t, "run-mismatch: the error names the session's directory and this one",
		strings.Contains(stderr, wsA) && strings.Contains(stderr, wsB))
	check(t, "run-mismatch: the error offers cd and --allow-cwd-change",
		strings.Contains(stderr, "cd ") && strings.Contains(stderr, "--allow-cwd-change"))
	check(t, "run-mismatch: no turn ran", !strings.Contains(stdout, resumeCwdMark))

	// 6) --allow-cwd-change continues in A and moves the session back.
	stdout, _, code = resumeRun(t, binary, wsA, env, id, "--allow-cwd-change")
	check(t, "run-allow: exits 0", code == 0)
	// The stream-json output carries the tool result, so the read shows there.
	check(t, "run-allow: the turn read the file of directory A", strings.Contains(stdout, resumeCwdMark))
	check(t, "run-allow: sessions.cwd moved back to A", resumeSessionCwd(t, binary, wsA, env, id) == wsA)
	check(t, "run-allow: the second move is in the trace", len(resumeCwdMoves(t, binary, wsA, env, id)) == 2)

	// 7) chat without a terminal (a pipe) is refused like run.
	cmd := exec.Command(binary, "chat", "--resume", id)
	cmd.Dir, cmd.Env = wsB, env
	cmd.Stdin = strings.NewReader(":quit\n")
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	pipedCode := 0
	if exit, ok := err.(*exec.ExitError); ok {
		pipedCode = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("piped chat: %v", err)
	}
	check(t, "chat-piped: exits non-zero", pipedCode != 0)
	check(t, "chat-piped: the error names both directories",
		strings.Contains(errOut.String(), wsA) && strings.Contains(errOut.String(), wsB))
	check(t, "chat-piped: the error offers --allow-cwd-change", strings.Contains(errOut.String(), "--allow-cwd-change"))
	check(t, "chat-piped: sessions.cwd is unchanged", resumeSessionCwd(t, binary, wsB, env, id) == wsA)
}

// resumeCwdQuestionScenario runs one chat resumed in B and answers the three-choice
// question: the question must come up before anything else, and the choice decides
// where the session goes on. A choice of "3" leaves without opening chat; the
// others start chat, and followUp reads A's relative file to prove the directory
// the tools got.
func resumeCwdQuestionScenario(t *testing.T, binary, wsA, wsB string, env []string, id, choice, name string, followUp bool) *ptytest.Session {
	t.Helper()
	s, _ := startChatIn(t, binary, wsB, chatCols, 40, []string{"chat", "--resume", id}, env)
	mark := s.Mark()
	asked := check2(t, name+": the cwd question is asked before anything else",
		s.Expect(resumeCwdQuestion, mark, 15*time.Second))
	if asked {
		text := s.Since(mark)
		check(t, name+": the question names the session's directory", strings.Contains(text, wsA))
		check(t, name+": the question names the current directory", strings.Contains(text, wsB))
		check(t, name+": the question offers going back, staying and cancelling",
			strings.Contains(text, "1) 切到 "+wsA) && strings.Contains(text, "2) 在 "+wsB) && strings.Contains(text, "3) 取消"))
	}
	mark = s.Mark()
	s.SendLine(choice)
	if choice == "3" {
		check(t, name+": cancelling reports it and leaves", s.Expect(`已取消`, mark, 15*time.Second))
		check(t, name+": no chat was opened", !s.Contains(`❯ `, mark))
		waitExit(t, s)
		return s
	}
	check(t, name+": chat starts", s.Expect(`❯ `, mark, 15*time.Second))
	if followUp {
		mark = s.Mark()
		// The relative read is what tells which directory the tools got.
		s.SendLine(resumeCwdPrompt)
		check(t, name+": the relative file of directory A is found", s.Expect(resumeCwdMark, mark, 15*time.Second))
		check(t, name+": the turn ends", s.Expect(`tokens · `, mark, 15*time.Second))
	}
	mark = s.Mark()
	s.SendLine(":exit")
	check(t, name+": exit summary", s.Expect(`续接：suga chat --resume `, mark, 15*time.Second))
	waitExit(t, s)
	return s
}

// resumeSessionCwd is the recorded directory of one session, as
// `suga session list --json --all` reports it ("" when the row is missing).
//
// --all: the session may have moved to another directory, and sessions lists the
// current directory's sessions by default.
func resumeSessionCwd(t *testing.T, binary, cwd string, env []string, id string) string {
	t.Helper()
	out, _, _ := sugaRun(t, binary, cwd, env, "session", "list", "--json", "-n", "0", "--all")
	var rows []struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal([]byte(out), &rows) != nil {
		return ""
	}
	for _, row := range rows {
		if row.ID == id {
			return row.Cwd
		}
	}
	return ""
}

// resumeCwdMoves lists the session_cwd_changed trace events of a session.
func resumeCwdMoves(t *testing.T, binary, cwd string, env []string, id string) []map[string]any {
	t.Helper()
	var moves []map[string]any
	for _, event := range traceEvents(t, binary, cwd, env, id) {
		if event["name"] == "session_cwd_changed" {
			moves = append(moves, event)
		}
	}
	return moves
}

// resumeRun is one `suga run --resume` from cwd: the prompt makes the model read
// the relative path, so a turn that ran in A carries the file's content in the
// stream-json output (the text output shows only the model's reply).
func resumeRun(t *testing.T, binary, cwd string, env []string, id string, extra ...string) (stdout, stderr string, code int) {
	t.Helper()
	args := append([]string{"run", "--read-only", "--output-format", "stream-json", "--resume", id, "--prompt", resumeCwdPrompt}, extra...)
	return sugaRun(t, binary, cwd, env, args...)
}

// resolvedTempDir is a t.TempDir with its symbolic links resolved (a scratch path
// may go through one, and suga records the resolved directory).
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}
