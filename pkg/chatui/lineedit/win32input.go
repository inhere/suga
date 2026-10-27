package lineedit

// win32-input-mode (DECSET 9001): a Windows console terminal that has it switched
// on (ConPTY asks for it by itself, Windows Terminal answers CSI ? 9001 h) reports
// every key press as a record instead of a byte sequence
//
//	CSI Vk ; Sc ; Uc ; Kd ; Cs ; Rc _
//
// that is virtual key code, scan code, UTF-16 code unit, key-down flag (1 down,
// 0 up), control-key state mask and repeat count, each in decimal. It is what
// makes Ctrl+Enter expressible at all: the combination has Uc=10, exactly the byte
// Ctrl+J sends, so a terminal without the mode cannot tell the two apart (Windows
// Terminal binds Alt+Enter to fullscreen by default and has no default Ctrl+Enter
// binding).
//
// Two properties of the records decide the shape of the parser:
//   - every press arrives twice, once with Kd=1 and once with Kd=0, so a key-up
//     record stands for no key at all and is skipped;
//   - a held key arrives as one record with Rc>1, which stands for that many
//     presses.
//
// The mode is opt-in and the parser follows the switch (SetWin32InputMode): while
// it is on, readLine parses a record before falling back to the byte parser, so a
// terminal that ignores DECSET 9001 - or a byte sequence that is not a record, a
// bracketed-paste marker above all - keeps taking the old path unchanged.

import (
	"unicode/utf8"
)

// win32ModeOn and win32ModeOff switch the mode on the terminal (DECSET 9001).
var (
	win32ModeOn  = []byte{keyEscape, '[', '?', '9', '0', '0', '1', 'h'}
	win32ModeOff = []byte{keyEscape, '[', '?', '9', '0', '0', '1', 'l'}
)

// Virtual key codes of the records mapped below (the Vk field; Win32
// VK_* constants).
const (
	win32VKBack   = 0x08
	win32VKTab    = 0x09
	win32VKReturn = 0x0d
	win32VKEscape = 0x1b
	win32VKEnd    = 0x23
	win32VKHome   = 0x24
	win32VKLeft   = 0x25
	win32VKUp     = 0x26
	win32VKRight  = 0x27
	win32VKDown   = 0x28
	win32VKDelete = 0x2e
)

// Control-key state bits of the Cs field (Win32
// RIGHT_ALT/LEFT_ALT/RIGHT_CTRL/LEFT_CTRL/SHIFT_PRESSED).
const (
	win32RightAlt  = 0x1
	win32LeftAlt   = 0x2
	win32RightCtrl = 0x4
	win32LeftCtrl  = 0x8
	win32Shift     = 0x10
)

// win32RecordState is what parseWin32Record found at the start of the input.
type win32RecordState uint8

const (
	// win32NotRecord: the bytes are not the start of a record (a byte sequence, a
	// paste marker, plain text): the byte parser reads them.
	win32NotRecord win32RecordState = iota
	// win32Incomplete: a record starts here but the input ends inside it; like any
	// other partial sequence it waits for the next read.
	win32Incomplete
	// win32Complete: rec holds the whole record and rest the bytes after it.
	win32Complete
)

// win32Record is one parsed record: the six decimal fields, in order.
type win32Record struct {
	vk, sc, uc, kd, cs, rc int
}

// SetWin32InputMode switches the terminal's win32-input-mode (DECSET 9001) on or
// off and makes the line editor read its records while it is on. It is written
// where bracketed paste is written - around a read that holds raw mode - so the
// terminal goes back to plain key bytes whenever chat leaves the line editor
// (a turn, a local command, the end of chat); a terminal that does not know the
// sequence ignores it, and the byte parser still reads its keys.
func (t *Terminal) SetWin32InputMode(on bool) {
	if on {
		_, _ = t.c.Write(win32ModeOn)
	} else {
		_, _ = t.c.Write(win32ModeOff)
	}
	t.lock.Lock()
	defer t.lock.Unlock()

	t.win32Input = on
	// A record cut in half by the switch or a restarted read is not continued.
	t.win32Repeat = nil
	t.win32High = 0
}

// win32Key reads the next key press of win32-input-mode from *rest. It returns
// utf8.RuneError with *rest unchanged when the input holds no complete key yet
// (the caller reads more), keyIgnored when a record carried no key (a key-up),
// and otherwise the key with the bytes it was read from cut off *rest. Anything
// that is not a record takes the byte parser, bracketed-paste markers included;
// a record inside a paste (a terminal that turns pasted text into key records as
// well) is read like any other, and handleKey inserts its character as text
// because the paste is active (paste.go).
// t.lock must be held.
func (t *Terminal) win32Key(rest *[]byte) rune {
	b := *rest
	if len(b) == 0 {
		// A partial sequence like any other.
		key, after := bytesToKey(b, t.pasteActive)
		*rest = after
		return key
	}
	rec, after, state := parseWin32Record(b)
	switch state {
	case win32Incomplete:
		return utf8.RuneError
	case win32NotRecord:
		// Bytes, not a record: the byte parser reads them, text of a paste and the
		// bracketed-paste markers included.
		key, consumed := bytesToKey(b, t.pasteActive)
		*rest = consumed
		return key
	}
	*rest = after
	if rec.kd != 1 {
		// Kd=0 is the key going up again, never a key press.
		return keyIgnored
	}
	key, ok := win32KeyOf(rec, &t.win32High)
	if !ok {
		// A record nothing maps: its bytes go to the byte parser, which either knows
		// them or drops the key (keyUnknown) - the mode must not swallow a key it
		// does not understand.
		if key, rem := bytesToKey(after, false); key != utf8.RuneError {
			*rest = rem
			return key
		}
		return keyUnknown
	}
	if rec.rc > 1 && key != keyIgnored {
		// A held key: the record stands for rc presses, this one and the rest, which
		// the next loop turns of readLine hand over one by one.
		t.win32Repeat = make([]rune, rec.rc-1)
		for i := range t.win32Repeat {
			t.win32Repeat[i] = key
		}
	}
	return key
}

