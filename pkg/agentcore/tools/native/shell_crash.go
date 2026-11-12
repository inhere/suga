package native

import "fmt"

// shellCrashCodes names the Windows NTSTATUS values that end a process through
// a crash rather than through its own exit: a PowerShell process that overflows
// its stack while formatting a long pipeline (0xC00000FD) or hits an access
// violation / stack buffer overrun dies this way, taking everything the
// pipeline had not returned yet with it.
var shellCrashCodes = map[uint32]string{
	0xC00000FD: "STATUS_STACK_OVERFLOW",
	0xC0000005: "STATUS_ACCESS_VIOLATION",
	0xC0000409: "STATUS_STACK_BUFFER_OVERRUN",
}

// shellCrashNotice explains that the shell process itself crashed, for a result
// whose exit code is a Windows crash code; it returns "" for any other exit
// code (and so on Linux/macOS, where these codes never appear).
//
// The exit code is compared as uint32: ExitCode() reports the same NTSTATUS as
// a negative int32 or as the unsigned value depending on the platform and Go
// version, and both forms convert to the same uint32.
func shellCrashNotice(exitCode int) string {
	name, ok := shellCrashCodes[uint32(exitCode)]
	if !ok {
		return ""
	}
	// Without this line a crash reads like the command's own failure (suga-oup1:
	// the model took STATUS_STACK_OVERFLOW for a Go stack overflow).
	return fmt.Sprintf("[shell crashed: %s (0x%08X); the shell process itself died, so output still in the pipe never came back to this tool. "+
		"Redirect long output to a file and read that file, or rerun without the pipe.]", name, uint32(exitCode))
}
