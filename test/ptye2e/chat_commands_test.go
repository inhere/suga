//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestChatCommands ports scripts/ptysmoke/chat_commands.py: chat's local
// commands (:help and per-command usage, :queue, :checkpoints, :usage, :tasks and
// its options, :skills, :context, the closest-command hint and ":)" as a
// message) and the terminal bell after a long turn. A second session reads
// target.txt through the display-mode model and checks :context --files.
func TestChatCommands(t *testing.T) {
	binary := buildSuga(t)

	t.Run("commands and tasks", func(t *testing.T) {
		// A background task that outlives the checks (5 seconds), then is killed.
		command := "sleep 5; echo TASK_COMMAND"
		if runtime.GOOS == "windows" {
			command = "Start-Sleep -Seconds 5; Write-Output TASK_COMMAND"
		}
		server := httptest.NewServer(&fakeLLM{Mode: "tool", Background: true, Command: command, Delay: time.Second})
		defer server.Close()
		s, _ := startChatOpts(t, binary, chatOpts{
			Home: t.TempDir(), Work: t.TempDir(), URL: server.URL,
			ToolsExtra: "  approval: auto\n", Extra: "chat:\n  notify_after: 1s\n",
		})

		step := func(line, pattern string) int {
			t.Helper()
			mark := s.Mark()
			s.SendLine(line)
			expect(t, s, pattern, mark)
			return mark
		}
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		mark = step(":help", `:usage`)
		expect(t, s, `:tasks`, mark)
		expect(t, s, `查看命令详情`, mark)
		step(":session -h", `用法：:session`)
		step(":queue -h", `用法：:queue`)
		step(":checkpoints -h", `用法：:checkpoints`)
		step(":help :queue", `用法：:queue`)
		step(":queue -d 1", `没有第 1 条排队的消息`)
		step(":checkpoints 3", `没有可回滚的快照`)
		step(":usage", `本次 0 轮`)
		step(":tasks", `没有后台任务`)
		step(":tasks -h", `:tasks -k <ID>`)
		step(":tasks -x", `未知参数：-x`)
		step(":tasks -k t1", `没有任务 t1`)
		// A turn of two model requests (1.0s each) that starts a background task:
		// longer than chat.notify_after (1s), so its end rings the bell.
		mark = s.Mark()
		s.SendLine("hello")
		expect(t, s, `tokens · `, mark)
		// The bell may follow the status line by a moment (it is written when the
		// turn is wrapped up): wait for it instead of checking at once.
		if !s.Expect(`\x07`, mark, 3*time.Second) {
			t.Errorf("FAIL completion bell; terminal tail:\n%q", tail(s.RawText()))
		}
		step(":usage", `本次 1 轮`)
		mark = step(":tasks", `running`)
		check(t, "running task listed", s.Contains(`TASK_COMMAND|sleep 5|Start-Sleep`, mark))
		step(":tasks -k t1", `已终止任务 t1：`)
		step(":tasks", `t1 · killed`)
		step(":tasks -k t1", `任务 t1 已结束（killed），无需终止`)
		mark = step(":skills", `已加载 \d+ 个 skill`)
		// (?s): a long description wraps at the terminal width, so the line may break.
		expect(t, s, `(?s)suga-guide — .*· 内置`, mark)
		step(":skills --only-names", `suga-guide`)
		step(":skills -h", `用法：:skills`)
		mark = step(":context", `上下文构成`)
		expect(t, s, `工具 schema：`, mark)
		expect(t, s, `历史消息：`, mark)
		step(":context -h", `用法：:context`)
		step(":sessio", `未知命令 :sessio，是否想用 :session`)
		step(":)", `tokens · `)
		s.SendCtrlC()
		time.Sleep(200 * time.Millisecond)
		s.SendCtrlC()
	})

	t.Run("context files lists the file the model read", func(t *testing.T) {
		server := httptest.NewServer(&fakeLLM{Mode: "display", Delay: 200 * time.Millisecond})
		defer server.Close()
		work := t.TempDir()
		if err := os.WriteFile(filepath.Join(work, "target.txt"), []byte("SMOKE_TARGET old\nsecond line\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		s, _ := startChatOpts(t, binary, chatOpts{
			Home: t.TempDir(), Work: work, URL: server.URL, ToolsExtra: "  approval: auto\n",
		})
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.SendLine("read it")
		expect(t, s, `tokens · `, mark)
		mark = s.Mark()
		s.SendLine(":context --files")
		expect(t, s, `target.txt · file_read`, mark)
	})
}
