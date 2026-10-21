package output

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

// LiveText prints a turn's text deltas as they stream in. Each model message
// ends on its own line, so commentary before a tool call and the final answer
// do not run together.
type LiveText struct {
	w        io.Writer
	prefix   string  // printed before each model message; empty for none
	md       *MDTerm // non-nil: the text is rendered as terminal markdown
	streamed bool    // any delta printed this turn
	open     bool    // the last printed delta did not end with a newline
	started  bool    // the current model message already printed its prefix
	// whole makes the text wait for line ends (SetWholeLines): only complete lines
	// leave, the unfinished rest waits in tail.
	whole   bool
	tail    string           // text after the last newline of the current message
	cleaner termtext.Cleaner // keeps escape-sequence state across streamed deltas
}

// NewLiveText returns a LiveText writing to w.
func NewLiveText(w io.Writer) *LiveText { return &LiveText{w: w} }

// NewPrefixedLiveText marks each model message with prefix (chat uses "▣ ").
func NewPrefixedLiveText(w io.Writer, prefix string) *LiveText {
	return &LiveText{w: w, prefix: prefix}
}

// SetMarkdown renders the streamed text as terminal markdown, line by line,
// instead of printing the deltas as they are (chat). color styles the rendered
// lines; without it the markers are still replaced, but nothing is colored.
func (l *LiveText) SetMarkdown(color bool) {
	l.md = NewMDTerm(l.w, color)
	l.md.SetLinePrefix(l.wholePrefix())
}

// SetWholeLines makes the streamed text wait for line ends: only complete lines
// are written, the unfinished rest stays buffered until its newline or the end of
// the message (closeLine). chat turns it on while it keeps an input line on screen
// during a turn (chat.steering): the line editor repaints its line after every
// write, so a partial line would be left on screen next to the line being edited.
// With markdown on the renderer is already line based; it then prints the message
// prefix itself, since the prefix may not leave as a write of its own.
func (l *LiveText) SetWholeLines(on bool) {
	if l.whole == on {
		return
	}
	l.whole = on
	if l.md != nil {
		l.md.SetLinePrefix(l.wholePrefix())
	}
}

// SetCode configures the code blocks of the markdown renderer: highlighting by
// the fence's language, the fold limit of a block (chat.code_*) and the terminal
// width a table is kept within. A LiveText without markdown rendering ignores it.
func (l *LiveText) SetCode(highlight bool, maxLines, width int) {
	if l.md == nil {
		return
	}
	l.md.SetCode(highlight, maxLines)
	l.md.SetWidth(width)
}

// SetMarkdownTheme picks the palette the markdown renderer paints with
// (chat.theme: "dark" or "light"; anything else keeps the dark default). A
// LiveText without markdown rendering ignores it.
func (l *LiveText) SetMarkdownTheme(theme string) {
	if l.md != nil {
		l.md.SetTheme(theme)
	}
}

// CodeBlocks returns the code blocks of the most recent rendered message, for
// chat's :code command (nil without markdown rendering).
func (l *LiveText) CodeBlocks() []CodeBlock {
	if l.md == nil {
		return nil
	}
	return l.md.CodeBlocks()
}

// wholePrefix is the prefix the markdown renderer prints itself ("" when the
// text prints it, i.e. when whole lines are off).
func (l *LiveText) wholePrefix() string {
	if l.whole {
		return l.prefix
	}
	return ""
}

// Handle prints a text delta or closes the line on message_stop; it reports
// whether the event was a streaming event.
func (l *LiveText) Handle(event personal.Event) bool {
	switch event.Kind {
	case string(agentcore.StreamEventTextDelta):
		if event.Text != "" {
			l.writeDelta(event.Text)
		}
		return true
	case string(agentcore.StreamEventMessageStop):
		l.closeLine()
		l.started = false
		return true
	case string(agentcore.StreamEventRetry):
		// The caller prints the notice (RetryNotice) on a line of its own;
		// a restarted message gets its prefix again.
		l.closeLine()
		l.started = false
	}
	return false
}

// RetryNotice is the one-line notice for a "retry" event.
func RetryNotice(event personal.Event) string {
	info := event.Retry
	if info == nil {
		info = &personal.RetryInfo{}
	}
	line := fmt.Sprintf("↻ 模型请求失败（%s），%s 后重试（第 %d 次）", termtext.Clean(event.Text), info.Wait.Round(100*time.Millisecond), info.Attempt)
	if info.Restart {
		line += "；上面的回复不完整，将重新输出"
	}
	return line
}

// writeDelta emits one delta of the current model message: as it is, buffered to
// complete lines while chat keeps an input line on screen (whole-line mode), or
// through the markdown renderer - which buffers the incomplete line even without
// whole-line mode and prints the message prefix itself in whole-line mode.
func (l *LiveText) writeDelta(text string) {
	l.streamed = true
	text = l.cleaner.Write(text)
	if text == "" {
		return
	}
	switch {
	case l.md != nil:
		// In whole-line mode the markdown renderer prints the prefix (with the line
		// it belongs to); otherwise the prefix goes out before the first delta.
		if !l.whole && !l.started {
			fmt.Fprint(l.w, l.prefix)
			l.started = true
		}
		l.md.Write(text)
	case l.whole:
		l.writeWholeLines(text)
	default:
		if !l.started {
			fmt.Fprint(l.w, l.prefix)
			l.started = true
		}
		fmt.Fprint(l.w, text)
		l.open = !strings.HasSuffix(text, "\n")
	}
}

// writeWholeLines writes every line a delta completes (newline included); the rest
// waits in tail for the next delta or for closeLine (whole-line mode).
func (l *LiveText) writeWholeLines(text string) {
	l.tail += text
	for {
		end := strings.IndexByte(l.tail, '\n')
		if end < 0 {
			return
		}
		l.writeWholeLine(l.tail[:end+1])
		l.tail = l.tail[end+1:]
	}
}

// writeWholeLine writes one complete line, with the message prefix in front of it
// while this message has not shown one yet.
func (l *LiveText) writeWholeLine(line string) {
	if !l.started {
		line, l.started = l.prefix+line, true
	}
	fmt.Fprint(l.w, line)
}

// closeLine ends the current model message: markdown renders what is left of
// its last line (an open code block ends with it), whole-line mode writes the
// buffered tail, and otherwise the open line gets its newline.
func (l *LiveText) closeLine() {
	// Do not let a partial escape sequence consume text from the next message.
	l.cleaner.Flush()
	if l.md != nil {
		l.md.Flush()
		return
	}
	if l.whole {
		if l.tail != "" {
			l.writeWholeLine(l.tail + "\n")
			l.tail = ""
		}
		return
	}
	if l.open {
		fmt.Fprintln(l.w)
		l.open = false
	}
}

// Answer prints the final text of a turn that was not streamed: with markdown on
// it goes through the renderer of the streamed text (a multi-line answer would
// otherwise show its raw markers), otherwise the prefix and the text are one line.
// The prefix goes out with a line whoever owns it: the markdown renderer while
// whole lines are on (it may not leave as a write of its own), this text
// otherwise.
func (l *LiveText) Answer(text string) {
	text = termtext.Clean(text)
	if l.md == nil {
		fmt.Fprintln(l.w, l.prefix+text)
		return
	}
	if !l.whole {
		fmt.Fprint(l.w, l.prefix)
	}
	l.md.Print(text)
}

// Finish ends the turn's output and reports whether anything was streamed.
func (l *LiveText) Finish() bool {
	l.closeLine()
	l.started = false
	return l.streamed
}
