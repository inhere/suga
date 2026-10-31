//go:build linux || darwin

package pump

import (
	"time"

	"golang.org/x/sys/unix"
)

// WaitInputSupported reports that WaitTerminalInput really asks the platform
// whether a byte is ready: the decisions built on it (a lone Esc, a pasted
// newline) may then trust an answer of "nothing ready".
const WaitInputSupported = true

// WaitTerminalInput reports whether the terminal has a byte ready within timeout,
// without reading it (poll(2)): the wait behind the lone-Esc decision, which must
// not leave a group+blocked read behind. A poll that fails (a closed terminal, no
// such fd) reports nothing ready, which keeps the Esc byte in the editor as the
// start of a sequence.
func WaitTerminalInput(fd int, timeout time.Duration) bool {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	ms := int(timeout / time.Millisecond)
	n, err := unix.Poll(fds, ms)
	return err == nil && n > 0
}
