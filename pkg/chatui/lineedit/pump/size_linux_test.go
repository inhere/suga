//go:build linux

package pump

import (
	"bytes"
	"io"
	"log/slog"
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

// TestWatchSizeLogger: the size note and the read failure go to the logger the
// host passes, not to slog.Default().
func TestWatchSizeLogger(t *testing.T) {
	t.Run("the size note goes to the given logger", func(t *testing.T) {
		slave := openTestPTY(t)
		assert.NoErr(t, unix.IoctlSetWinsize(slave, unix.TIOCSWINSZ, &unix.Winsize{Col: 90, Row: 20}))
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		stop := WatchSizeLogger(lineedit.NewTerminal(rw{&bufInput{}, io.Discard}, ""), slave, nil, log)
		stop()
		assert.StrContains(t, buf.String(), "terminal size")
		assert.StrContains(t, buf.String(), "width=90")
	})
	t.Run("a size that cannot be read warns on the given logger", func(t *testing.T) {
		r, w, err := os.Pipe()
		assert.Require(t, assert.NoErr(t, err))
		defer r.Close()
		defer w.Close()
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		stop := WatchSizeLogger(lineedit.NewTerminal(rw{&bufInput{}, io.Discard}, ""), int(r.Fd()), nil, log)
		stop()
		assert.StrContains(t, buf.String(), "level=WARN")
		assert.StrContains(t, buf.String(), "read the terminal size")
	})
}
