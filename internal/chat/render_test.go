package chat

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gookit/color"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// turnEvents is one turn: commentary, a tool call and result, a plan, then the answer.
func turnEvents() []personal.Event {
	return []personal.Event{
		{Kind: "text_delta", Text: "looking"},
		{Kind: "message_stop"},
		{Kind: "tool_call", ToolName: "file_read", Text: `{"path":"a.go"}`},
		{Kind: "tool_result", ToolName: "file_read", Text: "package a\n\nfunc A() {}"},
		{Kind: "tool_call", ToolName: "shell", Text: `{"cmd":"false"}`},
		{Kind: "tool_result", ToolName: "shell", Text: "exit status 1", IsError: true},
		{Kind: "plan", Plan: &native.Plan{Explanation: "two steps", Steps: []native.PlanStep{
			{Step: "read", Status: "completed"}, {Step: "edit", Status: "in_progress"}, {Step: "test", Status: "pending"},
		}}},
		{Kind: "text_delta", Text: "done"},
		{Kind: "message_stop"},
	}
}

func runTurn(r *Renderer, elapsed time.Duration) {
	r.BeginTurn()
	for _, e := range turnEvents() {
		r.Handle(e)
	}
	r.Finish()
	r.Status(personal.RunResult{Usage: agentcore.Usage{InputTokens: 1200, OutputTokens: 34}}, elapsed, 0)
}

// testClock is the local clock chat tests inject into the status line, so the
// end-of-turn time is deterministic: 12:34.
func testClock() time.Time { return time.Date(2026, 10, 13, 12, 34, 0, 0, time.Local) }

// TestRendererLastReply: a continued session shows the model's last reply
// above the first prompt, with a dim label, the usual ▣ marker and the same
// markdown rendering as an answer; long replies are cut to their tail with a
// hint naming the trace command.
func TestRendererLastReply(t *testing.T) {
	t.Run("plain: the label, then the reply with the answer marker", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).LastReply("第一行\n第二行", "sess_1", false)
		assert.Eq(t, "上一次回复：\n▣ 第一行\n第二行\n", out.String())
		assert.StrNotContains(t, out.String(), "\x1b[")
	})
	t.Run("colored: the label is dim", func(t *testing.T) {
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Color: true}).LastReply("回答", "sess_1", false)
		assert.Eq(t, "\x1b[90m上一次回复：\x1b[0m\n\x1b[32m▣\x1b[0m 回答\n", out.String())
	})
	t.Run("markdown on a colored terminal renders the reply", func(t *testing.T) {
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Color: true, Markdown: true}).LastReply("## 标题", "sess_1", false)
		assert.StrContains(t, out.String(), "\x1b[32m▣\x1b[0m \x1b[1;36m标题\x1b[0m\n")
		assert.StrNotContains(t, out.String(), "##")
	})
	t.Run("incomplete adds the note", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).LastReply("回答", "sess_1", true)
		assert.Eq(t, "上一次回复：\n上一回合未正常结束\n▣ 回答\n", out.String())
	})
	t.Run("a long reply shows its last lines with a trace hint", func(t *testing.T) {
		lines := make([]string, 45)
		for i := range lines {
			lines[i] = fmt.Sprintf("line%02d", i+1)
		}
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).LastReply(strings.Join(lines, "\n"), "sess_1", false)
		got := out.String()
		assert.StrContains(t, got, "已截断前 5 行，完整记录见 suga inspect trace sess_1\n")
		assert.StrNotContains(t, got, "line05")
		assert.StrContains(t, got, "▣ line06\n")
		assert.StrContains(t, got, "line45\n")
	})
	t.Run("exactly the line limit is shown whole", func(t *testing.T) {
		lines := make([]string, replyTailLines)
		for i := range lines {
			lines[i] = fmt.Sprintf("line%02d", i+1)
		}
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).LastReply(strings.Join(lines, "\n"), "sess_1", false)
		assert.StrContains(t, out.String(), "▣ line01\n")
		assert.StrNotContains(t, out.String(), "已截断")
	})
}

