package wiring

import (
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// TestNativeEnvShellLimitsReachShellTool: tools.shell_limits is read into the
// native environment and reaches shell_exec's description through the catalog -
// the path a running agent's tool table takes.
func TestNativeEnvShellLimitsReachShellTool(t *testing.T) {
	resetConfig(t)

	t.Run("configured limits reach the tool", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.shell_limits.memory", "2GiB"))
		assert.NoErr(t, gkconfig.Set("tools.shell_limits.processes", 64))
		assert.NoErr(t, gkconfig.Set("tools.shell_limits.cpu_time", "10m"))

		env := nativeEnv(t.TempDir(), nil)
		assert.Eq(t, uint64(2<<30), env.ShellLimits.MemoryBytes)
		assert.Eq(t, 64, env.ShellLimits.Processes)
		assert.Eq(t, 10*time.Minute, env.ShellLimits.CPUTime)
		desc := shellExecDescription(t, env)
		assert.StrContains(t, desc, "内存 2GiB")
		assert.StrContains(t, desc, "进程数 64")
		assert.StrContains(t, desc, "CPU 时间 10m0s")
	})

	t.Run("defaults when the section is absent", func(t *testing.T) {
		resetConfig(t)
		env := nativeEnv(t.TempDir(), nil)
		assert.Eq(t, native.DefaultShellLimitMemory, env.ShellLimits.MemoryBytes)
		assert.Eq(t, native.DefaultShellLimitProcesses, env.ShellLimits.Processes)
		assert.Eq(t, time.Duration(0), env.ShellLimits.CPUTime)
	})

	t.Run("off turns every limit off", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.shell_limits.off", true))

		env := nativeEnv(t.TempDir(), nil)
		assert.True(t, env.ShellLimits.Empty())
		assert.StrNotContains(t, shellExecDescription(t, env), "tools.shell_limits")
	})

	t.Run("a single limit can be turned off", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.shell_limits.memory", "off"))
		assert.NoErr(t, gkconfig.Set("tools.shell_limits.processes", 0))

		env := nativeEnv(t.TempDir(), nil)
		assert.True(t, env.ShellLimits.Empty())
	})

	t.Run("an unreadable size keeps the default", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.shell_limits.memory", "4GBytes"))

		env := nativeEnv(t.TempDir(), nil)
		assert.Eq(t, native.DefaultShellLimitMemory, env.ShellLimits.MemoryBytes)
	})
}

// shellExecDescription is the description of the catalog's shell_exec for env.
func shellExecDescription(t *testing.T, env native.Env) string {
	t.Helper()
	for _, tool := range native.BuildTools(env, true) {
		if tool.Name() == "shell_exec" {
			return tool.Description()
		}
	}
	assert.Fail(t, "shell_exec is not in the catalog")
	return ""
}
