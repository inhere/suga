//go:build windows

package chat

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestCtrlJInsertsNewline: on Windows a bare LF is Ctrl+J - a console hands a cooked
// line over as CRLF, whose LF the pump drops, so nothing else produces one - and it
// inserts a newline in the input instead of submitting. (On Unix ICRNL turns Enter
// into a bare LF, which is why the pump does not take it that way there; that side is
// covered by TestChatInputSource_PretypedLine.)
func TestCtrlJInsertsNewline(t *testing.T) {
	h := newAskHarness(t)
	h.source.inputWait = kbWait(h.kb)
	assert.True(t, h.source.Request("> "))

	h.kb.Type("第一行\n第二行") // Ctrl+J between the lines
	assertNoLine(t, h.source)
	h.kb.Type("\r") // the person's own Enter submits the whole input
	event := h.nextLine(t)
	assert.NoErr(t, event.err)
	assert.Eq(t, "第一行\n第二行", event.line)
}

// testNewline is the byte sequence a terminal hands over for Enter (and for the line
// break of a paste or a pre-typed line): a Windows console sends CRLF, and the pump
// drops the LF of that pair. A bare LF is Ctrl+J or Ctrl+Enter here
// (ctrlJIsInputNewline).
const testNewline = "\r\n"