// TestRendererToolArgsAreReadable: encoding/json escapes <, > and & as
// \u003c, \u003e and \u0026 (its HTML escaping), which turns the command of a
// shell call such as `gofer issue ready 2>&1 | grep x < in > out` into unreadable
// escapes on the tool line. The line shows the arguments as they were written;
// the events, the transcript and stream-json keep the escaped machine text.
func TestRendererToolArgsAreReadable(t *testing.T) {
	const args = `{"command":"gofer issue ready 2>&1 | Select-Object -First 40 < in > out","timeout":120}`

	t.Run("a shell command is shown as it is", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).Handle(personal.Event{
			Kind: "tool_call", ToolName: "shell_exec", Text: args})
		assert.Eq(t, "⏺ shell_exec "+args+"\n", out.String())
		assert.StrNotContains(t, out.String(), `\u00`)
	})

	t.Run("a sub-agent's tool call as well", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, AgentProgress: "full"}).Handle(personal.Event{Kind: "agent_progress", Agent: "survey",
			ToolName: "file_grep", Text: `{"pattern":"a<b>c&d"}`})
		assert.Eq(t, `  · file_grep {"pattern":"a<b>c&d"}`+"\n", out.String())
	})

	t.Run("numbers keep their exact form", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).Handle(personal.Event{Kind: "tool_call", ToolName: "t",
			Text: `{"timeout":120,"big":9007199254740993,"ratio":1.50}`})
		// Keys are encoded sorted, like the summary itself was (a map).
		assert.Eq(t, `⏺ t {"big":9007199254740993,"ratio":1.50,"timeout":120}`+"\n", out.String())
	})

	t.Run("anything that is not one JSON value is shown as it is", func(t *testing.T) {
		for _, text := range []string{"not json 2>&1", `{"a":1} {"b":"<x>"}`, ""} {
			var out bytes.Buffer
			newRenderer(rendererOptions{Out: &out}).Handle(personal.Event{Kind: "tool_call", ToolName: "t", Text: text})
			want := "⏺ t" + map[bool]string{true: " " + text, false: ""}[text != ""] + "\n"
			assert.Eq(t, want, out.String())
		}
	})
}

