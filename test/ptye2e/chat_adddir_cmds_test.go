//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	cmdsReadMark  = "SMOKE_CMD_READ"
	cmdsWriteMark = "SMOKE_CMD_WRITE"
	cmdsReply     = "读完了"
	cmdsReplyText = "收到"
)

// TestChatAddDirCommands ports scripts/ptysmoke/chat_adddir_cmds.py: directory
// authorization in the middle of a session, in two chats.
//
// Chat 1 (adddir fake model, started without --add-dir). Python check -> Go:
//
//	banner omits the rare directory commands          banner has no ":add-dir/:dirs/:rm-dir" (checked once the prompt is up)
//	:dirs starts with the workspace only              工作区 line and 附加目录：无
//	outside read: question asked                      unwrapped question text is exactly 授权访问 <dir>？[r] 只读 [w] 读写 [n] 拒绝：
//	outside read: held back until the answer          READ_MARK absent before the answer
//	outside read: r authorizes it and the read runs / turn ends
//	:dirs names the asked directory as 越界授权
//	:add-dir rw confirms / the write runs / turn ends / file on disk
//	:dirs shows both with mode and origin / labels the workspace
//	:rm-dir revokes it / relative :add-dir refused with usage
//	the write after :rm-dir asks again / is refused / wrote nothing / turn ends
//	exit: resume command / session id
//	trace: add_dir events (exact list) / a turn sees the added directory / last turn sees the revoked one gone
//
// Chat 2 (text fake model): a delivered ":add-dir <dir>" is a message, never a
// command: delivered, shown as [来自 cli], answered, :dirs has no such directory,
// no 已授权.
func TestChatAddDirCommands(t *testing.T) {
	binary := buildSuga(t)
	// The directories live under one short temp root (like the Python work dir): the
	// authorization question names a path and must stay within the 100 columns, or
	// it wraps at a point that depends on the random temp name.
	root, err := os.MkdirTemp("", "ad")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	mk := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ws, outside, shared, delivery := mk("ws"), mk("outside"), mk("shared"), mk("delivery")
	readPath, writePath := filepath.Join(outside, "note.txt"), filepath.Join(shared, "out.txt")
	if err := os.WriteFile(readPath, []byte(cmdsReadMark+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The question names the path the tool really resolves (a symlinked temp dir
	// is authorized in its real form).
	outsideReal, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ask and commands", func(t *testing.T) {
		server := httptest.NewServer(&fakeLLM{
			Mode: "adddir", Delay: 300 * time.Millisecond, ReadPath: readPath, WritePath: writePath,
			WriteBody: cmdsWriteMark, Reply: cmdsReply,
		})
		defer server.Close()
		s, env := startChatOpts(t, binary, chatOpts{
			Home: t.TempDir(), Work: ws, URL: server.URL, ToolsExtra: "  approval: auto\n",
		})

		m := s.Mark()
		until(t, s, "❯ prompt", `❯ `, m)
		check(t, "banner omits the rare directory commands", !s.Contains(`:add-dir/:dirs/:rm-dir`, m))

		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine(":dirs")
		check(t, ":dirs starts with the workspace only",
			s.Expect(`工作区：.+（读写）`, m, 15*time.Second) && s.Expect(`附加目录：无`, m, 15*time.Second))

		// A read outside the workspace: chat asks before the tool runs. The
		// question is longer than the line editor's width and wraps, so it is
		// matched with the wrap's line breaks taken out.
		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine("只读：看看外面的说明")
		check(t, "outside read: question asked", s.Expect(`\[n\] 拒绝：`, m, 15*time.Second) &&
			strings.Contains(unspaced(s.Since(m)), unspaced("授权访问 "+outsideReal+"？[r] 只读 [w] 读写 [n] 拒绝：")))
		check(t, "outside read: the tool is held back until the answer", !s.Contains(cmdsReadMark, m))
		s.SendLine("r")
		until(t, s, "outside read: r authorizes it and the read runs", cmdsReadMark, m)
		until(t, s, "outside read: turn ends", `tokens · `, m)

		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine(":dirs")
		until(t, s, ":dirs names the asked directory as 越界授权", `  `+regexp.QuoteMeta(outsideReal)+`（只读 · 越界授权）`, m)

		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine(":add-dir " + shared + ":rw")
		until(t, s, ":add-dir rw confirms", `已授权：`+regexp.QuoteMeta(shared)+`（读写）`, m)

		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine("只写：写个文件")
		until(t, s, "the write into the added directory runs", `wrote \d+ bytes to `, m)
		until(t, s, "write: turn ends", `tokens · `, m)
		check(t, "write: file on disk", readFile(writePath) == cmdsWriteMark)

		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine(":dirs")
		check(t, ":dirs shows both directories with mode and origin",
			s.Expect(`  `+regexp.QuoteMeta(outsideReal)+`（只读 · 越界授权）`, m, 15*time.Second) &&
				s.Expect(`  `+regexp.QuoteMeta(shared)+`（读写 · 会话中追加）`, m, 15*time.Second))
		check(t, ":dirs labels the workspace too", s.Contains(`工作区：.+（读写）`, m))

		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine(":rm-dir " + shared)
		until(t, s, ":rm-dir revokes it", `已撤销：`+regexp.QuoteMeta(shared), m)

		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine(":add-dir docs")
		check(t, "a relative :add-dir is refused with the usage",
			s.Expect(`:add-dir: .*is not an absolute path`, m, 15*time.Second) && s.Expect(`用法：:add-dir`, m, 15*time.Second))

		// The revoked directory is out of reach again: the write asks again
		// (chat.ask_outside_dirs is on), n leaves it refused, no file appears.
		if err := os.Remove(writePath); err != nil {
			t.Fatal(err)
		}
		promptReady(t, s, m)
		m = s.Mark()
		s.SendLine("只写：再写一次")
		check(t, "the write after :rm-dir asks again", s.Expect(`\[n\] 拒绝：`, m, 15*time.Second) &&
			strings.Contains(unspaced(s.Since(m)), unspaced("授权访问 "+shared+"？")))
		s.SendLine("n")
		until(t, s, "the write after :rm-dir is refused", `用户拒绝授权访问 `, m)
		_, statErr := os.Stat(writePath)
		check(t, "the write after :rm-dir wrote nothing", os.IsNotExist(statErr))
		until(t, s, "turn ends after the refusal", `tokens · `, m)

		session := ctrlCTwice(t, s)
		if !check2(t, "session id", session != "") {
			return
		}
		events := traceEvents(t, binary, ws, env, session)
		type change struct{ name, path, mode, from string }
		var changes []change
		for _, e := range events {
			if e["kind"] != "add_dir" {
				continue
			}
			a := attrs(e)
			changes = append(changes, change{e["name"].(string), str(a["path"]), str(a["mode"]), str(a["from"])})
		}
		wantChanges := []change{
			{"add", outsideReal, "ro", "ask"},
			{"add", shared, "rw", "keyboard"},
			{"remove", shared, "rw", "keyboard"},
		}
		if !check2(t, "trace: one add_dir event per authorization change", reflect.DeepEqual(changes, wantChanges)) {
			t.Logf("add_dir events: %v", changes)
		}
		var reach [][]string
		for _, e := range events {
			if e["kind"] != "turn_context" {
				continue
			}
			var dirs []string
			list, _ := attrs(e)["add_dirs"].([]any)
			for _, d := range list {
				dirs = append(dirs, str(d))
			}
			reach = append(reach, dirs)
		}
		seen := false
		for _, dirs := range reach {
			seen = seen || reflect.DeepEqual(dirs, []string{outsideReal + ":ro", shared + ":rw"})
		}
		check(t, "trace: a turn sees the added directory", seen)
		lastOK := len(reach) > 0 && reflect.DeepEqual(reach[len(reach)-1], []string{outsideReal + ":ro"})
		if !check2(t, "trace: the last turn sees the revoked one gone", lastOK) {
			t.Logf("turn_context add_dirs: %v", reach)
		}
	})

	t.Run("delivered text is never a command", func(t *testing.T) {
		server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 300 * time.Millisecond, Reply: cmdsReplyText})
		defer server.Close()
		home := t.TempDir()
		s, env := startChatOpts(t, binary, chatOpts{
			Home: home, Work: ws, URL: server.URL, ToolsExtra: "  approval: auto\n",
		})
		m := s.Mark()
		until(t, s, "second chat: ❯ prompt", `❯ `, m)
		s.SendLine("你好")
		check(t, "second chat: the model answers", s.Expect(cmdsReplyText, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
		sid := chatSessionID.FindStringSubmatch(s.Text())
		if check2(t, "second chat: session id", sid != nil) {
			promptReady(t, s, m)
			m = s.Mark()
			_, stderr, code := sugaRun(t, binary, ws, env, "send", "--session", sid[1], ":add-dir "+delivery)
			check(t, "the delivered :add-dir reaches the chat", code == 0 && trimSpace(stderr) == "delivered")
			until(t, s, "the delivered text is shown as a message", `\[来自 cli\] :add-dir `+regexp.QuoteMeta(delivery), m)
			check(t, "the model answers the delivered text", s.Expect(cmdsReplyText, m, 15*time.Second) && s.Expect(`tokens · `, m, 15*time.Second))
			promptReady(t, s, m)
			m2 := s.Mark()
			s.SendLine(":dirs")
			check(t, ":dirs has no delivered directory",
				s.Expect(`附加目录：无`, m2, 15*time.Second) && !s.Contains(regexp.QuoteMeta(delivery), m2))
			check(t, "no authorization came from the delivery", !s.Contains(`已授权`, m))
		}
		exitChat(t, s)
	})
}
