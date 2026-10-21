package lineedit

// Tests of win32-input-mode (win32input.go): the record parser, and the keys a
// record produces through the real key path (ReadLine over the in-memory
// terminal of terminal_test.go).

import (
	"fmt"
	"testing"
	"unicode/utf16"

	"github.com/gookit/goutil/x/assert"
)

// win32RecordBytes is one win32-input-mode record as the terminal sends it.
func win32RecordBytes(vk, sc, uc, kd, cs, rc int) string {
	return fmt.Sprintf("\x1b[%d;%d;%d;%d;%d;%d_", vk, sc, uc, kd, cs, rc)
}

// win32KeyRecord is the record of one key press (Vk and Uc given) with the given
// control-key state, down once and up once - the way a terminal reports a key.
func win32KeyRecord(vk, uc, cs int) string {
	return win32RecordBytes(vk, 0, uc, 1, cs, 1) + win32RecordBytes(vk, 0, uc, 0, cs, 1)
}

// win32Text is the records of plain typed text (no modifier), one per UTF-16 code
// unit: a character outside the BMP arrives as two records.
func win32Text(s string) string {
	var out string
	for _, unit := range utf16.Encode([]rune(s)) {
		out += win32KeyRecord(0, int(unit), 0)
	}
	return out
}

// win32Enter is Enter, Ctrl+Enter and the other line-break combinations.
var (
	win32Enter      = win32KeyRecord(win32VKReturn, 10, 0)
	win32CtrlEnter  = win32KeyRecord(win32VKReturn, 10, win32LeftCtrl)
	win32ShiftEnter = win32KeyRecord(win32VKReturn, 10, win32Shift)
	win32AltEnter   = win32KeyRecord(win32VKReturn, 10, win32LeftAlt)
)

// win32Ctrl is Ctrl+<letter> (its control byte is what Uc carries).
func win32Ctrl(letter rune) string {
	return win32KeyRecord(int(letter), int(letter-'A'+1), win32LeftCtrl)
}

// TestSetWin32InputMode checks the DECSET 9001 sequence the mode writes and that
// the parser follows the switch.
func TestSetWin32InputMode(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	assert.Eq(t, "\x1b[?9001h", mem.out.String())
	assert.True(t, tm.win32Input)
	tm.outBuf = nil
	mem.out.Reset()
	tm.SetWin32InputMode(false)
	assert.Eq(t, "\x1b[?9001l", mem.out.String())
	assert.False(t, tm.win32Input)
}

