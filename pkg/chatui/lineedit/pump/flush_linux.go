//go:build linux

package pump

import "golang.org/x/sys/unix"

// FlushTerminalInput discards terminal input received but not read yet
// (tcflush(fd, TCIFLUSH)).
func FlushTerminalInput(fd int) error {
	return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
