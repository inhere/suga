package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestChatInputSource_AskOnSteerReadRepaintsTheNextPrompt (issue suga-9ew7): a
// question answered on the borrowed steer read ends that read, so the turn is over
// with no read holding the block it painted (endAsk leaves the steer prompt on the
// row); the fresh read the chat asks for then has to repaint the block with its
// own prompt, the way the turn of a run without a question ends at it.
func TestChatInputSource_AskOnSteerReadRepaintsTheNextPrompt(t *testing.T) {
	for _, tc := range []struct{ name, typed string }{
		{"yes", "y\r"}, {"no", "n\r"}, {"always", "a\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAskHarness(t)
			h.source.SetSteering(true)
			h.source.SetBusy(true)
			_, ok := h.source.RequestSteer(steerPrompt)
			assert.True(t, ok)
			waitUntil(t, "steer read", func() bool { return h.kb.Reading() > 0 })
			waitUntil(t, "steer prompt painted", func() bool {
				return strings.Contains(h.out.String(), steerPrompt)
			})

			asked := h.ask()
			h.waitPrompt(t)
			h.kb.Type(tc.typed)
			res := h.answer(t, asked)
			assert.NoErr(t, res.err)

			// The turn is over: Run's loop asks for the prompt read like nextChatInput
			// does. The block on screen is still the one the turn's read left there
			// (endAsk repainted the steer prompt into it), and the fresh read owns it
			// now.
			before := len(h.out.String())
			assert.True(t, h.source.Request("> "))
			waitUntil(t, "the next prompt painted", func() bool {
				return strings.Contains(h.out.String()[before:], "> ")
			})
			seg := h.out.String()[before:]
			// The row was repainted in place: the stale block cleared, the prompt
			// drawn, and the old steer prompt not shown again.
			assert.StrContains(t, seg, "\x1b[K")
			assert.StrNotContains(t, seg, "↳")

			// The read is a real prompt read: what is typed next is a line of its
			// own (everything the chat sees after the turn behaves normally).
			h.kb.Type("abc\r")
			event := h.nextLine(t)
			assert.NoErr(t, event.err)
			assert.Eq(t, "abc", event.line)
		})
	}
}

func TestChatInputSource_AskTwiceInOneSteerReadShowsBothQuestions(t *testing.T) {
	h := newAskHarness(t)
	h.source.SetSteering(true)
	h.source.SetBusy(true)
	_, ok := h.source.RequestSteer(steerPrompt)
	assert.True(t, ok)
	waitUntil(t, "steer read", func() bool { return h.kb.Reading() > 0 })

	for i, tc := range []struct {
		prompt string
		answer string
	}{
		{prompt: "first question? ", answer: "a"},
		{prompt: "second question? ", answer: ""},
	} {
		done := make(chan askResult, 1)
		go func(prompt string) {
			line, err := h.source.Ask(context.Background(), "", prompt)
			done <- askResult{line, err}
		}(tc.prompt)
		waitUntil(t, tc.prompt, func() bool { return strings.Contains(h.out.String(), tc.prompt) })
		h.kb.Type(tc.answer + "\r")
		res := h.answer(t, done)
		assert.NoErr(t, res.err)
		assert.Eq(t, tc.answer, res.line)
		if i == 0 {
			assert.StrContains(t, h.out.String(), tc.prompt+tc.answer)
		}
	}
}
