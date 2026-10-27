package lineedit

// Local additions used by chat to ask a question while a line read started
// earlier is still waiting: the half-typed line is taken out, the question is
// read in its place (outside history), and the taken line is put back into the
// next read. All of them hold t.lock; a ReadLine blocked in Read has released
// it, so they never wait for a key press.

// TakeLine removes the line being edited and repaints the prompt without it.
// reading reports whether a ReadLine is in progress; if so, the rest of that
// read (whatever is typed from now on) is not added to history. When no read is
// running, the line set by SetPendingLine (not yet shown) is taken instead.
func (t *Terminal) TakeLine() (line string, reading bool) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if !t.reading {
		line, t.pendingLine = t.pendingLine, ""
		return line, false
	}
	line = string(t.line)
	t.noHistory = true
	t.historyIndex = -1
	if len(t.line) > 0 {
		t.setLine([]rune{}, 0)
		t.c.Write(t.outBuf)
		t.outBuf = t.outBuf[:0]
	}
	return line, true
}

// SetPendingLine prefills the next ReadLine with line (cursor at its end), as if
// it had been typed. An empty line clears a pending one.
func (t *Terminal) SetPendingLine(line string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.pendingLine = line
}

// PrependLine puts text in front of the line being edited (or of the pending
// line when no ReadLine runs) and shows it at once; the existing line follows on
// a line of its own, so nothing typed is lost. The cursor goes to the end. It is
// how chat writes a message that could not be inserted into a running turn back
// into the input line, for the person to review and submit; nothing is submitted
// here.
func (t *Terminal) PrependLine(text string) {
	if text == "" {
		return
	}
	t.lock.Lock()
	defer t.lock.Unlock()

	if !t.reading {
		// No read yet: the line is the one the coming ReadLine applies.
		if t.pendingLine != "" {
			text += "\n" + t.pendingLine
		}
		t.pendingLine = text
		return
	}
	if len(t.line) > 0 {
		text += "\n" + string(t.line)
	}
	t.line, t.pos = []rune(text), len([]rune(text))
	_ = t.repaintLocked()
}

// ReadLineNoHistory is ReadLine without adding the line to the history.
func (t *Terminal) ReadLineNoHistory() (line string, err error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.noHistory = true
	return t.readLine()
}

// applyPendingLine moves a pending line into the line being edited; it
// reports whether it did. t.lock must be held.
func (t *Terminal) applyPendingLine() bool {
	if t.pendingLine == "" {
		return false
	}
	t.line = []rune(t.pendingLine)
	t.pos = len(t.line)
	t.pendingLine = ""
	return true
}

// repaintLocked redraws the block - the status line (chat.steering), the prompt
// and the line being edited - in place, clearing every row it uses first (the same
// clearing as Write). t.lock must be held.
func (t *Terminal) repaintLocked() error {
	t.clearBlock()
	t.paintBlock()
	_, err := t.c.Write(t.outBuf)
	t.outBuf = t.outBuf[:0]
	return err
}

// AbortLine ends the screen state of a read that returned io.EOF (Ctrl-C, or
// Ctrl-D on an empty line): the cursor moves past the typed text to a new row
// and the text is dropped, so the next ReadLine starts on a clean line. A status
// line above the input line (chat.steering) goes with it: the read it belonged to
// is over.
func (t *Terminal) AbortLine() {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.abortLocked()
}

// abortLocked is AbortLine with t.lock held, so the same tidy-up can run from a
// caller that already owns the lock (TakeInterruptedLine).
func (t *Terminal) abortLocked() {
	if t.statusOn {
		// A status line above the input line (chat.steering) goes with the read it
		// belonged to: its row is cleared where it is, leaving the line and the cursor
		// where the caller left them.
		t.move(t.cursorY+1, 0, 0, 0)
		t.outBuf = append(t.outBuf, '\r')
		t.clearLineToRight()
		t.move(0, t.cursorY+1, 0, t.cursorX)
		t.statusOn = false
	}
	if t.cursorX != 0 || t.cursorY != 0 || len(t.line) > 0 {
		t.moveCursorToPos(len(t.line))
		t.queue([]rune("\r\n"))
	}
	if len(t.outBuf) > 0 {
		t.c.Write(t.outBuf)
		t.outBuf = t.outBuf[:0]
	}
	t.line, t.pos = t.line[:0], 0
	t.cursorX, t.cursorY, t.maxLine = 0, 0, 0
}

// TakeInterruptedLine takes the line a just-finished read left in the editor
// out of it and clears it from the screen, and returns its content. It is how
// chat reports what the person had typed when Ctrl-C ended a read that keeps
// the line (a running local command's read, whose Ctrl-C means "end the
// command"): the content rides on the read's abort event, so putting it back
// into the input line does not depend on the editor buffer surviving to the
// next read. A read that clears the line itself has nothing here to take; a
// read that is not over yet (a later ReadLine already runs) waits for it. When
// nothing is on the line the editor is left as it is.
func (t *Terminal) TakeInterruptedLine() string {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.reading || len(t.line) == 0 {
		return ""
	}
	line := string(t.line)
	t.abortLocked()
	return line
}

// keepRemainder stores the unprocessed input rest (a slice of t.inBuf) for
// the next loop or ReadLine. t.lock must be held.
func (t *Terminal) keepRemainder(rest []byte) {
	if len(rest) > 0 {
		n := copy(t.inBuf[:], rest)
		t.remainder = t.inBuf[:n]
	} else {
		t.remainder = nil
	}
}
