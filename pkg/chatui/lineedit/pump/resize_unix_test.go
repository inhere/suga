//go:build linux || darwin

package pump

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// TestResizeNotifySignal: the watcher is driven by SIGWINCH on these systems, so
// chat applies a resized window while it runs. The signal is sent to this process
// (the default action of SIGWINCH is to ignore it, so nothing else happens).
func TestResizeNotifySignal(t *testing.T) {
	events, stop := resizeNotify()
	defer stop()

	assert.NoErr(t, syscall.Kill(os.Getpid(), syscall.SIGWINCH))
	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("no SIGWINCH notification within 2s")
	}
}