// TestWin32KeyOf maps records to keys: the table of what the parser understands.
func TestWin32KeyOf(t *testing.T) {
	cases := []struct {
		name string
		rec  win32Record
		want rune
		ok   bool
	}{
		{name: "enter", rec: win32Record{vk: win32VKReturn, uc: 13, kd: 1, rc: 1}, want: keyEnter, ok: true},
		{name: "ctrl+enter is a newline", rec: win32Record{vk: win32VKReturn, uc: 10, kd: 1, cs: win32LeftCtrl, rc: 1}, want: keyNewline, ok: true},
		{name: "ctrl+enter right ctrl", rec: win32Record{vk: win32VKReturn, uc: 10, kd: 1, cs: win32RightCtrl, rc: 1}, want: keyNewline, ok: true},
		{name: "shift+enter is a newline", rec: win32Record{vk: win32VKReturn, uc: 10, kd: 1, cs: win32Shift, rc: 1}, want: keyNewline, ok: true},
		{name: "alt+enter is a newline", rec: win32Record{vk: win32VKReturn, uc: 10, kd: 1, cs: win32LeftAlt, rc: 1}, want: keyNewline, ok: true},
		{name: "backspace", rec: win32Record{vk: win32VKBack, kd: 1, rc: 1}, want: keyBackspace, ok: true},
		{name: "tab", rec: win32Record{vk: win32VKTab, uc: 9, kd: 1, rc: 1}, want: '\t', ok: true},
		{name: "shift+tab", rec: win32Record{vk: win32VKTab, uc: 9, kd: 1, cs: win32Shift, rc: 1}, want: '\t', ok: true},
		{name: "escape", rec: win32Record{vk: win32VKEscape, uc: 27, kd: 1, rc: 1}, want: keyEscape, ok: true},
		{name: "home", rec: win32Record{vk: win32VKHome, kd: 1, rc: 1}, want: keyHome, ok: true},
		{name: "end", rec: win32Record{vk: win32VKEnd, kd: 1, rc: 1}, want: keyEnd, ok: true},
		{name: "left", rec: win32Record{vk: win32VKLeft, kd: 1, rc: 1}, want: keyLeft, ok: true},
		{name: "right", rec: win32Record{vk: win32VKRight, kd: 1, rc: 1}, want: keyRight, ok: true},
		{name: "up", rec: win32Record{vk: win32VKUp, kd: 1, rc: 1}, want: keyUp, ok: true},
		{name: "down", rec: win32Record{vk: win32VKDown, kd: 1, rc: 1}, want: keyDown, ok: true},
		{name: "alt+left", rec: win32Record{vk: win32VKLeft, kd: 1, cs: win32LeftAlt, rc: 1}, want: keyAltLeft, ok: true},
		{name: "alt+right", rec: win32Record{vk: win32VKRight, kd: 1, cs: win32RightAlt, rc: 1}, want: keyAltRight, ok: true},
		{name: "delete", rec: win32Record{vk: win32VKDelete, kd: 1, rc: 1}, want: keyCtrlD, ok: true},
		{name: "ctrl+a", rec: win32Record{vk: 'A', uc: 1, kd: 1, cs: win32LeftCtrl, rc: 1}, want: keyHome, ok: true},
		{name: "ctrl+e", rec: win32Record{vk: 'E', uc: 5, kd: 1, cs: win32LeftCtrl, rc: 1}, want: keyEnd, ok: true},
		{name: "ctrl+w", rec: win32Record{vk: 'W', uc: 23, kd: 1, cs: win32LeftCtrl, rc: 1}, want: keyDeleteWord, ok: true},
		{name: "ctrl+u", rec: win32Record{vk: 'U', uc: 21, kd: 1, cs: win32LeftCtrl, rc: 1}, want: keyCtrlU, ok: true},
		{name: "ctrl+c", rec: win32Record{vk: 'C', uc: 3, kd: 1, cs: win32LeftCtrl, rc: 1}, want: keyCtrlC, ok: true},
		{name: "ctrl+d", rec: win32Record{vk: 'D', uc: 4, kd: 1, cs: win32LeftCtrl, rc: 1}, want: keyCtrlD, ok: true},
		{name: "ctrl+j is a newline", rec: win32Record{vk: 'J', uc: 10, kd: 1, cs: win32LeftCtrl, rc: 1}, want: keyNewline, ok: true},
		{name: "alt+v", rec: win32Record{vk: 'V', uc: 118, kd: 1, cs: win32LeftAlt, rc: 1}, want: keyAltV, ok: true},
		{name: "plain letter", rec: win32Record{vk: 'A', uc: 97, kd: 1, rc: 1}, want: 'a', ok: true},
		{name: "CJK", rec: win32Record{vk: 0, uc: '你', kd: 1, rc: 1}, want: '你', ok: true},
		{name: "modifier alone", rec: win32Record{vk: 0x10, uc: 0, kd: 1, cs: win32Shift, rc: 1}, want: keyIgnored, ok: true},
		{name: "shift+ctrl keeps the control byte", rec: win32Record{vk: 'Q', uc: 17, kd: 1, cs: win32Shift | win32LeftCtrl, rc: 1}, want: 17, ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var high rune
			key, ok := win32KeyOf(tc.rec, &high)
			assert.Eq(t, tc.ok, ok)
			if tc.ok {
				assert.Eq(t, tc.want, key)
			}
		})
	}
}

// TestWin32SurrogatePair joins the two records of a character outside the BMP.
func TestWin32SurrogatePair(t *testing.T) {
	var high rune
	key, ok := win32KeyOf(win32Record{vk: 0, uc: 0xd83d, kd: 1, rc: 1}, &high)
	assert.True(t, ok)
	assert.Eq(t, rune(keyIgnored), key)
	assert.Eq(t, rune(0xd83d), high)
	key, ok = win32KeyOf(win32Record{vk: 0, uc: 0xde00, kd: 1, rc: 1}, &high)
	assert.True(t, ok)
	assert.Eq(t, '😀', key)
	assert.Eq(t, rune(0), high)
	// A low half without its first half is no character at all.
	key, ok = win32KeyOf(win32Record{vk: 0, uc: 0xde00, kd: 1, rc: 1}, &high)
	assert.True(t, ok)
	assert.Eq(t, rune(keyIgnored), key)
	// A dangling first half must not pair with a low half that arrives much later.
	high = 0
	key, ok = win32KeyOf(win32Record{vk: 0, uc: 0xd83d, kd: 1, rc: 1}, &high)
	assert.True(t, ok)
	assert.Eq(t, rune(keyIgnored), key)
	key, ok = win32KeyOf(win32Record{vk: 0, uc: 'a', kd: 1, rc: 1}, &high)
	assert.True(t, ok)
	assert.Eq(t, 'a', key)
	assert.Eq(t, rune(0), high)
	key, ok = win32KeyOf(win32Record{vk: 0, uc: 0xde00, kd: 1, rc: 1}, &high)
	assert.True(t, ok)
	assert.Eq(t, rune(keyIgnored), key)
}

