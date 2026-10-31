//go:build linux || darwin

package pump

import (
	"os"
	"os/signal"
	"syscall"
)

// resizeNotify returns a channel that receives one value per window resize
// (SIGWINCH) and a release func. The channel is buffered(1): a resize burst
// collapses into the single "the size may have changed" note the caller needs.
func resizeNotify() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	return ch, func() { signal.Stop(ch) }
}
