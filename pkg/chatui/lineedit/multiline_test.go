package lineedit

import (
	"bytes"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// newMultiLineTerminal builds a terminal holding line, with echoing off (a unit
// test has no screen to keep in step) and the cursor at its end.
func newMultiLineTerminal(line string) *Terminal {
	tm := NewTerminal(&bytes.Buffer{}, "❯ ")
	tm.echo = false
	tm.line = []rune(line)
	tm.pos = len(tm.line)
	return tm
}

// TestMultiLineArrowKeys: an input holding a newline (a paste, or Ctrl+J) moves the
// cursor inside it with Up/Down - keeping the screen column, ending after the last
// rune of a shorter row - and never walks the history. A single-line input keeps the
// history behaviour.
func TestMultiLineArrowKeys(t *testing.T) {
	t.Run("up keeps the column inside the text of the row", func(t *testing.T) {
		// The rows are "abcd" and "efgh"; the second row starts at column 0 and the
		// first after the two-column prompt, so keeping the screen column would drop
		// the cursor into the prompt's offset - the text column is used instead.
		tm := newMultiLineTerminal("abcd\nefgh")
		assert.Eq(t, len(tm.line), tm.pos)
		tm.handleKey(keyUp)
		assert.Eq(t, 4, tm.pos) // end of "abcd"
		tm.handleKey(keyUp)     // already on the first row: nothing to do
		assert.Eq(t, 4, tm.pos)
		tm.handleKey(keyDown) // back to the end of "efgh"
		assert.Eq(t, 9, tm.pos)
	})

	t.Run("a shorter row ends after its last rune", func(t *testing.T) {
		tm := newMultiLineTerminal("ab\nabcd")
		tm.pos = 0
		tm.handleKey(keyDown)
		assert.Eq(t, 3, tm.pos) // the second row's column 0, i.e. before "abcd"
		tm.handleKey(keyUp)
		assert.Eq(t, 0, tm.pos)
	})

	t.Run("wide runes keep the text column", func(t *testing.T) {
		tm := newMultiLineTerminal("第一行\n第二行")
		assert.Eq(t, 7, tm.pos)
		tm.handleKey(keyUp)
		assert.Eq(t, 3, tm.pos) // after the third wide rune of the first row
	})

	t.Run("a single line still walks the history", func(t *testing.T) {
		tm := newMultiLineTerminal("abcd")
		tm.history.Add("old")
		tm.handleKey(keyUp)
		assert.Eq(t, "old", string(tm.line))
	})

	t.Run("up walks the history from the first row", func(t *testing.T) {
		tm := newMultiLineTerminal("abcd\nefgh")
		tm.history.Add("old")
		tm.handleKey(keyUp) // inside the input: to the end of the first row
		assert.Eq(t, 4, tm.pos)
		tm.handleKey(keyUp) // already on the first row: the history
		assert.Eq(t, "old", string(tm.line))
	})

	t.Run("down brings the whole input back from the history", func(t *testing.T) {
		tm := newMultiLineTerminal("abcd\nefgh")
		tm.history.Add("old")
		tm.handleKey(keyUp)
		tm.handleKey(keyUp)
		assert.Eq(t, "old", string(tm.line))
		tm.handleKey(keyDown) // back to the input as it was
		assert.Eq(t, "abcd\nefgh", string(tm.line))
	})
}

// TestDeclaredNewlineInsertsNewline: the pump declares a newline as text (Alt+Enter,
// a paste), and the editor then inserts it instead of submitting - which is what
// makes a multi-line input reachable. The declaration is for one key only.
func TestDeclaredNewlineInsertsNewline(t *testing.T) {
	tm := newMultiLineTerminal("ab")
	tm.SetNewlineAsText()
	tm.handleKey(keyLF)
	assert.Eq(t, "ab\n", string(tm.line))
	assert.Eq(t, 3, tm.pos)

	// The declaration is used up: the next LF submits (ok true).
	line, ok := tm.handleKey(keyLF)
	assert.Eq(t, true, ok)
	assert.Eq(t, "ab\n", line)
}
