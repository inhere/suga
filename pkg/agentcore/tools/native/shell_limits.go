package native

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/internal/proctree"
)

// ShellLimits caps what one shell_exec command and every process it starts may
// use (tools.shell_limits). It is not a sandbox: a command can still write every
// file its user can write and reach the network; it only stops a runaway build
// or test run from eating the machine.
//
// Windows sets every field on the process tree's job object. Unix sets them with
// setrlimit: memory per process and CPU time per process work as expected, while
// the process count is RLIMIT_NPROC - a per-user limit, which suga therefore
// raises by the user's current process count (a command may add roughly
// processes more, and the limit is left off on a Unix without /proc).
type ShellLimits struct {
	// MemoryBytes caps the memory of one process and of the tree; 0 = no cap.
	MemoryBytes uint64
	// Processes caps how many processes the tree may have; 0 = no cap.
	Processes int
	// CPUTime caps the CPU time of one process; 0 = no cap.
	CPUTime time.Duration
}

// Defaults of tools.shell_limits: generous enough for a full Go build, small
// enough that one runaway command cannot take the machine down.
const (
	// DefaultShellLimitMemory is the memory limit when tools.shell_limits.memory
	// is not set (4 GiB).
	DefaultShellLimitMemory uint64 = 4 << 30
	// DefaultShellLimitProcesses is the process limit when
	// tools.shell_limits.processes is not set (256).
	DefaultShellLimitProcesses = 256
)

// Empty reports that no limit is set: a command runs the way it always did.
func (l ShellLimits) Empty() bool {
	return l.MemoryBytes == 0 && l.Processes <= 0 && l.CPUTime <= 0
}

// tree is the proctree form the process tree is created with.
func (l ShellLimits) tree() proctree.Limits {
	return proctree.Limits{MemoryBytes: l.MemoryBytes, Processes: l.Processes, CPUTime: l.CPUTime}
}

// describe names the limits that are on, for the tool result and the
// description ("内存 4GiB / 进程数 256").
func (l ShellLimits) describe() string {
	var parts []string
	if l.MemoryBytes > 0 {
		parts = append(parts, "内存 "+FormatByteSize(l.MemoryBytes))
	}
	if l.Processes > 0 {
		parts = append(parts, fmt.Sprintf("进程数 %d", l.Processes))
	}
	if l.CPUTime > 0 {
		parts = append(parts, "CPU 时间 "+l.CPUTime.String())
	}
	return strings.Join(parts, " / ")
}

// notice is the line a result carries when the run hit a limit: the model (and
// the person reading the trace) has to know the failure is suga's cap and not
// the program's own problem.
func (l ShellLimits) notice() string {
	return fmt.Sprintf("[超出 suga 的资源限制（%s）：进程被终止或分配失败；这是 tools.shell_limits 的上限，不是命令本身的问题]", l.describe())
}

// taskLimitNotice is what a background task asks for its output (tasks.Spec.
// LimitNotice): the notice when the run hit a limit, empty otherwise.
func (l ShellLimits) taskLimitNotice(terminated bool, exitCode int, output string) string {
	if l.limitHit(terminated, exitCode, output) {
		return l.notice()
	}
	return ""
}

// limitHit reports whether a finished run looks like it hit the limits.
// terminated is what the process tree says (the system ended a process of it,
// which Windows job accounting counts); a refused allocation or fork ends
// nothing and only shows in the output, so the output of a failed run is read
// as well (Unix has no counter to ask).
func (l ShellLimits) limitHit(terminated bool, exitCode int, output string) bool {
	if l.Empty() || exitCode == 0 {
		return false
	}
	if terminated {
		return true
	}
	if l.MemoryBytes == 0 && l.Processes == 0 {
		return false // a CPU time limit shows as a killed process, not as text
	}
	return limitHitPattern.MatchString(output)
}

// limitHitPattern matches what an exhausted memory or process limit leaves in a
// program's output: a refused allocation ("out of memory", "MemoryError",
// "bad_alloc", Windows' commitment limit - "errno=1455" or its message "the
// paging file is too small", 页面文件太小 on a localized Windows), a refused fork
// ("resource temporarily unavailable") and the "not enough quota" a full job
// reports (the English text of the same error a localized Windows prints as
// 配额不足).
var limitHitPattern = regexp.MustCompile(`(?i)out of memory|memoryerror|memory error|outofmemory|cannot allocate memory|` +
	`bad_alloc|not enough memory|insufficient memory|heap limit|errno=1455|commitment limit|paging file is too small|页面文件太小|` +
	`resource temporarily unavailable|not enough quota|配额不足|too many processes|cannot create (a )?process|process limit`)

// FormatByteSize renders a byte count the way the limits are configured
// ("4GiB", "512MiB", "64KiB").
func FormatByteSize(n uint64) string {
	for _, unit := range []struct {
		name string
		size uint64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if n < unit.size {
			continue
		}
		v := float64(n) / float64(unit.size)
		if v == math.Trunc(v) {
			return fmt.Sprintf("%d%s", int64(v), unit.name)
		}
		return fmt.Sprintf("%.1f%s", v, unit.name)
	}
	return fmt.Sprintf("%dB", n)
}

// memoryUnits are the suffixes ParseMemorySize accepts, in bytes.
var memoryUnits = map[string]uint64{
	"":    1,
	"b":   1,
	"k":   1 << 10,
	"kb":  1 << 10,
	"kib": 1 << 10,
	"m":   1 << 20,
	"mb":  1 << 20,
	"mib": 1 << 20,
	"g":   1 << 30,
	"gb":  1 << 30,
	"gib": 1 << 30,
	"t":   1 << 40,
	"tb":  1 << 40,
	"tib": 1 << 40,
}

// ParseMemorySize reads tools.shell_limits.memory: a plain byte count
// ("1073741824") or a size with a unit ("4GiB", "512MiB", "1.5G"). "" and "off"
// (also "0") mean no limit; an unknown unit is an error, so a typo is a startup
// warning rather than a silently ignored limit.
func ParseMemorySize(s string) (uint64, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "", "0", "off", "none", "unlimited":
		return 0, nil
	}
	i := 0
	for i < len(v) && (v[i] >= '0' && v[i] <= '9' || v[i] == '.') {
		i++
	}
	num, unit := v[:i], strings.TrimSpace(v[i:])
	if num == "" {
		return 0, fmt.Errorf("invalid memory size %q: no number", s)
	}
	factor, ok := memoryUnits[unit]
	if !ok {
		return 0, fmt.Errorf("invalid memory size %q: unknown unit %q (use B, KiB, MiB, GiB or TiB)", s, unit)
	}
	value, err := strconv.ParseFloat(num, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid memory size %q", s)
	}
	return uint64(value * float64(factor)), nil
}
