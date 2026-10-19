// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Key parsing of the line editor: the escape codes it writes and the key
// sequences it recognises. readLine (terminal.go) feeds bytes to bytesToKey,
// which returns one key, and handleKey acts on that key.

package lineedit

import (
	"bytes"
	"unicode/utf8"
)

// EscapeCodes contains escape sequences that can be written to the terminal in
// order to achieve different styles of text.
type EscapeCodes struct {
	// Foreground colors
	Black, Red, Green, Yellow, Blue, Magenta, Cyan, White []byte

	// Reset all attributes
	Reset []byte
}

var vt100EscapeCodes = EscapeCodes{
	Black:   []byte{keyEscape, '[', '3', '0', 'm'},
	Red:     []byte{keyEscape, '[', '3', '1', 'm'},
	Green:   []byte{keyEscape, '[', '3', '2', 'm'},
	Yellow:  []byte{keyEscape, '[', '3', '3', 'm'},
	Blue:    []byte{keyEscape, '[', '3', '4', 'm'},
	Magenta: []byte{keyEscape, '[', '3', '5', 'm'},
	Cyan:    []byte{keyEscape, '[', '3', '6', 'm'},
	White:   []byte{keyEscape, '[', '3', '7', 'm'},

	Reset: []byte{keyEscape, '[', '0', 'm'},
}

const (
	keyCtrlC = 3
	keyCtrlD = 4
	keyCtrlU = 21
	keyCtrlR = 18
	keyCtrlG = 7
	keyEnter = '\r'
	// keyLF is Enter as well: a terminal hands Enter over as LF when the line was
	// typed in cooked mode (a line buffered before the prompt: ICRNL on Unix, or
	// the Windows console's CRLF), and x/term takes a bare LF as Enter too.
	keyLF        = '\n'
	keyEscape    = 27
	keyBackspace = 127
	keyUnknown   = 0xd800 /* UTF-16 surrogate area */ + iota
	keyUp
	keyDown
	keyLeft
	keyRight
	keyAltLeft
	keyAltRight
	keyAltV
	keyHome
	keyEnd
	keyDeleteWord
	keyDeleteLine
	keyClearScreen
	keyPasteStart
	keyPasteEnd
)

var (
	crlf       = []byte{'\r', '\n'}
	pasteStart = []byte{keyEscape, '[', '2', '0', '0', '~'}
	pasteEnd   = []byte{keyEscape, '[', '2', '0', '1', '~'}
)

// bytesToKey tries to parse a key sequence from b. If successful, it returns
// the key and the remainder of the input. Otherwise it returns utf8.RuneError.
func bytesToKey(b []byte, pasteActive bool) (rune, []byte) {
	if len(b) == 0 {
		return utf8.RuneError, nil
	}

	if !pasteActive {
		switch b[0] {
		case 1: // ^A
			return keyHome, b[1:]
		case 2: // ^B
			return keyLeft, b[1:]
		case 5: // ^E
			return keyEnd, b[1:]
		case 6: // ^F
			return keyRight, b[1:]
		case 8: // ^H
			return keyBackspace, b[1:]
		case 11: // ^K
			return keyDeleteLine, b[1:]
		case 12: // ^L
			return keyClearScreen, b[1:]
		case 23: // ^W
			return keyDeleteWord, b[1:]
		case 14: // ^N
			return keyDown, b[1:]
		case 16: // ^P
			return keyUp, b[1:]
		}
	}

	if b[0] != keyEscape {
		if !utf8.FullRune(b) {
			return utf8.RuneError, b
		}
		r, l := utf8.DecodeRune(b)
		return r, b[l:]
	}

	if !pasteActive && len(b) >= 3 && b[0] == keyEscape && b[1] == '[' {
		switch b[2] {
		case 'A':
			return keyUp, b[3:]
		case 'B':
			return keyDown, b[3:]
		case 'C':
			return keyRight, b[3:]
		case 'D':
			return keyLeft, b[3:]
		case 'H':
			return keyHome, b[3:]
		case 'F':
			return keyEnd, b[3:]
		}
	}

	if !pasteActive && len(b) >= 2 && b[0] == keyEscape && b[1] == 'v' {
		// Alt+V (ESC v): chat's clipboard paste of a picture; Ctrl+V stays with the
		// terminal's own text paste, which never reaches this editor.
		return keyAltV, b[2:]
	}

	if !pasteActive && len(b) >= 6 && b[0] == keyEscape && b[1] == '[' && b[2] == '1' && b[3] == ';' && b[4] == '3' {
		switch b[5] {
		case 'C':
			return keyAltRight, b[6:]
		case 'D':
			return keyAltLeft, b[6:]
		}
	}

	if !pasteActive && len(b) >= 6 && bytes.Equal(b[:6], pasteStart) {
		return keyPasteStart, b[6:]
	}

	if pasteActive && len(b) >= 6 && bytes.Equal(b[:6], pasteEnd) {
		return keyPasteEnd, b[6:]
	}

	// If we get here then we have a key that we don't recognise, or a
	// partial sequence. It's not clear how one should find the end of a
	// sequence without knowing them all, but it seems that [a-zA-Z~] only
	// appears at the end of a sequence.
	for i, c := range b[0:] {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '~' {
			return keyUnknown, b[i+1:]
		}
	}

	return utf8.RuneError, b
}

