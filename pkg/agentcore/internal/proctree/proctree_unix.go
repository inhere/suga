//go:build !windows

package proctree

import (
	"fmt"
	"math"
	"os/exec"
	"strings"
	"syscall"
)

// Tree is the command's process group: the shell starts a new group,
// so killing -pid reaches every child it spawned.
type Tree struct{ cmd *exec.Cmd }

// New prepares cmd (before Start) so its whole process tree can be killed.
func New(cmd *exec.Cmd) *Tree { return NewLimited(cmd, Limits{}) }

// NewLimited is New with a resource cap on the tree: the limits are set with
// setrlimit in a short sh wrapper before the shell itself starts, so the whole
// tree inherits them. A limit that cannot be set here (a shell without that
// ulimit flag) leaves that one limit off instead of failing the command.
func NewLimited(cmd *exec.Cmd, limits Limits) *Tree {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	applyLimits(cmd, limits)
	return &Tree{cmd: cmd}
}

// Started is a no-op: the group exists from Start.
func (p *Tree) Started() error { return nil }

// Kill sends SIGKILL to the whole group.
func (p *Tree) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
}

// Close kills children the command left running in the background.
func (p *Tree) Close() { _ = p.Kill() }

// Exceeded reports whether a limit ended a process of the tree. Unix has no
// counter for that (setrlimit makes the allocation or the fork itself fail), so
// the tool reads the command's own output instead: see ShellLimits.limitHit.
func (p *Tree) Exceeded() bool { return false }

// applyLimits rewrites cmd to run through sh, which sets the limits and then
// execs the real argv: setrlimit cannot be applied to a child from Go's
// SysProcAttr, and a wrapper keeps it to the shell suga already depends on.
func applyLimits(cmd *exec.Cmd, limits Limits) {
	if limits.Empty() || len(cmd.Args) == 0 {
		return
	}
	var script strings.Builder
	// The hard limit goes with the soft one (no -S): a command then cannot raise
	// its own limit back. A shell or kernel that does not know one of these
	// leaves that one limit off, the rest still apply.
	//
	// Memory is RLIMIT_DATA (ulimit -d), not RLIMIT_AS (ulimit -v): Go, Java and
	// Node reserve large inaccessible address ranges at start, which RLIMIT_AS
	// counts, so a 1GiB virtual cap failed a Go program allocating 64MiB. Linux
	// (4.7+) counts private writable mappings in RLIMIT_DATA - the memory a
	// program really uses - and not those reservations.
	if limits.MemoryBytes > 0 {
		fmt.Fprintf(&script, "ulimit -d %d 2>/dev/null\n", limits.MemoryBytes/1024)
	}
	if n := limitProcesses(limits.Processes); n > 0 {
		fmt.Fprintf(&script, "ulimit -u %d 2>/dev/null\n", n)
	}
	if limits.CPUTime > 0 {
		secs := int(math.Ceil(limits.CPUTime.Seconds()))
		fmt.Fprintf(&script, "ulimit -t %d 2>/dev/null\n", secs)
	}
	script.WriteString(`exec "$0" "$@"`)
	sh := shellPath()
	// argv[0] is the shell path: the wrapper execs it with the rest of argv.
	cmd.Args = append([]string{"sh", "-c", script.String(), cmd.Path}, cmd.Args[1:]...)
	cmd.Path = sh
}

// limitProcesses is the RLIMIT_NPROC value for an allowance of allow processes:
// the user's own count plus the allowance (0 = no process limit).
func limitProcesses(allow int) int {
	if allow <= 0 {
		return 0
	}
	current := userProcessCount()
	if current == 0 {
		return 0 // unknown (no /proc): leave the limit off
	}
	return current + allow
}

// shellPath is the sh the wrapper runs through.
func shellPath() string {
	if path, err := exec.LookPath("sh"); err == nil {
		return path
	}
	return "/bin/sh"
}
