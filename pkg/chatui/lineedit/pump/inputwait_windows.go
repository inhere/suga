//go:build windows

package pump

import (
	"encoding/binary"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WaitInputSupported reports that WaitTerminalInput really asks the platform
// whether a byte is ready: the decisions built on it (a lone Esc, a pasted
// newline) may then trust an answer of "nothing ready".
const WaitInputSupported = true

// procPeekConsoleInput looks at the console input queue without taking events off
// it (golang.org/x/sys/windows has no wrapper for it).
var procPeekConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekConsoleInputW")

const (
	// keyEventType is INPUT_RECORD.EventType of a KEY_EVENT_RECORD.
	keyEventType = 0x0001
	// inputRecordSize is sizeof(INPUT_RECORD): a WORD event type, padding and a
	// 16-byte union.
	inputRecordSize = 20
	// peekRecords is how many queued events one look checks.
	peekRecords = 64
	// inputPollStep is how long the wait sleeps between looks while the queue
	// holds only events that are no byte (the queue handle stays signalled).
	inputPollStep = 2 * time.Millisecond
)

// WaitTerminalInput reports whether the console input has a byte ready within
// timeout, without reading it. The console input handle is signalled while any
// event is queued - also the key-up of the Enter just read, focus, mouse and
// buffer-size events, which never become a byte. Some terminals queue the key-up
// of Enter right after it (Windows Terminal does not), and taking that for "a byte
// follows" made every Enter a pasted newline (and a lone Esc the start of a
// sequence). So only a key-down that is not a bare modifier counts. A handle the
// queue cannot be looked at (not a console) falls back to the plain wait. Whether
// a Ctrl+C pressed while the console waits comes back as the byte 0x03 is not
// verified here; see scripts/wincons.
func WaitTerminalInput(fd int, timeout time.Duration) bool {
	handle := windows.Handle(fd)
	deadline := time.Now().Add(timeout)
	for {
		ready, ok := consoleKeyQueued(handle)
		if !ok {
			event, err := windows.WaitForSingleObject(handle, uint32(time.Until(deadline).Milliseconds()))
			return err == nil && event == windows.WAIT_OBJECT_0
		}
		if ready {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		var queued uint32
		if err := windows.GetNumberOfConsoleInputEvents(handle, &queued); err == nil && queued > 0 {
			// Only events that are no byte are queued: the handle stays signalled,
			// so sleep instead of waiting on it.
			time.Sleep(min(inputPollStep, remaining))
			continue
		}
		event, err := windows.WaitForSingleObject(handle, uint32(remaining.Milliseconds()))
		if err != nil || event != windows.WAIT_OBJECT_0 {
			return false
		}
	}
}

// consoleKeyQueued reports whether the console input queue holds a key-down that
// becomes input; ok is false when the queue cannot be looked at.
func consoleKeyQueued(handle windows.Handle) (ready, ok bool) {
	var records [peekRecords * inputRecordSize]byte
	var read uint32
	r, _, _ := procPeekConsoleInput.Call(uintptr(handle), uintptr(unsafe.Pointer(&records[0])),
		peekRecords, uintptr(unsafe.Pointer(&read)))
	if r == 0 {
		return false, false
	}
	for i := 0; i < int(read); i++ {
		if keyDownIsInput(records[i*inputRecordSize : (i+1)*inputRecordSize]) {
			return true, true
		}
	}
	return false, true
}

// keyDownIsInput reports whether one INPUT_RECORD is a key-down that the console
// turns into input: a KEY_EVENT with bKeyDown set whose key is not a bare
// modifier (Shift, Ctrl, Alt, Caps Lock, Win). The KEY_EVENT_RECORD starts at
// offset 4: bKeyDown (BOOL), wRepeatCount, wVirtualKeyCode, ...
func keyDownIsInput(record []byte) bool {
	if binary.LittleEndian.Uint16(record[0:2]) != keyEventType {
		return false
	}
	if binary.LittleEndian.Uint32(record[4:8]) == 0 {
		return false
	}
	switch binary.LittleEndian.Uint16(record[10:12]) {
	case 0x10, 0x11, 0x12, 0x14, 0x5b, 0x5c, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5:
		return false
	}
	return true
}
