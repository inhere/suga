//go:build ptye2e

package ptye2e

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gookit/gopty/ptytest"
)

// vtScreen is the Go port of ptydrive.Screen: a minimal VT screen, enough of a
// terminal to tell where chat's lines ended up - whether the input line is still
// one line of its own - instead of only grepping the byte stream.
//
// It handles CR, LF, backspace, ESC[K (erase to the end of the line), ESC[nK
// (erase the line), ESC[2J (erase the screen), ESC[nA/B/C/D (cursor
// up/down/right/left), ESC[nG (column), ESC[n;mH (absolute row and column, what
// ConPTY repaints with) and wraps at the window width; every other
// escape (colors, titles, cursor visibility) is skipped. Rows are kept as written
// (no scrolling), and a wide character marks its two columns as one (the second
// cell stays empty).
//
// A byte-stream regexp cannot replace it: a terminal that repaints the whole row
// (ConPTY) and a terminal editor that only writes the new text (a Linux pty after
// Ctrl-C cleared the line) produce different bytes for the same screen, so the
// check has to read the screen, as the Python smokes did.
type vtScreen struct {
	width int
	// rows holds one cell per column; the second cell of a wide rune stays zero.
	rows [][]rune
	x, y int
	// pending is the deferred wrap of a real terminal: once a row is filled to the
	// width the cursor stays on its last column and only moves to the next row when
	// the following rune arrives, so a line break in between moves down a single row.
	// ConPTY writes such a row whole (exactly <width> columns) and then CRLF; without
	// the deferred wrap every one of those lines moved the model two rows down and it
	// kept text the terminal no longer showed.
	pending bool
}

var (
	// vtCSI matches one control sequence: the parameters and the final letter.
	vtCSI = regexp.MustCompile("^\x1b\\[([0-9;?]*)([A-Za-z])")
	// vtOSC matches an operating system command (a title) ended by BEL or ST.
	vtOSC = regexp.MustCompile("^\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
)

func newVTScreen(width int) *vtScreen {
	return &vtScreen{width: width, rows: [][]rune{{}}}
}

// feed applies raw pty output (ptytest's RawText) to the screen.
func (s *vtScreen) feed(data string) {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRuneInString(data[i:])
		switch r {
		case '\r':
			s.x, s.pending = 0, false
		case '\n':
			s.y, s.pending = s.y+1, false
		case '\b':
			if s.pending {
				// The cursor waits on the last column of the row: there is nothing to
				// move back to.
				s.pending = false
			} else if s.x > 0 {
				s.x--
			}
		case '\a':
			// BEL on its own: nothing on the screen.
		case '\x1b':
			i += s.escape(data[i:])
			continue
		default:
			s.put(r)
		}
		i += size
	}
}

// escape handles the escape sequence data starts with and returns its length.
func (s *vtScreen) escape(data string) int {
	if m := vtCSI.FindStringSubmatch(data); m != nil {
		s.csi(m[1], m[2])
		return len(m[0])
	}
	if loc := vtOSC.FindStringIndex(data); loc != nil {
		return loc[1]
	}
	if strings.HasPrefix(data, "\x1b[") {
		// A sequence the output was read in the middle of: skip it rather than
		// printing its bytes as text (the next feed carries it whole, since every
		// check feeds the complete output again).
		return len(data)
	}
	// A two character escape (charset, cursor save/restore, ...): skip both.
	if len(data) < 2 {
		return len(data)
	}
	_, size := utf8.DecodeRuneInString(data[1:])
	return 1 + size
}

// csi applies one control sequence: the cursor moves and the erases.
func (s *vtScreen) csi(params, final string) {
	n := csiParam(params)
	switch final {
	case "A", "B", "C", "D", "G", "H", "f":
		// A cursor move cancels a pending wrap: the terminal stops waiting to break
		// the row once the cursor is moved.
		s.pending = false
	}
	switch final {
	case "A":
		s.y = max(0, s.y-n)
	case "B":
		s.y += n
	case "C":
		s.x += n
	case "D":
		s.x = max(0, s.x-n)
	case "G":
		s.x = max(0, n-1)
	case "H", "f":
		// Absolute cursor positioning: row;column, 1 based (ConPTY repaints a row it
		// has already written with it, e.g. ESC[8;1H for the status line of a turn).
		row, col := csiRowCol(params)
		s.y, s.x = row-1, col-1
	case "K":
		s.eraseLine(params)
	case "J":
		s.eraseDisplay(n)
	}
	// Everything else (SGR colors, cursor visibility, ...) leaves the text alone.
}

// eraseLine clears the row the cursor is on: all of it for ESC[2K, up to the
// cursor for ESC[1K, and from the cursor for the plain form.
func (s *vtScreen) eraseLine(params string) {
	row := s.cells(s.y)
	switch params {
	case "2":
		row = row[:0]
	case "1":
		for i := 0; i < min(s.x+1, len(row)); i++ {
			row[i] = ' '
		}
	default:
		if s.x < len(row) {
			row = row[:s.x]
		}
	}
	s.rows[s.y] = row
}

