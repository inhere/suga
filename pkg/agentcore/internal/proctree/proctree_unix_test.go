//go:build !windows

package proctree

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// TestApplyLimitsSetsRlimits: the wrapper sets the limits with setrlimit before
// the shell starts, so a command sees them as its own limits (hard and soft: it
// cannot raise them back). Unix has no counter for "a limit ended this", which
// is why the value itself is checked.
func TestApplyLimitsSetsRlimits(t *testing.T) {
	cmd := exec.Command("sh", "-c", "ulimit -v; ulimit -t")
	applyLimits(cmd, Limits{MemoryBytes: 512 << 20, CPUTime: 30 * time.Second})
	out, err := cmd.Output()
	assert.Require(t, assert.NoErr(t, err))
	lines := strings.Fields(string(out))
	assert.Require(t, assert.Eq(t, 2, len(lines)))
	assert.Eq(t, strconv.Itoa(512<<10), lines[0], "RLIMIT_AS in KiB")
	assert.Eq(t, "30", lines[1], "RLIMIT_CPU in seconds")
}

// TestApplyLimitsLeavesEmptyLimitsAlone: without limits the command runs exactly
// as before (no wrapper, no extra process).
func TestApplyLimitsLeavesEmptyLimitsAlone(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo hi")
	before := append([]string(nil), cmd.Args...)
	applyLimits(cmd, Limits{})
	assert.Eq(t, before, cmd.Args)
	New(cmd)
	assert.Eq(t, before, cmd.Args, "New without limits keeps the command line")
}

// TestNewLimitedKeepsProcessGroup: the limits are added without losing the
// process group the tree is killed with.
func TestNewLimitedKeepsProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo hi")
	tree := NewLimited(cmd, Limits{MemoryBytes: 256 << 20})
	assert.Require(t, assert.NotNil(t, tree))
	assert.True(t, cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid, "the process group is still set")
	assert.Eq(t, "sh", cmd.Args[0])
	assert.StrContains(t, cmd.Args[2], "ulimit -v 262144")
	assert.StrContains(t, cmd.Args[2], `exec "$0" "$@"`)
	assert.Eq(t, cmd.Path, cmd.Args[3], "the wrapper execs the real command")
}
