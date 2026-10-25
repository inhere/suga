package chat

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
)

// skillsFixture is the listing :skills prints in these tests.
func skillsFixture() []SkillInfo {
	return []SkillInfo{
		{Name: "ask-matt", Description: "grills a plan\nbefore coding", Source: "内置"},
		{Name: "suga-guide", Description: "suga 的安装与配置", Source: "内置", AlwaysLoad: true},
		{Name: "team-style", Description: strings.Repeat("很长的说明", 40), Source: "项目"},
	}
}

// TestBuildChatSkills pins the three shapes of the listing: descriptions with
// their source, the names alone and a limit that says how many are left.
func TestBuildChatSkills(t *testing.T) {
	t.Run("descriptions and sources, sorted as given", func(t *testing.T) {
		got := BuildChatSkills(skillsFixture(), false, 0)
		assert.StrContains(t, got, "已加载 3 个 skill：")
		assert.StrContains(t, got, "ask-matt — grills a plan before coding · 内置")
		assert.StrContains(t, got, "suga-guide — suga 的安装与配置 · 内置 · 常驻")
		assert.StrContains(t, got, "team-style — 很长的说明")
		assert.StrContains(t, got, " · 项目")
		assert.NotContains(t, got, "未显示")
		// A long description is cut to one bounded line.
		for _, line := range strings.Split(got, "\n") {
			assert.Require(t, assert.True(t, len([]rune(line)) <= maxSkillLineDescription+40))
		}
	})

	t.Run("only names", func(t *testing.T) {
		got := BuildChatSkills(skillsFixture(), true, 0)
		assert.Eq(t, "ask-matt, suga-guide, team-style", got)
	})

	t.Run("limit says how many are left", func(t *testing.T) {
		assert.StrContains(t, BuildChatSkills(skillsFixture(), false, 2), "还有 1 个未显示")
		assert.Eq(t, "ask-matt, suga-guide …（还有 1 个，省略 -n 显示全部）", BuildChatSkills(skillsFixture(), true, 2))
		// A limit at or above the count hides nothing.
		assert.NotContains(t, BuildChatSkills(skillsFixture(), false, 3), "还有")
	})

	t.Run("no skill at all", func(t *testing.T) {
		got := BuildChatSkills(nil, false, 0)
		assert.StrContains(t, got, "没有加载任何 skill")
		assert.StrContains(t, got, "suga inspect manifest")
	})
}

// TestChatSkillsCommand drives :skills through runChatCommand, the path chat
// takes: the listing, --only-names, -n, -h, an unknown option and a state
// without a skill registry.
func TestChatSkillsCommand(t *testing.T) {
	var buf bytes.Buffer
	source, err := newChatInputSource(context.Background(), strings.NewReader(""), &buf)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = source.Close() })
	s := &chatState{ctx: context.Background(), source: source, sessionID: "sid",
		render: newRenderer(rendererOptions{Out: &buf, Err: &buf}), skills: skillsFixture}

	t.Run("default and options", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":skills")))
		assert.StrContains(t, buf.String(), "ask-matt — grills a plan before coding · 内置")

		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":skills --only-names -n 2")))
		assert.StrContains(t, buf.String(), "ask-matt, suga-guide …（还有 1 个")
	})

	t.Run("bad arguments show the usage", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":skills -n abc")))
		assert.StrContains(t, buf.String(), "未知参数：-n abc 需要非负整数")
		assert.StrContains(t, buf.String(), "用法：:skills")

		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":skills --verbose")))
		assert.StrContains(t, buf.String(), "未知参数：--verbose")
	})

	t.Run("help", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":skills -h")))
		assert.StrContains(t, buf.String(), "用法：:skills [--only-names] [-n N]")
	})

	t.Run("without a registry", func(t *testing.T) {
		buf.Reset()
		broken := &chatState{ctx: context.Background(), source: source, sessionID: "sid",
			render: newRenderer(rendererOptions{Out: &buf, Err: &buf})}
		assert.Require(t, assert.True(t, broken.runChatCommand(":skills")))
		assert.StrContains(t, buf.String(), "skill 列表在本进程不可用")
	})
}

