package chat

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/chatui/lineedit"
)

// Ctrl-C on a non-empty input line drops that line and nothing else
// (lineedit.SetCtrlCClearsLine, wll1): at the prompt the read goes on instead of
// starting the exit confirmation, and during a turn the turn is not ended. An
// empty line keeps both old meanings, and the answer of an approval question keeps
// Ctrl-C as "cancel" whatever is typed (ask.go).

func TestChatInputSource_CtrlCClearsTurnLine(t *testing.T) {
	t.Run("a non-empty line is dropped, the turn is not ended", func(t *testing.T) {
		h := newAskHarness(t)
		h.source.SetBusy(true)
		h.requestSteer(t)
		// The batch is handled in order: the text, the Ctrl-C that drops it, then the
		// message typed in its place. A first key event here would be the keyAbort of
		// a Ctrl-C that ended the turn.
		h.kb.Type("不要这行\x03只改 README\r")
		event := h.nextTurnEvent(t)
		assert.Eq(t, keyNone, event.key)
		assert.Eq(t, "只改 README", event.line) // the dropped text is not part of it
		h.source.mu.Lock()
		counted := h.source.interruptSeen
		h.source.mu.Unlock()
		assert.False(t, counted) // not the first of the two presses that leave chat
	})

	t.Run("an empty line still ends this turn only", func(t *testing.T) {
		h := newAskHarness(t)
		h.source.SetBusy(true)
		h.requestSteer(t)
		h.kb.Type("\x03")
		assert.Eq(t, keyAbort, h.nextTurnEvent(t).key)
	})
}

func TestChatInputSource_CtrlCClearsPromptLine(t *testing.T) {
	t.Run("a non-empty line is dropped, the read goes on", func(t *testing.T) {
		h := newAskHarness(t)
		assert.True(t, h.source.Request("> "))
		h.kb.Type("不要这行\x03放弃这行\r")
		event := h.nextLine(t)
		assert.NoErr(t, event.err) // not an interrupt: no exit confirmation started
		assert.Eq(t, "放弃这行", event.line)
		h.source.mu.Lock()
		counted := h.source.interruptSeen
		h.source.mu.Unlock()
		assert.False(t, counted)
	})

	t.Run("an empty line still asks to leave", func(t *testing.T) {
		h := newAskHarness(t)
		assert.True(t, h.source.Request("> "))
		h.kb.Type("\x03")
		assert.ErrIs(t, h.nextLine(t).err, lineedit.ErrInterrupt)
	})
}

// TestChatInputSource_AskKeepsCtrlCOnTypedText: the answer read is the one read
// whose Ctrl-C cancels the question even with text typed - the prompt and the
// turn's read drop such a line instead. A question borrowing the turn's read
// (ask.go) has to take that behaviour over with it.
func TestChatInputSource_AskKeepsCtrlCOnTypedText(t *testing.T) {
	t.Run("a fresh answer read", func(t *testing.T) {
		h := newAskHarness(t)
		h.source.SetBusy(true)
		done := h.ask()
		h.waitPrompt(t)
		h.kb.Type("ab\x03")
		res := h.answer(t, done)
		assert.ErrIs(t, res.err, errAskCancelled)
	})

	t.Run("a question borrowing the turn's read", func(t *testing.T) {
		h := newAskHarness(t)
		h.source.SetBusy(true)
		h.requestSteer(t)
		h.kb.Type("半句") // typed into the running turn, still being edited
		waitUntil(t, "the half line echoed", func() bool { return strings.Contains(h.out.String(), "半句") })
		done := h.ask()
		h.waitPrompt(t)
		h.kb.Type("ab\x03")
		res := h.answer(t, done)
		assert.ErrIs(t, res.err, errAskCancelled)
	})
}
