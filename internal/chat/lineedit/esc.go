package lineedit

// A lone Esc press is ambiguous: bytesToKey cannot tell it from the first byte of
// an escape sequence, so upstream the editor keeps waiting for the rest of the
// sequence (unchanged here). A caller that decided the question itself — chat's
// byte pump, which waits briefly for a follow-up byte without leaving a Read
// pending — declares the decision with SetEscAlone before handing the 0x1b byte
// over; the editor then reports the key through Terminal.EscCallback instead of
// waiting for more bytes.

// SetEscAlone declares that the 0x1b byte the editor is about to read is an Esc
// key press on its own: the next read that ends on an Esc byte with nothing after
// it reports the key instead of waiting for the rest of a sequence. A caller that
// sees a follow-up byte never declares it, so a real sequence is unaffected.
// SetPasteImageCallback binds chat's Alt+V clipboard paste: the callback runs
// from inside ReadLine on an Alt+V press (see PasteImageCallback).
func (t *Terminal) SetPasteImageCallback(cb func()) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.PasteImageCallback = cb
}

func (t *Terminal) SetEscAlone() {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.escAlone = true
}

// takeEscAlone consumes a pending declaration when rest ends with the declared Esc
// byte, cutting that byte off the remainder: it is the key, not the start of a
// sequence. It reports whether the declaration was consumed. t.lock must be held.
func (t *Terminal) takeEscAlone(rest *[]byte) bool {
	if !t.escAlone {
		return false
	}
	b := *rest
	if len(b) == 0 || b[len(b)-1] != keyEscape {
		// No Esc byte to report after all: drop the declaration instead of holding
		// it for some later Esc.
		t.escAlone = false
		return false
	}
	t.escAlone = false
	*rest = b[:len(b)-1]
	return true
}
