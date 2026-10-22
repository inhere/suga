//go:build windows

package native

import "golang.org/x/sys/windows"

// probeAlloc commits n bytes and returns the OS error when it cannot. The
// helper uses the OS call rather than a Go allocation so a refused request
// fails cleanly: a Go allocation over the job limit takes the runtime's fatal
// out-of-memory path, which dumps megabytes of stack and can linger, and the
// test wants the plain failure a runaway build shows.
func probeAlloc(n uint64) error {
	addr, err := windows.VirtualAlloc(0, uintptr(n), windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return err
	}
	return windows.VirtualFree(addr, 0, windows.MEM_RELEASE)
}
