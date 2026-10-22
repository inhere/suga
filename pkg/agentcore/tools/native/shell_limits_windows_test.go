//go:build windows

package native

import (
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// TestShellLimitsProcessLimit: the job object's ActiveProcessLimit refuses a
// command that starts more processes than the limit, and the result says the
// limit refused them instead of showing the shell's own error only.
func TestShellLimitsProcessLimit(t *testing.T) {
	tool := limitTool(t.TempDir(), ShellLimits{Processes: 4})
	// Each child lives about a second, so the ones past the limit are started
	// while the earlier ones still run; the refused ones decide the exit code.
	command := `$refused = 0; ` +
		`1..10 | ForEach-Object { try { $null = Start-Process -PassThru -NoNewWindow -ErrorAction Stop ` +
		`-FilePath $env:ComSpec -ArgumentList '/c','ping -n 2 127.0.0.1' } catch { $refused++ } }; ` +
		`if ($refused -gt 0) { Write-Output "$refused processes were refused"; exit 1 }`
	content, meta := runLimitedShell(t, tool, command)
	assert.True(t, meta["exit_code"] != 0, "starting past the limit fails the command: %v", content)
	assert.StrContains(t, content, "超出 suga 的资源限制")
	assert.StrContains(t, content, "进程数 4")
}

// TestShellLimitsCPUTimeLimit: the job object's PerProcessUserTimeLimit ends a
// process that burns too much CPU, and the job accounting reports it (the only
// signal for a limit that ends a process instead of failing an allocation).
func TestShellLimitsCPUTimeLimit(t *testing.T) {
	tool := limitTool(t.TempDir(), ShellLimits{CPUTime: 5 * time.Second})
	content, meta := runLimitedShell(t, tool, busyCommand(t))
	assert.True(t, meta["exit_code"] != 0, "the run was ended: %v", content)
	assert.Eq(t, true, meta["limits_exceeded"])
	assert.StrContains(t, content, "超出 suga 的资源限制")
	assert.StrContains(t, content, "CPU 时间 5s")
}

// busyCommand is the helper command line that burns CPU instead of allocating
// (only the Windows CPU-time test uses it).
func busyCommand(t *testing.T) string {
	t.Helper()
	t.Setenv(busyEnv, "1")
	return helperLine()
}