// eraseDisplay clears everything for ESC[2J and ESC[3J.
func (s *vtScreen) eraseDisplay(n int) {
	if n != 2 && n != 3 {
		return
	}
	s.rows = [][]rune{{}}
	s.x, s.y = 0, 0
}

// cells returns row y, creating the rows up to it with empty ones.
func (s *vtScreen) cells(y int) []rune {
	for len(s.rows) <= y {
		s.rows = append(s.rows, nil)
	}
	return s.rows[y]
}

// put writes one rune at the cursor and advances it, wrapping at the width. A row
// filled to the width leaves the cursor on its last column with the wrap pending
// (see vtScreen.pending), so the line break ConPTY writes after such a row moves
// down one row instead of two.
func (s *vtScreen) put(r rune) {
	width := vtCharWidth(r)
	if s.pending {
		s.x, s.y, s.pending = 0, s.y+1, false
	}
	if width == 2 && s.width >= 2 && s.x == s.width-1 {
		s.put(' ') // a wide character never straddles the end of a row
		// The space filled the row: the wide rune starts on the next one.
		s.x, s.y, s.pending = 0, s.y+1, false
	}
	row := s.cells(s.y)
	for len(row) < s.x {
		row = append(row, ' ')
	}
	if s.x < len(row) {
		row[s.x] = r
	} else {
		row = append(row, r)
	}
	if width == 2 {
		// The second column of a wide character stays empty (its own cell).
		if len(row) <= s.x+1 {
			row = append(row, 0)
		} else {
			row[s.x+1] = 0
		}
	}
	s.rows[s.y] = row
	s.x += width
	if s.width > 0 && s.x >= s.width {
		// The cursor stays on the last column and wraps when the next rune comes.
		s.x, s.pending = s.width-1, true
	}
}

