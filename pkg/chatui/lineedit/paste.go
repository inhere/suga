package lineedit

// Newlines while a line is being edited. Upstream x/term ends the read on any
// CR (Enter), a bracketed paste included, and reports the pasted line with
// ErrPasteIndicator: a pasted multi-line text is cut at its first line. Here a
// newline inside a paste is part of the pasted text instead (handleKey), so the
// whole paste lands on the input line, the person reads it and presses Enter
// once to send it as one message, its internal newlines kept.
//
// A terminal that does not bracket pastes hands the newlines over as plain CR/LF
// bytes, with nothing to tell them from a typed Enter. The caller that does know
// — chat's byte pump, which sees whether a byte follows within a few
// milliseconds — declares those newlines with SetNewlineAsText before handing
// them over; they are inserted as text too.

// SetNewlineAsText declares that the newline key the editor is about to read is
// text of a paste, not a submit: it is inserted into the line like any other
// pasted rune. The declaration covers one key only; a caller that sees something
// other than a newline follow it does not declare it (esc.go's SetEscAlone works
// the same way). Like SetEscAlone it is called from the byte pump, that is while
// a ReadLine is blocked in Read and holds no terminal lock.
func (t *Terminal) SetNewlineAsText() {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.newlineAsText = true
}
