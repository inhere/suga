package lineedit

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// highlight is the input style the tests switch on: short markers instead of
// real ANSI codes, so the expectations stay readable.
var highlight = InputStyle{Prefix: "<bg>", Suffix: "<reset>"}

// readOne feeds one line of input and returns what ReadLine completed. The
// output buffer then holds the prompt, what was echoed while typing and the
// highlight repaint of the submitted line (nothing after it: no read follows).
func readOne(t *testing.T, tm *Terminal, mem *memTerm, input string) (string, error) {
	t.Helper()
	mem.in = append(mem.in, input)
	return tm.ReadLine()
}

// TestTerminal_submitHighlight: a submitted line is repainted where it already
// is, on the input style's background, its background reaching the end of every
// row the line occupies (automatic wrapping and pasted newlines included).
func TestTerminal_submitHighlight(t *testing.T) {
	t.Run("single row is filled to the terminal width", func(t *testing.T) {
		tm, mem := newMem("> ", 10)
		tm.SetInputStyle(highlight)
		line, err := readOne(t, tm, mem, "hi\r")
		assert.NoErr(t, err)
		assert.Eq(t, "hi", line)
		// Back to the start of the row ("\x1b[4D" after the prompt and "hi"), then
		// the prompt and the line again, filled with 6 spaces to 10 columns.
		assert.Eq(t, "> hi"+"\x1b[4D"+"<bg>"+"> "+"<bg>"+"hi"+"      "+"<reset>\r\n", mem.out.String())
	})

	t.Run("the prompt keeps its own colors", func(t *testing.T) {
		tm, mem := newMem("\x1b[36m> \x1b[0m", 10)
		tm.SetInputStyle(highlight)
		line, err := readOne(t, tm, mem, "hi\r")
		assert.NoErr(t, err)
		assert.Eq(t, "hi", line)
		// The prompt's reset would drop the background, so the style is opened
		// again behind it; its columns still count once in the padding.
		want := "\x1b[36m> \x1b[0m" + "hi" + "\x1b[4D" +
			"<bg>" + "\x1b[36m> \x1b[0m" + "<bg>" + "hi" + "      " + "<reset>" + "\r\n"
		assert.Eq(t, want, mem.out.String())
	})

	t.Run("every wrapped row is filled", func(t *testing.T) {
		tm, mem := newMem("> ", 10)
		tm.SetInputStyle(highlight)
		line, err := readOne(t, tm, mem, "abcdefghij\r")
		assert.NoErr(t, err)
		assert.Eq(t, "abcdefghij", line)
		// Typing: the 8th character fills row 0, so the echo already wrapped. Row
		// 0 is full (nothing to pad), row 1 ("ij") gets 8 spaces.
		want := "> abcdefgh\r\nij" + "\x1b[A\x1b[2D" +
			"<bg>" + "> " + "<bg>" + "abcdefgh" + "<reset>" + "\r\n" +
			"<bg>" + "ij" + "        " + "<reset>" + "\r\n"
		assert.Eq(t, want, mem.out.String())
	})

	t.Run("wide runes count two columns", func(t *testing.T) {
		tm, mem := newMem("", 10)
		tm.SetInputStyle(highlight)
		line, err := readOne(t, tm, mem, "你好ab\r")
		assert.NoErr(t, err)
		assert.Eq(t, "你好ab", line)
		assert.Eq(t, "你好ab"+"\x1b[6D"+"<bg>"+"你好ab"+"    "+"<reset>\r\n", mem.out.String())
	})

	t.Run("a wide rune pushed to the next row leaves the cell filled", func(t *testing.T) {
		tm, mem := newMem("> a", 10)
		tm.SetInputStyle(highlight)
		line, err := readOne(t, tm, mem, "你好世界\r")
		assert.NoErr(t, err)
		assert.Eq(t, "你好世界", line)
		// "> a你好世" is exactly 10 columns: the cell left for 界 is filled with a
		// space and 界 starts row 1 (writeLine's rule), which is then padded.
		want := "> a" + "你好世 \r\n界" + "\x1b[A\x1b[2D" +
			"<bg>" + "> a" + "<bg>" + "你好世 " + "<reset>" + "\r\n" +
			"<bg>" + "界" + "        " + "<reset>" + "\r\n"
		assert.Eq(t, want, mem.out.String())
	})

	t.Run("a pasted multi-line input paints every line", func(t *testing.T) {
		tm, mem := newMem("> ", 10)
		tm.SetInputStyle(highlight)
		line, err := readOne(t, tm, mem, "\x1b[200~a\nb\x1b[201~\r")
		assert.NoErr(t, err)
		assert.Eq(t, "a\nb", line)
		// The pasted newline is a real row break in the echo, and both rows carry
		// the background to the end of the row.
		want := "> a\r\nb" + "\x1b[A\x1b[D" +
			"<bg>" + "> " + "<bg>" + "a" + "       " + "<reset>" + "\r\n" +
			"<bg>" + "b" + "         " + "<reset>" + "\r\n"
		assert.Eq(t, want, mem.out.String())
	})

	t.Run("a fill width past the layout width fills further", func(t *testing.T) {
		tm, mem := newMem("> ", 10)
		tm.SetInputStyle(InputStyle{Prefix: "<bg>", Suffix: "<reset>", FillWidth: 20})
		line, err := readOne(t, tm, mem, "hi\r")
		assert.NoErr(t, err)
		assert.Eq(t, "hi", line)
		assert.Eq(t, "> hi"+"\x1b[4D"+"<bg>"+"> "+"<bg>"+"hi"+strings.Repeat(" ", 16)+"<reset>\r\n", mem.out.String())
	})

	t.Run("no style keeps the plain path", func(t *testing.T) {
		tm, mem := newMem("> ", 10)
		line, err := readOne(t, tm, mem, "hi\r")
		assert.NoErr(t, err)
		assert.Eq(t, "hi", line)
		assert.Eq(t, "> hi\r\n", mem.out.String())
	})

	t.Run("an empty line paints nothing", func(t *testing.T) {
		tm, mem := newMem("> ", 10)
		tm.SetInputStyle(highlight)
		line, err := readOne(t, tm, mem, "\r")
		assert.NoErr(t, err)
		assert.Eq(t, "", line)
		assert.Eq(t, "> \r\n", mem.out.String())
	})
}

