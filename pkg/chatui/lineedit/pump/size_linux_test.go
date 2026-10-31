//go:build linux

package pump

import (
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/chatui/lineedit"
	"golang.org/x/sys/unix"
)

// TestWatchSize: the size of the terminal is applied at once, a changed window is
// followed on SIGWINCH, and stop releases the watcher.
func TestWatchSize(t *testing.T) {
	slave := openTestPTY(t)
	assert.NoErr(t, unix.IoctlSetWinsize(slave, unix.TIOCSWINSZ, &unix.Winsize{Col: 100, Row: 30}))
	term := lineedit.NewTerminal(rw{&bufInput{}, io.Discard}, "")

	widths := make(chan int, 4)
	stop := WatchSize(term, slave, func(w, _ int) { widths <- w })
	defer stop()
	assert.Eq(t, 100, <-widths) // applied before WatchSize returned

	assert.NoErr(t, unix.IoctlSetWinsize(slave, unix.TIOCSWINSZ, &unix.Winsize{Col: 132, Row: 30}))
	assert.NoErr(t, syscall.Kill(os.Getpid(), syscall.SIGWINCH))
	select {
	case w := <-widths:
		assert.Eq(t, 132, w)
	case <-time.After(3 * time.Second):
		t.Fatal("no size change reported after SIGWINCH")
	}
}
