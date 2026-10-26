package chat

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gookit/color"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

// The tests in this file pin the exact text of the three outputs chat builds
// around a session - the startup banner, the end-of-turn status line and the
// exit summary - so moving their construction into layout.go can not change a
// single byte (plain and colored): every case below asserts the whole output,
// not just a part of it.

// TestBannerHelpTextKeepsRareCommandsInHelp: the banner names the commands of
// every turn only; the removed ones still work, still complete and are listed
// by :help.
func TestBannerHelpTextKeepsRareCommandsInHelp(t *testing.T) {
	help := testTranslator.T("status.banner.help")
	for _, cmd := range []string{":add-dir", ":dirs", ":rm-dir", ":checkpoints", ":undo"} {
		assert.StrNotContains(t, help, cmd)
		assert.StrContains(t, buildChatHelp(testTranslator), cmd)
	}
	// The commands that stay are still named, in a readable sentence.
	for _, cmd := range []string{":session", ":compact", ":diff", ":quit/:exit", ":help"} {
		assert.StrContains(t, help, cmd)
	}
	assert.StrNotContains(t, help, "，，")
}

// TestLayoutBanner: the four banner lines, whole, with and without colors and
// with every part of the head present or missing.
func TestLayoutBanner(t *testing.T) {
	const help = "输入任务开始对话；!<命令> 直接在本地执行（!!<命令> 把结果附给下一条消息）；:session 显示会话，:compact 压缩上下文，:diff 显示文件改动，:quit/:exit 退出；:help 查看命令；Alt+Enter（Windows 上 Ctrl+Enter / Ctrl+J）在输入里换行，多行时上下键移动光标。"
	const readOnly = "工具：只读。要写文件、执行 shell，在 SUGA_HOME/config.yaml 设 tools.allow_write: true。"
	const writeOK = "工具：可读写文件、执行 shell（tools.approval 决定是否先确认）。"

	t.Run("plain: every part of the head and the read-only tool line", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Provider: "opencode-go", Model: "glm-5",
			Thinking: "high", ContextWindow: 950000})
		r.SetSessionName("ws-k7")
		r.Banner()
		assert.Eq(t, "Suga chat · opencode-go/glm-5 · thinking high · ctx 950k · 会话 ws-k7\n"+
			help+"\n"+readOnly+"\n", out.String())
		assert.StrNotContains(t, out.String(), "\x1b[")
	})

	t.Run("plain: no model, no name, write tools on", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, WriteTools: true}).Banner()
		assert.Eq(t, "Suga chat\n"+help+"\n"+writeOK+"\n", out.String())
	})

	t.Run("plain: the provider alone labels the head", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Provider: "fake"}).Banner()
		assert.StrContains(t, out.String(), "Suga chat · fake\n")
		assert.StrNotContains(t, out.String(), " · \n")
	})

	t.Run("colored: the head is cyan, the help and tool lines grey", func(t *testing.T) {
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Color: true, Provider: "p", Model: "m",
			Thinking: "low", ContextWindow: 1000})
		r.SetSessionName("ws-k7")
		r.Banner()
		assert.Eq(t, "\x1b[36mSuga chat · p/m · thinking low · ctx 1k · 会话 ws-k7\x1b[0m\n"+
			"\x1b[90m"+help+"\x1b[0m\n"+
			"\x1b[90m"+readOnly+"\x1b[0m\n", out.String())
	})
}

