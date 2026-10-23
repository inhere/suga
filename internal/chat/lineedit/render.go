// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Drawing side of the line editor: the output buffer, cursor positions and
// repainting. Every position is measured in terminal columns (a wide rune is
// two), so the helpers here and width.go are what make the editor display-width
// aware; see the package comment in terminal.go.

package lineedit

import (
	"bytes"
	"io"
	"strconv"
)

// queue appends data to the end of t.outBuf
func (t *Terminal) queue(data []rune) {
	t.outBuf = append(t.outBuf, []byte(string(data))...)
}

var space = []rune{' '}

// moveCursorToPos appends data to t.outBuf which will move the cursor to the
// given, logical position in the text.
func (t *Terminal) moveCursorToPos(pos int) {
	if !t.echo {
		return
	}

	x, y := t.posToXY(pos)

	up := 0
	if y < t.cursorY {
		up = t.cursorY - y
	}

	down := 0
	if y > t.cursorY {
		down = y - t.cursorY
	}

	left := 0
	if x < t.cursorX {
		left = t.cursorX - x
	}

	right := 0
	if x > t.cursorX {
		right = x - t.cursorX
	}

	t.cursorX = x
	t.cursorY = y
	t.move(up, down, left, right)
}

func (t *Terminal) move(up, down, left, right int) {
	m := []rune{}

	// 1 unit up can be expressed as ^[[A or ^[A
	// 5 units up can be expressed as ^[[5A

	if up == 1 {
		m = append(m, keyEscape, '[', 'A')
	} else if up > 1 {
		m = append(m, keyEscape, '[')
		m = append(m, []rune(strconv.Itoa(up))...)
		m = append(m, 'A')
	}

	if down == 1 {
		m = append(m, keyEscape, '[', 'B')
	} else if down > 1 {
		m = append(m, keyEscape, '[')
		m = append(m, []rune(strconv.Itoa(down))...)
		m = append(m, 'B')
	}

	if right == 1 {
		m = append(m, keyEscape, '[', 'C')
	} else if right > 1 {
		m = append(m, keyEscape, '[')
		m = append(m, []rune(strconv.Itoa(right))...)
		m = append(m, 'C')
	}

	if left == 1 {
		m = append(m, keyEscape, '[', 'D')
	} else if left > 1 {
		m = append(m, keyEscape, '[')
		m = append(m, []rune(strconv.Itoa(left))...)
		m = append(m, 'D')
	}

	t.queue(m)
}

func (t *Terminal) clearLineToRight() {
	op := []rune{keyEscape, '[', 'K'}
	t.queue(op)
}

const maxLineLength = 4096

func (t *Terminal) setLine(newLine []rune, newPos int) {
	if t.echo {
		// Blank the cells the old line used beyond the end of the new one.
		oldEnd := t.cellIndex(len(t.line))
		t.moveCursorToPos(0)
		t.writeLine(newLine)
		t.line = newLine
		for i := t.cellIndex(len(newLine)); i < oldEnd; i++ {
			t.writeLine(space)
		}
		t.moveCursorToPos(newPos)
	}
	t.line = newLine
	t.pos = newPos
}

func (t *Terminal) advanceCursor(places int) {
	t.cursorX += places
	t.cursorY += t.cursorX / t.termWidth
	if t.cursorY > t.maxLine {
		t.maxLine = t.cursorY
	}
	t.cursorX = t.cursorX % t.termWidth

	if places > 0 && t.cursorX == 0 {
		// Normally terminals will advance the current position
		// when writing a character. But that doesn't happen
		// for the last character in a line. However, when
		// writing a character (except a new line) that causes
		// a line wrap, the position will be advanced two
		// places.
		//
		// So, if we are stopping at the end of a line, we
		// need to write a newline so that our cursor can be
		// advanced to the next line.
		t.outBuf = append(t.outBuf, '\r', '\n')
	}
}

// clearInputLine drops the line being edited and blanks every screen row it was
// drawn on, leaving the cursor at the start of the empty input - just after the
// prompt, which is drawn again. It is what Ctrl-C does to a non-empty line
// (readLine), so the text goes away without the read ending. t.lock must be held.
func (t *Terminal) clearInputLine() {
	if !t.echo {
		// Nothing of the line is on screen: only the state goes away.
		t.line, t.pos = t.line[:0], 0
		t.cursorX, t.cursorY, t.maxLine = 0, 0, 0
		return
	}
	// From the last row of the text back up to the row the prompt is on: every row
	// the input used is blanked on the way, a multi-line input (pasted text, a
	// message put back with Up) included.
	t.moveCursorToPos(len(t.line))
	for {
		if t.cursorX > 0 {
			t.move(0, 0, t.cursorX, 0)
			t.cursorX = 0
		}
		t.clearLineToRight()
		if t.cursorY == 0 {
			break
		}
		t.move(1, 0, 0, 0)
		t.cursorY--
	}
	// The prompt went with its row (the rows are blanked from column 0): draw it
	// again, so the cursor sits at the start of the empty input line.
	t.line, t.pos = t.line[:0], 0
	t.queue(t.prompt)
	t.cursorX, t.cursorY = 0, 0
	t.advanceCursor(visualLength(t.prompt))
	t.maxLine = t.cursorY
}

