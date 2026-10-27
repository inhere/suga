package lineedit

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// escMem is terminal input that hands out one input chunk per Read and, like
// chat's byte pump, declares a chunk that is exactly the Esc byte as a lone Esc
// press before the editor parses it.
type escMem struct {
	*memTerm
	tm      *Terminal
	declare bool
}

func (m *escMem) Read(p []byte) (int, error) {
	n, err := m.memTerm.Read(p)
	if m.declare && n == 1 && p[0] == keyEscape {
		m.tm.SetEscAlone()
	}
	return n, err
}

// newEscMemTerminal builds a terminal on one-byte input chunks with a counter for
// the declared Esc key presses.
func newEscMemTerminal(chunks []string, declare bool) (*Terminal, *memTerm, *int) {
	mem := &escMem{memTerm: &memTerm{in: chunks}, declare: declare}
	tm := NewTerminal(mem, "")
	mem.tm = tm
	esc := 0
	tm.EscCallback = func() { esc++ }
	return tm, mem.memTerm, &esc
}

// TestTerminal_EscAlone: a lone Esc the caller declared is reported as the Esc key
// and consumed, so the line editor neither waits for the rest of a sequence nor
// types the byte into the line. Without a declaration the Esc stays what it is for
// x/term: the start of a sequence.
func TestTerminal_EscAlone(t *testing.T) {
	t.Run("a declared Esc is reported and not typed into the line", func(t *testing.T) {
		tm, mem, esc := newEscMemTerminal([]string{"\x1b", "ab\r"}, true)
		lines := readAll(t, tm, mem, "")
		assert.Eq(t, []string{"ab"}, lines)
		assert.Eq(t, 1, *esc)
	})

	t.Run("an undeclared Esc is not reported", func(t *testing.T) {
		tm, mem, esc := newEscMemTerminal([]string{"\x1b", "ab\r"}, false)
		lines := readAll(t, tm, mem, "")
		// The Esc and the 'a' after it are one unknown key sequence, exactly as
		// upstream: only "b" is left as the line.
		assert.Eq(t, []string{"b"}, lines)
		assert.Eq(t, 0, *esc)
	})

	t.Run("an escape sequence is still a sequence", func(t *testing.T) {
		tm, mem, esc := newEscMemTerminal([]string{"a", "b", "\x1b", "[", "D", "c\r"}, false)
		lines := readAll(t, tm, mem, "")
		// The Left key moved the cursor, so the 'c' went in before the 'b'.
		assert.Eq(t, []string{"acb"}, lines)
		assert.Eq(t, 0, *esc)
		assert.StrContains(t, mem.out.String(), "\x1b[D")
	})

	t.Run("a declaration that meets no Esc byte is dropped", func(t *testing.T) {
		// The read starts on a partial rune, so the declaration goes away with it;
		// the Esc of a later read is then not reported.
		tm, mem, esc := newEscMemTerminal([]string{"\xe4", "\xbd\xa0x\r", "\x1b"}, false)
		tm.SetEscAlone()
		lines := readAll(t, tm, mem, "")
		assert.Eq(t, []string{"你x"}, lines)
		assert.Eq(t, 0, *esc)
	})
}
