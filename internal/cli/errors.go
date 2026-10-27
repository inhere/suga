package cli

import (
	"errors"

	"github.com/inhere/suga/internal/i18n"
)

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

// localizedError is an error that can render its human-facing line in the
// process language: Error() stays a stable fact for machine lines (stream-json,
// traces, logs), LocalizedText is what text mode prints. errors.Is keeps
// answering the sentinel wrapped inside it.
type localizedError interface {
	LocalizedText(tr i18n.Translator) string
}

// ErrorText is the line text mode prints for err: the localized rendering when
// the error has one, else err.Error(). A nil translator keeps err.Error(), so a
// caller without a catalog never loses the message.
func ErrorText(err error, tr i18n.Translator) string {
	var localized localizedError
	if tr != nil && errors.As(err, &localized) {
		return localized.LocalizedText(tr)
	}
	return err.Error()
}