// lines returns the rows of the screen, trailing spaces removed; rows the output
// never reached are empty strings (and dropped at the end).
func (s *vtScreen) lines() []string {
	out := make([]string, 0, len(s.rows))
	for _, row := range s.rows {
		var b strings.Builder
		for _, r := range row {
			if r != 0 {
				b.WriteRune(r)
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// inputRow is ptydrive's input_row: the row the input line occupies as the
// terminal would show it - the last row of the screen starting with one of the
// prompts, trailing spaces removed, so a cleared line reads as the prompt alone
// and a line holding text reads as the prompt plus that text.
func inputRow(s *ptytest.Session, cols int) string {
	screen := newVTScreen(cols)
	screen.feed(s.RawText())
	rows := screen.lines()
	for i := len(rows) - 1; i >= 0; i-- {
		if strings.HasPrefix(rows[i], "❯") || strings.HasPrefix(rows[i], "↳") {
			return rows[i]
		}
	}
	return ""
}

// screenRows is what the terminal shows right now: the raw output fed through the
// screen model, one string per row.
func screenRows(s *ptytest.Session, cols int) []string {
	screen := newVTScreen(cols)
	screen.feed(s.RawText())
	return screen.lines()
}

// screenText is the whole screen as one string with every run of whitespace
// dropped: a line the terminal wrapped is joined again ("用户 " + "拒绝写入记忆"
// reads as 用户拒绝写入记忆) and the padding a ConPTY wrap may add does not count.
func screenText(s *ptytest.Session, cols int) string {
	return unspaced(strings.Join(screenRows(s, cols), ""))
}

// waitScreen polls the screen until pattern shows up on it - on a row, or over
// the break of a wrapped row - and reports whether it did within the timeout.
//
// A line-content check reads the screen, not the byte stream: chat's own writes
// can be split by a repaint of the input or status row, which a terminal applies
// (the row still reads whole) while the same bytes, ANSI stripped, no longer
// spell the text out.
func waitScreen(s *ptytest.Session, cols int, pattern *regexp.Regexp, timeout time.Duration) bool {
	re := regexp.MustCompile(unspaced(pattern.String()))
	end := time.Now().Add(timeout)
	for {
		if re.MatchString(screenText(s, cols)) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// untilScreen is until for a check on the text a line carries: it waits for the
// screen to show it and records the verdict as the named check (the rows are
// logged on a miss).
func untilScreen(t *testing.T, s *ptytest.Session, cols int, name, pattern string) bool {
	t.Helper()
	ok := waitScreen(s, cols, regexp.MustCompile(pattern), 15*time.Second)
	if !ok {
		t.Logf("%s: no row matched %q; rows %q", name, pattern, screenRows(s, cols))
	}
	check(t, name, ok)
	return ok
}

// waitInputRow polls the input row until it is want, and reports whether it got
// there within the timeout (a failed wait logs the row it saw).
func waitInputRow(t *testing.T, s *ptytest.Session, cols int, want string, timeout time.Duration) bool {
	t.Helper()
	end := time.Now().Add(timeout)
	for {
		if got := inputRow(s, cols); got == want {
			return true
		}
		if time.Now().After(end) {
			t.Logf("input row = %q, want %q; text tail %q", inputRow(s, cols), want, tail(s.Text()))
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// csiRowCol is the row and column an absolute cursor move carries (row;column, 1
// based): 1 for a parameter the sequence leaves out or does not spell out as a
// number.
func csiRowCol(params string) (row, col int) {
	fields := strings.Split(params, ";")
	part := func(i int) int {
		if i >= len(fields) {
			return 1
		}
		n := 0
		for _, r := range fields[i] {
			if r < '0' || r > '9' {
				return 1
			}
			n = n*10 + int(r-'0')
		}
		if n == 0 {
			return 1
		}
		return n
	}
	return part(0), part(1)
}

// csiParam is the count one control sequence carries: its decimal parameter, or 1
// when the parameters are empty or not a single number ("1;2", "?").
func csiParam(params string) int {
	if params == "" {
		return 1
	}
	n := 0
	for _, r := range params {
		if r < '0' || r > '9' {
			return 1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// vtCharWidth is the columns one rune takes: 2 for the East Asian wide and full
// width ones (the terminal's own rule), 1 for everything else.
func vtCharWidth(r rune) int {
	if r < 0x1100 {
		return 1
	}
	for _, span := range vtWide {
		if r >= span[0] && r <= span[1] {
			return 2
		}
	}
	return 1
}

// vtWide holds the East Asian wide (W) and fullwidth (F) ranges a chat line can
// carry: CJK, Hangul, Hiragana/Katakana, the fullwidth forms and the emoji.
var vtWide = [][2]rune{
	{0x1100, 0x115F}, {0x2E80, 0x303E}, {0x3041, 0x33FF}, {0x3400, 0x4DBF},
	{0x4E00, 0x9FFF}, {0xA000, 0xA4CF}, {0xA960, 0xA97F}, {0xAC00, 0xD7A3},
	{0xF900, 0xFAFF}, {0xFE10, 0xFE19}, {0xFE30, 0xFE6F}, {0xFF00, 0xFF60},
	{0xFFE0, 0xFFE6}, {0x1F300, 0x1F64F}, {0x1F900, 0x1F9FF}, {0x20000, 0x3FFFD},
}

// selfTestVTScreen checks the screen model itself against the cases
// ptydrive._self_test lists, so a broken screen fails here instead of inside a
// slow chat scenario.
func selfTestVTScreen(t *testing.T) {
	t.Helper()
	cases := []struct {
		name  string
		width int
		feed  string
		want  []string
	}{
		{"lines", 10, "hello\r\nworld", []string{"hello", "world"}},
		{"wrap", 10, "abcdefghijklm", []string{"abcdefghij", "klm"}},
		{"erase whole row", 10, "abc\r\n\x1b[2Kdef", []string{"abc", "def"}},
		{"up, column 0, erase to end", 10, "first\r\nsecond\x1b[1A\r\x1b[Kthird", []string{"third", "second"}},
		{"absolute row and column", 10, "first\r\nsecond\x1b[1;1Hthird", []string{"third", "second"}},
		{"colors, title, column move", 10, "\x1b[31mred\x1b[0m \x1b]0;suga\x07\x1b[9Gok", []string{"red     ok"}},
		{"wide rune takes two columns", 8, strings.Repeat("宽", 5), []string{"宽宽宽宽", "宽"}},
		// ConPTY writes a row it filled to the width whole and then a line break: the
		// cursor still sits on the row's last column when the break arrives, so the
		// break moves down one row and no empty row is left behind.
		{"a full row then a line break", 10, "abcdefghij\r\nklm", []string{"abcdefghij", "klm"}},
		{"a full row then a carriage return", 10, "abcdefghij\rX", []string{"Xbcdefghij"}},
		{"a wrapped row then a line break", 10, "abcdefghijklm\r\nn", []string{"abcdefghij", "klm", "n"}},
		{"wide runes fill a row then a line break", 8, strings.Repeat("宽", 4) + "\r\n宽", []string{"宽宽宽宽", "宽"}},
		// A move cancels the pending wrap instead of leaving the cursor a row ahead.
		{"backspace on a full row", 10, "abcdefghij\bX", []string{"abcdefghiX"}},
		{"cursor left on a full row", 10, "abcdefghij\x1b[3DX", []string{"abcdefXhij"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newVTScreen(c.width)
			s.feed(c.feed)
			got := s.lines()
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("lines = %q, want %q", got, c.want)
			}
		})
	}
}

// TestVTScreen keeps the screen model of the input-row checks honest.
func TestVTScreen(t *testing.T) { selfTestVTScreen(t) }
