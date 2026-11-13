//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/gookit/gopty"
	"github.com/gookit/gopty/ptytest"
)

// upArrow is the escape sequence a terminal sends for one press of Up.
const upArrow = "\x1b[A"

// TestChatPTYSmokeHistoryScope: chat.history_scope defaults to project, so Up
// only recalls the lines typed in the current project (a git repository's root
// or, outside one, the start directory), while history_scope: all keeps the old
// behaviour that falls through to the other projects. Real history.jsonl I/O
// and key presses take the assembled chat; nothing here calls chat internals.
func TestChatPTYSmokeHistoryScope(t *testing.T) {
	binary := buildSuga(t)

	t.Run("up recalls only the current project's lines", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "text"})
		defer server.Close()
		home, projectA, projectB := t.TempDir(), t.TempDir(), t.TempDir()
		// A 做成 git 仓库形状（后面要从子目录验证同一项目）；B 非 git 目录，自身即项目。
		if err := os.MkdirAll(filepath.Join(projectA, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}

		// One line per project, the way two workspaces fill a shared file.
		inA := startHistoryChat(t, binary, server.URL, home, projectA, "project")
		expect(t, inA, `❯ `, inA.Mark())
		inA.SendLine("alpha one")
		replied := inA.Mark()
		expect(t, inA, `收到：alpha one`, replied)
		// The next line is typed only once the prompt is back: a line sent while
		// the reply is still being drawn can be lost on Windows ConPTY.
		promptReady(t, inA, replied)
		inA.SendLine("alpha two")
		expect(t, inA, `收到：alpha two`, inA.Mark())
		exitChat(t, inA)

		inB := startHistoryChat(t, binary, server.URL, home, projectB, "project")
		expect(t, inB, `❯ `, inB.Mark())
		inB.SendLine("beta one")
		expect(t, inB, `收到：beta one`, inB.Mark())
		exitChat(t, inB)

		// Back in project A — started from a subdirectory, still the same
		// project: Up reaches A's lines only, never B's. Up starts at the most
		// recent line of the current project's group.
		sub := filepath.Join(projectA, "sub")
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}
		inSub := startHistoryChat(t, binary, server.URL, home, sub, "project")
		mark := inSub.Mark()
		expect(t, inSub, `❯ `, mark)
		inSub.Send(upArrow)
		expect(t, inSub, `alpha two`, mark)
		inSub.Send(upArrow)
		expect(t, inSub, `alpha one`, mark)
		if inSub.Contains(`beta one`, 0) {
			t.Fatalf("project scope recalled another project's line; terminal tail:\n%s", tail(inSub.Text()))
		}
		exitChat(t, inSub)
	})

	t.Run("all scope falls through to the other projects", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "text"})
		defer server.Close()
		home, projectA, projectB := t.TempDir(), t.TempDir(), t.TempDir()

		inA := startHistoryChat(t, binary, server.URL, home, projectA, "all")
		expect(t, inA, `❯ `, inA.Mark())
		inA.SendLine("alpha one")
		replied := inA.Mark()
		expect(t, inA, `收到：alpha one`, replied)
		// The next line is typed only once the prompt is back: a line sent while
		// the reply is still being drawn can be lost on Windows ConPTY.
		promptReady(t, inA, replied)
		inA.SendLine("alpha two")
		expect(t, inA, `收到：alpha two`, inA.Mark())
		exitChat(t, inA)

		inB := startHistoryChat(t, binary, server.URL, home, projectB, "all")
		expect(t, inB, `❯ `, inB.Mark())
		inB.SendLine("beta one")
		expect(t, inB, `收到：beta one`, inB.Mark())
		exitChat(t, inB)

		// Back in project B: the editor walks Up from the newest line overall,
		// all scope keeps the other projects' lines attached beyond B's own.
		third := startHistoryChat(t, binary, server.URL, home, projectB, "all")
		mark := third.Mark()
		expect(t, third, `❯ `, mark)
		third.Send(upArrow)
		expect(t, third, `alpha two`, mark)
		third.Send(upArrow)
		expect(t, third, `alpha one`, mark)
		third.Send(upArrow)
		expect(t, third, `beta one`, mark)
		exitChat(t, third)
	})
}