func TestRendererPlainTurn(t *testing.T) {
	var out, errOut bytes.Buffer
	r := newRenderer(rendererOptions{Out: &out, Err: &errOut, Provider: "opencode", Model: "glm-5", now: testClock})
	runTurn(r, 2340*time.Millisecond)

	// Each model message starts with the ▣ marker; tool and plan lines do not.
	want := strings.Join([]string{
		"▣ looking",
		`⏺ file_read {"path":"a.go"}`,
		"  ⎿ ok: package a func A() {}",
		`⏺ shell {"cmd":"false"}`,
		"  ⎿ error: exit status 1",
		"⏺ plan two steps",
		"  ✔ read",
		"  ▶ edit",
		"  ○ test",
		"▣ done",
		"opencode/glm-5 · in 1200 / out 34 tokens · 2.3s · 12:34",
		"",
	}, "\n")
	assert.Eq(t, want, out.String())
	assert.StrNotContains(t, out.String(), "\x1b[")

	t.Run("prompt and error", func(t *testing.T) {
		assert.Eq(t, "❯ ", r.Prompt(false))
		assert.Eq(t, "❯ (可等待外部回复) ", r.Prompt(true))
		r.Error(errors.New("boom"))
		assert.Eq(t, "Error: boom\n", errOut.String())
	})
	t.Run("status without model", func(t *testing.T) {
		var b bytes.Buffer
		newRenderer(rendererOptions{Out: &b, now: testClock}).Status(personal.RunResult{}, 40*time.Millisecond, 0)
		assert.Eq(t, "in 0 / out 0 tokens · 40ms · 12:34\n", b.String())
	})
	t.Run("status ends with the end-of-turn local time", func(t *testing.T) {
		var b bytes.Buffer
		newRenderer(rendererOptions{Out: &b, Model: "m", now: func() time.Time {
			return time.Date(2026, 10, 13, 0, 3, 59, 0, time.Local)
		}}).Status(personal.RunResult{}, time.Second, 0)
		assert.Eq(t, "m · in 0 / out 0 tokens · 1.0s · 00:03\n", b.String())
	})
	t.Run("status shows how full the context is", func(t *testing.T) {
		for _, tc := range []struct {
			name           string
			window, lastIn int
			want           string
		}{
			{"share of the last request", 1000, 550, "ctx 55%"},
			{"rounds down", 1000, 9, "ctx 0%"},
			{"above the window stays honest", 1000, 1050, "ctx 105%"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var b bytes.Buffer
				newRenderer(rendererOptions{Out: &b, Model: "m", ContextWindow: tc.window, now: testClock}).
					Status(personal.RunResult{Usage: agentcore.Usage{InputTokens: 12, OutputTokens: 3}, LastInputTokens: tc.lastIn}, time.Second, 0)
				assert.Eq(t, "m · in 12 / out 3 tokens · "+tc.want+" · 1.0s · 12:34\n", b.String())
			})
		}
	})
	t.Run("status without a window or a model call omits the share", func(t *testing.T) {
		for _, o := range []rendererOptions{
			{Model: "m", now: testClock},                      // window unknown
			{Model: "m", ContextWindow: 1000, now: testClock}, // no call reported tokens
		} {
			var b bytes.Buffer
			o.Out = &b
			newRenderer(o).Status(personal.RunResult{Usage: agentcore.Usage{InputTokens: 12}}, time.Second, 0)
			assert.Eq(t, "m · in 12 / out 0 tokens · 1.0s · 12:34\n", b.String())
		}
	})
}

// TestRendererInterrupt: an interrupted turn reads 已中断本轮 on the chat output
// (the runner's raw cancellation error never reaches the screen; it goes to
// stderr and the session record instead).
func TestRendererInterrupt(t *testing.T) {
	var out, errOut bytes.Buffer
	newRenderer(rendererOptions{Out: &out, Err: &errOut}).Interrupt()
	assert.Eq(t, interruptNotice+"\n", out.String())
	assert.Eq(t, "", errOut.String())
	assert.StrNotContains(t, out.String(), "context canceled")
}

func TestRendererColoredTurn(t *testing.T) {
	// Force the color library on regardless of the test environment.
	color.ForceColor()
	t.Cleanup(func() { color.RevertColorLevel() })

	var out, errOut bytes.Buffer
	r := newRenderer(rendererOptions{Out: &out, Err: &errOut, Color: true, ErrColor: true, Provider: "p", Model: "m", now: testClock})
	runTurn(r, time.Second)
	got := out.String()

	assert.StrContains(t, got, "\x1b[")
	// Tool line and status line are grey (90), failed results red (31).
	assert.StrContains(t, got, "\x1b[90m⏺ file_read {\"path\":\"a.go\"}\x1b[0m")
	assert.StrContains(t, got, "\x1b[31m  ⎿ error: exit status 1\x1b[0m")
	assert.StrContains(t, got, "\x1b[90mp/m · in 1200 / out 34 tokens · 1.0s · 12:34\x1b[0m")
	assert.StrContains(t, got, "\x1b[32m  ✔ read\x1b[0m")
	assert.Eq(t, "\x1b[36m❯ \x1b[0m", r.Prompt(false))

	r.Error(errors.New("boom"))
	assert.StrContains(t, errOut.String(), "\x1b[31mError: boom\x1b[0m")
}

