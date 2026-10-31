//go:build linux

package pump

import "golang.org/x/sys/unix"

// getTermiosRequest and setTermiosRequest are tcgetattr/tcsetattr on Linux.
const (
	getTermiosRequest = unix.TCGETS
	setTermiosRequest = unix.TCSETS
)
