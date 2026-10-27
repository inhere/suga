package lineedit

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestTerminal_AltV: the Alt+V byte pair (ESC v) reaches PasteImageCallback
// through the real ReadLine byte path, leaves the line untouched and does not
// answer Enter in its place.
func TestTerminal_AltV(t *testing.T) {
	t.Run("ESC v runs the clipboard paste", func(t *testing.T) {
		tm, mem := newMem("", 0)
		pastes := 0
		tm.SetPasteImageCallback(func() {
			pastes++
			// The callback may write to the terminal, like chat's note.
			_, _ = mem.Write([]byte("[已附图 1：4×2 image/png]"))
		})
		lines := readAll(t, tm, mem, "\x1bvhi\r")
		assert.Eq(t, []string{"hi"}, lines)
		assert.Eq(t, 1, pastes)
	})
	t.Run("without a callback the key is just consumed", func(t *testing.T) {
		tm, mem := newMem("", 0)
		lines := readAll(t, tm, mem, "\x1bvok\r")
		assert.Eq(t, []string{"ok"}, lines)
		assert.False(t, mem.in == nil, "the read still completed normally")
	})
}
