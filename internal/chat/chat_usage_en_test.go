package chat

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

// testTranslator is the Chinese catalog the migrated command text is asserted
// against: the existing tests pin what a zh chat prints.
var testTranslator = i18n.Fallback()

// enTranslator is an English catalog for the assertions of a language: en chat.
func enTranslator(t *testing.T) i18n.Translator {
	t.Helper()
	catalog, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	return catalog
}

// TestCommandHelpEnglish pins :help, a command's usage and the answers of an
// unknown command in English: the command names, the arguments and the "-h"
// syntax stay as they are.
func TestCommandHelpEnglish(t *testing.T) {
	tr := enTranslator(t)

	t.Run("help lists every command in English", func(t *testing.T) {
		help := BuildChatHelp(tr)
		assert.StrContains(t, help, ":session Show this session")
		assert.StrContains(t, help, ":queue")
		assert.StrContains(t, help, "See a command's details: :<命令> -h")
		assert.StrContains(t, help, "Keys: Enter/Alt+Enter/Ctrl+J")
		assert.NotContains(t, help, "显示会话")
	})

	t.Run("a command usage keeps its name and options", func(t *testing.T) {
		usage := commandUsageText(tr, queueCmd)
		assert.StrContains(t, usage, "Usage: :queue [-d <N>|clear]")
		assert.StrContains(t, usage, ":queue -d 2")
		assert.NotContains(t, usage, "用法")
		// A line whose argument does not parse names the argument as it was typed.
		assert.StrContains(t, commandUsageText(tr, ":nope"), "Unknown command: :nope")
	})

	t.Run("unknown command hints", func(t *testing.T) {
		assert.Eq(t, "Unknown command :sessio, did you mean :session? (close: :session; :help lists every command)",
			unknownCommandHint(tr, ":sessio"))
		assert.Eq(t, "Unknown command :zzz; :help lists every command", unknownCommandHint(tr, ":zzz"))
		assert.Eq(t, "", unknownCommandHint(tr, "hello"))
	})

	t.Run("help for one command", func(t *testing.T) {
		assert.Eq(t, commandUsageText(tr, undoCmd), chatHelpFor(tr, " :undo "))
		assert.Eq(t, "Unknown command: :nope; :help lists every command", chatHelpFor(tr, ":nope"))
	})

	t.Run("a usage ask and an argument a command cannot take", func(t *testing.T) {
		assert.Eq(t, commandUsageText(tr, ":session"), commandUsageReply(tr, ":session -h", true))
		assert.Eq(t, "Unknown argument: foo\n"+commandUsageText(tr, ":session"), commandUsageReply(tr, ":session foo", true))
	})
}

// TestUsageAndContextEnglish pins the :usage line and the :context title lines in
// English: only the labels move, the numbers and their order do not.
func TestUsageAndContextEnglish(t *testing.T) {
	tr := enTranslator(t)

	t.Run("usage keeps the counters", func(t *testing.T) {
		stats := chatStats{
			turns:   2,
			usage:   agentcore.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3, CacheWriteTokens: 1, CostUSD: 0.25},
			session: agentcore.Usage{InputTokens: 10, OutputTokens: 4, CostUSD: 0.25},
		}
		got := BuildChatUsage(tr, stats)
		assert.StrContains(t, got, ":usage usage: this session 2 turns · input 10 / output 4 · cache read 3 / write 1 · cost $0.250000")
		assert.NotContains(t, got, "用量")
	})

	t.Run("context titles", func(t *testing.T) {
		got := BuildChatContext(tr, contextFixture(), ToolsInfo{Bytes: 24000, Tokens: 6000, Count: 21}, personal.ContextUsage{})
		assert.StrContains(t, got, "Context composition (estimated tokens, the algorithm of the context trace event):")
		assert.StrContains(t, got, "System prompt")
		assert.StrContains(t, got, "Tool schemas: 23.4 KB · about 6k tok · 21 tools")
		assert.StrContains(t, got, "Estimated total:")
		assert.NotContains(t, got, "上下文构成")
	})

	t.Run("context files", func(t *testing.T) {
		assert.Eq(t, "The context carries no loaded files yet: files read by file_read and rule files show up here",
			BuildChatContextFiles(tr, ContextReport{}, nil))
	})
}