// TestWin32TypedText types CJK text, an emoji (two records) and ASCII through the
// line editor and submits it with Enter.
func TestWin32TypedText(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	lines := readAll(t, tm, mem, win32Text("你好😀ab")+win32Enter)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "你好😀ab", lines[0])
}

// TestWin32EditingKeys presses the editing keys of the table over real input.
func TestWin32EditingKeys(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "backspace", input: win32Text("abc") + win32KeyRecord(win32VKBack, 8, 0) + win32Enter, want: "ab"},
		{name: "left", input: win32Text("ab") + win32KeyRecord(win32VKLeft, 0, 0) + win32Text("X") + win32Enter, want: "aXb"},
		{name: "right", input: win32Text("ab") + win32KeyRecord(win32VKLeft, 0, 0) + win32KeyRecord(win32VKRight, 0, 0) + win32Text("X") + win32Enter, want: "abX"},
		{name: "home", input: win32Text("ab") + win32KeyRecord(win32VKHome, 0, 0) + win32Text("X") + win32Enter, want: "Xab"},
		{name: "end", input: win32Text("ab") + win32KeyRecord(win32VKHome, 0, 0) + win32KeyRecord(win32VKEnd, 0, 0) + win32Text("X") + win32Enter, want: "abX"},
		{name: "ctrl+a then type", input: win32Text("ab") + win32Ctrl('A') + win32Text("X") + win32Enter, want: "Xab"},
		{name: "ctrl+e then type", input: win32Text("ab") + win32Ctrl('A') + win32Ctrl('E') + win32Text("X") + win32Enter, want: "abX"},
		{name: "delete", input: win32Text("abc") + win32KeyRecord(win32VKHome, 0, 0) + win32KeyRecord(win32VKDelete, 0, 0) + win32Enter, want: "bc"},
		{name: "ctrl+w", input: win32Text("foo bar") + win32Ctrl('W') + win32Enter, want: "foo "},
		{name: "ctrl+u", input: win32Text("foo bar") + win32Ctrl('U') + win32Enter, want: ""},
		{name: "alt+left moves a word", input: win32Text("foo bar") + win32KeyRecord(win32VKLeft, 0, win32LeftAlt) + win32Text("X") + win32Enter, want: "foo Xbar"},
		{name: "shift+enter is no submit", input: win32Text("a") + win32ShiftEnter + win32Text("b") + win32Enter, want: "a\nb"},
		{name: "ctrl+enter is no submit", input: win32Text("a") + win32CtrlEnter + win32Text("b") + win32Enter, want: "a\nb"},
		{name: "alt+enter is no submit", input: win32Text("a") + win32AltEnter + win32Text("b") + win32Enter, want: "a\nb"},
		{name: "ctrl+j is a newline", input: win32Text("a") + win32Ctrl('J') + win32Text("b") + win32Enter, want: "a\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tm, mem := newMem("> ", 0)
			tm.SetWin32InputMode(true)
			lines := readAll(t, tm, mem, tc.input)
			assert.Eq(t, 1, len(lines))
			assert.Eq(t, tc.want, lines[0])
		})
	}
}

// TestWin32CtrlEnterNotSubmit is the reverse-verification case on its own: a
// Ctrl+Enter record must never end the read, while Enter must.
func TestWin32CtrlEnterNotSubmit(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	mem.in = append(mem.in, win32Text("a")+win32CtrlEnter+win32Enter)
	line, err := tm.ReadLine()
	assert.NoErr(t, err)
	assert.Eq(t, "a\n", line)
	// Nothing but the Enter ended the read: the input is exhausted now.
	_, err = tm.ReadLine()
	assert.Err(t, err)
}

// TestWin32KeyUpIgnored checks that the key-up half of a press is no key.
func TestWin32KeyUpIgnored(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	// Down, up, up again: one 'a'.
	input := win32RecordBytes('A', 0, 97, 1, 0, 1) +
		win32RecordBytes('A', 0, 97, 0, 0, 1) +
		win32RecordBytes('A', 0, 97, 0, 0, 1) + win32Enter
	lines := readAll(t, tm, mem, input)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "a", lines[0])
}

