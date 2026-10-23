//go:build windows

package proctree

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"golang.org/x/sys/windows"
)

// childPIDFile polls until the launcher wrote the pid of the spawned child and
// returns it; the child is a ping that would run on its own for minutes.
func childPIDFile(t *testing.T, path string, out *bytes.Buffer) uint32 {
	t.Helper()
	// A cold PowerShell on a CI runner can take well over 15 s to start
	// (v0.46.0 windows-latest), so the wait is generous; the launcher's output
	// is printed when it never writes the pid.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			var pid uint32
			if _, err := fmt.Sscanf(string(data), "%d", &pid); err == nil && pid != 0 {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the launcher never wrote the child pid; its output:\n%s", out.String())
	return 0
}

// waitProcessGone fails the test if the process of pid (or any process holding
// the reused pid later) still runs within within; a pid Windows cannot open is
// considered gone.
func waitProcessGone(t *testing.T, pid uint32, within time.Duration) {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, uint32(within.Milliseconds()))
	assert.Require(t, assert.Nil(t, err), "waiting a live process handle only fails", err)
	assert.Eq(t, uint32(windows.WAIT_OBJECT_0), ev, "the child outside the job was never killed")
}

func TestWindowsNewSuspendsUntilStarted(t *testing.T) {
	t.Run("the shell is started suspended", func(t *testing.T) {
		cmd := exec.Command("cmd", "/c", "echo ok")
		New(cmd)
		assert.True(t, cmd.SysProcAttr != nil &&
			cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED != 0,
			"New marks the command to be started suspended")
	})

	t.Run("an existing SysProcAttr is kept", func(t *testing.T) {
		cmd := exec.Command("cmd", "/c", "echo ok")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		NewLimited(cmd, Limits{MemoryBytes: 64 << 20, Processes: 8})
		flags := cmd.SysProcAttr.CreationFlags
		assert.True(t, cmd.SysProcAttr.HideWindow, "the caller's SysProcAttr must survive")
		assert.True(t, flags&windows.CREATE_NO_WINDOW != 0 && flags&windows.CREATE_SUSPENDED != 0,
			"the caller's creation flags are kept next to the suspend flag")
		assert.True(t, cmd.SysProcAttr.CreationFlags&flags == flags, "flags are written in place")
	})

	t.Run("the run succeeds once Started resumed it", func(t *testing.T) {
		cmd := exec.Command("cmd", "/c", "echo ok")
		tree := NewLimited(cmd, Limits{})
		assert.Require(t, assert.NoError(t, cmd.Start()), "start")
		assert.NoError(t, tree.Started(), "the suspended shell joins the job and resumes")
		assert.NoError(t, cmd.Wait(), "the resumed shell ran to the end")
		tree.Close()
		assert.False(t, tree.Exceeded(), "nothing hit a limit")
	})
}

// TestWindowsJobKillsSpawnedChild starts a shell that detaches a long running
// child, then ends the tree: the child must share the job's fate (and the job's
// active process limit must see it).
func TestWindowsJobKillsSpawnedChild(t *testing.T) {
	t.Run("kill ends a detached child", func(t *testing.T) {
		pidfile := filepath.Join(t.TempDir(), "child.pid")
		// PowerShell runs as the direct child and reports the pid of the ping
		// process it detached, then exits; the ping alone would run for minutes.
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf("$p = Start-Process cmd -ArgumentList '/c','ping -n 600 127.0.0.1 > NUL' -PassThru; Set-Content -Path '%s' -Value $p.Id", pidfile))
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		cmd.WaitDelay = 2 * time.Second // the detached child inherits the pipe
		tree := New(cmd)
		assert.Require(t, assert.NoError(t, cmd.Start()), "start")
		assert.Require(t, assert.NoError(t, tree.Started()), "assign the shell to the job")
		pid := childPIDFile(t, pidfile, &out)
		time.Sleep(300 * time.Millisecond) // the child is up and running now
		assert.NoError(t, tree.Kill(), "kill the tree")
		_ = cmd.Wait()
		tree.Close()
		waitProcessGone(t, pid, 5*time.Second)
	})

	t.Run("the active process limit counts the detached child", func(t *testing.T) {
		pidfile := filepath.Join(t.TempDir(), "child.pid")
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf("$p = Start-Process cmd -ArgumentList '/c','ping -n 600 127.0.0.1 > NUL' -PassThru; Set-Content -Path '%s' -Value $p.Id", pidfile))
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		cmd.WaitDelay = 2 * time.Second
		tree := NewLimited(cmd, Limits{Processes: 1}) // the shell fills the limit
		assert.Require(t, assert.NoError(t, cmd.Start()), "start")
		assert.Require(t, assert.NoError(t, tree.Started()), "assign the shell to the job")
		waitErr := cmd.Wait()
		tree.Close()
		// The system refuses to create a child beyond the job's one active
		// process quota (Start-Process fails with "not enough quota"), but
		// PowerShell still runs the last statement of its script, so the pid
		// file appears empty instead of holding a pid of a created child.
		assert.False(t, tree.Exceeded(), "a refused creation does not end a process")
		time.Sleep(500 * time.Millisecond)
		data, rerr := os.ReadFile(pidfile)
		assert.NoError(t, rerr, "the launcher wrote its pid file")
		var written uint32
		_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &written)
		assert.Eq(t, uint32(0), written, "the child beyond the process limit was never created")
		assert.Nil(t, waitErr, "the launcher itself ran to its end")
	})
}

// TestWindowsAssignmentFailureEndsShell terminates the suspended shell before
// Started (without reaping it): assigning a terminated process fails, and
// Started must return that error while the shell stays dead (nothing running
// outside the job).
func TestWindowsAssignmentFailureEndsShell(t *testing.T) {
	var out bytes.Buffer
	cmd := exec.Command("cmd", "/c", "echo hello")
	cmd.Stdout, cmd.Stderr = &out, &out
	tree := New(cmd)
	assert.Require(t, assert.NoError(t, cmd.Start()), "start (suspended)")
	// Kill the still suspended shell; the process object lives until Wait reaps
	// it, and assigning a terminated process is refused by Windows.
	assert.NoError(t, cmd.Process.Kill(), "terminate the suspended shell")

	startErr := tree.Started()
	assert.Error(t, startErr, "assigning a terminated shell to the job fails")
	assert.ErrSubMsg(t, startErr, "assign the shell to its job", "the error names the failed assignment")
	// exec reports the terminated shell as an exit status, not an error path of
	// its own; the state proves the shell really ended instead of waiting
	// suspended somewhere.
	_ = cmd.Wait()
	assert.Eq(t, 1, cmd.ProcessState.ExitCode(), "the shell was terminated, not left suspended")
	assert.Eq(t, 0, out.Len(), "the suspended shell never ran anything outside the job")
	tree.Close()
}
