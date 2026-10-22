//go:build !windows

package native

import "syscall"

// probeAlloc maps n bytes and returns the OS error when it cannot (RLIMIT_AS
// refuses it with "cannot allocate memory"). The helper uses the OS call rather
// than a Go allocation so a refused request fails cleanly: a Go allocation over
// the limit takes the runtime's fatal out-of-memory path, which dumps megabytes
// of stack and can linger, and the test wants the plain failure a runaway build
// shows.
func probeAlloc(n uint64) error {
	b, err := syscall.Mmap(-1, 0, int(n), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return err
	}
	return syscall.Munmap(b)
}