// TestRendererMarkdown: chat renders the model's markdown on a colored
// terminal; without colors (a pipe, a redirect or NO_COLOR) or with
// chat.markdown off the text is printed exactly as it streams. The ▣ prefix
// stays on the first line of the message in every case.
func TestRendererMarkdown(t *testing.T) {
	color.ForceColor()
	t.Cleanup(func() { color.RevertColorLevel() })

	stream := func(opts rendererOptions) string {
		var out bytes.Buffer
		opts.Out = &out
		r := newRenderer(opts)
		r.Handle(personal.Event{Kind: "text_delta", Text: "## Title\nsee **docs**\n"})
		r.Handle(personal.Event{Kind: "message_stop"})
		r.Finish()
		return out.String()
	}
	const raw = "▣ ## Title\nsee **docs**\n"

	t.Run("terminal with colors", func(t *testing.T) {
		got := stream(rendererOptions{Color: true, Markdown: true})
		assert.StrContains(t, got, "\x1b[32m▣\x1b[0m \x1b[1;36mTitle\x1b[0m\n")
		assert.StrContains(t, got, "see \x1b[1mdocs\x1b[0m\n")
		assert.StrNotContains(t, got, "##")
	})
	t.Run("no colors: the text is raw", func(t *testing.T) {
		assert.Eq(t, raw, stream(rendererOptions{Markdown: true}))
	})
	t.Run("markdown off: the text is raw", func(t *testing.T) {
		assert.StrContains(t, stream(rendererOptions{Color: true}), "\x1b[32m▣\x1b[0m ## Title\nsee **docs**\n")
	})
	t.Run("a final answer that was not streamed", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Color: true, Markdown: true})
		r.Answer("## Title\nsee **docs**")
		assert.Eq(t, "\x1b[32m▣\x1b[0m \x1b[1;36mTitle\x1b[0m\nsee \x1b[1mdocs\x1b[0m\n", out.String())
	})
	t.Run("an answer without markdown", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Color: true}).Answer("## Title")
		assert.Eq(t, "\x1b[32m▣\x1b[0m ## Title\n", out.String())
	})
}

func TestColorEnabled(t *testing.T) {
	t.Run("tty without NO_COLOR", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		assert.True(t, colorEnabled(true))
	})
	t.Run("NO_COLOR disables", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		assert.False(t, colorEnabled(true))
	})
	t.Run("not a tty", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		assert.False(t, colorEnabled(false))
		assert.False(t, colorEnabledFor(&bytes.Buffer{}))
	})
}

func TestRendererExternalInput(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).External("[gofer web 回复] 你好\n第二行")
		assert.Eq(t, "[gofer web 回复] 你好\n第二行\n", out.String())
	})
	t.Run("colored per line", func(t *testing.T) {
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Color: true}).External("[来自 cli] a\nb")
		got := out.String()
		assert.Eq(t, 2, strings.Count(got, "\x1b[35m"))
		assert.StrContains(t, got, "[来自 cli] a")
		assert.StrContains(t, got, "b")
	})
	t.Run("highlighted per line, filled to the row end", func(t *testing.T) {
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Color: true, HighlightInput: true, HighlightWidth: func() int { return 20 }}).
			External("[来自 cli] a\nb")
		// "[来自 cli] a" is 12 columns wide: both lines are filled to 20.
		want := externalHighlightPrefix + "[来自 cli] a" + "        " + highlightSuffix + "\n" +
			externalHighlightPrefix + "b" + strings.Repeat(" ", 19) + highlightSuffix + "\n"
		assert.Eq(t, want, out.String())
	})
	t.Run("highlight needs colors and the switch", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, HighlightInput: true, HighlightWidth: func() int { return 20 }}).External("[来自 cli] a")
		assert.Eq(t, "[来自 cli] a\n", out.String())
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		out.Reset()
		newRenderer(rendererOptions{Out: &out, Color: true, HighlightWidth: func() int { return 20 }}).External("[来自 cli] a")
		assert.Eq(t, "\x1b[35m[来自 cli] a\x1b[0m\n", out.String())
	})
}

