//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const targetOld = "line one\nSMOKE_TARGET old\nline three\n"

// TestChatEdit ports scripts/ptysmoke/chat_edit.py: file_grep runs without a
// question and finds the line, the file_edit question previews the old and new
// text, y applies the edit on disk, two Ctrl-C leave, and the trace records a
// complete snapshot for the edit.
func TestChatEdit(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "edit", Delay: 300 * time.Millisecond})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	target := filepath.Join(work, "target.txt")
	if err := os.WriteFile(target, []byte(targetOld), 0o600); err != nil {
		t.Fatal(err)
	}
	s, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL})

	mark := s.Mark()
	expect(t, s, `Suga chat · fake/m1`, mark)
	mark = s.Mark()
	s.SendLine("把 old 改成 new")
	expect(t, s, `target.txt:2: SMOKE_TARGET old`, mark)
	check(t, "grep: ran without a question", !s.Contains(`允许执行 file_grep`, mark))
	expect(t, s, `允许执行 file_edit`, mark)
	check(t, "edit: old text shown", s.Contains(`原文：\s+SMOKE_TARGET old`, mark))
	check(t, "edit: new text shown", s.Contains(`改为：\s+SMOKE_TARGET new`, mark))
	s.SendLine("y")
	// The result line is cut to the terminal width after the absolute path, so
	// match its start; the disk check below proves the replacement.
	expect(t, s, `ok: edited `, mark)
	expect(t, s, `完成`, mark)
	time.Sleep(300 * time.Millisecond)
	body, err := os.ReadFile(target)
	check(t, "edit: file changed on disk", err == nil && string(body) == "line one\nSMOKE_TARGET new\nline three\n")

	session := ctrlCTwice(t, s)
	check(t, "session id", session != "")
	if session != "" {
		tool := findEvent(traceEvents(t, binary, work, env, session), "tool", "file_edit")
		snapshot, _ := attrs(tool)["snapshot"].(map[string]any)
		check(t, "trace: file_edit has complete snapshot attrs", snapshot != nil && snapshot["status"] == "complete")
	}
}

// TestChatDiff ports scripts/ptysmoke/chat_diff.py: the approval question shows
// the diff, the applied result prints it again, and :diff shows the whole thing.
func TestChatDiff(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "edit", Delay: 200 * time.Millisecond})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "target.txt"), []byte(targetOld), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL})

	mark := s.Mark()
	expect(t, s, `Suga chat · fake/m1`, mark)
	s.SendLine("改文件")
	expect(t, s, `-  2 SMOKE_TARGET old`, mark)
	expect(t, s, `\+  2 SMOKE_TARGET new`, mark)
	expect(t, s, `\[y\]是`, mark)
	mark = s.Mark()
	s.SendLine("y")
	expect(t, s, `diff target.txt`, mark)
	expect(t, s, `tokens · `, mark)
	check(t, "the edit is applied", !s.Contains(`用户拒绝执行`, mark))
	mark = s.Mark()
	s.SendLine(":diff")
	expect(t, s, `\+  2 SMOKE_TARGET new`, mark)
	ctrlCTwice(t, s)
}
