// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Display-width helpers added to the x/term v0.29.0 line editor (not in
// upstream). See the package comment in terminal.go.

package lineedit

import (
	"unicode"

	"golang.org/x/text/width"
)

// runeWidth returns the number of terminal columns r occupies: 2 for East
// Asian wide and fullwidth runes, 0 for combining marks and format
// characters, otherwise 1. Control characters count as 1 like upstream x/term.
// East Asian ambiguous runes count as 1, matching Windows Terminal and most
// emulators in non-CJK locales. (goutil strutil.RuneWidth is not used: it
// returns 2 for ambiguous and combining runes and 0 for "neutral" scripts.)
func runeWidth(r rune) int {
	if r < 0x20 || r == 0x7f {
		return 1
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// escapeState tracks an ESC sequence (ESC ... letter) across runes; the whole
// sequence is zero width, as in upstream visualLength.
type escapeState struct{ inEscapeSeq bool }

// width returns the display width of r given the preceding runes.
func (e *escapeState) width(r rune) int {
	switch {
	case e.inEscapeSeq:
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			e.inEscapeSeq = false
		}
		return 0
	case r == '\x1b':
		e.inEscapeSeq = true
		return 0
	}
	return runeWidth(r)
}

// posToXY returns the screen position (relative to the start of the prompt)
// of the logical position pos in t.line, laying the runes out exactly like
// writeLine: a wide rune that would start in the last column wraps whole to
// the next row, and a newline starts a new row at column 0.
func (t *Terminal) posToXY(pos int) (x, y int) {
	x = visualLength(t.prompt)
	y = x / t.termWidth
	x = x % t.termWidth

	var esc escapeState
	for _, r := range t.line[:pos] {
		if r == '\n' {
			x, y = 0, y+1
			continue
		}
		w := esc.width(r)
		if w == 2 && t.termWidth >= 2 && x == t.termWidth-1 {
			x, y = 0, y+1
		}
		x += w
		y += x / t.termWidth
		x = x % t.termWidth
	}
	return x, y
}

// cellIndex returns the linear screen cell (row*termWidth + column) of the
// logical position pos; differences between two indexes count the cells to
// blank when the line shrinks.
func (t *Terminal) cellIndex(pos int) int {
	x, y := t.posToXY(pos)
	return y*t.termWidth + x
}

// multiLineInput reports whether the line being edited holds a newline: Up and Down
// then move inside the input instead of walking the history.
func (t *Terminal) multiLineInput() bool {
	for _, r := range t.line {
		if r == '\n' {
			return true
		}
	}
	return false
}

// moveCursorVertically moves the cursor one display row up (dy -1) or down (dy +1)
// inside the input, keeping the column it has *within the text of its row* (the first
// row starts after the prompt, the rows below start at column 0, so the screen column
// alone would drop the cursor into the first row's prompt offset) and ending after the
// row's last rune when the row is shorter. It reports whether a row in that direction
// exists (false: Up on the first row, Down on the last).
func (t *Terminal) moveCursorVertically(dy int) bool {
	x, y := t.posToXY(t.pos)
	target := y + dy
	if target < 0 {
		return false
	}
	want := t.rowStart(target) + x - t.rowStart(y)
	// The last position of the target row that is not past want; when the row is
	// shorter, its end.
	best := -1
	for pos := 0; pos <= len(t.line); pos++ {
		px, py := t.posToXY(pos)
		if py < target {
			continue
		}
		if py > target {
			break
		}
		if px > want {
			break
		}
		best = pos
	}
	if best < 0 {
		return false
	}
	t.pos = best
	t.moveCursorToPos(t.pos)
	return true
}

// rowStart is the column a display row starts at: the prompt occupies the beginning of
// the row the input starts on, the rows a newline opens start at column 0. (A prompt
// wider than the terminal is not considered: chat's is two columns.)
func (t *Terminal) rowStart(y int) int {
	if y == 0 {
		return visualLength(t.prompt)
	}
	return 0
}

// prevClusterLen returns how many runes before t.pos form the previous glyph:
// one base rune plus any zero-width runes (combining marks) after it.
func (t *Terminal) prevClusterLen() int {
	n := 0
	for t.pos-n > 0 {
		n++
		if runeWidth(t.line[t.pos-n]) != 0 {
			break
		}
	}
	return n
}

// nextClusterLen returns how many runes from t.pos form the next glyph: the
// rune at t.pos plus any zero-width runes that follow it.
func (t *Terminal) nextClusterLen() int {
	n := 1
	for t.pos+n < len(t.line) && runeWidth(t.line[t.pos+n]) == 0 {
		n++
	}
	return n
}
