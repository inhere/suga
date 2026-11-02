package lineedit

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// screenModel replays the bytes the editor writes on a small terminal grid
// (ASCII only): printable runes with the pending-wrap rule of xterm, CR, LF,
// cursor moves (ESC[nA/B/C/D) and erase to the end of the row (ESC[K). It is
// enough to see what is left on screen after an edit (suga-e6it).
type screenModel struct {
	width   int
	rows    map[int][]rune
	x, y    int
	pending bool
}

func newScreenModel(width int) *screenModel {
	return &screenModel{width: width, rows: map[int][]rune{}}
}

func (s *screenModel) row(y int) []rune {
	r := s.rows[y]
	for len(r) < s.width {
		r = append(r, ' ')
	}
	s.rows[y] = r
	return r
}

func (s *screenModel) feed(data []byte) {
	in := []rune(string(data))
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c == '\r':
			s.x, s.pending = 0, false
		case c == '\n':
			s.y, s.pending = s.y+1, false
		case c == keyEscape && i+1 < len(in) && in[i+1] == '[':
			j, n := i+2, 0
			for j < len(in) && in[j] >= '0' && in[j] <= '9' {
				j++
			}
			if j > i+2 {
				n, _ = strconv.Atoi(string(in[i+2 : j]))
			}
			if n == 0 {
				n = 1
			}
			s.pending = false
			switch in[j] {
			case 'A':
				s.y -= n
			case 'B':
				s.y += n
			case 'C':
				s.x = min(s.x+n, s.width-1)
			case 'D':
				s.x = max(s.x-n, 0)
			case 'K':
				r := s.row(s.y)
				for k := s.x; k < s.width; k++ {
					r[k] = ' '
				}
			}
			i = j
		default:
			if s.pending {
				s.x, s.y, s.pending = 0, s.y+1, false
			}
			s.row(s.y)[s.x] = c
			if s.x == s.width-1 {
				s.pending = true
			} else {
				s.x++
			}
		}
	}
}

// text returns the non-empty rows, trailing spaces trimmed.
func (s *screenModel) text() []string {
	var out []string
	for y := 0; y <= 20; y++ {
		if line := strings.TrimRight(string(s.rows[y]), " "); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// editScreen types text into a terminal of the given width, runs edit and
// returns what the screen shows afterwards.
func editScreen(t *testing.T, width int, text string, edit func(tm *Terminal)) []string {
	t.Helper()
	tm := NewTerminal(&bytes.Buffer{}, "> ")
	assert.NoErr(t, tm.SetSize(width, 24))
	tm.writeLine(tm.prompt)
	for _, r := range text {
		tm.handleKey(r)
	}
	edit(tm)
	screen := newScreenModel(width)
	screen.feed(tm.outBuf)
	return screen.text()
}

// TestScreenAfterMidLineNewline: a newline put into the middle of a long, wrapped
// line moves the rest of the line to the next row and leaves nothing of it behind
// on the old rows (suga-e6it: the rest showed twice).
func TestScreenAfterMidLineNewline(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz0123"
	t.Run("a newline in a wrapped line", func(t *testing.T) {
		got := editScreen(t, 20, long, func(tm *Terminal) {
			for i := 0; i < 15; i++ {
				tm.handleKey(keyLeft)
			}
			tm.newlineAsText = true
			tm.handleKey(keyLF)
			assert.Eq(t, "abcdefghijklmno\npqrstuvwxyz0123", string(tm.line))
		})
		assert.Eq(t, []string{"> abcdefghijklmno", "pqrstuvwxyz0123"}, got)
	})
	t.Run("a newline in a short line", func(t *testing.T) {
		got := editScreen(t, 40, "hello world", func(tm *Terminal) {
			for i := 0; i < 5; i++ {
				tm.handleKey(keyLeft)
			}
			tm.newlineAsText = true
			tm.handleKey(keyLF)
		})
		assert.Eq(t, []string{"> hello", "world"}, got)
	})
	t.Run("deleting the newline joins the rows again", func(t *testing.T) {
		got := editScreen(t, 40, "hello world", func(tm *Terminal) {
			for i := 0; i < 5; i++ {
				tm.handleKey(keyLeft)
			}
			tm.newlineAsText = true
			tm.handleKey(keyLF)
			tm.handleKey(keyBackspace)
		})
		assert.Eq(t, []string{"> hello world"}, got)
	})
	t.Run("deleting before a newline leaves no stale cell", func(t *testing.T) {
		got := editScreen(t, 40, "abc", func(tm *Terminal) {
			tm.newlineAsText = true
			tm.handleKey(keyLF)
			for _, r := range "def" {
				tm.handleKey(r)
			}
			for i := 0; i < 4; i++ {
				tm.handleKey(keyLeft)
			}
			tm.handleKey(keyBackspace) // removes "c"
		})
		assert.Eq(t, []string{"> ab", "def"}, got)
	})
}