// TestWin32RepeatCount expands a held key (Rc>1) into that many presses.
func TestWin32RepeatCount(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	input := win32RecordBytes('A', 0, 97, 1, 0, 3) + win32RecordBytes('A', 0, 97, 0, 0, 3) + win32Enter
	lines := readAll(t, tm, mem, input)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "aaa", lines[0])

	// The same for a key with an effect of its own.
	tm, mem = newMem("> ", 0)
	tm.SetWin32InputMode(true)
	input = win32Text("abcd") + win32RecordBytes(win32VKBack, 0, 8, 1, 0, 3) +
		win32RecordBytes(win32VKBack, 0, 8, 0, 0, 3) + win32Enter
	lines = readAll(t, tm, mem, input)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "a", lines[0])
}

// TestWin32TruncatedRecord hands the records over one chunk boundary at a time,
// the way a terminal read cuts them: the key must survive the split.
func TestWin32TruncatedRecord(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	mem.in = append(mem.in, "\x1b[13;28;", "10;1;8;", "1_", win32Text("b"), win32Enter)
	line, err := tm.ReadLine()
	assert.NoErr(t, err)
	assert.Eq(t, "\nb", line)
	// A lone ESC is no complete record: it waits instead of ending the read.
	tm, mem = newMem("> ", 0)
	tm.SetWin32InputMode(true)
	mem.in = append(mem.in, "\x1b[", "13;28;10;1;8;1_", win32Enter)
	line, err = tm.ReadLine()
	assert.NoErr(t, err)
	assert.Eq(t, "\n", line)
}

// TestWin32FallbackPaths feeds what is not a record: plain text and a legacy
// sequence keep the byte path, an unknown record is dropped instead of stalling.
func TestWin32FallbackPaths(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	// Bytes, not records (a terminal that ignored DECSET 9001), a legacy arrow
	// sequence, an unknown record, then Enter as a record.
	input := "ab" + "\x1b[D" + "X" + win32RecordBytes(0x70, 0, 0, 1, 0, 1) + "Y" + win32Enter
	lines := readAll(t, tm, mem, input)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "aXYb", lines[0])
}

// TestWin32BracketedPaste keeps a pasted multi-line text as text: the paste
// markers of DECSET 2004 still take the byte path under DECSET 9001.
func TestWin32BracketedPaste(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	input := string(pasteStart) + "line1\nline2" + string(pasteEnd) + win32Enter
	lines := readAll(t, tm, mem, input)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "line1\nline2", lines[0])
}

// TestWin32PasteAsRecords reads a paste whose text a terminal turned into key
// records as well: the characters and the pasted line break are still text, and
// only the Enter after the paste submits.
func TestWin32PasteAsRecords(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	input := string(pasteStart) + win32Text("x") + win32Enter + win32Text("y") +
		string(pasteEnd) + win32Enter
	lines := readAll(t, tm, mem, input)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "x\ny", lines[0])
}

// TestWin32EscapeKey reports a lone Esc as the key press it is in this mode: the
// record carries it, so nothing has to wait for a follow-up byte.
func TestWin32EscapeKey(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	pressed := 0
	tm.EscCallback = func() { pressed++ }
	lines := readAll(t, tm, mem, win32KeyRecord(win32VKEscape, 27, 0)+win32Enter)
	assert.Eq(t, 1, pressed)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "", lines[0])
}

// TestWin32TabReachesCompletion checks that Tab is handed to the completion
// callback, which is what completion keys on.
func TestWin32TabReachesCompletion(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	var seen rune
	tm.AutoCompleteCallback = func(line string, pos int, key rune) (string, int, bool) {
		seen = key
		return line + "!", pos + 1, true
	}
	lines := readAll(t, tm, mem, win32KeyRecord(win32VKTab, 9, 0)+win32Enter)
	assert.Eq(t, '\t', seen)
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "!", lines[0])
}

// TestWin32ModeOffKeepsBytes checks that leaving the mode (a turn, a local
// command) puts the byte path back: the same bytes then mean what they always did.
func TestWin32ModeOffKeepsBytes(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.SetWin32InputMode(true)
	tm.SetWin32InputMode(false)
	// On the byte path Ctrl+Enter does not exist and a CR submits.
	lines := readAll(t, tm, mem, "ab\r")
	assert.Eq(t, 1, len(lines))
	assert.Eq(t, "ab", lines[0])
}
