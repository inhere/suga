//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

const undoConfirm = `执行？\[y\] 回滚`

// TestChatUndo ports scripts/ptysmoke/chat_undo.py: undo checkpoints, driven
// through the real chat with the fake provider's edit mode.
//
// Python check -> Go assertion (same names):
//
//	banner / prompt                          expect Suga chat banner and ❯
//	completion offers :undo                  Tab after ":u"
//	edit tool completed / edit changed on disk   approve + file == new
//	checkpoint list                          :checkpoints shows 快照点 and 恢复 target.txt
//	undo plan / confirmation / empty confirmation cancels  :undo, Enter -> 已取消
//	undo plan repeats / confirmation repeats / undo restores edit / edit restored on disk
//	next turn after undo                     model turn after the undo (notice reaches the request log)
//	shell tool completed / shell is shown as outside undo / checkpoint terminal text is clean
//	new file write / exists / new-file undo plan / confirmation / new file deleted by undo
//	remove tool completed / deleted file is absent / trash undo plan / confirmation / restores file
//	conflict edit / completed / conflict listed / confirmation / skipped / manual conflict remains
//	force plan / force undo confirmation / force restores content conflict / force restored on disk
//	added-dir write / file exists / added-dir removal / removed added root is explained
//	exit reports session
//	cwd-change question / chat opens / checkpoint is explained   resumed in another cwd
//	next turn received suga.undo             fake request log has the notice
//	trace command succeeds / has EventUndo / EventUndo has restored/skipped attrs / tool events have snapshot attrs
//	no-snapshot prompt / edit still runs / write completes / reports no checkpoint / undo is also empty
func TestChatUndo(t *testing.T) {
	binary := buildSuga(t)
	t.Run("checkpoints", func(t *testing.T) { undoScenario(t, binary) })
	t.Run("no snapshot", func(t *testing.T) { noSnapshotScenario(t, binary) })
}

func readFile(path string) string {
	body, _ := os.ReadFile(path)
	return string(body)
}

// approveTurn answers the approval question of a file_edit/file_write/
// file_remove/shell_exec with y and reports whether the turn then ended.
func approveTurn(t *testing.T, s *ptytest.Session, mark int, label string) bool {
	t.Helper()
	until(t, s, label+": question", `允许执行 (?:file_edit|file_write|file_remove|shell_exec)`, mark)
	s.SendLine("y")
	return s.Expect(`tokens · `, mark, 15*time.Second)
}

