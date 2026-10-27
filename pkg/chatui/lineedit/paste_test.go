package lineedit

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestTerminal_pasteIsText: a newline inside a bracketed paste is text of the
// paste, not a submit (paste.go). The whole paste lands on the input line, the
// person reads it and their own Enter — the only newline that ends a read —
// sends it as one line, its internal newlines kept. Terminals hand the line
// breaks of a paste over as CR or as LF, so both are covered.
func TestTerminal_pasteIsText(t *testing.T) {
	t.Run("a CR inside the paste does not submit", func(t *testing.T) {
		tm, mem := newMem("> ", 0)
		mem.in = []string{"\x1b[200~你好\r世界\x1b[201~", "\r"}
		line, err := tm.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "你好\n世界", line)
		// The pasted line break is echoed as a row break (CRLF in raw mode), and the
		// person's Enter is what moved on.
		assert.StrContains(t, mem.out.String(), "> 你好\r\n世界")
		assert.True(t, strings.HasSuffix(mem.out.String(), "\r\n"))
	})

	t.Run("an LF inside the paste stays in the text", func(t *testing.T) {
		tm, mem := newMem("> ", 0)
		line, err := readOne(t, tm, mem, "\x1b[200~a\nb\x1b[201~\r")
		assert.NoErr(t, err)
		assert.Eq(t, "a\nb", line)
	})

	t.Run("the paste alone does not end the read", func(t *testing.T) {
		// Only the paste, no Enter after it: the read goes on with an empty input
		// line, so the pasted text is still there to be edited (the read ends here
		// on the input's EOF).
		tm, mem := newMem("> ", 0)
		line, err := readOne(t, tm, mem, "\x1b[200~a\nb\x1b[201~")
		assert.True(t, errors.Is(err, io.EOF))
		assert.Eq(t, "", line)
	})

	t.Run("typing continues on the pasted line", func(t *testing.T) {
		// The paste is one input on one line: anything typed after it (and before
		// the Enter) joins it.
		tm, mem := newMem("> ", 0)
		mem.in = []string{"\x1b[200~a\nb\x1b[201~", "c", "\r"}
		line, err := tm.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "a\nbc", line)
	})
}

// pasteMem is terminal input that hands out one chunk per Read and, like chat's
// byte pump on a terminal that does not bracket pastes, declares a chunk that is
// exactly a newline as text of a paste before the editor parses it
// (SetNewlineAsText) — but only when another chunk follows it, which is what the
// pump sees a pasted newline by.
type pasteMem struct {
	*memTerm
	tm      *Terminal
	declare bool
}

func (m *pasteMem) Read(p []byte) (int, error) {
	n, err := m.memTerm.Read(p)
	if m.declare && n == 1 && (p[0] == keyEnter || p[0] == keyLF) && len(m.memTerm.in) > 0 {
		m.tm.SetNewlineAsText()
	}
	return n, err
}

// newPasteMemTerminal builds a terminal on one-byte input chunks where every
// newline chunk is declared as pasted text (declare) or not.
func newPasteMemTerminal(chunks []string, declare bool) (*Terminal, *memTerm, *pasteMem) {
	mem := &pasteMem{memTerm: &memTerm{in: chunks}, declare: declare}
	tm := NewTerminal(mem, "")
	mem.tm = tm
	return tm, mem.memTerm, mem
}

// TestTerminal_declaredNewlineIsText: a terminal without bracketed pastes gives
// the same bytes for a pasted and a typed newline. A caller that can tell them
// apart (chat's byte pump) declares the pasted ones with SetNewlineAsText; the
// editor then inserts them into the line instead of ending the read, and the
// declaration covers that one key only.
func TestTerminal_declaredNewlineIsText(t *testing.T) {
	t.Run("a declared newline is inserted as text", func(t *testing.T) {
		tm, _, _ := newPasteMemTerminal([]string{"a", "\r", "b", "\r"}, true)
		lines := []string{}
		for {
			line, err := tm.ReadLine()
			if errors.Is(err, io.EOF) {
				break
			}
			assert.NoErr(t, err)
			lines = append(lines, line)
		}
		// The first newline went into the line, the second (not declared) submitted it.
		assert.Eq(t, []string{"a\nb"}, lines)
	})

	t.Run("an undeclared newline submits", func(t *testing.T) {
		tm, _, _ := newPasteMemTerminal([]string{"a", "\r", "b", "\r"}, false)
		lines := []string{}
		for {
			line, err := tm.ReadLine()
			if errors.Is(err, io.EOF) {
				break
			}
			assert.NoErr(t, err)
			lines = append(lines, line)
		}
		assert.Eq(t, []string{"a", "b"}, lines)
	})

	t.Run("a declaration that meets no newline is dropped", func(t *testing.T) {
		// The 'a' takes the declaration; the newline after it is a submit again.
		tm, mem := newMem("", 0)
		mem.in = []string{"a", "\r"}
		tm.SetNewlineAsText()
		line, err := tm.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "a", line)
	})
}