// TestChatHistoryRecallAndCompletion ports scripts/ptysmoke/chat_history.py: the
// input history a chat writes is read back by the next chat of the same project
// (Up), Ctrl-R searches it incrementally, and Tab completes a :command and a
// directory argument of :add-dir.
func TestChatHistoryRecallAndCompletion(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeChat{mode: "text"})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "tm-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A wide window: the completed absolute path must not wrap (the check reads
	// the output, where a wrapped line is broken by the editor redraw).
	const cols, rows = 240, 50

	// The first chat fills the persisted history with two lines.
	first := startHistoryChatSize(t, binary, server.URL, home, work, "project", cols, rows)
	expect(t, first, `❯ `, first.Mark())
	first.SendLine("first history line")
	replied := first.Mark()
	expect(t, first, `收到：first history line`, replied)
	// The next line is typed only once the prompt is back: a line sent while the
	// reply is still being drawn can be lost on Windows ConPTY.
	promptReady(t, first, replied)
	first.SendLine("second history line")
	replied = first.Mark()
	expect(t, first, `收到：second history line`, replied)
	promptReady(t, first, replied)
	check(t, "first chat: exit names the resume command", ctrlCTwice(t, first) != "")

	// The next chat of the same project reads both lines back.
	s := startHistoryChatSize(t, binary, server.URL, home, work, "project", cols, rows)
	mark := s.Mark()
	expect(t, s, `❯ `, mark)
	s.Send(upArrow)
	until(t, s, "Up restores prior local input", `second history line`, mark)

	// Ctrl-R searches the history incrementally: "second" narrows to the line that
	// carries it, and Ctrl-G drops the search again.
	mark = s.Mark()
	s.Send("\x15") // Ctrl-U empties the line the Up left there
	s.Send("\x12") // Ctrl-R
	s.Send("second")
	check(t, "Ctrl-R finds a match",
		s.Expect(`reverse-i-search`, mark, 15*time.Second) && s.Contains(`second history line`, mark))
	s.Send("\x07") // Ctrl-G

	// Tab completes a :command.
	mark = s.Mark()
	s.Send(":se\t")
	until(t, s, "command Tab completion (:session)", `:session`, mark)

	// Tab completes a directory of a :add-dir argument, with its separator.
	mark = s.Mark()
	s.Send("\x15:add-dir " + filepath.Join(work, "tm") + "\t")
	until(t, s, "directory Tab completion",
		`:add-dir `+quoteRegex(filepath.Join(work, "tm-dir"))+`[\\/]`, mark)
	// Ctrl-U empties the line the completion left there: Ctrl-C on a non-empty
	// line drops the line instead of counting as the first exit press.
	s.Send("\x15")
	check(t, "second chat: exit names the resume command", ctrlCTwice(t, s) != "")
}

// quoteRegex escapes a filesystem path for a check pattern (Windows separators).
func quoteRegex(path string) string { return regexp.QuoteMeta(path) }

// startHistoryChat is startHistoryChatSize in the window the scope checks read:
// a completed absolute path must not wrap, since a check reads the text where a
// wrapped line is broken by the editor's redraw.
func startHistoryChat(t *testing.T, binary, baseURL, home, work, scope string) *ptytest.Session {
	t.Helper()
	return startHistoryChatSize(t, binary, baseURL, home, work, scope, 100, 40)
}

// startHistoryChatSize is startChatSpec with the input history on and an explicit
// chat.history_scope, since the shared config template keeps history off.
func startHistoryChatSize(t *testing.T, binary, baseURL, home, work, scope string, cols, rows int) *ptytest.Session {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	config := "model: fake/m1\nproviders:\n  fake:\n    type: openai\n    base_url: " + baseURL + "\n" +
		"    api_key: test\ntools:\n  allow_write: true\nmemory:\n  project:\n    backend: files\n" +
		"chat:\n  markdown: false\n  spinner: false\n  highlight_input: false\n  terminal_title: false\n" +
		"  history: true\n  history_scope: " + scope + "\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	spec := gopty.Spec{
		Command: binary,
		Args:    []string{"chat"},
		Dir:     work,
		Env:     testEnv(home),
		Cols:    cols,
		Rows:    rows,
	}
	return ptytest.StartSpec(t, spec)
}
