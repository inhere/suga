package proctree

import "time"

// Limits caps what a command's process tree may use (tools.shell_limits). The
// zero value keeps every limit off. Windows enforces all three fields on the
// tree's job object; Unix sets them with setrlimit before the shell starts
// (memory and CPU time per process, the process count per user - a limitation
// of RLIMIT_NPROC the documentation spells out). A field the platform cannot
// enforce is ignored rather than approximated.
type Limits struct {
	// MemoryBytes caps the memory of one process and of the whole tree
	// (Windows: ProcessMemoryLimit and JobMemoryLimit; Unix: RLIMIT_DATA).
	MemoryBytes uint64
	// Processes caps how many processes the tree may have at once (Windows:
	// ActiveProcessLimit; Unix: RLIMIT_NPROC, which counts the whole user).
	Processes int
	// CPUTime caps the CPU time one process may use (Windows:
	// PerProcessUserTimeLimit; Unix: RLIMIT_CPU).
	CPUTime time.Duration
}

// Empty reports that the limits put no cap on anything: the tree is created
// exactly as it would be without limits.
func (l Limits) Empty() bool {
	return l.MemoryBytes == 0 && l.Processes <= 0 && l.CPUTime <= 0
}
