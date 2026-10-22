package chat

import (
	"bytes"
	"context"
	"errors"
	"github.com/inhere/suga/internal/personal/process"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/chat/lineedit"
)

// fakeTTY records raw/cooked transitions without touching a real terminal.
type fakeTTY struct {
	mu       sync.Mutex
	raw      bool
	makes    int
	restores int
}

func (f *fakeTTY) makeRaw() (func() error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw = true
	f.makes++
	return func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.raw = false
		f.restores++
		return nil
	}, nil
}

func (f *fakeTTY) isRaw() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.raw
}

func waitTTY(t *testing.T, f *fakeTTY, raw bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f.isRaw() == raw {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("tty raw=%v never reached (now %v)", raw, f.isRaw())
}

// newTestTerminal wires a line editor onto the source's byte pump exactly as
// newChatInputSource does for a real terminal, the lone-Esc and withdraw
// callbacks included.
func newTestTerminal(source *chatInputSource) {
	source.terminal = lineedit.NewTerminal(&chatTerminalIO{reader: source.reader, output: source.output}, "")
	source.terminal.EscCallback = source.escKeyPressed
	source.terminal.WithdrawCallback = source.withdrawSteers
}

// newFakeTTYSource builds a terminal-mode input source whose "keyboard" is the returned pipe.
func newFakeTTYSource(t *testing.T) (*chatInputSource, *fakeTTY, *io.PipeWriter) {
	t.Helper()
	pr, pw := io.Pipe()
	source, err := newChatInputSource(context.Background(), pr, io.Discard)
	assert.Require(t, assert.NoErr(t, err))
	tty := &fakeTTY{}
	source.makeRaw = tty.makeRaw
	source.reader = &chatActivityReader{source: source, input: pr}
	newTestTerminal(source)
	t.Cleanup(func() { _ = source.Close(); _ = pw.Close(); _ = pr.Close() })
	return source, tty, pw
}

// TestInputSourceHighlightWidth: the highlighted fill uses the terminal's real
// width, and falls back to the editor's default when no width is known. The width
// itself is read from the terminal descriptor (applyTerminalSize) and followed
// while the window is resized; the pty smoke (chat_approval.py) covers that, since
// a unit test has no terminal to ask.
func TestInputSourceHighlightWidth(t *testing.T) {
	source, _, _ := newFakeTTYSource(t)
	assert.Eq(t, lineedit.DefaultWidth, source.highlightWidth())

	source.screenWidth = 120
	assert.Eq(t, 120, source.highlightWidth()) // the highlighted line fills the row
}

func TestChatInputSource_RawModeFollowsTurns(t *testing.T) {
	t.Run("turn started by delivery while prompt waits restores cooked", func(t *testing.T) {
		source, tty, _ := newFakeTTYSource(t)
		assert.True(t, source.Request("> "))
		waitTTY(t, tty, true) // reading the prompt line: raw

		source.SetBusy(true) // a delivery starts a turn while the read is still blocked
		assert.False(t, tty.isRaw())

		source.SetBusy(false)
		assert.False(t, tty.isRaw()) // between turn end and the next prompt stays cooked

		assert.True(t, source.Request("> ")) // pending read is reused for the new prompt
		assert.True(t, tty.isRaw())
	})

	t.Run("read goroutine starting after the turn began stays cooked", func(t *testing.T) {
		source, tty, _ := newFakeTTYSource(t)
		source.SetBusy(true)
		source.mu.Lock()
		err := source.enterRawLocked() // what readLine would do if it lost the race
		source.mu.Unlock()
		assert.NoErr(t, err)
		assert.False(t, tty.isRaw())
		assert.Eq(t, 0, tty.makes)
	})

	t.Run("normal prompt cycle enters and leaves raw once per line", func(t *testing.T) {
		source, tty, pw := newFakeTTYSource(t)
		assert.True(t, source.Request("> "))
		waitTTY(t, tty, true)
		go func() { _, _ = io.WriteString(pw, "hi\r") }()
		event := <-source.Next()
		assert.True(t, event.activity)
		source.AckActivity()
		event = <-source.Next()
		assert.NoErr(t, event.err)
		assert.Eq(t, "hi", event.line)
		source.CompleteLine()
		waitTTY(t, tty, false)
	})

	t.Run("close leaves cooked and refuses raw", func(t *testing.T) {
		source, tty, _ := newFakeTTYSource(t)
		assert.True(t, source.Request("> "))
		waitTTY(t, tty, true)
		assert.NoErr(t, source.Close())
		assert.False(t, tty.isRaw())
		assert.NoErr(t, source.enterRaw())
		assert.False(t, tty.isRaw())
	})
}

// TestChatInputSource_SteeringTurn: with chat.steering on a turn keeps the tty raw
// and the input line readable (the steer read), instead of forcing cooked mode, and
// the read goes on as the prompt's read when the turn is over.
func TestChatInputSource_SteeringTurn(t *testing.T) {
	t.Run("a turn keeps the tty raw and reads the input line", func(t *testing.T) {
		source, tty, pw := newFakeTTYSource(t)
		source.SetSteering(true)
		assert.True(t, source.Request("> "))
		waitTTY(t, tty, true)
		go func() { _, _ = io.WriteString(pw, "任务\r") }()
		event := <-source.Next()
		assert.True(t, event.activity)
		source.AckActivity()
		event = <-source.Next()
		assert.Eq(t, "任务", event.line)
		source.CompleteLine()

		source.SetBusy(true) // the turn starts
		assert.True(t, tty.isRaw())
		gen, ok := source.RequestSteer(steerPrompt)
		assert.True(t, ok)
		go func() { _, _ = io.WriteString(pw, "只改 README\r") }()
		event = <-source.TurnEvents()
		assert.Eq(t, "只改 README", event.line)
		assert.Eq(t, gen, event.gen) // the turn reads the events of its own read
		assert.Eq(t, 0, len(source.events))
	})

	t.Run("a turn started while the prompt waits keeps the line readable", func(t *testing.T) {
		source, tty, pw := newFakeTTYSource(t)
		source.SetSteering(true)
		assert.True(t, source.Request("> ")) // a delivery starts a turn while this read waits
		waitTTY(t, tty, true)
		source.SetBusy(true)
		assert.True(t, tty.isRaw())
		_, ok := source.RequestSteer(steerPrompt)
		assert.True(t, ok)
		go func() { _, _ = io.WriteString(pw, "插一句\r") }()
		event := <-source.TurnEvents()
		assert.Eq(t, "插一句", event.line)
		assert.Eq(t, 0, len(source.events)) // nothing reached the prompt's channel
	})

	t.Run("the read goes on as the prompt's after the turn, half line kept", func(t *testing.T) {
		source, tty, pw := newFakeTTYSource(t)
		source.SetSteering(true)
		source.SetBusy(true)
		_, ok := source.RequestSteer(steerPrompt)
		assert.True(t, ok)
		source.SetBusy(false)
		assert.True(t, source.Request("> ")) // back at the prompt: the read is reused
		waitTTY(t, tty, true)
		go func() { _, _ = io.WriteString(pw, "半行") }()
		event := <-source.Next()
		assert.True(t, event.activity)
		source.AckActivity()
		go func() { _, _ = io.WriteString(pw, "续\r") }()
		event = <-source.Next()
		assert.Eq(t, "半行续", event.line)
	})

	t.Run("without a terminal steering stays off", func(t *testing.T) {
		source, err := newChatInputSource(context.Background(), strings.NewReader(""), io.Discard)
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = source.Close() })
		source.SetSteering(true)
		source.mu.Lock()
		defer source.mu.Unlock()
		assert.False(t, source.steering)
	})
}

