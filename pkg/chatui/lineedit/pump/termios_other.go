//go:build !linux && !darwin

package pump

// RestoreOutputProcessing is a no-op where the tty does not keep output flags in
// the same termios structure: the Windows console (and a ConPTY) handles CR/LF
// translation itself, raw console input included.
func RestoreOutputProcessing(int) error { return nil }

// ClearOutputProcessing is the no-op counterpart of RestoreOutputProcessing: there
// are no output flags to take off on these systems either.
func ClearOutputProcessing(int) error { return nil }
