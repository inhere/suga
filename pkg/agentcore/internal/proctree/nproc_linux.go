//go:build linux

package proctree

import (
	"os"
	"strconv"
	"strings"
)

// userProcessCount counts the processes of the current user (0 when the count
// is unknown). RLIMIT_NPROC counts every process of the user rather than the
// ones of one tree, so the limit for a command is set to this count plus the
// configured allowance: without the adjustment a machine already running more
// processes than the limit would refuse every fork of every command.
func userProcessCount() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	uid := strconv.Itoa(os.Geteuid())
	count := 0
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		// status carries the real uid of the process ("Uid:\t<real>\t<eff>...").
		data, err := os.ReadFile("/proc/" + entry.Name() + "/status")
		if err != nil {
			continue // the process ended while scanning
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			rest, ok := strings.CutPrefix(line, "Uid:")
			if !ok {
				continue
			}
			if fields := strings.Fields(rest); len(fields) > 0 && fields[0] == uid {
				count++
			}
			break
		}
	}
	return count
}
