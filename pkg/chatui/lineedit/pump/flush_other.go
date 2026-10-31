//go:build !linux && !darwin && !windows

package pump

// FlushTerminalInput is a no-op where no flush call is wired up; Ask still
// drops bytes already held by the reader.
func FlushTerminalInput(int) error { return nil }
