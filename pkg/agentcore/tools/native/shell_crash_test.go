package native

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestShellCrashNotice: a Windows crash code names the NTSTATUS that ended the
// shell and says the pipeline's output is gone; an ordinary exit code says
// nothing (suga-oup1).
func TestShellCrashNotice(t *testing.T) {
	// A crash code reaches ExitCode() as the unsigned NTSTATUS or as its negative
	// int32 form depending on the platform; both must be recognised.
	crash := func(code uint32) int { return int(code) }
	signed := func(code uint32) int { return int(int32(code)) }
	tests := []struct {
		name     string
		exitCode int
		want     string // "" = no notice
	}{
		{name: "stack overflow unsigned", exitCode: crash(0xC00000FD), want: "STATUS_STACK_OVERFLOW"},
		{name: "stack overflow int32", exitCode: signed(0xC00000FD), want: "STATUS_STACK_OVERFLOW"},
		{name: "access violation", exitCode: signed(0xC0000005), want: "STATUS_ACCESS_VIOLATION"},
		{name: "stack buffer overrun", exitCode: crash(0xC0000409), want: "STATUS_STACK_BUFFER_OVERRUN"},
		{name: "success", exitCode: 0},
		{name: "failure", exitCode: 1},
		{name: "unknown", exitCode: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice := shellCrashNotice(tt.exitCode)
			if tt.want == "" {
				assert.Eq(t, "", notice)
				return
			}
			assert.StrContains(t, notice, tt.want)
			assert.StrContains(t, notice, "crashed")
			assert.StrContains(t, notice, "never came back to this tool")
		})
	}
}

// TestShellExecDescriptionNoManualTruncation: with PowerShell the description
// tells the model not to cut a long command's output with Select-Object, which
// can crash the shell process itself (suga-oup1).
func TestShellExecDescriptionNoManualTruncation(t *testing.T) {
	t.Run("powershell", func(t *testing.T) {
		tool := NewShellExecTool(t.TempDir(), 0)
		tool.shell = shellSpec{path: "pwsh", powershell: true}
		assert.StrContains(t, tool.Description(), "do not truncate with | Select-Object -First N")
	})
	t.Run("posix shell", func(t *testing.T) {
		tool := NewShellExecTool(t.TempDir(), 0)
		tool.shell = shellSpec{path: "sh"}
		assert.NotContains(t, tool.Description(), "Select-Object")
	})
}
