//go:build !linux && !darwin

package pump

import (
	"os"
	"time"
)

// resizePollInterval is how often the window size is checked where the host has
// no resize signal: Windows has none (a console window event would need the
// input handle the byte pump owns), so chat polls instead. One second keeps a
// resize feeling immediate without waking up more than once a second.
const resizePollInterval = time.Second

// resizeNotify returns a channel that receives a value on every poll tick and a
// release func: the caller re-reads the terminal size and applies it only when
// it really changed, so a tick without a resize costs one ioctl.
func resizeNotify() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(resizePollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case ch <- nil:
				default: // a pending tick is enough
				}
			}
		}
	}()
	return ch, func() { close(stop) }
}