func TestBannerAndExitSummary(t *testing.T) {
	t.Run("banner shows the model", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Provider: "opencode-go", Model: "deepseek-v4-flash"}).Banner()
		assert.StrContains(t, out.String(), "Suga chat · opencode-go/deepseek-v4-flash\n")
	})
	t.Run("banner adds the thinking level and the context window", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Provider: "command-code", Model: "deepseek-v4.1-flash",
			Thinking: "high", ContextWindow: 950000}).Banner()
		assert.StrContains(t, out.String(), "Suga chat · command-code/deepseek-v4.1-flash · thinking high · ctx 950k\n")
	})
	t.Run("banner names both exit commands", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out}).Banner()
		assert.StrContains(t, out.String(), ":quit/:exit 退出")
	})
	t.Run("banner names the session", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Provider: "fake", Model: "m1"})
		r.SetSessionName("ws-k7")
		r.Banner()
		assert.StrContains(t, out.String(), "Suga chat · fake/m1 · 会话 ws-k7\n")
	})
	t.Run("token counts", func(t *testing.T) {
		for n, want := range map[int]string{512: "512", 128000: "128k", 950000: "950k", 1000000: "1M", 1048576: "1.05M", 200500: "200.5k"} {
			assert.Eq(t, want, formatTokens(n), n)
		}
	})
	t.Run("exit summary", func(t *testing.T) {
		var s chatStats
		s.add(personal.RunResult{Usage: agentcore.Usage{InputTokens: 100, OutputTokens: 20}})
		s.add(personal.RunResult{Usage: agentcore.Usage{InputTokens: 50, OutputTokens: 5},
			SessionUsage: agentcore.Usage{InputTokens: 900, OutputTokens: 80, TotalTokens: 980}})
		var out bytes.Buffer
		s.print(&out, "sess_x", "", 125*time.Second)
		assert.Eq(t, "\n本次 2 轮 · in 150 / out 25 tokens（会话累计 in 900 / out 80） · 运行 2m5s\n续接：suga chat --resume sess_x\n", out.String())
	})
	t.Run("exit summary names the short name", func(t *testing.T) {
		var s chatStats
		s.add(personal.RunResult{Usage: agentcore.Usage{InputTokens: 100, OutputTokens: 20}})
		var out bytes.Buffer
		s.print(&out, "sess_x", "ws-k7", time.Second)
		assert.StrContains(t, out.String(), "\n会话 ws-k7 · 续接：suga chat --resume sess_x\n")
	})
	t.Run("no session no summary", func(t *testing.T) {
		var out bytes.Buffer
		chatStats{}.print(&out, "", "ws-k7", time.Second)
		assert.Eq(t, "", out.String())
	})
	t.Run("elapsed formats", func(t *testing.T) {
		assert.Eq(t, "40ms", FormatElapsed(40*time.Millisecond))
		assert.Eq(t, "2.5s", FormatElapsed(2500*time.Millisecond))
		assert.Eq(t, "1h2m3s", FormatElapsed(time.Hour+2*time.Minute+3*time.Second))
	})
}

func TestRendererBannerToolLine(t *testing.T) {
	t.Run("read only points at tools.allow_write", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Model: "m"}).Banner()
		assert.StrContains(t, out.String(), "工具：只读")
		assert.StrContains(t, out.String(), "tools.allow_write: true")
	})
	t.Run("write tools on", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Model: "m", WriteTools: true}).Banner()
		assert.StrContains(t, out.String(), "执行 shell")
		assert.StrNotContains(t, out.String(), "工具：只读")
	})
}

