//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	addDirReadMark  = "SMOKE_ADD_DIR_READ"
	addDirWriteMark = "SMOKE_ADD_DIR_WRITE"
)

// TestChatAddDir ports scripts/ptysmoke/chat_adddir.py: `suga chat --add-dir
// <outside> --add-dir <shared>:rw`; the model reads a file outside the
// workspace and writes into the :rw directory (tools.approval: auto, no
// question).
//
// Python check -> Go assertion:
//
//	❯ prompt / file_read reaches the added directory     until
//	file_write reaches the :rw directory / turn end      until
//	exit: resume command / session id                    exitAfterTurn
//	file_write: file on disk                             file content == mark
//	trace: turn_context recorded / add_dirs lists both   exact list [outside:ro, shared:rw]
func TestChatAddDir(t *testing.T) {
	binary := buildSuga(t)
	home, ws, outside, shared := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	readPath, writePath := filepath.Join(outside, "note.txt"), filepath.Join(shared, "out.txt")
	if err := os.WriteFile(readPath, []byte(addDirReadMark+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(&fakeLLM{
		Mode: "adddir", Delay: 300 * time.Millisecond, ReadPath: readPath, WritePath: writePath,
		WriteBody: addDirWriteMark, Reply: "读完了",
	})
	defer server.Close()
	s, env := startChatOpts(t, binary, chatOpts{
		Home: home, Work: ws, URL: server.URL, SetEnv: []string{"HOME=" + home},
		ToolsExtra: "  approval: auto\n", Args: []string{"--add-dir", outside, "--add-dir", shared + ":rw"},
	})

	m := s.Mark()
	until(t, s, "❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("读一下附加目录，然后写个文件")
	// file_read of a path outside the workspace only works through --add-dir.
	until(t, s, "file_read reaches the added directory", addDirReadMark, m)
	until(t, s, "file_write reaches the :rw directory", `wrote \d+ bytes to `, m)
	until(t, s, "turn end", `tokens · `, m)
	session := exitAfterTurn(t, s, m)

	check(t, "file_write: file on disk", readFile(writePath) == addDirWriteMark)
	if !check2(t, "session id", session != "") {
		return
	}
	events := traceEvents(t, binary, ws, env, session)
	turn := findEvent(events, "turn_context", "")
	if turn == nil {
		turn = findEvent(events, "", "turn_context")
	}
	if !check2(t, "trace: turn_context recorded", turn != nil) {
		return
	}
	dirs, _ := attrs(turn)["add_dirs"].([]any)
	var got []string
	for _, d := range dirs {
		got = append(got, d.(string))
	}
	want := []string{outside + ":ro", shared + ":rw"}
	check(t, "trace: add_dirs lists both directories with their modes", len(got) == 2 && got[0] == want[0] && got[1] == want[1])
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Logf("add_dirs: %v want %v", got, want)
	}
}
