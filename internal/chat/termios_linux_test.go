//go:build linux

package chat

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"golang.org/x/sys/unix"
)

// TestTerminalSizeFD: the terminal size is read from the output end when it is a
// terminal (Windows needs a screen-buffer handle for GetConsoleScreenBufferInfo,
// and the input handle fails inside a ConPTY - the bug that kept the line editor at
// its 80-column default). Without a terminal at all there is nothing to read.
func TestTerminalSizeFD(t *testing.T) {
	t.Run("a pty is a terminal on either end", func(t *testing.T) {
		// The *os.File gets a dup of the slave: an os.File closes its fd when it
		// is closed or garbage collected, and openTestPTY's cleanup closes the
		// slave already. Wrapping the same number twice closed it twice; by the
		// second close (a GC finalizer) the number had been reused, once by the
		// coverage writer, which failed CI with "bad file descriptor" (suga-f3e7).
		dup, err := unix.Dup(openTestPTY(t))
		assert.Require(t, assert.NoErr(t, err))
		slave := os.NewFile(uintptr(dup), "pty")
		t.Cleanup(func() { _ = slave.Close() })
		assert.Eq(t, dup, terminalSizeFD(slave, slave))
	})

	t.Run("a pipe is not a terminal", func(t *testing.T) {
		assert.Eq(t, -1, terminalSizeFD(bytes.NewReader(nil), &bytes.Buffer{}))
	})
}

// openTestPTY opens a pty (/dev/ptmx plus its /dev/pts/N slave) and returns the
// slave; a host without /dev/ptmx (some sandboxes) skips the test instead of
// failing it.
func openTestPTY(t *testing.T) (slave int) {
	t.Helper()
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no /dev/ptmx:", err)
	}
	t.Cleanup(func() { _ = unix.Close(master) })
	// unlockpt: clear TIOCSPTLCK, then ask for the slave's number.
	assert.NoErr(t, unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0))
	n, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	assert.NoErr(t, err)
	slave, err = unix.Open(fmt.Sprintf("/dev/pts/%d", n), unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skip("cannot open the pty slave:", err)
	}
	t.Cleanup(func() { _ = unix.Close(slave) })
	return slave
}
