package chatui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gookit/color"
	"github.com/gookit/goutil/x/assert"
)

// renderMD renders one piece of markdown text in plain mode (no colors).
func renderMD(text string) string {
	var out bytes.Buffer
	term := NewMDTerm(&out, false)
	term.Write(text)
	term.Flush()
	return out.String()
}

// TestMDTermLinePrefix: a caller that may not write the message prefix itself
// (chat keeps an input line on screen while a turn runs) has the renderer put it
// at the start of every message's first line.
func TestMDTermLinePrefix(t *testing.T) {
	var out bytes.Buffer
	term := NewMDTerm(&out, false)
	term.SetLinePrefix("▣ ")
	term.Write("第一行\n第二行\n")
	term.Flush()
	term.Write("下一条")
	term.Flush()
	assert.Eq(t, "▣ 第一行\n第二行\n▣ 下一条\n", out.String())
}

// TestMDTermRendersElements covers every supported element, one table case each.
func TestMDTermRendersElements(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"heading", "## Title\n", "Title\n"},
		{"heading level 6", "###### deep\n", "deep\n"},
		{"hash without space is text", "#nospace\n", "#nospace\n"},
		{"bold", "a **bold** word\n", "a bold word\n"},
		{"bold with inner code", "**run `go test`**\n", "run go test\n"},
		{"italic stars", "an *italic* word\n", "an italic word\n"},
		{"italic underscores", "an _italic_ word\n", "an italic word\n"},
		{"intraword underscores stay", "call foo_bar_baz()\n", "call foo_bar_baz()\n"},
		{"inline code", "use `go test` here\n", "use go test here\n"},
		{"inline code keeps markers", "`**not bold**`\n", "**not bold**\n"},
		{"inline code keeps backticks in text", "`a` and `b`\n", "a and b\n"},
		{"unpaired bold stays", "a **b\n", "a **b\n"},
		{"unpaired italic stays", "a *b\n", "a *b\n"},
		{"unpaired underscore stays", "a _b\n", "a _b\n"},
		{"unpaired code stays", "a `b\n", "a `b\n"},
		{"bullet dash", "- one\n", "• one\n"},
		{"bullet star", "* one\n", "• one\n"},
		{"bullet plus with indent", "  + one\n", "  • one\n"},
		{"bullet with bold", "- **one** two\n", "• one two\n"},
		{"ordered list keeps the number", "1. one\n", "1. one\n"},
		{"ordered list keeps the indent and paren", "  10) ten\n", "  10) ten\n"},
		{"quote", "> quoted\n", "│ quoted\n"},
		{"nested quote", "> > deep\n", "│ │ deep\n"},
		{"link", "see [docs](https://x.example/y) now\n", "see docs (https://x.example/y) now\n"},
		{"link without a closing paren stays", "[docs](https://x\n", "[docs](https://x\n"},
		{"rule dashes", "---\n", strings.Repeat("─", mdRuleWidth) + "\n"},
		{"rule stars", "***\n", strings.Repeat("─", mdRuleWidth) + "\n"},
		{"table row is untouched", "| a | **b** |\n", "| a | **b** |\n"},
		{"plain text", "just text\n", "just text\n"},
		{"empty line", "\n", "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, renderMD(tc.in))
		})
	}
}

// TestMDTermCodeBlock: the fences become a line with the language, the body is
// kept exactly (no inline rendering) and an unclosed block stays code until the
// end of the message.
func TestMDTermCodeBlock(t *testing.T) {
	t.Run("closed block", func(t *testing.T) {
		got := renderMD("before\n```go\nx := `raw` **not bold**\n# not a heading\n```\nafter\n")
		assert.Eq(t, strings.Join([]string{
			"before",
			"─── go",
			"x := `raw` **not bold**",
			"# not a heading",
			"───",
			"after",
			"",
		}, "\n"), got)
	})
	t.Run("fence without a language", func(t *testing.T) {
		assert.Eq(t, "───\nbody\n───\n", renderMD("```\nbody\n```\n"))
	})
	t.Run("unclosed block runs to the end of the message", func(t *testing.T) {
		var out bytes.Buffer
		term := NewMDTerm(&out, false)
		term.Write("```\n**raw**\n")
		term.Flush() // end of the message: the block ends with it
		assert.Eq(t, "───\n**raw**\n", out.String())
		out.Reset()
		term.Write("**bold**\n") // the next message is not inside the block
		term.Flush()
		assert.Eq(t, "bold\n", out.String())
	})
}