// TestLayoutTurnStatus: the end-of-turn lines, whole: the memory lines of the
// turn (green), then the grey status line with its optional model label,
// context share and inserted-message count.
func TestLayoutTurnStatus(t *testing.T) {
	memoryTurn := func(r *Renderer) {
		r.Handle(personal.Event{Kind: "tool_result", ToolName: "memory",
			Text: "Saved memory use-tabs (project): Use tabs\nkind: feedback"})
	}

	t.Run("plain: every part of the line, memory line first", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Provider: "opencode", Model: "glm-5",
			ContextWindow: 1000, now: testClock})
		r.BeginTurn()
		memoryTurn(r)
		out.Reset() // the memory tool's own result line is not what these cases pin
		r.Status(personal.RunResult{
			Usage: agentcore.Usage{InputTokens: 1200, OutputTokens: 34}, LastInputTokens: 550,
		}, 2340*time.Millisecond, 2)
		assert.Eq(t, "已记住：Use tabs（project）\n"+
			"opencode/glm-5 · in 1200 / out 34 tokens · ctx 55% · 2.3s · 12:34 · 插话 2\n", out.String())
		assert.StrNotContains(t, out.String(), "\x1b[")
	})

	t.Run("plain: no window and no inserted message keep both parts away", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Model: "m", now: testClock}).
			Status(personal.RunResult{Usage: agentcore.Usage{InputTokens: 12}, LastInputTokens: 550}, time.Second, 0)
		assert.Eq(t, "m · in 12 / out 0 tokens · 1.0s · 12:34\n", out.String())
	})

	t.Run("plain: a window without a model call keeps the share away", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, ContextWindow: 1000, now: testClock}).
			Status(personal.RunResult{Usage: agentcore.Usage{InputTokens: 12}}, time.Second, 1)
		assert.Eq(t, "in 12 / out 0 tokens · 1.0s · 12:34 · 插话 1\n", out.String())
	})

	t.Run("colored: the memory line is green, the status line grey", func(t *testing.T) {
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Color: true, Model: "m", now: testClock})
		r.BeginTurn()
		memoryTurn(r)
		out.Reset()
		r.Status(personal.RunResult{Usage: agentcore.Usage{InputTokens: 1, OutputTokens: 2}}, time.Second, 0)
		assert.Eq(t, "\x1b[32m已记住：Use tabs（project）\x1b[0m\n"+
			"\x1b[90mm · in 1 / out 2 tokens · 1.0s · 12:34\x1b[0m\n", out.String())
	})
}

// TestLayoutExitSummary: the whole exit summary, line for line: the blank line
// the prompt leaves behind, this process's turns and tokens (with the session
// total when known), the running time and the resume command behind the
// session's short name.
func TestLayoutExitSummary(t *testing.T) {
	t.Run("plain: turns, tokens, session total, short name and resume command", func(t *testing.T) {
		var s chatStats
		s.add(personal.RunResult{Usage: agentcore.Usage{InputTokens: 100, OutputTokens: 20}})
		s.add(personal.RunResult{Usage: agentcore.Usage{InputTokens: 50, OutputTokens: 5},
			SessionUsage: agentcore.Usage{InputTokens: 900, OutputTokens: 80, TotalTokens: 980}})
		var out bytes.Buffer
		s.print(&out, "sess_x", "ws-k7", 125*time.Second)
		assert.Eq(t, "\n本次 2 轮 · in 150 / out 25 tokens（会话累计 in 900 / out 80） · 运行 2m5s\n"+
			"会话 ws-k7 · 续接：suga chat --resume sess_x\n", out.String())
		assert.StrNotContains(t, out.String(), "\x1b[")
	})

	t.Run("plain: without a session total and without a short name", func(t *testing.T) {
		var s chatStats
		s.add(personal.RunResult{Usage: agentcore.Usage{InputTokens: 100, OutputTokens: 20}})
		var out bytes.Buffer
		s.print(&out, "sess_x", "", time.Second)
		assert.Eq(t, "\n本次 1 轮 · in 100 / out 20 tokens · 运行 1.0s\n续接：suga chat --resume sess_x\n", out.String())
	})

	t.Run("plain: a chat that ran no turn still names the session", func(t *testing.T) {
		var out bytes.Buffer
		chatStats{}.print(&out, "sess_x", "", 40*time.Millisecond)
		assert.Eq(t, "\n本次 0 轮 · in 0 / out 0 tokens · 运行 40ms\n续接：suga chat --resume sess_x\n", out.String())
	})

	t.Run("no session id: nothing at all", func(t *testing.T) {
		var out bytes.Buffer
		chatStats{}.print(&out, "", "ws-k7", time.Second)
		assert.Eq(t, "", out.String())
	})
}

// TestLayoutLastReplyNotices pins the startup notices above a continued
// session's last reply: the dim label, the incomplete note and the truncation
// hint, each on a line of its own.
func TestLayoutLastReplyNotices(t *testing.T) {
	t.Run("plain: label, incomplete note and truncation hint", func(t *testing.T) {
		lines := make([]string, replyTailLines+3)
		for i := range lines {
			lines[i] = "line"
		}
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).LastReply(strings.Join(lines, "\n"), "sess_x", true)
		got := out.String()
		assert.StrContains(t, got, "上一次回复：\n上一回合未正常结束\n已截断前 3 行，完整记录见 suga inspect trace sess_x\n▣ ")
		assert.StrNotContains(t, got, "\x1b[")
	})

	t.Run("colored: the notices are dim", func(t *testing.T) {
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Color: true}).LastReply("回答", "sess_x", false)
		assert.Eq(t, "\x1b[90m上一次回复：\x1b[0m\n\x1b[32m▣\x1b[0m 回答\n", out.String())
	})
}
