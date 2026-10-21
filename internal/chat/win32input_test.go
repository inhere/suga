package chat

// Tests of chat.win32_input_mode at the input source: the decision of the mode,
// the DECSET 9001 sequences written around a read (and given back on every way
// out of it), Ctrl+Enter as a newline on the real key path, and the pause a local
// command makes. The record parser itself is tested in lineedit
// (win32input_test.go).

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// The sequences of win32-input-mode (DECSET 9001), as the terminal gets them.
const (
	win32ModeOnSeq  = "\x1b[?9001h"
	win32ModeOffSeq = "\x1b[?9001l"
)

// TestWin32InputModeWanted: auto (the default) asks for the mode in Windows
// Terminal only, on always, off never.
func TestWin32InputModeWanted(t *testing.T) {
	cases := []struct {
		name string
		mode string
		wt   bool
		want bool
	}{
		{name: "auto outside Windows Terminal", mode: "auto", wt: false, want: false},
		{name: "auto in Windows Terminal", mode: "auto", wt: true, want: true},
		{name: "empty means auto in Windows Terminal", mode: "", wt: true, want: true},
		{name: "empty means auto elsewhere", mode: "", wt: false, want: false},
		{name: "on", mode: "on", wt: false, want: true},
		{name: "off", mode: "off", wt: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wt {
				t.Setenv("WT_SESSION", "test-session")
			} else {
				t.Setenv("WT_SESSION", "")
			}
			assert.Eq(t, tc.want, win32InputModeWanted(tc.mode))
		})
	}
}

// newWin32Source builds a terminal-mode input source whose keyboard is the
// returned pipe and whose output is recorded, like newFakeTTYSource (which writes
// to io.Discard).
func newWin32Source(t *testing.T) (*chatInputSource, *fakeTTY, *io.PipeWriter, *lockedBuffer) {
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
	return source, tty, pw, out
}

// TestChatInputSource_Win32Sequences: a read with the mode on brackets itself with
// DECSET 9001 next to the bracketed-paste mode, and every way out of the read turns
// it off again (the line submitted, Ctrl-C, Close).
func TestChatInputSource_Win32Sequences(t *testing.T) {
	t.Run("a submitted line turns the mode on and off", func(t *testing.T) {
		source, tty, pw, out := newWin32Source(t)
		source.SetWin32InputMode(true)
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
		text := out.String()
		// The mode goes on with the bracketed-paste mode, and off with it when the
		// line is done: nothing of a cooked-mode read ever sees a record.
		assert.True(t, strings.Contains(text, bracketPasteOn+win32ModeOnSeq))
		assert.True(t, strings.Contains(text, bracketPasteOff+win32ModeOffSeq))
	})

	t.Run("Ctrl-C restores the terminal", func(t *testing.T) {
		source, tty, pw, out := newWin32Source(t)
		source.SetWin32InputMode(true)
		assert.True(t, source.Request("> "))
		waitTTY(t, tty, true)
		go func() { _, _ = io.WriteString(pw, "\x03") }()
		event := <-source.Next()
		assert.True(t, event.activity)
		source.AckActivity()
		event = <-source.Next()
		assert.Err(t, event.err)
		assert.False(t, source.win32Records())
		assert.True(t, strings.Contains(out.String(), win32ModeOffSeq))
	})

	t.Run("Close restores the terminal", func(t *testing.T) {
		source, tty, _, out := newWin32Source(t)
		source.SetWin32InputMode(true)
		assert.True(t, source.Request("> "))
		waitTTY(t, tty, true)
		source.PauseWin32Input()
		source.ResumeWin32Input()
		assert.True(t, source.win32Records())
		// The end of chat (also the path a panic takes: chat.Run defers Close).
		assert.NoErr(t, source.Close())
		assert.False(t, source.win32Records())
		assert.True(t, strings.HasSuffix(out.String(), win32ModeOffSeq))
	})

	t.Run("without the mode no sequence is written", func(t *testing.T) {
		source, tty, pw, out := newWin32Source(t)
		source.SetWin32InputMode(false)
		assert.True(t, source.Request("> "))
		waitTTY(t, tty, true)
		go func() { _, _ = io.WriteString(pw, "hi\r") }()
		event := <-source.Next()
		assert.True(t, event.activity)
		source.AckActivity()
		element := <-source.Next()
		assert.NoErr(t, element.err)
		source.CompleteLine()
		assert.False(t, strings.Contains(out.String(), "9001"))
	})
}

// TestChatInputSource_Win32CtrlEnter types Ctrl+Enter over the real read: it is a
// newline inside the line, Enter submits the whole text.
func TestChatInputSource_Win32CtrlEnter(t *testing.T) {
	source, tty, pw, _ := newWin32Source(t)
	source.SetWin32InputMode(true)
	assert.True(t, source.Request("> "))
	waitTTY(t, tty, true)
	ctrlEnter := "\x1b[13;28;10;1;8;1_"
	enter := "\x1b[13;28;13;1;0;1_"
	typeA := "\x1b[65;0;97;1;0;1_"
	typeB := "\x1b[66;0;98;1;0;1_"
	go func() { _, _ = io.WriteString(pw, typeA+ctrlEnter+typeB+enter) }()
	event := <-source.Next()
	assert.True(t, event.activity)
	source.AckActivity()
	event = <-source.Next()
	assert.NoErr(t, event.err)
	assert.Eq(t, "a\nb", event.line)
	source.CompleteLine()
}

// TestChatInputSource_Win32Pause: a local command's child process reads plain keys,
// so the mode goes off for as long as it runs and back on afterwards.
func TestChatInputSource_Win32Pause(t *testing.T) {
	source, tty, _, out := newWin32Source(t)
	source.SetWin32InputMode(true)
	assert.True(t, source.Request("> "))
	waitTTY(t, tty, true)
	before := strings.Count(out.String(), win32ModeOnSeq)
	source.PauseWin32Input()
	assert.False(t, source.win32Records())
	assert.True(t, strings.HasSuffix(out.String(), win32ModeOffSeq))
	// A read that starts while the command runs must not turn it back on.
	source.mu.Lock()
	source.setWin32InputLocked(true)
	source.mu.Unlock()
	assert.False(t, source.win32Records())
	source.ResumeWin32Input()
	assert.True(t, source.win32Records())
	assert.Eq(t, before+1, strings.Count(out.String(), win32ModeOnSeq))
}