// TestMDTermStreamsPerLine: an incomplete line waits in the buffer and appears
// once its newline arrives, so the caller can print while the message streams.
func TestMDTermStreamsPerLine(t *testing.T) {
	var out bytes.Buffer
	term := NewMDTerm(&out, false)
	term.Write("## Ti")
	assert.Eq(t, "", out.String())
	term.Write("tle\nsecond")
	assert.Eq(t, "Title\n", out.String())
	term.Write(" line\n")
	assert.Eq(t, "Title\nsecond line\n", out.String())
	term.Write("")
	term.Flush()
	assert.Eq(t, "Title\nsecond line\n", out.String())
}

// TestMDTermIncrementalWrites: feeding the text in any chunking gives the same
// output as feeding it whole (cutting inside a line and inside a marker).
func TestMDTermIncrementalWrites(t *testing.T) {
	const text = "## Title\n\nsome **bold** and `code`\n\n```go\nfmt.Println(\"**raw**\")\n```\n" +
		"> quote\n- item\n[link](https://x.example)\n---\ntail without newline"
	whole := renderMD(text)

	t.Run("one byte at a time", func(t *testing.T) {
		var out bytes.Buffer
		term := NewMDTerm(&out, false)
		for i := 0; i < len(text); i++ {
			term.Write(text[i : i+1])
		}
		term.Flush()
		assert.Eq(t, whole, out.String())
	})
	t.Run("every two-part split", func(t *testing.T) {
		for cut := 1; cut < len(text); cut++ {
			var out bytes.Buffer
			term := NewMDTerm(&out, false)
			term.Write(text[:cut])
			term.Write(text[cut:])
			term.Flush()
			assert.Eq(t, whole, out.String(), cut)
		}
	})
	t.Run("chunks of five", func(t *testing.T) {
		var out bytes.Buffer
		term := NewMDTerm(&out, false)
		for i := 0; i < len(text); i += 5 {
			term.Write(text[i:min(i+5, len(text))])
		}
		term.Flush()
		assert.Eq(t, whole, out.String())
	})
}

// TestMDTermPlainWithoutColor: with colors off the output has no escape codes,
// whatever the marker.
func TestMDTermPlainWithoutColor(t *testing.T) {
	got := renderMD("# Title\n**bold**\n> quote\n```go\ncode\n```\n---\n")
	assert.StrNotContains(t, got, "\x1b[")
}

// TestMDTermColors: with colors on every element is styled.
func TestMDTermColors(t *testing.T) {
	color.ForceColor()
	t.Cleanup(func() { color.RevertColorLevel() })

	var out bytes.Buffer
	term := NewMDTerm(&out, true)
	term.Write("# Title\nplain **bold** *it*\n> quote\n`code`\n```go\ncode line\n```\n---\n")
	got := out.String()

	assert.StrContains(t, got, "\x1b[1;36mTitle\x1b[0m")                // heading: bold cyan
	assert.StrContains(t, got, "\x1b[1mbold\x1b[0m")                    // bold
	assert.StrContains(t, got, "\x1b[3mit\x1b[0m")                      // italic
	assert.StrContains(t, got, "\x1b[90m│ \x1b[0m\x1b[90mquote\x1b[0m") // quote, dim
	assert.StrContains(t, got, "\x1b[93mcode\x1b[0m")                   // inline code
	assert.StrContains(t, got, "\x1b[90m─── go\x1b[0m")                 // fence line, dim
	assert.StrContains(t, got, "\x1b[90mcode line\x1b[0m")              // code body, dim
	assert.StrContains(t, got, "\x1b[90m"+strings.Repeat("─", mdRuleWidth)+"\x1b[0m")
}

// TestMDTermPrint: a whole answer printed at once ends its line; empty text
// only closes the line the caller's prefix was written on.
func TestMDTermPrint(t *testing.T) {
	printed := func(text string) string {
		var out bytes.Buffer
		term := NewMDTerm(&out, false)
		term.Print(text)
		return out.String()
	}
	t.Run("without a trailing newline", func(t *testing.T) {
		assert.Eq(t, "a\nb\n", printed("a\n**b**"))
	})
	t.Run("with a trailing newline", func(t *testing.T) {
		assert.Eq(t, "a\n", printed("a\n"))
	})
	t.Run("empty text", func(t *testing.T) {
		assert.Eq(t, "\n", printed(""))
	})
}
