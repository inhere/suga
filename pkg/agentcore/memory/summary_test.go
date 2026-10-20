package memory

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestCleanSummary(t *testing.T) {
	t.Run("short text is kept", func(t *testing.T) {
		assert.Eq(t, "一句话摘要", CleanSummary("一句话摘要"))
	})
	t.Run("newlines fold into one line", func(t *testing.T) {
		assert.Eq(t, "first second third", CleanSummary("first\nsecond\r\n\tthird\n"))
	})
	t.Run("long text is cut with an ellipsis", func(t *testing.T) {
		got := CleanSummary(strings.Repeat("摘", SummaryMaxRunes+20))
		assert.Eq(t, SummaryMaxRunes, len([]rune(got)))
		assert.True(t, strings.HasSuffix(got, "…"))
	})
	t.Run("exactly at the limit is untouched", func(t *testing.T) {
		in := strings.Repeat("a", SummaryMaxRunes)
		assert.Eq(t, in, CleanSummary(in))
	})
}

func TestFirstSentence(t *testing.T) {
	cases := map[string]string{
		"中文。第二句":                     "中文。",
		"One sentence. Another one.": "One sentence.",
		"no terminator here":         "no terminator here",
		"line one\nline two":         "line one line two",
		"升级到 v0.31.1 后修复了启动卡死":       "升级到 v0.31.1 后修复了启动卡死",
		"":    "",
		"   ": "",
	}
	for in, want := range cases {
		assert.Eq(t, want, FirstSentence(in))
	}
}

func TestIndexLineRenderSummary(t *testing.T) {
	t.Run("no summary keeps the old format", func(t *testing.T) {
		l := IndexLine{ID: "use-tabs", Title: "Use tabs", Kind: KindFeedback}
		assert.Eq(t, "- use-tabs (feedback): Use tabs", l.Render())
	})
	t.Run("summary is appended", func(t *testing.T) {
		l := IndexLine{ID: "use-tabs", Title: "Use tabs", Kind: KindFeedback, Summary: "项目缩进约定"}
		assert.Eq(t, "- use-tabs (feedback): Use tabs — 项目缩进约定", l.Render())
	})
}

func TestIndexRenderSingleParagraph(t *testing.T) {
	ix := Index{
		Scope: UserScope(),
		Lines: []IndexLine{
			{ID: "a", Title: "A", Kind: KindUser, Summary: "who the user is"},
			{ID: "b", Title: "B", Kind: KindProject},
		},
	}
	assert.Eq(t, "### Memory: user\n- a (user): A — who the user is\n- b (project): B\n", ix.Render())
	assert.Eq(t, "", Index{Scope: UserScope()}.Render())
}
