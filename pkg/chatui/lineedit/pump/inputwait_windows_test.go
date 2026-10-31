//go:build windows

package pump

import (
	"encoding/binary"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// keyRecord builds one INPUT_RECORD of a key event (see keyDownIsInput).
func keyRecord(eventType uint16, down bool, vk uint16) []byte {
	record := make([]byte, inputRecordSize)
	binary.LittleEndian.PutUint16(record[0:2], eventType)
	if down {
		binary.LittleEndian.PutUint32(record[4:8], 1)
	}
	binary.LittleEndian.PutUint16(record[10:12], vk)
	return record
}

// TestKeyDownIsInput: only a key-down that becomes input counts as a byte that
// follows; the key-up of Enter that some terminals queue right after it must not
// (it made every Enter a pasted newline there).
func TestKeyDownIsInput(t *testing.T) {
	t.Run("key-down of a character", func(t *testing.T) {
		assert.True(t, keyDownIsInput(keyRecord(keyEventType, true, 0x41)))
	})
	t.Run("key-up of enter", func(t *testing.T) {
		assert.False(t, keyDownIsInput(keyRecord(keyEventType, false, 0x0d)))
	})
	t.Run("bare modifier", func(t *testing.T) {
		assert.False(t, keyDownIsInput(keyRecord(keyEventType, true, 0x10)))
	})
	t.Run("focus event", func(t *testing.T) {
		assert.False(t, keyDownIsInput(keyRecord(0x0010, true, 0x41)))
	})
}
