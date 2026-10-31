//go:build linux || darwin

package pump

import "golang.org/x/sys/unix"

// RestoreOutputProcessing turns OPOST|ONLCR back on after the tty was put into
// raw mode. Raw input (which chat.steering keeps while a turn runs) clears the
// output flags too, and chat's own output then writes "\n" without the carriage
// return: every line of a tool result, a notice or the model's answer would start
// where the previous one ended (a staircase down the screen). The line editor is
// unaffected - it writes "\r\n" itself.
//
// It is called for the read of a running turn only (outputProcessingForRawLocked):
// a read between turns keeps the flags raw input set, or the terminal would add a
// CR to the line editor's own CR LF.
//
// The request codes differ per system (see termios_*.go); a failure is cosmetic
// (output only), so callers may ignore the error.
func RestoreOutputProcessing(fd int) error {
	termios, err := unix.IoctlGetTermios(fd, getTermiosRequest)
	if err != nil {
		return err
	}
	termios.Oflag |= unix.OPOST | unix.ONLCR
	return unix.IoctlSetTermios(fd, setTermiosRequest, termios)
}

// ClearOutputProcessing takes OPOST back off: the state raw input leaves the output
// flags in (term.MakeRaw clears OPOST, see outputProcessingForRawLocked). It is what
// a read between turns needs, and it exists because entering raw mode a second time
// would save the state of this raw mode as the one to restore to.
func ClearOutputProcessing(fd int) error {
	termios, err := unix.IoctlGetTermios(fd, getTermiosRequest)
	if err != nil {
		return err
	}
	termios.Oflag &^= unix.OPOST
	return unix.IoctlSetTermios(fd, setTermiosRequest, termios)
}