func isPrintable(key rune) bool {
	isInSurrogateArea := key >= 0xd800 && key <= 0xdbff
	return key >= 32 && !isInSurrogateArea
}

// handleKey processes the given key and, optionally, returns a line of text
// that the user has entered.
func (t *Terminal) handleKey(key rune) (line string, ok bool) {
	// A newline the caller declared as pasted text (paste.go) is handled like one
	// inside a paste; the declaration is for this key only, so it goes away even
	// when another key follows it.
	declaredNewline := t.newlineAsText
	t.newlineAsText = false
	if t.pasteActive {
		if key == keyEnter || key == keyLF {
			// Text of the paste, not a submit: the line stays on screen and the
			// person's own Enter (with nothing following it) ends the read.
			t.addKeyToLine('\n')
			return
		}
		t.addKeyToLine(key)
		return
	}
	if declaredNewline && (key == keyEnter || key == keyLF) {
		t.addKeyToLine('\n')
		return
	}

	switch key {
	case keyBackspace:
		if t.reverseActive {
			t.reverseSearchBackspace()
			return
		}
		if t.pos == 0 {
			return
		}
		t.eraseNPreviousChars(t.prevClusterLen())
	case keyAltLeft:
		// move left by a word.
		t.pos -= t.countToLeftWord()
		t.moveCursorToPos(t.pos)
	case keyAltV:
		// Clipboard paste of a picture (design 0.3): the callback reads the
		// clipboard and prints its note above the input line; the line itself is
		// unchanged. The lock goes away like for WithdrawCallback, because the
		// callback writes to the terminal.
		if t.PasteImageCallback != nil {
			t.lock.Unlock()
			t.PasteImageCallback()
			t.lock.Lock()
		}
	case keyAltRight:
		// move right by a word.
		t.pos += t.countToRightWord()
		t.moveCursorToPos(t.pos)
	case keyLeft:
		if t.pos == 0 {
			return
		}
		t.pos -= t.prevClusterLen()
		t.moveCursorToPos(t.pos)
	case keyRight:
		if t.pos == len(t.line) {
			return
		}
		t.pos += t.nextClusterLen()
		t.moveCursorToPos(t.pos)
	case keyHome:
		if t.pos == 0 {
			return
		}
		t.pos = 0
		t.moveCursorToPos(t.pos)
	case keyEnd:
		if t.pos == len(t.line) {
			return
		}
		t.pos = len(t.line)
		t.moveCursorToPos(t.pos)
	case keyUp:
		// An empty line lets the caller fill one in (WithdrawCallback): chat takes a
		// message still waiting in the running turn back into it. The callback runs
		// with the lock released, like AutoCompleteCallback, since it writes to the
		// terminal itself (the withdrawn message is shown above the line).
		if len(t.line) == 0 && t.WithdrawCallback != nil {
			t.lock.Unlock()
			text, filled := t.WithdrawCallback()
			t.lock.Lock()
			if filled && text != "" {
				runes := []rune(text)
				t.setLine(runes, len(runes))
				return
			}
		}
		if t.multiLineInput() && t.moveCursorVertically(-1) {
			// Inside a multi-line input Up moves the cursor up; on its first row it falls
			// through to the history, like a single-line input (the history keeps the
			// line as it was, so Down brings the whole input back).
			return
		}
		entry, ok := t.history.NthPreviousEntry(t.historyIndex + 1)
		if !ok {
			return "", false
		}
		if t.historyIndex == -1 {
			t.historyPending = string(t.line)
		}
		t.historyIndex++
		runes := []rune(entry)
		t.setLine(runes, len(runes))
	case keyDown:
		if t.multiLineInput() && t.moveCursorVertically(1) {
			// Symmetrical to Up: from the last row Down falls through to the history.
			return
		}
		switch t.historyIndex {
		case -1:
			return
		case 0:
			runes := []rune(t.historyPending)
			t.setLine(runes, len(runes))
			t.historyIndex--
		default:
			entry, ok := t.history.NthPreviousEntry(t.historyIndex - 1)
			if ok {
				t.historyIndex--
				runes := []rune(entry)
				t.setLine(runes, len(runes))
			}
		}
	case keyCtrlR:
		t.reverseSearch()
	case keyCtrlG:
		t.cancelReverseSearch()
	case keyEnter, keyLF:
		t.endReverseSearch()
		// A submitted line stays on screen, repainted on the input style's
		// background when one is set (highlight.go).
		if !t.repaintSubmittedLine() {
			t.moveCursorToPos(len(t.line))
		}
		t.queue([]rune("\r\n"))
		line = string(t.line)
		ok = true
		t.line = t.line[:0]
		t.pos = 0
		t.cursorX = 0
		t.cursorY = 0
		t.maxLine = 0
	case keyDeleteWord:
		// Delete zero or more spaces and then one or more characters.
		t.eraseNPreviousChars(t.countToLeftWord())
	case keyDeleteLine:
		// Delete everything from the current cursor position to the
		// end of line.
		for i := t.cellIndex(t.pos); i < t.cellIndex(len(t.line)); i++ {
			t.queue(space)
			t.advanceCursor(1)
		}
		t.line = t.line[:t.pos]
		t.moveCursorToPos(t.pos)
	case keyCtrlD:
		// Erase the character under the current position.
		// The EOF case when the line is empty is handled in
		// readLine().
		if t.pos < len(t.line) {
			n := t.nextClusterLen()
			t.pos += n
			t.eraseNPreviousChars(n)
		}
	case keyCtrlU:
		if t.reverseActive {
			t.reverseQuery = ""
			t.reverseIndex = 0
			t.reversePromptForState()
			t.findReverseMatch()
			return
		}
		t.eraseNPreviousChars(t.pos)
	case keyClearScreen:
		// Erases the screen and moves the cursor to the home position.
		t.queue([]rune("\x1b[2J\x1b[H"))
		t.queue(t.prompt)
		t.cursorX, t.cursorY = 0, 0
		t.advanceCursor(visualLength(t.prompt))
		t.setLine(t.line, t.pos)
	default:
		if t.reverseActive && isPrintable(key) {
			t.reverseQuery += string(key)
			t.reverseIndex = 0
			t.reversePromptForState()
			t.findReverseMatch()
			return
		}
		if t.AutoCompleteCallback != nil {
			prefix := string(t.line[:t.pos])
			suffix := string(t.line[t.pos:])

			t.lock.Unlock()
			newLine, newPos, completeOk := t.AutoCompleteCallback(prefix+suffix, len(prefix), key)
			t.lock.Lock()

			if completeOk {
				t.setLine([]rune(newLine), utf8.RuneCount([]byte(newLine)[:newPos]))
				return
			}
		}
		if !isPrintable(key) {
			return
		}
		if len(t.line) == maxLineLength {
			return
		}
		t.addKeyToLine(key)
	}
	return
}

// addKeyToLine inserts the given key at the current position in the current
// line.
func (t *Terminal) addKeyToLine(key rune) {
	if len(t.line) == cap(t.line) {
		newLine := make([]rune, len(t.line), 2*(1+len(t.line)))
		copy(newLine, t.line)
		t.line = newLine
	}
	t.line = t.line[:len(t.line)+1]
	copy(t.line[t.pos+1:], t.line[t.pos:])
	t.line[t.pos] = key
	if t.echo {
		t.writeLine(t.line[t.pos:])
	}
	t.pos++
	t.moveCursorToPos(t.pos)
}
