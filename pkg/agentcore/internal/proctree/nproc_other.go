//go:build !windows && !linux

package proctree

// userProcessCount is unknown on a Unix that has no /proc: the process limit is
// left out there instead of guessing (RLIMIT_NPROC counts the whole user, and a
// wrong guess would refuse every fork of every command).
func userProcessCount() int { return 0 }
