package chatui

import (
	"fmt"
	"io"
	"strings"

	"github.com/inhere/suga/pkg/chatui/termtext"
)

const (
	StreamTextDelta   = "text_delta"
	StreamMessageStop = "message_stop"
	StreamRetry       = "retry"
)

// StreamEvent is the smallest event contract needed by the streaming text area.
// Hosts adapt their engine-specific event type at the application boundary.
type StreamEvent struct {
	Kind string
	Text string
}

// LiveText owns one model message's streamed text, line buffering and optional
// markdown rendering. It has no dependency on suga's engine or application layers.
type LiveText struct {
	w          io.Writer
	prefix     string
	md         *MDTerm
	streamed   bool
	open       bool
	started    bool
	whole      bool
	tail       string
	cleaner    termtext.Cleaner
	translator Translator
}

func NewLiveText(w io.Writer) *LiveText { return &LiveText{w: w} }
func NewPrefixedLiveText(w io.Writer, prefix string) *LiveText {
	return &LiveText{w: w, prefix: prefix}
}

func (l *LiveText) SetMarkdown(color bool) {
	l.md = NewMDTerm(l.w, color)
	l.md.SetTranslator(l.translator)
	l.md.SetLinePrefix(l.wholePrefix())
}
func (l *LiveText) SetTranslator(tr Translator) {
	l.translator = tr
	if l.md != nil {
		l.md.SetTranslator(tr)
	}
}
func (l *LiveText) SetWholeLines(on bool) {
	if l.whole == on {
		return
	}
	l.whole = on
	if l.md != nil {
		l.md.SetLinePrefix(l.wholePrefix())
	}
}
func (l *LiveText) SetCode(highlight bool, maxLines, width int) {
	if l.md == nil {
		return
	}
	l.md.SetCode(highlight, maxLines)
	l.md.SetWidth(width)
}
func (l *LiveText) SetMarkdownTheme(theme string) {
	if l.md != nil {
		l.md.SetTheme(theme)
	}
}
func (l *LiveText) CodeBlocks() []CodeBlock {
	if l.md == nil {
		return nil
	}
	return l.md.CodeBlocks()
}
func (l *LiveText) wholePrefix() string {
	if l.whole {
		return l.prefix
	}
	return ""
}

// Handle writes deltas and closes a model message. It returns true for stream events.
func (l *LiveText) Handle(event StreamEvent) bool {
	switch event.Kind {
	case StreamTextDelta:
		if event.Text != "" {
			l.writeDelta(event.Text)
		}
		return true
	case StreamMessageStop:
		l.closeLine()
		l.started = false
		return true
	case StreamRetry:
		l.closeLine()
		l.started = false
	}
	return false
}
func (l *LiveText) writeDelta(text string) {
	l.streamed = true
	text = l.cleaner.Write(text)
	if text == "" {
		return
	}
	switch {
	case l.md != nil:
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
func (l *LiveText) writeWholeLine(line string) {
	if !l.started {
		line, l.started = l.prefix+line, true
	}
	fmt.Fprint(l.w, line)
}
func (l *LiveText) closeLine() {
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
func (l *LiveText) Finish() bool { l.closeLine(); l.started = false; return l.streamed }
