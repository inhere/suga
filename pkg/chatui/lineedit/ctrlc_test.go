package lineedit

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// Ctrl-C with a non-empty input line (SetCtrlCClearsLine): the line is dropped and
// the read goes on, so the caller sees none of it. It is off by default - a reader
// that wants Ctrl-C as "cancel" (chat's approval question) leaves it off - and an
// empty line always ends the read.

func TestTerminal_CtrlCClearsNonEmptyLine(t *testing.T) {
	t.Run("the line is dropped and what follows is a line of its own", func(t *testing.T) {
		tm, mem := newMem("> ", 0)
		tm.SetCtrlCClearsLine(true)
		mem.in = append(mem.in, "abc\x03def\r")
		line, err := tm.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "def", line)
		// The three runes typed, then the line dropped - back to column 0, erase to
		// the end of the row, the prompt drawn again - then the next runes and the
		// submit. Nothing of "abc" survives on the screen.
		assert.Eq(t, "> abc\x1b[5D\x1b[K> def\r\n", mem.out.String())
	})

	t.Run("every row of a multi-line input is blanked", func(t *testing.T) {
		tm, mem := newMem("> ", 0)
		tm.SetCtrlCClearsLine(true)
		// A bracketed paste: the two lines are one input holding a newline, on the
		// screen the prompt row and one row below it.
		mem.in = append(mem.in, "\x1b[200~第一行\n第二行\x1b[201~\x03x\r")
		line, err := tm.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "x", line)
		// The paste as the editor echoes it (its own CR LF per line break), then the
		// drop: from the end of the last row back up - left to column 0, erase, one
		// row up, erase again (the prompt row) - and the prompt drawn again at the
		// start of the empty input.
		assert.Eq(t, "> 第一行\r\n第二行\x1b[6D\x1b[K\x1b[A\x1b[K> x\r\n", mem.out.String())
	})

	t.Run("an empty line still ends the read", func(t *testing.T) {
		tm, mem := newMem("> ", 0)
		tm.SetCtrlCClearsLine(true)
		mem.in = append(mem.in, "\x03")
		_, err := tm.ReadLine()
		assert.ErrIs(t, err, ErrInterrupt)
		assert.Eq(t, "> ", mem.out.String()) // nothing to drop, nothing redrawn
	})

	t.Run("without the flag a non-empty line ends the read as before", func(t *testing.T) {
		tm, mem := newMem("> ", 0)
		mem.in = append(mem.in, "abc", "\x03")
		_, err := tm.ReadLine()
		assert.ErrIs(t, err, ErrInterrupt)
		assert.Eq(t, "> abc", mem.out.String()) // the line stays for the caller
	})

	t.Run("the flag can be turned off again (a question borrowing the read)", func(t *testing.T) {
		tm, mem := newMem("> ", 0)
		tm.SetCtrlCClearsLine(true)
		tm.SetCtrlCClearsLine(false)
		mem.in = append(mem.in, "abc", "\x03")
		_, err := tm.ReadLine()
		assert.ErrIs(t, err, ErrInterrupt)
		assert.Eq(t, "> abc", mem.out.String())
	})

	t.Run("a read with echoing off drops the line without drawing", func(t *testing.T) {
		tm, mem := newMem("> ", 0)
		tm.SetCtrlCClearsLine(true)
		tm.echo = false
		mem.in = append(mem.in, "ab\x03cd\r")
		line, err := tm.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "cd", line)
		// The prompt and the row break of the submit, nothing of either line.
		assert.Eq(t, "> \r\n", mem.out.String())
	})
}
