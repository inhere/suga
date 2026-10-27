package lineedit

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestTerminal_ReverseSearchAcceptAndCancel(t *testing.T) {
	t.Run("accepts latest match and searches older on repeat", func(t *testing.T) {
		tm, mem := newMem("❯ ", 0)
		tm.SetHistory([]string{"old needle", "new needle"})
		mem.in = append(mem.in, "needle\x12\x12\r")
		line, err := tm.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "old needle", line)
	})
	t.Run("ctrl-g cancels", func(t *testing.T) {
		tm, mem := newMem("❯ ", 0)
		tm.SetHistory([]string{"saved"})
		mem.in = append(mem.in, "draft\x12\x07\r")
		line, err := tm.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "draft", line)
	})
}