func (t *Terminal) eraseNPreviousChars(n int) {
	if n == 0 {
		return
	}

	if t.pos < n {
		n = t.pos
	}
	oldEnd := t.cellIndex(len(t.line))
	t.pos -= n
	t.moveCursorToPos(t.pos)

	copy(t.line[t.pos:], t.line[n+t.pos:])
	t.line = t.line[:len(t.line)-n]
	if t.echo {
		t.writeLine(t.line[t.pos:])
		// Blank as many cells as the line shrank (2 per wide rune).
		cells := oldEnd - t.cellIndex(len(t.line))
		for i := 0; i < cells; i++ {
			t.queue(space)
		}
		t.advanceCursor(cells)
		t.moveCursorToPos(t.pos)
	}
}

// countToLeftWord returns then number of characters from the cursor to the
// start of the previous word.
func (t *Terminal) countToLeftWord() int {
	if t.pos == 0 {
		return 0
	}

	pos := t.pos - 1
	for pos > 0 {
		if t.line[pos] != ' ' {
			break
		}
		pos--
	}
	for pos > 0 {
		if t.line[pos] == ' ' {
			pos++
			break
		}
		pos--
	}

	return t.pos - pos
}

// countToRightWord returns then number of characters from the cursor to the
// start of the next word.
func (t *Terminal) countToRightWord() int {
	pos := t.pos
	for pos < len(t.line) {
		if t.line[pos] == ' ' {
			break
		}
		pos++
	}
	for pos < len(t.line) {
		if t.line[pos] != ' ' {
			break
		}
		pos++
	}
	return pos - t.pos
}

// visualLength returns the number of terminal columns the runes occupy.
func visualLength(runes []rune) int {
	var esc escapeState
	length := 0
	for _, r := range runes {
		length += esc.width(r)
	}
	return length
}

// writeLine writes line at the cursor, wrapping by display width. A wide rune
// never straddles two rows: when only the last column is left it is filled
// with a space first (the same rule posToXY applies). A newline inside the line
// (pasted multi-line input) starts a new row at column 0.
func (t *Terminal) writeLine(line []rune) {
	var esc escapeState
	for i := range line {
		if line[i] == '\n' {
			// Raw mode has OPOST off, so echoing a bare LF would move the cursor
			// down and keep the column, drifting away from cursorX/cursorY.
			t.outBuf = append(t.outBuf, '\r', '\n')
			t.cursorX = 0
			t.cursorY++
			if t.cursorY > t.maxLine {
				t.maxLine = t.cursorY
			}
			continue
		}
		w := esc.width(line[i])
		if w == 2 && t.termWidth >= 2 && t.cursorX == t.termWidth-1 {
			t.queue(space)
			t.advanceCursor(1)
		}
		t.queue(line[i : i+1])
		t.advanceCursor(w)
	}
}

// writeWithCRLF writes buf to w but replaces all occurrences of \n with \r\n.
func writeWithCRLF(w io.Writer, buf []byte) (n int, err error) {
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, '\n')
		todo := len(buf)
		if i >= 0 {
			todo = i
		}

		var nn int
		nn, err = w.Write(buf[:todo])
		n += nn
		if err != nil {
			return n, err
		}
		buf = buf[todo:]

		if i >= 0 {
			if _, err = w.Write(crlf); err != nil {
				return n, err
			}
			n++
			buf = buf[1:]
		}
	}

	return n, nil
}

func (t *Terminal) Write(buf []byte) (n int, err error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.cursorX == 0 && t.cursorY == 0 && !t.statusOn {
		// This is the easy case: there's nothing on the screen that we
		// have to move out of the way.
		return writeWithCRLF(t.c, buf)
	}

	// We have a prompt and possibly user input on the screen. We
	// have to clear it first - the status line's row (chat.steering) included,
	// when there is one, since the block starts there.
	t.clearBlock()

	if _, err = t.c.Write(t.outBuf); err != nil {
		return
	}
	t.outBuf = t.outBuf[:0]

	if n, err = writeWithCRLF(t.c, buf); err != nil {
		return
	}

	if t.statusOn && !endsLine(buf) {
		// The output did not end a line: the status line and the prompt go on rows
		// of their own anyway, so the block keeps the layout it is tracked with
		// (chat writes complete lines while the input line is up: whole-line mode).
		if _, err = t.c.Write([]byte("\r\n")); err != nil {
			return
		}
	}

	t.paintBlock()

	if _, err = t.c.Write(t.outBuf); err != nil {
		return
	}
	t.outBuf = t.outBuf[:0]
	return
}

