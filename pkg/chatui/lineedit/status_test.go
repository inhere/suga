package lineedit

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// The status line chat shows above the input line while a turn runs
// (chat.steering): what appears there, what a frame of it writes, and how a write
// of the editor takes it out of the way and puts it back.

// startStatusRead starts a ReadLine whose prompt is on screen (the editor draws it
// before it asks for input) and returns the terminal, the input it reads and the
// channel the finished line arrives on.
func startStatusRead(t *testing.T, prompt string) (*Terminal, *feedingTerm, chan string) {
	t.Helper()
	feed := &feedingTerm{in: make(chan string, 1), waiting: make(chan struct{}, 1)}
	tm := NewTerminal(feed, prompt)
	lines := make(chan string, 1)
	go func() {
		line, _ := tm.ReadLine()
		lines <- line
	}()
	waitRead(t, feed.waiting)
	return tm, feed, lines
}

// finishRead submits an empty line, so the read of a test ends and no goroutine is
// left blocked in Read.
func finishRead(t *testing.T, feed *feedingTerm, lines chan string) {
	t.Helper()
	feed.in <- "\r"
	_ = readLineWithin(t, lines)
}

// TestTerminal_StatusLineAboveTheInputLine: the row appears above the prompt, a
// frame rewrites that row only, and an empty status takes the row away, the prompt
// and the line moving back up to where the status line was.
func TestTerminal_StatusLineAboveTheInputLine(t *testing.T) {
	t.Run("the row appears above the prompt and a frame rewrites it in place", func(t *testing.T) {
		tm, feed, lines := startStatusRead(t, "↳ ")

		before := len(feed.out.String())
		tm.SetStatus("⠋ 思考中… 1s")
		// The row appears where the prompt is: the prompt row is cleared, the status
		// text takes it, and the prompt is drawn again one row below.
		assert.Eq(t, "\x1b[2D\x1b[K⠋ 思考中… 1s\r\n↳ ", feed.out.String()[before:])

		// Every further frame rewrites that row only: one row up, erase, the text,
		// back down and right to the cursor (the prompt is two columns wide).
		before = len(feed.out.String())
		tm.SetStatus("⠙ 思考中… 2s")
		assert.Eq(t, "\x1b[A\r\x1b[K⠙ 思考中… 2s\r\x1b[B\x1b[2C", feed.out.String()[before:])
		assert.Eq(t, "⠙ 思考中… 2s", string(tm.status))

		// The same text again is not a frame: nothing is written.
		before = len(feed.out.String())
		tm.SetStatus("⠙ 思考中… 2s")
		assert.Eq(t, before, len(feed.out.String()))

		finishRead(t, feed, lines)
	})

	t.Run("a frame leaves the line being edited and the cursor alone", func(t *testing.T) {
		tm, feed, lines := startStatusRead(t, "↳ ")
		tm.SetStatus("⠋ 思考中… 1s")
		feed.in <- "ab"
		waitRead(t, feed.waiting) // typed and echoed

		before := len(feed.out.String())
		tm.SetStatus("⠹ 思考中… 2s")
		frame := feed.out.String()[before:]
		// Only the status row is rewritten, and the cursor comes back to column 4
		// (the prompt plus what was typed): nothing of the line is drawn again.
		assert.Eq(t, "\x1b[A\r\x1b[K⠹ 思考中… 2s\r\x1b[B\x1b[4C", frame)
		assert.Eq(t, 4, tm.cursorX)
		assert.Eq(t, 0, tm.cursorY)

		// The row goes away: the line's rows and the status row are erased and the
		// prompt is drawn again on the row the status line had (the block shrinks
		// back up), with the cursor at the end of what was typed.
		before = len(feed.out.String())
		tm.SetStatus("")
		assert.Eq(t, "\x1b[4D\x1b[K\x1b[A\x1b[K↳ ab", feed.out.String()[before:])
		assert.False(t, tm.statusOn)
		assert.Eq(t, 4, tm.cursorX)

		// A frame again: the row comes back, taking the row the prompt had.
		before = len(feed.out.String())
		tm.SetStatus("⠋ 思考中… 3s")
		assert.Eq(t, "\x1b[4D\x1b[K⠋ 思考中… 3s\r\n↳ ab", feed.out.String()[before:])
		assert.True(t, tm.statusOn)

		finishRead(t, feed, lines)
	})
}

// TestTerminal_WriteWithStatusLine: a write of the editor erases the whole block
// (the status line and the input line), prints the output where the block started
// and draws the block again below it - the status line first, then the prompt with
// the line being edited: output, status line, input line, in that order.
func TestTerminal_WriteWithStatusLine(t *testing.T) {
	tm, feed, lines := startStatusRead(t, "↳ ")
	tm.SetStatus("⠋ 执行 shell_exec… 3s")
	feed.in <- "ab"
	waitRead(t, feed.waiting)

	before := len(feed.out.String())
	_, err := tm.Write([]byte("$ shell_exec\n"))
	assert.NoErr(t, err)
	assert.Eq(t, "\x1b[4D\x1b[K\x1b[A\x1b[K"+
		"$ shell_exec\r\n"+
		"⠋ 执行 shell_exec… 3s\r\n"+
		"↳ ab", feed.out.String()[before:])
	assert.Eq(t, 4, tm.cursorX)
	assert.True(t, tm.statusOn)

	finishRead(t, feed, lines)
}

