package lineedit

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestWithdrawCallbackOnUp: Up on an empty input line asks the caller for a line to
// fill in and consumes the key when it gives one (what chat uses to take a message
// still waiting in a running turn back for editing); a line that already holds text
// - or a caller with nothing to give - keeps Up's usual meaning, the history
// included. The callback runs inside the read, like EscCallback.
func TestWithdrawCallbackOnUp(t *testing.T) {
	t.Run("an empty line is filled in by the caller", func(t *testing.T) {
		tm, mem := newMem("", 0)
		calls := 0
		tm.WithdrawCallback = func() (string, bool) { calls++; return "第一行\n第二行", true }
		// Up, then Enter: the text the caller filled in is the line that is
		// submitted, its line break included.
		lines := readAll(t, tm, mem, "\x1b[A\r")
		assert.Eq(t, []string{"第一行\n第二行"}, lines)
		assert.Eq(t, 1, calls)
	})

	t.Run("a non-empty line keeps the history", func(t *testing.T) {
		tm, mem := newMem("", 0)
		tm.history.Add("old")
		calls := 0
		tm.WithdrawCallback = func() (string, bool) { calls++; return "别动", true }
		lines := readAll(t, tm, mem, "半行\x1b[A\r")
		assert.Eq(t, []string{"old"}, lines) // Up walked the history instead
		assert.Eq(t, 0, calls)
	})

	t.Run("a caller with nothing to give leaves Up alone", func(t *testing.T) {
		tm, mem := newMem("", 0)
		tm.WithdrawCallback = func() (string, bool) { return "", false }
		lines := readAll(t, tm, mem, "\x1b[A\r")
		assert.Eq(t, []string{""}, lines) // the empty line is what Enter submits
	})

	t.Run("without a callback Up keeps its meaning", func(t *testing.T) {
		tm, mem := newMem("", 0)
		tm.history.Add("old")
		lines := readAll(t, tm, mem, "\x1b[A\r")
		assert.Eq(t, []string{"old"}, lines)
	})
}
