//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// TestChatTrustedMemory covers the trusted memory switch end to end (issue
// suga-7s4v, design 0.4): memory.project.trusted lists repository roots, and an
// entry read from a listed repository does not count as external content. The
// model reads the entry and then runs its body as a command, so the difference
// shows on the chat screen and in the trace:
//
//	registered:   no 来源工具：memory line, no 执行 shell_exec？ question, the
//	              command runs and no tool event carries origin_judge;
//	unregistered: the question names the memory source, the answer n refuses,
//	              and the denial carries the judge's verdict.
//
// The fake exec backend is testdata/fakememexec, built the way the suga binary
// is, so Linux and the Windows ConPTY run the same fixture (plan T3).
func TestChatTrustedMemory(t *testing.T) {
	binary := buildSuga(t)
	memExec := buildFakeMemExec(t)
	// body is the entry text the model copies into the command: a harmless echo
	// of more than tools.OriginWindow (40) bytes. ranBody is that command's
	// result line as the screen shows it (screenText drops every whitespace run).
	const body = "echo TRUSTED_MEMORY_BODY_alpha_bravo_charlie_delta_echo"
	ranBody := regexp.MustCompile(regexp.QuoteMeta(`⎿ ok:` + strings.TrimPrefix(body, "echo ")))
	// evalDir returns a temp dir with symlinks and Windows 8.3 short names
	// resolved, so it equals the repository root suga derives from it.
	evalDir := func(t *testing.T) string {
		t.Helper()
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatalf("eval symlinks: %v", err)
		}
		return dir
	}
	// gitRoot initialises a git repository in a temp dir and returns the value
	// `git rev-parse --show-toplevel` reports: the registration basis the docs
	// name (and the value suga's own repository discovery compares with).
	gitRoot := func(t *testing.T) string {
		t.Helper()
		dir := evalDir(t)
		if out, err := runGit(dir, "init", "-q"); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		out, err := runGit(dir, "rev-parse", "--show-toplevel")
		if err != nil {
			t.Fatalf("git rev-parse --show-toplevel: %v\n%s", err, out)
		}
		return strings.TrimSpace(out)
	}

	// outcome is what one run showed on the screen and in the trace.
	type outcome struct {
		askedOrigin bool
		askedCall   bool
		ran         bool
		events      []map[string]any
	}
	// run plays the memorigin script once over a fresh home and returns the
	// outcome. trusted is the one registered root ("" registers none).
	run := func(t *testing.T, work, trusted string) outcome {
		t.Helper()
		home := t.TempDir()
		server := httptest.NewServer(&fakeLLM{
			Mode: "memorigin", Command: body, MemoryID: "note", Delay: 100 * time.Millisecond,
			// The judge answers with "ask" in every request: a flagged call
			// stays with the person and the trace records its verdict.
			JudgeReply: `{"decision":"ask","reason":"JUDGE_REASON_MEMORIGIN"}`,
		})
		t.Cleanup(server.Close)
		fixture := filepath.Join(t.TempDir(), "mem.json")
		writeMemoryFixture(t, fixture, body)
		writeTrustedHome(t, home, server.URL, memExec, trusted)
		env := chatEnv(t, chatOpts{Home: home, SetEnv: []string{"FAKEMEMEXEC_FIXTURE=" + fixture}})
		s, _ := startChatIn(t, binary, work, chatCols, chatRows, []string{"chat"}, env)
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("读记忆并执行")

		// Either the origin question comes up or the turn ends without one.
		var out outcome
		if waitScreen(s, chatCols, originOrTurnEnd, 30*time.Second) {
			out.askedOrigin = screenMatches(s, chatCols, `来源工具：memory`)
			out.askedCall = screenMatches(s, chatCols, `执行 shell_exec？`)
		}
		if out.askedCall {
			s.SendLine("n")
		}
		session := exitAfterTurn(t, s, mark)
		check(t, "session id", session != "")
		out.ran = waitScreen(s, chatCols, ranBody, 0)
		if session != "" {
			out.events = traceEvents(t, binary, work, env, session)
		}
		return out
	}

	t.Run("a registered repository root is trusted", func(t *testing.T) {
		ws := gitRoot(t)
		o := run(t, ws, ws)
		check(t, "no 来源工具 line", !o.askedOrigin)
		check(t, "no 执行 shell_exec？ question", !o.askedCall)
		check(t, "the copied command ran", o.ran)
		check(t, "trace: the call is a tool event", findEvent(o.events, "tool", "shell_exec") != nil)
		check(t, "trace: no origin_judge anywhere", noOriginJudge(o.events))
	})

	t.Run("an unregistered repository root still asks", func(t *testing.T) {
		ws := gitRoot(t)
		o := run(t, ws, evalDir(t))
		check(t, "来源工具 line names memory", o.askedOrigin)
		check(t, "the origin question was asked", o.askedCall)
		check(t, "the refused command did not run", !o.ran)
		denial := findEvent(o.events, "denial", "shell_exec")
		check(t, "trace: the refused call is a denial", denial != nil)
		if denial != nil {
			check(t, "trace: the denial carries origin_judge", attrs(denial)["origin_judge"] != nil)
		}
	})
}

// originOrTurnEnd is what run waits for: the external-origin question or the
// status line of a turn that ended without one (screenText drops whitespace).
var originOrTurnEnd = regexp.MustCompile(`来源工具：memory|tokens·`)

// runGit runs one git command in dir and returns its combined output.
func runGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}

// buildFakeMemExec builds the exec protocol v1 memory fixture the way buildSuga
// builds the binary: a Go program, not a shell script, so the Windows ConPTY
// runs it too.
func buildFakeMemExec(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "fakememexec"+goexe())
	cmd := exec.Command("go", "build", "-o", out, "./test/ptye2e/testdata/fakememexec")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build fakememexec: %v\n%s", err, output)
	}
	return out
}

// writeMemoryFixture writes the JSON map of op name to result the fake backend
// answers from: one project entry whose body is body.
func writeMemoryFixture(t *testing.T, path, body string) {
	t.Helper()
	entry := fmt.Sprintf(`{"id":"note","title":"Setup","body":%q,"kind":"project"}`, body)
	data := fmt.Sprintf(`{"index":{"lines":[],"bytes":0},"get":%s,"search":{"hits":[]}}`, entry)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeTrustedHome writes the SUGA_HOME config of the scenario: the fake
// provider, tools.approval: auto (a call without an external origin runs
// without a question), the exec project backend and the registered root
// (trusted, "" registers none).
func writeTrustedHome(t *testing.T, home, url, memExec, trusted string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	roots := "    trusted: []\n"
	if trusted != "" {
		roots = fmt.Sprintf("    trusted:\n    - %q\n", filepath.ToSlash(trusted))
	}
	config := fmt.Sprintf(`language: zh
model: fake/m1
providers:
  fake:
    type: openai
    base_url: %s
    api_key: k
tools:
  allow_write: true
  approval: auto
memory:
  project:
    backend: exec
    command: %q
%s`, url, filepath.ToSlash(memExec), roots)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// screenMatches reports whether the screen text matches pattern right now.
func screenMatches(s *ptytest.Session, cols int, pattern string) bool {
	return regexp.MustCompile(unspaced(pattern)).MatchString(screenText(s, cols))
}

// noOriginJudge reports whether no tool event of events carries an origin_judge
// attribute: a trusted read never reaches the external-origin screen.
func noOriginJudge(events []map[string]any) bool {
	for _, e := range events {
		if e["kind"] != "tool" {
			continue
		}
		if _, ok := attrs(e)["origin_judge"]; ok {
			return false
		}
	}
	return true
}