// TestRendererMemoryWrites: successful memory writes of the turn are listed
// before the status line; failed ones and other tools are not.
func TestRendererMemoryWrites(t *testing.T) {
	var out bytes.Buffer
	r := newRenderer(rendererOptions{Out: &out, Err: &out, Model: "m"})
	r.BeginTurn()
	for _, e := range []personal.Event{
		{Kind: "tool_result", ToolName: "memory", Text: "Saved memory use-tabs (project): Use tabs\nkind: feedback"},
		{Kind: "tool_result", ToolName: "memory", Text: "Updated memory name (user): My name"},
		{Kind: "tool_result", ToolName: "memory", Text: "Removed memory old (project): Old note"},
		{Kind: "tool_result", ToolName: "memory", Text: "Saved memory x (project): denied", IsError: true},
		{Kind: "tool_result", ToolName: "file_read", Text: "Saved memory y (project): not memory"},
	} {
		r.Handle(e)
	}
	r.Status(personal.RunResult{}, time.Second, 0)
	assert.StrContains(t, out.String(), "已记住：Use tabs（project）\n已更新：My name（user）\n已删除：Old note（project）\nm · in 0")
	assert.NotContains(t, out.String(), "denied（")
	assert.NotContains(t, out.String(), "not memory（")

	t.Run("the next turn starts empty", func(t *testing.T) {
		out.Reset()
		r.BeginTurn()
		r.Status(personal.RunResult{}, time.Second, 0)
		assert.NotContains(t, out.String(), "已记住")
	})
}

// TestRendererAgentProgress: a sub-agent's tool calls show indented under the
// agent call, its result as usual.
func TestRendererAgentProgress(t *testing.T) {
	var out bytes.Buffer
	r := newRenderer(rendererOptions{Out: &out, Err: &out, AgentProgress: "full"})
	r.Handle(personal.Event{Kind: "tool_call", ToolName: "agent", Text: `{"description":"find Foo"}`})
	r.Handle(personal.Event{Kind: "agent_progress", Agent: "find Foo", ToolName: "file_grep", Text: `{"pattern":"Foo"}`})
	r.Handle(personal.Event{Kind: "tool_result", ToolName: "agent", Text: "Foo is in a.go:3"})
	assert.Eq(t, strings.Join([]string{
		`⏺ agent {"description":"find Foo"}`,
		`  · file_grep {"pattern":"Foo"}`,
		"  ⎿ ok: Foo is in a.go:3",
		"",
	}, "\n"), out.String())
}

// TestRendererAgentProgressModes covers the three chat display modes and keeps
// separate counts for interleaved child descriptions.
func TestRendererAgentProgressModes(t *testing.T) {
	for _, tc := range []struct {
		name, mode        string
		progress, summary bool
	}{
		{name: "summary", mode: "summary", progress: false, summary: true},
		{name: "full", mode: "full", progress: true, summary: false},
		{name: "off", mode: "off", progress: false, summary: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r := newRenderer(rendererOptions{Out: &out, AgentProgress: tc.mode})
			r.Handle(personal.Event{Kind: "tool_call", ToolName: "agent", Text: `{"description":"审计 bug"}`})
			r.Handle(personal.Event{Kind: "tool_call", ToolName: "agent", Text: `{"description":"读文档"}`})
			r.Handle(personal.Event{Kind: "agent_progress", Agent: "审计 bug", ToolName: "file_read"})
			r.Handle(personal.Event{Kind: "agent_progress", Agent: "读文档", ToolName: "file_grep"})
			r.Handle(personal.Event{Kind: "agent_progress", Agent: "审计 bug", ToolName: "file_find"})
			r.Handle(personal.Event{Kind: "agent_progress", Agent: "读文档", ToolName: "file_read"})
			r.Handle(personal.Event{Kind: "tool_result", ToolName: "agent", Text: "report 1",
				AgentSummary: &personal.AgentSummary{Description: "审计 bug", ToolCalls: 2, Duration: 2 * time.Second}})
			r.Handle(personal.Event{Kind: "tool_result", ToolName: "agent", Text: "report 2",
				AgentSummary: &personal.AgentSummary{Description: "读文档", ToolCalls: 2, Duration: 3 * time.Second}})
			got := out.String()
			assert.Eq(t, tc.progress, strings.Contains(got, "  · "))
			assert.Eq(t, tc.summary, strings.Contains(got, "子 agent 审计 bug：2 次工具调用，耗时 2.0s") &&
				strings.Contains(got, "子 agent 读文档：2 次工具调用，耗时 3.0s"))
		})
	}
}