// TestTerminal_StatusLineAroundRowChanges: the status line keeps its place when the
// block spans several rows (a pasted multi-line input) or when the window changes.
func TestTerminal_StatusLineAroundRowChanges(t *testing.T) {
	t.Run("a frame finds its row with the cursor on a later row of the line", func(t *testing.T) {
		tm, feed, lines := startStatusRead(t, "↳ ")
		tm.SetStatus("⠋ 思考中… 1s")
		// A pasted two-line text: the line takes two rows and the cursor sits on the
		// second one.
		feed.in <- "\x1b[200~a\nb\x1b[201~"
		waitRead(t, feed.waiting)
		assert.Eq(t, 3, tm.pos)
		cursorX, cursorY := tm.cursorX, tm.cursorY
		assert.Eq(t, 1, cursorY)

		before := len(feed.out.String())
		tm.SetStatus("⠙ 思考中… 2s")
		// Up one row past the prompt row (the status line is above it), erase it, and
		// back down to where the cursor is (row 1, column 1).
		assert.Eq(t, "\x1b[2A\r\x1b[K⠙ 思考中… 2s\r\x1b[2B\x1b[C", feed.out.String()[before:])
		assert.Eq(t, cursorX, tm.cursorX)
		assert.Eq(t, cursorY, tm.cursorY)

		feed.in <- "\r"
		assert.Eq(t, "a\nb", <-lines)
	})

	t.Run("a window change repaints the status line above the prompt", func(t *testing.T) {
		tm, feed, lines := startStatusRead(t, "↳ ")
		tm.SetStatus("⠋ 思考中… 1s")
		feed.in <- "ab"
		waitRead(t, feed.waiting)

		before := len(feed.out.String())
		assert.NoErr(t, tm.SetSize(100, 24))
		// The block is cleared - the status line's row first - and drawn again from the
		// top of the block: the status line, then the prompt with the typed line.
		assert.Eq(t, "\x1b[4D\x1b[A\x1b[K\x1b[B\x1b[K\x1b[A"+
			"⠋ 思考中… 1s\r\n↳ ab", feed.out.String()[before:])
		assert.True(t, tm.statusOn)
		assert.Eq(t, 4, tm.cursorX)
		assert.Eq(t, 0, tm.cursorY)

		finishRead(t, feed, lines)
	})
}

// TestTerminal_StatusLineBeforeThePromptIsDrawn: chat may set the status line
// while nothing of the block is on screen (a turn starts before its read is asked
// for): nothing is drawn then, and the read that follows draws the status line
// above its prompt.
func TestTerminal_StatusLineBeforeThePromptIsDrawn(t *testing.T) {
	feed := &feedingTerm{in: make(chan string, 1), waiting: make(chan struct{}, 1)}
	tm := NewTerminal(feed, "↳ ")
	tm.SetStatus("⠋ 思考中… 1s")
	assert.Eq(t, "", feed.out.String())
	assert.False(t, tm.statusOn)

	lines := make(chan string, 1)
	go func() {
		line, _ := tm.ReadLine()
		lines <- line
	}()
	waitRead(t, feed.waiting)
	assert.Eq(t, "⠋ 思考中… 1s\r\n↳ ", feed.out.String())
	assert.True(t, tm.statusOn)

	finishRead(t, feed, lines)
}

// TestTerminal_StatusLineIsOneRow: the status text is prepared for one terminal row
// - control runes become spaces, the text is cut to what fits (one column left
// free, so the row cannot wrap into the row of the prompt) and a color the text
// carries is closed with a reset.
func TestTerminal_StatusLineIsOneRow(t *testing.T) {
	cases := []struct {
		name  string
		width int
		text  string
		want  string
	}{
		{"fits", 40, "⠋ 思考中… 12s", "⠋ 思考中… 12s"},
		{"cut at the width", 10, "⠋ 执行 shell_exec… 3s", "⠋ 执行 sh"},
		{"a wide rune is dropped whole", 9, "abcdefg中x", "abcdefg"},
		{"control runes become spaces", 8, "a\nb\rc\td", "a b c d"},
		{"a plain text gets no reset", 40, "abcdef", "abcdef"},
		{"a color survives the cut", 6, "\x1b[90mabcdef\x1b[0m", "\x1b[90mabcde\x1b[0m"},
		{"a color is preserved", 40, "\x1b[90m⠋ 思考中… 1s\x1b[0m", "\x1b[90m⠋ 思考中… 1s\x1b[0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(fitStatus([]rune(tc.text), tc.width))
			assert.Eq(t, tc.want, got)
			assert.True(t, visualLength([]rune(got)) <= tc.width-1, got)
		})
	}

	t.Run("no terminal width falls back to the default", func(t *testing.T) {
		long := string(make([]rune, DefaultWidth+10))
		assert.Eq(t, DefaultWidth-1, visualLength(fitStatus([]rune(long), 0)))
	})
}

// TestTerminal_AbortLineDropsTheStatusLine: the read ended (Ctrl-C/Ctrl-D): the
// status line's row goes with it, and the next read starts on a clean line.
func TestTerminal_AbortLineDropsTheStatusLine(t *testing.T) {
	feed := &feedingTerm{in: make(chan string, 1), waiting: make(chan struct{}, 1)}
	tm := NewTerminal(feed, "↳ ")
	lines := make(chan string, 1)
	go func() {
		line, _ := tm.ReadLine()
		lines <- line
	}()
	waitRead(t, feed.waiting)
	tm.SetStatus("⠋ 思考中… 1s")

	before := len(feed.out.String())
	tm.AbortLine()
	// The status row is erased where it is (up one row, erase, back down to the
	// cursor in the line), then the read's own row is left behind.
	assert.Eq(t, "\x1b[A\r\x1b[K\x1b[B\x1b[2C\r\n", feed.out.String()[before:])
	assert.False(t, tm.statusOn)
	assert.Eq(t, 0, tm.cursorX)
	assert.Eq(t, 0, tm.cursorY)

	// The read itself goes on (the chat loop takes the next line): submit one so the
	// test leaves no goroutine behind.
	feed.in <- "\r"
	assert.Eq(t, "", <-lines)
}