func undoScenario(t *testing.T, binary string) {
	server := httptest.NewServer(&fakeLLM{
		Mode: "edit", Delay: 150 * time.Millisecond, RemovePath: "delete.txt", WriteBody: "SMOKE_ADDED",
		ShellCommand: osCommand("printf '\\033[2JSMOKE_SHELL'", `Write-Host -NoNewline "$([char]27)[2JSMOKE_SHELL"`),
	})
	defer server.Close()
	fake := server.Config.Handler.(*fakeLLM)
	home, ws, ws2, shared := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	fake.WritePath = filepath.Join(shared, "added.txt")
	target, deleted := filepath.Join(ws, "target.txt"), filepath.Join(ws, "delete.txt")
	for path, body := range map[string]string{target: "SMOKE_TARGET old\n", deleted: "SMOKE_DELETE\n"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, env := startChatOpts(t, binary, chatOpts{
		Home: home, Work: ws, URL: server.URL, SetEnv: []string{"HOME=" + home},
		Args: []string{"--add-dir", shared + ":rw"},
	})

	m := s.Mark()
	until(t, s, "banner", `Suga chat · fake/m1`, m)
	until(t, s, "prompt", `❯ `, m)

	m = s.Mark()
	s.Send(":u\t")
	until(t, s, "completion offers :undo", `:undo`, m)
	s.Send("\x15")

	m = s.Mark()
	s.SendLine("编辑")
	check(t, "edit tool completed", approveTurn(t, s, m, "edit") && s.Contains(`ok: edited `, m))
	check(t, "edit changed on disk", readFile(target) == "SMOKE_TARGET new\n")

	m = s.Mark()
	s.SendLine(":checkpoints")
	check(t, "checkpoint list", s.Expect(`快照点`, m, 15*time.Second) && s.Expect(`恢复 target\.txt`, m, 15*time.Second))

	m = s.Mark()
	s.SendLine(":undo")
	until(t, s, "undo plan", `将回滚最近 1 个回合`, m)
	until(t, s, "undo confirmation", undoConfirm, m)
	s.Send("\r")
	until(t, s, "empty confirmation cancels", `已取消`, m)
	m = s.Mark()
	s.SendLine(":undo")
	until(t, s, "undo plan repeats", `将回滚最近 1 个回合`, m)
	until(t, s, "undo confirmation repeats", undoConfirm, m)
	s.SendLine("y")
	// :undo is a local command: no model turn, so no turn status line follows.
	// (ConPTY drops the prompt's trailing space when nothing follows it.)
	check(t, "undo restores edit", s.Expect(`已回滚 1 个文件`, m, 15*time.Second) && s.Expect(`❯`, m, 15*time.Second))
	check(t, "edit restored on disk", readFile(target) == "SMOKE_TARGET old\n")

	// A real following turn: the fake request log checks that the injected
	// suga.undo fact reached the model without becoming a new request.
	m = s.Mark()
	s.SendLine("收到回滚通知")
	check(t, "next turn after undo", s.Expect(`完成`, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))

	m = s.Mark()
	s.SendLine("shell")
	check(t, "shell tool completed", approveTurn(t, s, m, "shell") && s.Contains(`SMOKE_SHELL`, m))
	m, rawMark := s.Mark(), len(s.RawText())
	s.SendLine(":checkpoints")
	until(t, s, "shell is shown as outside undo", `shell_exec：.*改动不在快照范围内`, m)
	// The line editor itself writes escapes; what must not reach the terminal is
	// the command's own clear-screen sequence (cleanText escapes it).
	check(t, "checkpoint terminal text is clean", !strings.Contains(s.RawText()[rawMark:], "\x1b[2J"))

	m = s.Mark()
	s.SendLine("新建")
	check(t, "new file write", approveTurn(t, s, m, "new file") && s.Expect(`wrote \d+ bytes`, m, 15*time.Second))
	newPath := filepath.Join(ws, "new.txt")
	_, err := os.Stat(newPath)
	check(t, "new file exists", err == nil)
	m = s.Mark()
	s.SendLine(":undo")
	// :undo 1 covers only the newest turn (the new file); the shell-only turn
	// before it is outside that range, so its boundary is not repeated here.
	until(t, s, "new-file undo plan deletes the new file", `删除 new\.txt`, m)
	until(t, s, "new-file undo confirmation", undoConfirm, m)
	s.SendLine("y")
	_ = s.Expect(`删除 1 个新建文件`, m, 15*time.Second)
	_, err = os.Stat(newPath)
	check(t, "new file deleted by undo", s.Contains(`删除 1 个新建文件`, m) && os.IsNotExist(err))

	m = s.Mark()
	s.SendLine("删除")
	check(t, "remove tool completed", approveTurn(t, s, m, "remove") && s.Expect(`removed`, m, 15*time.Second))
	_, err = os.Stat(deleted)
	check(t, "deleted file is absent", os.IsNotExist(err))
	m = s.Mark()
	s.SendLine(":undo")
	until(t, s, "trash undo plan", `将回滚最近 1 个回合`, m)
	until(t, s, "trash undo confirmation", undoConfirm, m)
	s.SendLine("y")
	restored := s.Expect(`已回滚 1 个文件`, m, 15*time.Second)
	_, err = os.Stat(deleted)
	check(t, "trash undo restores file", restored && err == nil)

	// Conflict is introduced after a successful edit; the same checkpoint is then
	// retried with force, proving force is scoped to content.
	m = s.Mark()
	s.SendLine("冲突")
	until(t, s, "conflict edit", `允许执行 file_edit`, m)
	s.SendLine("y")
	check(t, "conflict edit completed", s.Expect(`ok: edited `, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
	if err := os.WriteFile(target, []byte("MANUAL_CONFLICT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = s.Mark()
	s.SendLine(":undo")
	until(t, s, "conflict listed", `跳过 target\.txt.*冲突`, m)
	until(t, s, "conflict undo confirmation", undoConfirm, m)
	s.SendLine("y")
	until(t, s, "conflict skipped", `跳过 1 个冲突`, m)
	check(t, "manual conflict remains", readFile(target) == "MANUAL_CONFLICT\n")
	m = s.Mark()
	s.SendLine(":undo force")
	until(t, s, "force plan", `涵盖内容冲突`, m)
	until(t, s, "force undo confirmation", undoConfirm, m)
	s.SendLine("y")
	until(t, s, "force restores content conflict", `已回滚 1 个文件`, m)
	check(t, "force restored on disk", readFile(target) == "SMOKE_TARGET old\n")

	m = s.Mark()
	s.SendLine("附加")
	check(t, "added-dir write", approveTurn(t, s, m, "added dir") && s.Expect(`wrote \d+ bytes`, m, 15*time.Second))
	_, err = os.Stat(filepath.Join(shared, "added.txt"))
	check(t, "added-dir file exists", err == nil)
	m = s.Mark()
	s.SendLine(":rm-dir " + shared)
	until(t, s, "added-dir removal", `已撤销`, m)
	m = s.Mark()
	s.SendLine(":checkpoints")
	until(t, s, "removed added root is explained", `附加目录已移除，无法恢复`, m)

	id := ctrlCTwice(t, s)
	check(t, "exit reports session", id != "")
	if id == "" {
		return
	}

	// Resume from another directory: the checkpoints cannot be restored there.
	resumed, env := startChatOpts(t, binary, chatOpts{
		Home: home, Work: ws2, URL: server.URL, SetEnv: []string{"HOME=" + home}, Args: []string{"--resume", id},
	})
	m = resumed.Mark()
	until(t, resumed, "cwd-change question", `当前工作目录是`, m)
	resumed.SendLine("2")
	until(t, resumed, "cwd-change chat opens", `❯ `, m)
	m = resumed.Mark()
	resumed.SendLine(":checkpoints")
	until(t, resumed, "cwd-change checkpoint is explained", `会话目录已变更，无法恢复`, m)
	ctrlCTwice(t, resumed)

	notice := false
	for _, req := range fake.Requests() {
		notice = notice || strings.Contains(req.Messages, "用户回滚了文件改动")
	}
	check(t, "next turn received suga.undo", notice)
	events := traceEvents(t, binary, ws2, env, id) // "trace command succeeds"
	var undo map[string]any
	for _, e := range events {
		if e["kind"] == "undo" {
			undo = e
			break
		}
	}
	check(t, "trace has EventUndo", undo != nil)
	if undo != nil {
		a := attrs(undo)
		check(t, "EventUndo has restored/skipped attrs", a["restored"] != nil && a["skipped"] != nil)
	}
	snapshots := false
	for _, e := range events {
		if e["kind"] != "tool" {
			continue
		}
		snap, _ := attrs(e)["snapshot"].(map[string]any)
		switch e["name"] {
		case "file_edit", "file_write", "file_remove":
			snapshots = snapshots || (snap != nil && snap["status"] != nil && snap["status"] != "")
		}
	}
	check(t, "tool events have snapshot attrs", snapshots)
}

func noSnapshotScenario(t *testing.T, binary string) {
	server := httptest.NewServer(&fakeLLM{Mode: "edit", Delay: 100 * time.Millisecond})
	defer server.Close()
	home, ws := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "target.txt"), []byte("SMOKE_TARGET old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := startChatOpts(t, binary, chatOpts{
		Home: home, Work: ws, URL: server.URL, SetEnv: []string{"HOME=" + home},
		ToolsExtra: "  snapshots:\n    enabled: false\n",
	})
	m := s.Mark()
	until(t, s, "no-snapshot prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("编辑")
	until(t, s, "no-snapshot edit still runs", `允许执行 file_edit`, m)
	s.SendLine("y")
	check(t, "no-snapshot write completes", s.Expect(`ok: edited `, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
	m = s.Mark()
	s.SendLine(":checkpoints")
	until(t, s, "no-snapshot reports no checkpoint", `没有可回滚的快照`, m)
	m = s.Mark()
	s.SendLine(":undo")
	until(t, s, "no-snapshot undo is also empty", `没有可回滚的快照`, m)
	ctrlCTwice(t, s)
}