// endsLine reports whether the output ends at column 0 of a fresh row.
func endsLine(buf []byte) bool {
	return len(buf) == 0 || buf[len(buf)-1] == '\n'
}

// clearBlock erases the input block - the status line, while one is up, above the
// prompt with the line being edited - and leaves the cursor at the block's first
// row, column 0. cursorY counts rows from the prompt row, so it is -1 while the
// cursor is on the status line's row. Every repaint starts from there (paintBlock).
func (t *Terminal) clearBlock() {
	t.move(0, 0, t.cursorX, 0)
	t.cursorX = 0
	t.clearLineToRight()
	for t.cursorY > 0 {
		t.move(1, 0, 0, 0)
		t.cursorY--
		t.clearLineToRight()
	}
	if t.statusOn {
		t.move(1, 0, 0, 0)
		t.cursorY = -1
		t.clearLineToRight()
	}
}

// paintBlock draws the block at the cursor - which must be at the block's first
// row, column 0, with cursorY -1 while the status line is up - and leaves the
// cursor where the line's own cursor sits. The status line takes the first row and
// the prompt follows on the next one; cursorY counts from the prompt row, which is
// why it is reset between the two.
func (t *Terminal) paintBlock() {
	// What the block is drawn with: the next read repaints the block only when its
	// own prompt differs from this one (readLine), so this has to be the paint's
	// prompt even when a caller repaints without a change of its own.
	t.drawnPrompt = string(t.prompt)
	pos := t.pos
	if t.statusOn {
		t.cursorX, t.cursorY = 0, 0
		t.writeLine(t.status)
		// writeLine is not used for the row break: it would count the row in maxLine,
		// which is measured from the prompt row.
		t.outBuf = append(t.outBuf, '\r', '\n')
	}
	t.cursorX, t.cursorY = 0, 0
	t.writeLine(t.prompt)
	if t.echo {
		t.writeLine(t.line)
	}
	t.moveCursorToPos(pos)
}

func (t *Terminal) clearAndRepaintLinePlusNPrevious(numPrevLines int) {
	// Move cursor to column zero at the start of the line.
	t.move(t.cursorY, 0, t.cursorX, 0)
	t.cursorX, t.cursorY = 0, 0
	if t.statusOn {
		// The status line's row is the first row of the block: clear it with the rest
		// and draw it again before the prompt.
		t.move(1, 0, 0, 0)
		t.cursorY = -1
	}
	t.clearLineToRight()
	for t.cursorY < numPrevLines {
		// Move down a line
		t.move(0, 1, 0, 0)
		t.cursorY++
		t.clearLineToRight()
	}
	// Move back to the beginning.
	up := t.cursorY
	if t.statusOn {
		up++ // the status line's row above the first row of the prompt
	}
	t.move(up, 0, 0, 0)
	t.cursorX, t.cursorY = 0, 0
	if t.statusOn {
		t.cursorY = -1
	}
	t.paintBlock()
}

func (t *Terminal) SetSize(width, height int) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	if width == 0 {
		width = 1
	}

	oldWidth := t.termWidth
	t.termWidth, t.termHeight = width, height

	switch {
	case width == oldWidth:
		// If the width didn't change then nothing else needs to be
		// done.
		return nil
	case len(t.line) == 0 && t.cursorX == 0 && t.cursorY == 0 && !t.statusOn:
		// If there is nothing on current line and no prompt printed,
		// just do nothing
		return nil
	case width < oldWidth:
		// Some terminals (e.g. xterm) will truncate lines that were
		// too long when shinking. Others, (e.g. gnome-terminal) will
		// attempt to wrap them. For the former, repainting t.maxLine
		// works great, but that behaviour goes badly wrong in the case
		// of the latter because they have doubled every full line.

		// We assume that we are working on a terminal that wraps lines
		// and adjust the cursor position based on every previous line
		// wrapping and turning into two. This causes the prompt on
		// xterms to move upwards, which isn't great, but it avoids a
		// huge mess with gnome-terminal.
		if t.cursorX >= t.termWidth {
			t.cursorX = t.termWidth - 1
		}
		t.cursorY *= 2
		t.clearAndRepaintLinePlusNPrevious(t.maxLine * 2)
	case width > oldWidth:
		// If the terminal expands then our position calculations will
		// be wrong in the future because we think the cursor is
		// |t.pos| chars into the string, but there will be a gap at
		// the end of any wrapped line.
		//
		// But the position will actually be correct until we move, so
		// we can move back to the beginning and repaint everything.
		t.clearAndRepaintLinePlusNPrevious(t.maxLine)
	}

	_, err := t.c.Write(t.outBuf)
	t.outBuf = t.outBuf[:0]
	return err
}
