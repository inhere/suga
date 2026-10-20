package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// renderMDWithCode renders text with the code options set; color switches the
// terminal styling on (chat colors a terminal only).
func renderMDWithCode(text string, color, highlight bool, theme string, maxLines int) (string, *MDTerm) {
	var out bytes.Buffer
	term := NewMDTerm(&out, color)
	term.SetCode(highlight, theme, maxLines)
	term.Write(text)
	term.Flush()
	return out.String(), term
}

// stripMDANSI drops the SGR styles, so the rendered text can be compared byte by
// byte with the source.
func stripMDANSI(s string) string { return mdANSIPattern.ReplaceAllString(s, "") }

// TestMDCodeHighlight: a fenced block with a known language is colored line by
// line, and only the colors are added: without them the text is the source.
func TestMDCodeHighlight(t *testing.T) {
	const body = "func main() {\n\tx := 1 // note\n\tfmt.Println(x)\n}"
	t.Run("highlighted and byte identical", func(t *testing.T) {
		got, _ := renderMDWithCode("```go\n"+body+"\n```\n", true, true, "", 0)
		assert.StrContains(t, got, "\x1b[")
		assert.Eq(t, "─── go\n"+body+"\n───\n", stripMDANSI(got))
	})
	t.Run("without a terminal or with NO_COLOR nothing is colored", func(t *testing.T) {
		got, _ := renderMDWithCode("```go\n"+body+"\n```\n", false, true, "", 0)
		assert.StrNotContains(t, got, "\x1b[")
	})
	t.Run("the switch turns the coloring off", func(t *testing.T) {
		got, _ := renderMDWithCode("```go\n"+body+"\n```\n", false, false, "", 0)
		assert.StrNotContains(t, got, "\x1b[")
		assert.Eq(t, "─── go\n"+body+"\n───\n", got)
	})
	t.Run("an unknown language stays plain", func(t *testing.T) {
		got, _ := renderMDWithCode("```not-a-language\nx := 1\n```\n", false, true, "", 0)
		assert.StrNotContains(t, got, "\x1b[")
		assert.Eq(t, "─── not-a-language\nx := 1\n───\n", got)
	})
	t.Run("a fence without a language stays plain", func(t *testing.T) {
		got, _ := renderMDWithCode("```\nx := 1\n```\n", false, true, "", 0)
		assert.StrNotContains(t, got, "\x1b[")
	})
	t.Run("a chosen theme colors the block", func(t *testing.T) {
		got, _ := renderMDWithCode("```go\n"+body+"\n```\n", true, true, "monokai", 0)
		assert.StrContains(t, got, "\x1b[")
		assert.Eq(t, "─── go\n"+body+"\n───\n", stripMDANSI(got))
	})
	t.Run("an unknown theme falls back to the default", func(t *testing.T) {
		got, _ := renderMDWithCode("```go\n"+body+"\n```\n", true, true, "no-such-theme", 0)
		assert.StrContains(t, got, "\x1b[")
		assert.Eq(t, "─── go\n"+body+"\n───\n", stripMDANSI(got))
	})
}

// TestMDCodeStreamsPerLine: a code line is colored as soon as its newline
// arrives, not when the block ends (a long block must not wait for its fence).
func TestMDCodeStreamsPerLine(t *testing.T) {
	var out bytes.Buffer
	term := NewMDTerm(&out, true)
	term.SetCode(true, "", 0)
	term.Write("```go\nfunc a()\n")
	assert.StrContains(t, stripMDANSI(out.String()), "func a()")
	assert.StrNotContains(t, stripMDANSI(out.String()), "───\n") // the closing fence is not there yet
	term.Write("```\n")
	term.Flush()
	assert.Eq(t, "─── go\nfunc a()\n───\n", stripMDANSI(out.String()))
}

// TestMDCodeLang: the language is the first word of the fence info, without its
// braces or leading dot.
func TestMDCodeLang(t *testing.T) {
	for info, want := range map[string]string{
		"":              "",
		"go":            "go",
		"Go":            "go",
		"go title=x":    "go",
		"{.go}":         "go",
		"  python  ":    "python",
		"{.go linenos}": "go",
	} {
		t.Run(info, func(t *testing.T) {
			assert.Eq(t, want, mdCodeLang(info))
		})
	}
}

// TestMDCodeHighlighter: the highlighter is only built for a known language.
func TestMDCodeHighlighter(t *testing.T) {
	assert.True(t, newMDCode("go", "") != nil)
	assert.True(t, newMDCode("go", "no-such-theme") != nil)
	assert.True(t, newMDCode("not-a-language", "") == nil)
	assert.True(t, newMDCode("", "") == nil)
	colored, ok := newMDCode("go", "").line("x := 1")
	assert.True(t, ok)
	assert.Eq(t, "x := 1", stripMDANSI(colored))
	assert.Eq(t, strings.Count(stripMDANSI(colored), "\n"), 0, "a colored line carries no newline of its own")
}
