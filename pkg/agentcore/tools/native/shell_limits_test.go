package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// allocEnv makes the helper below allocate memory and busyEnv makes it burn
// CPU: a shell command cannot do either portably (PowerShell on Windows, sh
// elsewhere), so the tests run this test binary again, driven by the variables.
const (
	allocEnv = "SUGA_TEST_ALLOC_MB"
	busyEnv  = "SUGA_TEST_BUSY"
)

// TestShellLimitsHelperProcess is the process the limit tests run: with
// SUGA_TEST_ALLOC_MB set it asks the OS for that many MiB, with SUGA_TEST_BUSY
// set it burns CPU, and either way it exits 0 unless a limit stopped it.
func TestShellLimitsHelperProcess(t *testing.T) {
	if os.Getenv(busyEnv) != "" {
		burnCPU(60 * time.Second)
		return
	}
	raw := os.Getenv(allocEnv)
	if raw == "" {
		return // the ordinary test run: not the helper
	}
	mb, err := strconv.Atoi(raw)
	if err != nil || mb <= 0 {
		t.Fatalf("SUGA_TEST_ALLOC_MB=%q", raw)
	}
	if err := probeAlloc(uint64(mb) << 20); err != nil {
		fmt.Fprintf(os.Stderr, "allocation refused: %v\n", err)
		os.Exit(3)
	}
	fmt.Printf("allocated %d MiB\n", mb)
}

// burnCPU keeps one core busy for d (the CPU time limit ends the process long
// before that).
func burnCPU(d time.Duration) {
	deadline := time.Now().Add(d)
	x := 0
	for time.Now().Before(deadline) {
		for i := 0; i < 1e6; i++ {
			x += i % 7
		}
	}
	fmt.Println("burned CPU", x)
}

// helperCommand is the shell command line that runs the helper with mb MiB. The
// flag is quoted because PowerShell splits an unquoted -test.run=... in two, and
// a quoted path needs PowerShell's call operator (sh runs it as it is).
func helperCommand(t *testing.T, mb int) string {
	t.Helper()
	t.Setenv(allocEnv, strconv.Itoa(mb))
	return helperLine()
}

// helperLine is the helper's command line with the environment of the test.
func helperLine() string {
	flag := " '-test.run=^TestShellLimitsHelperProcess$'"
	if runtime.GOOS == "windows" {
		return `& "` + os.Args[0] + `"` + flag
	}
	return `"` + os.Args[0] + `"` + flag
}

// limitTool is a shell_exec tool with the given limits, running in ws.
func limitTool(ws string, limits ShellLimits) *ShellExecTool {
	tool := NewShellExecTool(ws, time.Minute)
	tool.limits = limits
	return tool
}

// runShell executes command and returns the result content.
func runLimitedShell(t *testing.T, tool *ShellExecTool, command string) (string, map[string]any) {
	t.Helper()
	res, err := tool.Execute(context.Background(), api.ToolCall{Name: "shell_exec",
		Arguments: map[string]any{"command": command}})
	assert.Require(t, assert.NoErr(t, err))
	return res.Content, res.Metadata
}

// TestShellLimitsMemoryLimit: a command over the memory limit fails and its
// result says the limit did it, not the program; the same command stays well
// inside a limit that is high enough, and an empty limit caps nothing.
func TestShellLimitsMemoryLimit(t *testing.T) {
	ws := t.TempDir()

	t.Run("a command over the memory limit is stopped", func(t *testing.T) {
		tool := limitTool(ws, ShellLimits{MemoryBytes: 1 << 30})          // 1GiB
		content, meta := runLimitedShell(t, tool, helperCommand(t, 4096)) // 4GiB
		assert.StrContains(t, content, "超出 suga 的资源限制")
		assert.StrContains(t, content, "内存 1GiB")
		assert.Eq(t, true, meta["limits_exceeded"], "the trace metadata marks the run")
		assert.True(t, meta["exit_code"] != 0, "the command failed: %v", content)
	})

	t.Run("a command inside the limit is not affected", func(t *testing.T) {
		tool := limitTool(ws, ShellLimits{MemoryBytes: 1 << 30})
		content, meta := runLimitedShell(t, tool, helperCommand(t, 64))
		assert.StrNotContains(t, content, "超出 suga 的资源限制")
		assert.Eq(t, 0, meta["exit_code"], content)
		assert.StrContains(t, content, "[exit code: 0]")
	})

	t.Run("off limits nothing", func(t *testing.T) {
		tool := limitTool(ws, ShellLimits{})
		content, meta := runLimitedShell(t, tool, helperCommand(t, 64))
		assert.Eq(t, 0, meta["exit_code"], content)
		assert.StrNotContains(t, content, "超出 suga 的资源限制")
	})
}

