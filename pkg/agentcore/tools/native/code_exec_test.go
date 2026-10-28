package native

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

func runShell(t *testing.T, tool *ShellExecTool, args map[string]any) (*api.ToolResult, time.Duration) {
	t.Helper()
	start := time.Now()
	res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: "shell_exec", Arguments: args})
	assert.Require(t, assert.NoErr(t, err))
	return res, time.Since(start)
}

func TestShellExecCallTimeout(t *testing.T) {
	tool := NewShellExecTool(t.TempDir(), 0)
	tool.maxTimeout = 10 * time.Second
	t.Run("default", func(t *testing.T) {
		assert.Eq(t, DefaultShellTimeout, tool.callTimeout(map[string]any{}))
	})
	t.Run("number and string", func(t *testing.T) {
		assert.Eq(t, 3*time.Second, tool.callTimeout(map[string]any{"timeout": float64(3)}))
		assert.Eq(t, 4*time.Second, tool.callTimeout(map[string]any{"timeout": "4"}))
	})
	t.Run("clamped to max", func(t *testing.T) {
		assert.Eq(t, 10*time.Second, tool.callTimeout(map[string]any{"timeout": float64(9999)}))
	})
	t.Run("huge value is clamped without overflow", func(t *testing.T) {
		assert.Eq(t, 10*time.Second, tool.callTimeout(map[string]any{"timeout": float64(12000000031)}))
	})
}

func TestShellExecRequiresCommand(t *testing.T) {
	_, err := NewShellExecTool(t.TempDir(), 0).Execute(context.Background(), api.ToolCall{Arguments: map[string]any{"command": "  "}})
	assert.ErrMsgContains(t, err, "command is required")
}

func TestDecodeOutputFallsBackToGBK(t *testing.T) {
	gbk := []byte{0xd6, 0xd0, 0xce, 0xc4} // "中文" in GBK
	assert.Eq(t, "中文", decodeOutput(gbk))
	assert.Eq(t, "中文", decodeOutput([]byte("中文")))
}

func TestResolveShellExplicit(t *testing.T) {
	assert.True(t, resolveShell(`C:\Program Files\PowerShell\7\pwsh.exe`).powershell)
	assert.False(t, resolveShell("bash").powershell)
	assert.Eq(t, []string{"bash", "-c", "ls"}, resolveShell("bash").argv("ls"))
}

// TestShellExecDescriptionQuoteAndScriptAdvice: PowerShell needs the quoting
// advice (suga-cfeq) and, with an interpreter at hand, the write-a-script
// advice (suga-bkt8); a POSIX shell needs neither.
func TestShellExecDescriptionQuoteAndScriptAdvice(t *testing.T) {
	powerShell := shellSpec{path: "pwsh", powershell: true}
	t.Run("powershell with interpreters", func(t *testing.T) {
		tool := NewShellExecTool(t.TempDir(), 0)
		tool.shell = powerShell
		tool.interpreters = []Interpreter{{Name: "python3", Path: "/usr/bin/python3", Version: "3.12.4"}}
		desc := tool.Description()
		assert.StrContains(t, desc, PowerShellQuoteAdvice)
		assert.StrContains(t, desc, `"-Dincludes=g:a"`)
		assert.StrContains(t, desc, WriteScriptAdvice)
	})
	t.Run("powershell without interpreters", func(t *testing.T) {
		tool := NewShellExecTool(t.TempDir(), 0)
		tool.shell = powerShell
		desc := tool.Description()
		assert.StrContains(t, desc, PowerShellQuoteAdvice)
		assert.NotContains(t, desc, "先用 file_write")
	})
	t.Run("posix shell", func(t *testing.T) {
		tool := NewShellExecTool(t.TempDir(), 0)
		tool.shell = shellSpec{path: "sh"}
		tool.interpreters = []Interpreter{{Name: "node", Path: "/usr/bin/node", Version: "22.6.0"}}
		desc := tool.Description()
		assert.NotContains(t, desc, "整体加引号")
		assert.NotContains(t, desc, "先用 file_write")
		assert.StrContains(t, desc, "POSIX sh syntax")
	})
}

func TestShellExecOutputIsCapped(t *testing.T) {
	tool := NewShellExecTool(t.TempDir(), 0)
	tool.maxOut = 200
	cmd := "for i in $(seq 1 200); do echo line$i; done"
	if tool.shell.powershell {
		cmd = "1..200 | ForEach-Object { \"line$_\" }"
	}
	res, _ := runShell(t, tool, map[string]any{"command": cmd})
	assert.StrContains(t, res.Content, "line1\n")
	assert.StrContains(t, res.Content, "line200")
	assert.StrContains(t, res.Content, "省略")
	assert.StrContains(t, res.Content, "[exit code: 0]")
}

func TestForegroundShellTransfersOutputToTask(t *testing.T) {
	dir := t.TempDir()
	m := tasks.NewManager(tasks.Config{Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, id+".txt"), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(m.Close)
	shell := NewShellExecTool(t.TempDir(), 2*time.Second)
	shell.SetTasks(m)
	shell.backgroundAfter = 100 * time.Millisecond
	shell.maxTimeout = 3 * time.Second
	command := "echo before; sleep 1; echo after"
	if runtime.GOOS == "windows" {
		command = "Write-Output before; Start-Sleep -Milliseconds 1000; Write-Output after"
	}
	ctx := api.WithSessionID(context.Background(), "s1")
	res, err := shell.Execute(ctx, api.ToolCall{CallID: "c", Name: shell.Name(), Arguments: map[string]any{"command": command}})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, res.Content, "transferred to background task t1")
	assert.Eq(t, "t1", res.Metadata["task_id"])

	info, outcome, err := m.Wait(ctx, "s1", "t1", 5*time.Second)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, tasks.WaitEnded, outcome)
	assert.Eq(t, tasks.Exited, info.Status)
	data, err := os.ReadFile(info.Path)
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, string(data), "before")
	assert.StrContains(t, string(data), "after")
}
func TestCommandEnv(t *testing.T) {
	got := commandEnv([]string{"PATH=/bin", "SUGA_HOME=/h", "suga_config=/c", "SUGA_MODEL=m", "SUGA_HOOKS=off", "SUGA_HOMEX=1"})
	assert.Eq(t, []string{"PATH=/bin", "SUGA_HOOKS=off", "SUGA_HOMEX=1"}, got)
}