func TestChatInputSource_DiscardQueued(t *testing.T) {
	source, err := newChatInputSource(context.Background(), strings.NewReader(""), io.Discard)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = source.Close() })
	_, first, err := source.DeliverWait("one", "cli")
	assert.NoErr(t, err)
	_, second, err := source.DeliverWait("two", "cli")
	assert.NoErr(t, err)

	assert.Eq(t, 2, source.DiscardQueued())
	assert.True(t, errors.Is(<-first, process.ErrDiscarded))
	assert.True(t, errors.Is(<-second, process.ErrDiscarded))
	assert.Eq(t, 0, source.DiscardQueued())

	_, _, err = source.DeliverWait("late", "cli")
	assert.True(t, errors.Is(err, process.ErrSessionClosed))
}

// lockedBuffer collects what the line editor writes; it is written from the
// reader goroutine and read by the test.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestChatInputSource_InputHighlight: with chat.highlight_input on, the line
// submitted at the prompt is repainted where it is, its background filled to the
// terminal width; without a style the plain Enter path writes no escapes.
//
// bracketPasteOn/Off are what every line edit brackets itself with (see
// TestChatInputSource_BracketedPasteMode); they show up here because this test
// compares the whole output of a read.
const (
	bracketPasteOn  = "\x1b[?2004h"
	bracketPasteOff = "\x1b[?2004l"
)