// TestShellLimitsBackgroundTask: a background run carries the same limits and
// the notice is written into the task output, which is all the model sees of a
// background command.
func TestShellLimitsBackgroundTask(t *testing.T) {
	dir := t.TempDir()
	m := tasks.NewManager(tasks.Config{Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, id+".txt"), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(m.Close)
	shell := limitTool(t.TempDir(), ShellLimits{MemoryBytes: 1 << 30})
	shell.SetTasks(m)
	ctx := api.WithSessionID(context.Background(), "s1")
	res, err := shell.Execute(ctx, api.ToolCall{CallID: "c", Name: "shell_exec", Arguments: map[string]any{
		"command": helperCommand(t, 4096), "background": true}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "t1", res.Metadata["task_id"])
	waited, err := NewTaskTool(m, 0).Execute(ctx, api.ToolCall{CallID: "c2", Name: "task",
		Arguments: map[string]any{"action": "wait", "id": "t1", "timeout": 60}})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, waited.Content, "超出 suga 的资源限制")
}

// TestParseMemorySize covers the tools.shell_limits.memory forms: a byte count,
// a size with a unit, and the values that mean "no limit".
func TestParseMemorySize(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		bad  bool
	}{
		{in: "4GiB", want: 4 << 30},
		{in: "512MiB", want: 512 << 20},
		{in: "1.5G", want: 3 << 29},
		{in: "256k", want: 256 << 10},
		{in: "1073741824", want: 1 << 30},
		{in: "128B", want: 128},
		{in: "", want: 0},
		{in: "0", want: 0},
		{in: "off", want: 0},
		{in: " Off ", want: 0},
		{in: "4XB", bad: true},
		{in: "abc", bad: true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseMemorySize(c.in)
			if c.bad {
				assert.Err(t, err)
				return
			}
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, c.want, got)
		})
	}
}

// TestShellLimitsDescribe covers the text a result and the tool description
// carry: only the limits that are on, in the configured form.
func TestShellLimitsDescribe(t *testing.T) {
	t.Run("all limits", func(t *testing.T) {
		l := ShellLimits{MemoryBytes: 4 << 30, Processes: 256, CPUTime: 10 * time.Minute}
		assert.Eq(t, "内存 4GiB / 进程数 256 / CPU 时间 10m0s", l.describe())
		assert.False(t, l.Empty())
	})
	t.Run("only processes", func(t *testing.T) {
		assert.Eq(t, "进程数 8", ShellLimits{Processes: 8}.describe())
	})
	t.Run("none", func(t *testing.T) {
		assert.True(t, ShellLimits{}.Empty())
		assert.Eq(t, "", ShellLimits{}.describe())
	})
	t.Run("byte sizes", func(t *testing.T) {
		assert.Eq(t, "512B", FormatByteSize(512))
		assert.Eq(t, "64KiB", FormatByteSize(64<<10))
		assert.Eq(t, "1.5MiB", FormatByteSize(3<<19))
		assert.Eq(t, "4GiB", FormatByteSize(4<<30))
	})
}

// TestShellLimitsHitPattern: a limit that refuses an allocation or a fork ends
// nothing, so the tool reads what the failed program printed. A limit that is
// off never turns a failure into "suga's limit".
func TestShellLimitsHitPattern(t *testing.T) {
	limits := ShellLimits{MemoryBytes: 1 << 30, Processes: 256}
	cases := []struct {
		name       string
		limits     ShellLimits
		terminated bool
		exit       int
		output     string
		want       bool
	}{
		{name: "go runtime out of memory", limits: limits, exit: 2, output: "fatal error: runtime: out of memory", want: true},
		{name: "python MemoryError", limits: limits, exit: 1, output: "MemoryError", want: true},
		{name: "windows commitment limit", limits: limits, exit: 3, output: "VirtualAlloc of 4294967296 bytes failed with errno=1455", want: true},
		{name: "windows paging file", limits: limits, exit: 3, output: "allocation refused: The paging file is too small for this operation to complete.", want: true},
		{name: "refused fork", limits: limits, exit: 127, output: "sh: 1: fork: retry: Resource temporarily unavailable", want: true},
		{name: "windows quota", limits: limits, exit: 1, output: "Not enough quota is available to process this command.", want: true},
		{name: "a process the job ended", limits: limits, terminated: true, exit: -1, output: "", want: true},
		{name: "another failure is no limit", limits: limits, exit: 1, output: "undefined: foo", want: false},
		{name: "a run that succeeded", limits: limits, exit: 0, output: "out of memory", want: false},
		{name: "limits off", limits: ShellLimits{}, exit: 2, output: "out of memory", want: false},
		{name: "only a CPU limit is not read from the text", limits: ShellLimits{CPUTime: time.Minute}, exit: 2, output: "out of memory", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Eq(t, c.want, c.limits.limitHit(c.terminated, c.exit, c.output))
		})
	}
	t.Run("the notice names the limits", func(t *testing.T) {
		note := limits.notice()
		assert.StrContains(t, note, "内存 1GiB")
		assert.StrContains(t, note, "进程数 256")
		assert.StrContains(t, note, "tools.shell_limits")
	})
	t.Run("the description warns about the caps", func(t *testing.T) {
		tool := limitTool(t.TempDir(), limits)
		assert.StrContains(t, tool.Description(), "内存 1GiB")
		assert.StrContains(t, tool.Description(), "tools.shell_limits")
		assert.StrNotContains(t, limitTool(t.TempDir(), ShellLimits{}).Description(), "tools.shell_limits")
	})
}

// TestShellLimitsCommandNamesLimits: the tool description the model reads names
// the caps once they are on (see above) and a normal command still runs.
func TestShellLimitsNormalCommands(t *testing.T) {
	ws := t.TempDir()
	tool := limitTool(ws, ShellLimits{MemoryBytes: 4 << 30, Processes: 256})
	command := "echo hi"
	if runtime.GOOS == "windows" {
		command = "Write-Output hi"
	}
	content, meta := runLimitedShell(t, tool, command)
	assert.Eq(t, 0, meta["exit_code"], content)
	assert.StrContains(t, strings.TrimSpace(content), "hi")
}