// win32KeyOf maps one key-down record to the key press it stands for. ok is false
// for a record the editor has no key for. high carries the first half of a UTF-16
// surrogate pair: the terminal reports such a character (an emoji) as two records,
// one code unit each, which are joined here into the single rune.
func win32KeyOf(rec win32Record, high *rune) (rune, bool) {
	ctrl := rec.cs&(win32LeftCtrl|win32RightCtrl) != 0
	alt := rec.cs&(win32LeftAlt|win32RightAlt) != 0
	shift := rec.cs&win32Shift != 0
	switch rec.vk {
	case win32VKReturn:
		if ctrl || alt || shift {
			// Ctrl+Enter, Alt+Enter and Shift+Enter put a newline into the input line
			// instead of submitting it (paste.go's setNewlineAsText does the same for
			// a terminal without the mode).
			return keyNewline, true
		}
		return keyEnter, true
	case win32VKBack:
		return keyBackspace, true
	case win32VKTab:
		// Tab, Shift+Tab and Ctrl+Tab have no meaning of their own in the editor;
		// the rune is what AutoCompleteCallback sees (chat's completion).
		return '\t', true
	case win32VKEscape:
		return keyEscape, true
	case win32VKHome:
		return keyHome, true
	case win32VKEnd:
		return keyEnd, true
	case win32VKLeft:
		if alt {
			return keyAltLeft, true
		}
		return keyLeft, true
	case win32VKRight:
		if alt {
			return keyAltRight, true
		}
		return keyRight, true
	case win32VKUp:
		return keyUp, true
	case win32VKDown:
		return keyDown, true
	case win32VKDelete:
		// Delete erases the character under the cursor, which is the editor's
		// Ctrl-D (the byte path never saw this key: ESC [ 3 ~ was dropped).
		return keyCtrlD, true
	}
	// Ctrl+<letter> is the control byte the byte path would have got (Ctrl+A = 0x01)
	// and goes through the same mapping, so ^A is Home, ^W deletes a word and ^C ends
	// the read exactly as before.
	if ctrl && !alt && rec.vk >= 'A' && rec.vk <= 'Z' {
		b := byte(rec.vk - 'A' + 1)
		if b == 10 {
			// Ctrl+J: a newline inside the input. Windows Terminal has no other use
			// for the key, and it is what a Windows console always offered for a line
			// break (internal/chat's ctrlJIsInputNewline on the byte path).
			return keyNewline, true
		}
		if key, ok := ctrlByteToKey(b); ok {
			return key, true
		}
		return rune(b), true
	}
	if alt && rec.vk == 'V' {
		// Alt+V: chat's clipboard paste of a picture (keyAltV).
		return keyAltV, true
	}
	if rec.uc == 0 {
		// No character: a modifier pressed on its own, CapsLock and the like.
		return keyIgnored, true
	}
	uc := rune(rec.uc)
	switch {
	case uc >= 0xd800 && uc <= 0xdbff:
		// First half of a surrogate pair: the second record completes it.
		*high = uc
		return keyIgnored, true
	case uc >= 0xdc00 && uc <= 0xdfff:
		if *high == 0 {
			return keyIgnored, true
		}
		paired := *high
		*high = 0
		return 0x10000 + (paired-0xd800)<<10 + (uc - 0xdc00), true
	}
	// A character that is not a low surrogate ends a dangling pair: the terminal
	// never reported the second half (a dropped record), so it must not pair with a
	// low surrogate that arrives later.
	*high = 0
	return uc, true
}

// parseWin32Record reads one record at the start of b. A record is
// "ESC [" followed by digits and semicolons, terminated by '_'; anything else is
// not a record (win32NotRecord) and b ending inside one is win32Incomplete.
func parseWin32Record(b []byte) (rec win32Record, rest []byte, state win32RecordState) {
	if len(b) < 2 || b[0] != keyEscape || b[1] != '[' {
		return win32Record{}, b, win32NotRecord
	}
	fields := [6]int{}
	field, seen := 0, false
	for i := 2; i < len(b); i++ {
		c := b[i]
		switch {
		case c >= '0' && c <= '9':
			fields[field] = fields[field]*10 + int(c-'0')
			seen = true
		case c == ';':
			if field == len(fields)-1 {
				// More fields than a record has: not a record.
				return win32Record{}, b, win32NotRecord
			}
			field++
		case c == '_':
			if !seen {
				return win32Record{}, b, win32NotRecord
			}
			return win32Record{vk: fields[0], sc: fields[1], uc: fields[2],
				kd: fields[3], cs: fields[4], rc: fields[5]}, b[i+1:], win32Complete
		default:
			return win32Record{}, b, win32NotRecord
		}
	}
	return win32Record{}, b, win32Incomplete
}
