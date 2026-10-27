package lineedit

// Highlighting of a submitted input line (chat.highlight_input). The style is
// opaque to this package: chat passes the escape sequences and, when it could
// read it, the real terminal width. The line is repainted where it already is
// on screen, the background reaching the end of every row the line occupies, so
// whatever was typed stays visible as a block above the model's answer.
//
// The repaint leans on the same layout rules as writeLine (see width.go), so it
// follows automatic wrapping, wide runes and a prompt that is itself painted.

import "strings"

// InputStyle is the style a submitted line is repainted with; a zero InputStyle
// (the default) leaves the line exactly as it was typed.
type InputStyle struct {
	// Prefix opens the style (background, plus the foreground that stays
	// readable on it) and Suffix closes it; an empty Prefix turns the repaint
	// off.
	Prefix, Suffix string
	// FillWidth is the number of columns the background is filled to. The
	// terminal's own layout width wins when it is larger: rows are cut at that
	// width, so filling further would spill into the next row.
	FillWidth int
}

// SetInputStyle sets the style of a submitted line (a zero InputStyle turns the
// repaint off). Safe from any goroutine and at any time: it only takes effect
// when a line is submitted.
func (t *Terminal) SetInputStyle(style InputStyle) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.inputStyle = style
}

// inputRow is one screen row of the prompt and the line being edited: the
// prompt runes on that row (row 0, and the rows a long prompt wraps into) and
// the line runes on it.
type inputRow struct {
	prompt, text []rune
}

func (r *inputRow) appendRune(ch rune, prompt bool) {
	if prompt {
		r.prompt = append(r.prompt, ch)
		return
	}
	r.text = append(r.text, ch)
}

// width is the number of columns the row occupies (escape sequences count as
// zero, wide runes as two).
func (r inputRow) width() int { return visualLength(r.prompt) + visualLength(r.text) }

// inputRows lays the prompt and the line being edited out into the screen rows
// they occupy, exactly like writeLine: rows are cut at the terminal width, a
// wide rune never starts in the last column (the free cell is filled with a
// space), and a newline inside the line (pasted multi-line input) starts a new
// row at column 0.
func (t *Terminal) inputRows() []inputRow {
	width := t.termWidth
	if width < 1 {
		width = 1
	}
	rows := []inputRow{{}}
	col := 0
	var esc escapeState
	add := func(ch rune, prompt bool) {
		if ch == '\n' {
			// A newline inside the line (pasted multi-line input) starts a new row
			// at column 0, the way writeLine echoes it.
			rows = append(rows, inputRow{})
			col = 0
			return
		}
		w := esc.width(ch)
		if w == 2 && width >= 2 && col == width-1 {
			rows[len(rows)-1].appendRune(' ', prompt)
			rows = append(rows, inputRow{})
			col = 0
		}
		rows[len(rows)-1].appendRune(ch, prompt)
		col += w
		if col >= width {
			rows = append(rows, inputRow{})
			col = 0
		}
	}
	for _, ch := range t.prompt {
		add(ch, true)
	}
	for _, ch := range t.line {
		add(ch, false)
	}
	// A last row left empty is the row the cursor sits on, not text on screen.
	if n := len(rows); n > 1 && len(rows[n-1].prompt) == 0 && len(rows[n-1].text) == 0 {
		rows = rows[:n-1]
	}
	return rows
}

// repaintSubmittedLine redraws the line that was just submitted on the input
// style's background and reports whether it painted anything: without a style,
// or with nothing on screen to paint, the caller keeps the plain Enter path.
// t.lock must be held (it runs from handleKey).
//
// The cursor is left where the plain path leaves it: at column 0 of the row
// after the last row of the line, ready for the caller's own "\r\n".
func (t *Terminal) repaintSubmittedLine() bool {
	if !t.echo || t.inputStyle.Prefix == "" || len(t.line) == 0 {
		return false
	}
	rows := t.inputRows()
	if len(rows) == 0 {
		return false
	}
	// The cursor may sit anywhere in the line (Ctrl-A, a click): move it to the
	// end first, the way the plain path does, and go back to the start of the
	// line's first row from there.
	t.moveCursorToPos(len(t.line))
	t.move(t.cursorY, 0, t.cursorX, 0)
	t.cursorX, t.cursorY = 0, 0

	width := t.termWidth
	if t.inputStyle.FillWidth > width {
		width = t.inputStyle.FillWidth
	}
	for i, row := range rows {
		if i > 0 {
			t.queue([]rune("\r\n"))
		}
		t.queue([]rune(t.inputStyle.Prefix))
		if len(row.prompt) > 0 {
			// The prompt carries its own colors, which end with a reset: open the
			// style again behind them so the background survives.
			t.queue(row.prompt)
			t.queue([]rune(t.inputStyle.Prefix))
		}
		t.queue(row.text)
		for cols := row.width(); cols < width; cols++ {
			t.queue(space)
		}
		t.queue([]rune(t.inputStyle.Suffix))
	}
	t.cursorX, t.cursorY = 0, 0
	return true
}

// PadLine returns text followed by as many spaces as it takes to fill width
// columns, so the background of a highlighted line reaches the end of the
// terminal row; text that already reaches width comes back unchanged. It is
// what chat uses for the messages it prints on a background (Renderer.External),
// measured with the same rules as the line editor (escapes zero, wide runes two).
func PadLine(text string, width int) string {
	pad := width - visualLength([]rune(text))
	if pad <= 0 {
		return text
	}
	return text + strings.Repeat(" ", pad)
}
