//go:build windows

package native

import (
	"testing"

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
