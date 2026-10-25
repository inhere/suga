//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
		expect(t, inA, `收到：alpha one`, inA.Mark())
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
		expect(t, inA, `收到：alpha one`, inA.Mark())
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

// startHistoryChat is startChatSpec with the input history on and an explicit
// chat.history_scope, since the shared config template keeps history off.
func startHistoryChat(t *testing.T, binary, baseURL, home, work, scope string) *ptytest.Session {
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
		Cols:    100,
		Rows:    40,
	}
	return ptytest.StartSpec(t, spec)
}
