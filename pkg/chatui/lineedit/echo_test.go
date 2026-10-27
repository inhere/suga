package lineedit

import (
	"io"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// Echo during a read: the turn's steer read echoes (chat.steering keeps the
// person's input line on screen for the whole turn), a caller that wants the line
// hidden turns it off - the state ReadPassword reads in - and RestoreEcho gives it
// back (an approval question borrowing the turn's read, see chat.ask.endAsk).

// TestTerminal_EchoOff: a read with echoing off draws the prompt and not one
// character of what is typed, and keeps its line out of the history.
func TestTerminal_EchoOff(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.echo = false
	mem.in = append(mem.in, "secret\r")
	line, err := tm.ReadLine()
	assert.NoErr(t, err)
	assert.Eq(t, "secret", line)
	// The prompt and the row break of the submit, nothing of the line itself.
	assert.Eq(t, "> \r\n", mem.out.String())
	assert.Eq(t, 0, historyLen(tm)) // a hidden line is not a history entry
}

// TestTerminal_RestoreEcho: after RestoreEcho the prompt the caller sets is drawn
// and input echoes again - the line completes and enters the history as usual.
func TestTerminal_RestoreEcho(t *testing.T) {
	tm, mem := newMem("> ", 0)
	tm.echo = false
	// A caller that set a prompt while the line was hidden: it is drawn, the line
	// it would show is not (there is none yet).
	assert.NoErr(t, tm.UpdatePrompt("↳ "))
	assert.StrContains(t, mem.out.String(), "↳ ")

	tm.RestoreEcho()
	lines := readAll(t, tm, mem, "ab\r")
	assert.Eq(t, []string{"ab"}, lines)
	// The prompt stays the one UpdatePrompt put on screen; the input echoes after it.
	assert.StrContains(t, mem.out.String(), "↳ ab")
	assert.Eq(t, 1, historyLen(tm))
}

// feedingTerm hands the editor one chunk of input per call and reports when it is
// waiting for the next one: the test then knows the chunk it wrote has been read
// and handled (the editor comes back for more only after that).
type feedingTerm struct {
	in      chan string
	waiting chan struct{}
	out     syncBuffer
}

func (f *feedingTerm) Read(p []byte) (int, error) {
	select {
	case f.waiting <- struct{}{}: // nobody waits: the signal is dropped
	default:
	}
	chunk, ok := <-f.in
	if !ok {
		return 0, io.EOF
	}
	return copy(p, chunk), nil
}

func (f *feedingTerm) Write(p []byte) (int, error) { return f.out.Write(p) }

// TestTerminal_EchoComesBackDuringARead follows an approval question borrowing a
// running read (design N5): the hidden line stays off screen while the question
// works, and RestoreEcho plus the turn's prompt put it back - with the text typed
// meanwhile - after which input echoes again.
func TestTerminal_EchoComesBackDuringARead(t *testing.T) {
	feed := &feedingTerm{in: make(chan string, 1), waiting: make(chan struct{}, 1)}
	tm := NewTerminal(feed, "↳ ")
	tm.echo = false // the line is hidden, as in a read a question borrowed
	lines := make(chan string, 1)
	go func() {
		line, _ := tm.ReadLine()
		lines <- line
	}()

	waitRead(t, feed.waiting) // the read started: the turn's prompt is on screen
	assert.StrContains(t, feed.out.String(), "↳ ")

	feed.in <- "ab"           // typed while hidden
	waitRead(t, feed.waiting) // read and handled, and not a character drawn
	assert.StrContains(t, feed.out.String(), "↳ ")
	assert.NotContains(t, feed.out.String(), "ab")

	// The question draws its own prompt on the same read ...
	assert.NoErr(t, tm.UpdatePrompt("确认? "))
	assert.StrContains(t, feed.out.String(), "确认? ")
	// ... and the turn's line comes back: echoed again, the prompt redrawn with
	// what was typed while the line was hidden.
	tm.RestoreEcho()
	assert.NoErr(t, tm.UpdatePrompt("↳ "))
	assert.StrContains(t, feed.out.String(), "↳ ab")

	feed.in <- "cd\r"
	assert.Eq(t, "abcd", readLineWithin(t, lines)) // the hidden text is part of the line
	assert.StrContains(t, feed.out.String(), "cd") // and what is typed now echoes
}

// waitRead waits for one "waiting for input" signal, failing the test if it does
// not arrive.
func waitRead(t *testing.T, waiting <-chan struct{}) {
	t.Helper()
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("the read never came back for input")
	}
}

// readLineWithin waits for the line a read completed.
func readLineWithin(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(2 * time.Second):
		t.Fatal("the read never finished")
		return ""
	}
}

func TestTerminal_ReverseSearchUsesTypedKeys(t *testing.T) {
	feed := &feedingTerm{in: make(chan string, 1), waiting: make(chan struct{}, 1)}
	tm := NewTerminal(feed, "❯ ")
	tm.SetHistory([]string{"first", "second history line"})
	lines := make(chan string, 1)
	go func() {
		line, err := tm.ReadLine()
		if err != nil {
			t.Errorf("ReadLine: %v", err)
		}
		lines <- line
	}()
	waitRead(t, feed.waiting)
	feed.in <- "\x15\x12second\r"
	line := readLineWithin(t, lines)
	assert.Eq(t, "second history line", line)
	assert.StrContains(t, feed.out.String(), "(reverse-i-search)")
	assert.StrContains(t, feed.out.String(), "second history line")
}

func TestTerminal_AutoCompleteUsesTypedTab(t *testing.T) {
	feed := &feedingTerm{in: make(chan string, 1), waiting: make(chan struct{}, 1)}
	tm := NewTerminal(feed, "❯ ")
	tm.AutoCompleteCallback = func(line string, pos int, key rune) (string, int, bool) {
		if key == '\t' && line == ":se" && pos == len(line) {
			return ":session", len(":session"), true
		}
		return "", 0, false
	}
	lines := make(chan string, 1)
	go func() {
		line, err := tm.ReadLine()
		if err != nil {
			t.Errorf("ReadLine: %v", err)
		}
		lines <- line
	}()
	waitRead(t, feed.waiting)
	feed.in <- ":se\t\r"
	assert.Eq(t, ":session", readLineWithin(t, lines))
}