// contextFixture is the resident composition :context prints in these tests.
func contextFixture() ContextReport {
	return ContextReport{
		Parts: []ContextPart{
			{Name: "system_prompt", Bytes: 4000, Tokens: 1000},
			{Name: "environment", Bytes: 200, Tokens: 50},
			{Name: "rules", Bytes: 8000, Tokens: 2000,
				Files: []ContextFile{{Path: "AGENTS.md", Bytes: 8000, Included: true}}},
			{Name: "memory_index", Bytes: 0, Tokens: 0},
			{Name: "skills", Bytes: 600, Tokens: 150, Note: "9 个 skill"},
			{Name: "host_context", Bytes: 0},
		},
		Deferred: 12, Loaded: 2,
	}
}

// TestBuildChatContext pins the composition listing: every non-empty part with
// its tokens, the tool table with the deferred counts, the handoff, the history
// and both estimates.
func TestBuildChatContext(t *testing.T) {
	usage := personal.ContextUsage{Handoff: 2000, HandoffTokens: 500, History: 40000, HistoryTokens: 10000,
		HistoryMessages: 25, Input: 300, EstTokens: 14000, Window: 200000}
	got := BuildChatContext(contextFixture(), ToolsInfo{Bytes: 24000, Tokens: 6000, Count: 21, MCP: 3, Deferred: 12, Loaded: 2}, usage)
	assert.StrContains(t, got, "工具 schema：23.4 KB · 约 6k tok · 21 个工具（MCP 3）；按需加载 12 个，已加载 2 个")
	assert.StrContains(t, got, "系统提示：3.9 KB · 约 1k tok")
	assert.StrContains(t, got, "项目规则：7.8 KB · 约 2k tok · 1 个文件")
	assert.StrContains(t, got, "skills 列表：600 B · 约 150 tok（9 个 skill）")
	assert.NotContains(t, got, "记忆索引")
	assert.NotContains(t, got, "宿主上下文")
	assert.StrContains(t, got, "交接包：2.0 KB · 约 500 tok")
	assert.StrContains(t, got, "历史消息：39.1 KB · 约 10k tok · 25 条")
	assert.StrContains(t, got, "上一回合消息：300 B")
	assert.StrContains(t, got, "估算合计：约 19.7k tok")
	assert.StrContains(t, got, "上一回合请求实测估算 14k tok / 窗口 200k tok")
}

// TestBuildChatContextEmpty pins the degenerate case: an empty report still
// prints every section instead of an empty answer.
func TestBuildChatContextEmpty(t *testing.T) {
	got := BuildChatContext(ContextReport{}, ToolsInfo{}, personal.ContextUsage{})
	assert.StrContains(t, got, "常驻上下文：（空）")
	assert.StrContains(t, got, "交接包：0 B · 约 0 tok")
	assert.StrContains(t, got, "估算合计：约 0 tok")
	assert.NotContains(t, got, "上一回合")
}

// TestBuildChatContextFiles pins :context --files: the tool reads newest first,
// then the rule sources, and how much each contributed.
func TestBuildChatContextFiles(t *testing.T) {
	files := []personal.LoadedFile{
		{Path: "internal/chat/chat.go", Tool: "file_read", Turn: 3, Bytes: 2048},
		{Path: "README.md", Tool: "file_edit", Turn: 4, Bytes: 512},
	}
	got := BuildChatContextFiles(contextFixture(), files)
	assert.StrContains(t, got, "上下文已加载的文件（3 个，最近使用在前）：")
	assert.StrContains(t, got, "internal/chat/chat.go · file_read 第 3 轮 · 2.0 KB")
	assert.StrContains(t, got, "README.md · file_edit 第 4 轮 · 512 B")
	assert.StrContains(t, got, "AGENTS.md · 规则文件 · 7.8 KB")
	assert.Require(t, assert.True(t, strings.Index(got, "README.md") < strings.Index(got, "AGENTS.md")))

	assert.StrContains(t, BuildChatContextFiles(ContextReport{}, nil), "上下文里还没有已加载的文件")
}

