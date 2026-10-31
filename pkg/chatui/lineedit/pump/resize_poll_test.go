//go:build !linux && !darwin

package pump

import (
	"testing"
	"time"
)

// TestResizeNotifyPolls: where the host has no resize signal (Windows) chat
// polls instead, so a resized window is still picked up. The tick also proves the
// watcher goroutine starts, and stop releases it (a second call would panic on
// the closed channel).
func TestResizeNotifyPolls(t *testing.T) {
	events, stop := resizeNotify()
	select {
	case <-events:
	case <-time.After(3 * resizePollInterval):
		t.Fatal("no poll tick within three intervals")
	}
	stop()
}
