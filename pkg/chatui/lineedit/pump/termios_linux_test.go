//go:build linux

package pump

import (
	"fmt"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// TestRestoreOutputProcessing: raw input (which chat.steering keeps while a turn
// runs) clears OPOST|ONLCR along with the input flags, and chat puts the output
// flags back - otherwise anything written straight to stderr during the turn (a
// hook notice, a warning) would walk down the screen instead of starting a line.
// ClearOutputProcessing is the counterpart for a read between turns: the flags go
// back to what raw input left, so the line editor's own CR LF is not doubled.
func TestRestoreOutputProcessing(t *testing.T) {
	slave := openTestPTY(t)

	_, err := term.MakeRaw(slave)
	assert.NoErr(t, err)
	raw, err := unix.IoctlGetTermios(slave, getTermiosRequest)
	assert.NoErr(t, err)
	assert.True(t, raw.Oflag&unix.OPOST == 0) // raw mode cleared it

	assert.NoErr(t, RestoreOutputProcessing(slave))
	got, err := unix.IoctlGetTermios(slave, getTermiosRequest)
	assert.NoErr(t, err)
	assert.True(t, got.Oflag&unix.OPOST != 0)
	assert.True(t, got.Oflag&unix.ONLCR != 0)

	assert.NoErr(t, ClearOutputProcessing(slave))
	got, err = unix.IoctlGetTermios(slave, getTermiosRequest)
	assert.NoErr(t, err)
	assert.True(t, got.Oflag&unix.OPOST == 0)
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