// TestChatContextCommand drives :context through runChatCommand: the listing,
// --files, -h and an unknown option.
func TestChatContextCommand(t *testing.T) {
	var buf bytes.Buffer
	source, err := newChatInputSource(context.Background(), strings.NewReader(""), &buf)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = source.Close() })
	s := &chatState{ctx: context.Background(), source: source, sessionID: "sid",
		render:       newRenderer(rendererOptions{Out: &buf, Err: &buf}),
		contextParts: func(context.Context) ContextReport { return contextFixture() }}

	t.Run("composition", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":context")))
		assert.StrContains(t, buf.String(), "上下文构成")
		assert.StrContains(t, buf.String(), "系统提示：3.9 KB")
		assert.StrContains(t, buf.String(), "按需加载 12 个，已加载 2 个")
	})

	t.Run("files only", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":context --files")))
		assert.StrContains(t, buf.String(), "AGENTS.md · 规则文件")
		assert.NotContains(t, buf.String(), "上下文构成")
	})

	t.Run("help and an unknown option", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":context -h")))
		assert.StrContains(t, buf.String(), "用法：:context [--files]")

		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":context --dirs")))
		assert.StrContains(t, buf.String(), "未知参数：--dirs")
	})
}

// TestUnknownCommandHint pins which lines count as an attempted command: a
// single ":word" that names no command is answered here, while an emoticon, a
// colon with text, an IPv6 address and every line with a space stay messages
// for the model.
func TestUnknownCommandHint(t *testing.T) {
	cases := []struct {
		line string
		hint bool
		want string // a substring of the hint when hint is true
	}{
		{line: ":sessio", hint: true, want: "未知命令 :sessio，是否想用 :session？"},
		{line: ":contxt", hint: true, want: ":context"},
		{line: ":taskss", hint: true, want: ":tasks"},
		{line: ":zzzzzzzzzz", hint: true, want: "未知命令 :zzzzzzzzzz；:help 列出全部命令"},
		// Known commands, and commands with arguments, are not typos.
		{line: ":session", hint: false},
		{line: ":session foo", hint: false},
		{line: ":sessions", hint: true, want: ":session"}, // unknown, a close name to suggest
		// Ordinary text that starts with a colon is a message.
		{line: ":)", hint: false},
		{line: ":", hint: false},
		{line: ": 你好", hint: false},
		{line: ":中文", hint: false},
		{line: "::1", hint: false},
		{line: ":123", hint: false},
		{line: "hello:world", hint: false},
		{line: "", hint: false},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			if !c.hint {
				assert.Eq(t, "", unknownCommandHint(c.line))
				return
			}
			got := unknownCommandHint(c.line)
			assert.StrContains(t, got, "未知命令 "+c.line)
			if c.want != "" {
				assert.StrContains(t, got, c.want)
			}
			assert.Require(t, assert.True(t, len(similarCommands(c.line)) <= unknownCommandSuggestions))
		})
	}
}

// TestUnknownCommandSuggestions pins the ranking: the closest command first and
// at most three of them.
func TestUnknownCommandSuggestions(t *testing.T) {
	assert.Eq(t, ":session", similarCommands(":sessio")[0])
	assert.Eq(t, ":context", similarCommands(":cntext")[0])
	assert.Require(t, assert.True(t, len(similarCommands(":nope-nope")) <= unknownCommandSuggestions))
	assert.Eq(t, 0, editDistance(":dirs", ":dirs"))
	assert.Eq(t, 1, editDistance(":dirs", ":dirsx"))
}
