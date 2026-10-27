package lineedit

// The status line above the input line (chat.steering): while a turn runs chat
// shows what it is doing on a row of its own directly above the prompt
// ("⠋ 思考中… 12s", "⠋ 执行 shell_exec… 3s"). SetStatus is the whole interface.
//
// The row is part of the block the editor keeps on screen (the status line, the
// prompt and the line being edited), so a write of the editor (Write) clears that
// whole block, prints the output where the block started and puts the block back
// below it: the status line never fights the output or the input line for a row,
// and the cursor stays in the line that is being edited.
//
// The row comes and goes with the text. It takes the first row of the block - the
// prompt and the line move down one row - and gives that row back when the text
// is emptied. A row is neither inserted nor deleted on screen (the editor only
// moves the cursor and erases rows, which is what every terminal understands):
// the block simply starts on the row the status line occupies, or on the row of
// the prompt when there is none.

// SetStatus replaces the status line shown above the input line; an empty text
// drops it. The text must be one display line: control runes are turned into
// spaces and the text is cut to the width of the terminal, so it can never wrap
// into the row of the prompt and break the layout the block is tracked with.
//
// While nothing of the block is on screen the text is remembered and drawn
// together with the prompt of the read that follows (readLine); otherwise the row
// changes where it is, without touching the prompt, the line being edited or the
// cursor in it. Safe from any goroutine.
func (t *Terminal) SetStatus(text string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if text == t.statusText {
		return
	}
	t.statusText = text
	t.status = fitStatus([]rune(text), t.termWidth)
	t.showStatusLocked()
}

// showStatusLocked puts the screen in the state the status text asks for: the row
// appears with a text, disappears with an empty one, and a row that is already
// there is rewritten in place. t.lock must be held.
func (t *Terminal) showStatusLocked() {
	show := len(t.status) > 0
	if !t.lineShown() {
		// Nothing of the block is on screen: nothing to change here, the read that
		// draws the prompt draws the status line above it.
		t.statusOn = false
		return
	}
	switch {
	case show == t.statusOn:
		// The row is already where it belongs: rewrite it, leaving the prompt and
		// the line below untouched (one frame of a running turn).
		if show {
			t.repaintStatusRow()
		}
	case show:
		// The row appears: it takes the first row of the block, so the prompt and
		// the line move one row down.
		t.rebuildBlock(true)
	default:
		// The row goes away: the prompt and the line move back up to the row it
		// occupied, and the row below the block stays empty.
		t.rebuildBlock(false)
	}
}

// rebuildBlock redraws the whole block at the cursor - which must be at the
// block's first row, column 0 - with the status line on (show) or off, after the
// status text appeared or went away. The status line takes the row the prompt had
// and gives it back: the block extends one row down or shrinks back, and no screen
// row has to be inserted or deleted (the editor only moves the cursor and erases
// rows). t.lock must be held.
func (t *Terminal) rebuildBlock(show bool) {
	t.clearBlock()
	t.statusOn = show
	if show {
		// The cursor is on the status row now: the row -1 from the prompt row that
		// paintBlock writes the status line to.
		t.cursorY = -1
	} else {
		// Back on the prompt row, which the status line occupied until now.
		t.cursorY = 0
	}
	t.paintBlock()
	_, _ = t.c.Write(t.outBuf) // the terminal is going away if this fails: nothing to report
	t.outBuf = t.outBuf[:0]
}

// repaintStatusRow rewrites the status line in place, leaving the prompt, the line
// being edited and the cursor in it exactly where they are: a frame must never eat
// a character that is being typed. t.lock must be held.
func (t *Terminal) repaintStatusRow() {
	// One row up from the one the cursor is on (the status row is the row above the
	// prompt row cursorY is measured against), erase it, rewrite it, and go back to
	// the cursor: only the status row is touched.
	t.move(t.cursorY+1, 0, 0, 0)
	t.outBuf = append(t.outBuf, '\r')
	t.clearLineToRight()
	t.queue(t.status)
	t.outBuf = append(t.outBuf, '\r')
	t.move(0, t.cursorY+1, 0, t.cursorX)
	_, _ = t.c.Write(t.outBuf)
	t.outBuf = t.outBuf[:0]
}

// lineShown reports whether any part of the block - the status line, the prompt or
// the line being edited - is on screen. The convention is the line editor's own
// (Write): the cursor sitting at the terminal's origin means nothing was drawn.
func (t *Terminal) lineShown() bool {
	return t.cursorX != 0 || t.cursorY != 0 || t.statusOn
}

// fitStatus prepares a status text for one terminal row: control runes become
// spaces (a newline or a carriage return would move the cursor out of the row),
// the text is cut to what fits, and - only when it had to be cut - a color it
// carries is closed with a reset (chat paints the line dim, and an attribute left
// set would run on into the rest of the screen).
//
// One column is left free at the end: a row filled to its last cell holds the wrap
// back until the next character, which would push the prompt down one row.
func fitStatus(text []rune, width int) []rune {
	limit := width - 1
	if width < 2 {
		limit = DefaultWidth - 1
	}
	var (
		out       []rune
		esc       escapeState
		cols      int
		cut       int  // how much of out ends after a complete escape sequence
		colored   bool // an escape sequence was among the runes
		truncated bool // the text did not fit and was cut
	)
	for _, r := range text {
		if r != '\x1b' && (r < 0x20 || r == 0x7f) {
			// A control rune would move the cursor out of the row; ESC is the start of
			// the escape sequences the text may carry and belongs to esc.
			r = ' '
		}
		w := esc.width(r)
		if cols+w > limit {
			truncated = true
			break
		}
		cols += w
		out = append(out, r)
		if r == '\x1b' {
			colored = true
		}
		if !esc.inEscapeSeq {
			cut = len(out)
		}
	}
	out = out[:cut]
	if truncated && colored {
		out = append(out, resetSeq...)
	}
	return out
}

// resetSeq turns off the attributes a status text may carry (chat paints that line
// dim), so a color the cut left open cannot run on into the rest of the screen.
var resetSeq = []rune("\x1b[0m")
