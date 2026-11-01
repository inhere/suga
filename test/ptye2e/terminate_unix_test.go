//go:build ptye2e && !windows

package ptye2e

import (
	"syscall"
	"testing"
)

// terminateChat asks the chat process to shut down the way SIGTERM does (its
// context is cancelled, queued deliveries are discarded on the way out).
func terminateChat(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM %d: %v", pid, err)
	}
}
