//go:build ptye2e && windows

package ptye2e

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const ctrlHelperEnv = "SUGA_PTYE2E_CTRL_BREAK_PID"

// terminateChat does on Windows what SIGTERM does elsewhere: it delivers a console
// control event to the chat, which Go turns into an interrupt that cancels chat's
// context (the shutdown path that discards queued deliveries). Windows has no way
// to signal a process directly, and the event can only be sent from a process
// attached to the chat's (pseudo) console, so a helper process - this test binary
// re-run on TestConsoleCtrlBreakHelper - attaches to it and generates CTRL_BREAK.
func terminateChat(t *testing.T, pid int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestConsoleCtrlBreakHelper$")
	cmd.Env = append(os.Environ(), ctrlHelperEnv+"="+strconv.Itoa(pid))
	// No console of its own: the helper attaches to the chat's.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008} // DETACHED_PROCESS
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("console ctrl helper: %v\n%s", err, out)
	}
}

// TestConsoleCtrlBreakHelper is not a scenario: it is the helper process of
// terminateChat and does nothing unless that sets ctrlHelperEnv.
func TestConsoleCtrlBreakHelper(t *testing.T) {
	raw := os.Getenv(ctrlHelperEnv)
	if raw == "" {
		return
	}
	pid, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	free, attach := kernel.NewProc("FreeConsole"), kernel.NewProc("AttachConsole")
	handler, generate := kernel.NewProc("SetConsoleCtrlHandler"), kernel.NewProc("GenerateConsoleCtrlEvent")
	free.Call()
	if r, _, err := attach.Call(uintptr(pid)); r == 0 {
		t.Fatalf("AttachConsole(%d): %v", pid, err)
	}
	// The helper shares the console, so it must not die of its own event.
	handler.Call(0, 1)
	const ctrlBreakEvent = 1
	if r, _, err := generate.Call(ctrlBreakEvent, 0); r == 0 {
		t.Fatalf("GenerateConsoleCtrlEvent: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
}
