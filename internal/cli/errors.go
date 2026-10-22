package cli

import "errors"

// processExitError carries a specific process exit code through the command
// tree (send: 2 ambiguous, 3 offline, 4 too large; run: 2 bad usage, 5 --verify
// not passed, 6 --output-schema not satisfied).
type processExitError struct {
	code int
	err  error
}

func (e processExitError) Error() string { return e.err.Error() }
func (e processExitError) Unwrap() error { return e.err }
func (e processExitError) ExitCode() int { return e.code }

// ExitCode maps a command error to the process exit code: the error's own
// ExitCode() when it has one, else 1.
func ExitCode(err error) int {
	var exitCoder interface{ ExitCode() int }
	if errors.As(err, &exitCoder) {
		return exitCoder.ExitCode()
	}
	return 1
}
