package pump

import (
	"log/slog"
	"sync"

	"github.com/inhere/suga/pkg/chatui/lineedit"
	"golang.org/x/term"
)

// WatchSize applies the size of the terminal behind fd to t now and again on every
// window change, until stop is called (SIGWINCH on Unix, a poll where there is no
// signal; resize_unix.go / resize_poll.go). The line editor wraps at the size it
// was last given, so a resized window has to reach it (SetSize also repaints the
// line being edited; a blocked read releases the editor's lock, so that normally
// applies at once).
//
// onChange, when non-nil, is told every width that differs from the last applied
// one (the first size included, before WatchSize returns), after the editor got it.
// A window that did not change costs one ioctl and nothing else; a size that cannot
// be read leaves the editor with its default width, which decides how every line of
// the chat is laid out, so it is worth a log line. Without a terminal or with a
// negative fd there is nothing to watch and stop does nothing. Its log lines go to
// slog.Default(); WatchSizeLogger takes the logger instead.
func WatchSize(t *lineedit.Terminal, fd int, onChange func(width, height int)) (stop func()) {
	return WatchSizeLogger(t, fd, onChange, nil)
}

// WatchSizeLogger is WatchSize with its own logger (nil means slog.Default()): a
// host whose default logger writes to the terminal it draws on, or that keeps the
// size notes out of its log, passes one instead of swapping the process default.
func WatchSizeLogger(t *lineedit.Terminal, fd int, onChange func(width, height int), log *slog.Logger) (stop func()) {
	if t == nil || fd < 0 {
		return func() {}
	}
	if log == nil {
		log = slog.Default()
	}
	lastWidth := 0
	apply := func() {
		width, height, err := term.GetSize(fd)
		if err != nil || width <= 0 {
			log.Warn("read the terminal size; the chat keeps its default line width",
				"fd", fd, "err", err)
			return
		}
		if width == lastWidth {
			return
		}
		lastWidth = width
		log.Info("terminal size", "fd", fd, "width", width, "height", height)
		_ = t.SetSize(width, height)
		if onChange != nil {
			onChange(width, height)
		}
	}
	apply()
	events, release := resizeNotify()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer release()
		for {
			select {
			case <-done:
				return
			case <-events:
				apply()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done); <-finished })
	}
}