// TestTerminal_pastedNewlineBreaksTheRow: a newline inside a pasted line is
// echoed as CRLF, so the row bookkeeping keeps matching the screen (raw mode has
// OPOST off: a bare LF would only move the cursor down and keep the column).
func TestTerminal_pastedNewlineBreaksTheRow(t *testing.T) {
	tm, mem := newMem("> ", 10)
	line, err := readOne(t, tm, mem, "\x1b[200~abc\ndef\x1b[201~\r")
	assert.NoErr(t, err)
	assert.Eq(t, "abc\ndef", line)
	// "def" starts a new row at column 0, not at the column the LF left; the
	// person's Enter, which sends the line, then moves on to the next row.
	assert.Eq(t, "> abc\r\ndef\r\n", mem.out.String())

	t.Run("positions after the break are on the next row", func(t *testing.T) {
		tm, _ := newMem("> ", 10)
		line := []rune("abc\ndef")
		tm.line, tm.pos = line, len(line)
		x, y := tm.posToXY(tm.pos)
		assert.Eq(t, 3, x)
		assert.Eq(t, 1, y)
	})
}

// TestPadLine: the background of a highlighted line reaches the end of the row,
// measured the way the line editor measures (escapes zero, wide runes two).
func TestPadLine(t *testing.T) {
	pad := strings.Repeat(" ", 3)
	for _, tc := range []struct {
		name  string
		text  string
		width int
		want  string
	}{
		{name: "short text is padded", text: "abc", width: 6, want: "abc" + pad},
		{name: "exact width is unchanged", text: "abcd", width: 4, want: "abcd"},
		{name: "longer text is unchanged", text: "abcde", width: 4, want: "abcde"},
		{name: "escape sequences take no columns", text: "\x1b[35mab\x1b[0m", width: 5, want: "\x1b[35mab\x1b[0m" + pad},
		{name: "wide runes take two columns", text: "你好", width: 5, want: "你好 "},
		{name: "no width fills nothing", text: "ab", width: 0, want: "ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, PadLine(tc.text, tc.width))
		})
	}
}