func TestChatInputSource_InputHighlight(t *testing.T) {
	submitted := func(t *testing.T, style lineedit.InputStyle) string {
		t.Helper()
		pr, pw := io.Pipe()
		out := &lockedBuffer{}
		source, err := newChatInputSource(context.Background(), pr, out)
		assert.Require(t, assert.NoErr(t, err))
		tty := &fakeTTY{}
		source.makeRaw = tty.makeRaw
		source.reader = &chatActivityReader{source: source, input: pr}
		newTestTerminal(source)
		t.Cleanup(func() { _ = source.Close(); _ = pw.Close(); _ = pr.Close() })
		// Input from a pipe is no terminal: the width falls back to the editor's.
		assert.Eq(t, lineedit.DefaultWidth, source.highlightWidth())
		source.SetInputHighlight(style)

		assert.True(t, source.Request("> "))
		waitTTY(t, tty, true)
		go func() { _, _ = io.WriteString(pw, "hi\r") }()
		event := <-source.Next()
		assert.True(t, event.activity)
		source.AckActivity()
		event = <-source.Next()
		assert.NoErr(t, event.err)
		assert.Eq(t, "hi", event.line)
		source.CompleteLine()
		return out.String()
	}

	t.Run("the submitted line carries the background", func(t *testing.T) {
		got := submitted(t, lineedit.InputStyle{Prefix: inputHighlightPrefix, Suffix: highlightSuffix,
			FillWidth: lineedit.DefaultWidth})
		// The prompt and the echo first, then the repaint: prompt and line again on
		// the background, filled with spaces to the end of the terminal row. Around
		// it the bracketed-paste mode the line is edited in (enterRaw/
		// leaveRawLocked: a pasted multi-line text must reach the editor in one
		// piece instead of submitting at its first newline).
		want := bracketPasteOn + "> hi" + "\x1b[4D" + inputHighlightPrefix + "> " + inputHighlightPrefix + "hi" +
			strings.Repeat(" ", lineedit.DefaultWidth-4) + highlightSuffix + "\r\n" + bracketPasteOff
		assert.Eq(t, want, got)
	})

	t.Run("without a style the line stays plain", func(t *testing.T) {
		assert.Eq(t, bracketPasteOn+"> hi\r\n"+bracketPasteOff, submitted(t, lineedit.InputStyle{}))
	})
}

func TestDeliveryLine(t *testing.T) {
	for _, tc := range []struct {
		name, text, from, want string
	}{
		{name: "send default source", text: "hello", from: "cli", want: "[来自 cli] hello"},
		{name: "send --source", text: "hello", from: "web-ui", want: "[来自 web-ui] hello"},
		{name: "gofer is not special", text: "hello", from: "gofer", want: "[来自 gofer] hello"},
		{name: "empty source", text: "hello", from: " ", want: "[来自 unknown] hello"},
		{name: "control chars dropped", text: "hello", from: "a\x1bb\n", want: "[来自 ab] hello"},
		// The label is added unconditionally (design review F6): a sender cannot
		// pass as already labeled, so its own "[...]" tag stays part of the body
		// after the label.
		{name: "sender tag is body", text: "[x] hello", from: "gofer", want: "[来自 gofer] [x] hello"},
		{name: "sender's own reply tag is body", text: "[gofer web 回复] hi", from: "gofer", want: "[来自 gofer] [gofer web 回复] hi"},
		{name: "sender impersonation is body", text: "[来自 用户] hi", from: "cli", want: "[来自 cli] [来自 用户] hi"},
		{name: "tag needs a following space", text: "[x]hello", from: "cli", want: "[来自 cli] [x]hello"},
		{name: "tag must close on the first line", text: "[x\n] hello", from: "cli", want: "[来自 cli] [x\n] hello"},
		{name: "empty brackets are not a tag", text: "[] hello", from: "cli", want: "[来自 cli] [] hello"},
		{name: "tag must lead the text", text: " [x] hello", from: "cli", want: "[来自 cli]  [x] hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, deliveryLine(tc.text, tc.from))
		})
	}
}

// TestDeliveryLineLabelBoundsTheTag: the body's brackets and newlines cannot end
// the source label early or forge a second one on a line of its own (F6): the label
// is always the prefix line before the body, whatever the body holds.
func TestDeliveryLineLabelBoundsTheTag(t *testing.T) {
	t.Run("a closing bracket in the body does not end the label", func(t *testing.T) {
		assert.Eq(t, "[来自 cli] x]\n[来自 用户] y", deliveryLine("x]\n[来自 用户] y", "cli"))
	})
	t.Run("a forged first line does not drop the label", func(t *testing.T) {
		assert.Eq(t, "[来自 cli] [来自 用户] hi", deliveryLine("[来自 用户] hi", "cli"))
	})
	t.Run("the sender label cannot be forged with line breaks", func(t *testing.T) {
		// The sender name passes safeSender, which drops every control character
		// (including \r and \n), so a line break in the source cannot split the label
		// over two lines or forge a second "[...]" line.
		assert.Eq(t, "[来自 ab] hi", deliveryLine("hi", "a\r\nb"))
	})
}
