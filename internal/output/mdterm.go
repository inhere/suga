package output

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/gookit/color"
)

// Terminal markdown rendering, used by chat only. The model answers in
// markdown; on a terminal that answer is shown as a plain-text approximation of
// it instead of the raw markers. `suga run`, stream-json, ACP and the transcript
// keep the exact text.
//
// Rendering is line based: MDTerm buffers the text after the last newline and
// renders it as soon as its newline (or the end of the message) arrives, so a
// long answer appears while it streams and a marker split in half by a delta
// boundary is still rendered whole.

const (
	// mdRuleWidth is the number of ─ characters drawn for a horizontal rule.
	mdRuleWidth = 40
	// mdFence is the short line that replaces a code fence; an opening fence
	// adds the language it names.
	mdFence = "───"
)

// Terminal styles of the markdown elements.
var (
	mdHeadingStyle = color.Style{color.OpBold, color.FgCyan}
	mdDimStyle     = color.Style{color.FgGray}
	mdCodeStyle    = color.Style{color.FgLightYellow}
	// mdHeaderStyle marks the header line of an aligned table (mdtable.go).
	mdHeaderStyle = color.Style{color.OpBold}
)

// List items: the indent, the marker and the space behind it are kept, only the
// bullet of an unordered list is replaced. The marker needs a space behind it,
// so "**bold**" and "*italic*" are not read as list items.
var (
	mdBulletItem   = regexp.MustCompile(`^([ \t]*)([-*+])([ \t]+)(.*)$`)
	mdNumberedItem = regexp.MustCompile(`^([ \t]*)(\d{1,9}[.)])([ \t]+)(.*)$`)
)

// MDTerm renders markdown text line by line to a terminal writer. Zero value is
// not usable; call NewMDTerm.
type MDTerm struct {
	w       io.Writer
	color   bool
	pending string // text after the last newline, not rendered yet
	block   bool   // inside a fenced code block; spans the lines of one message
	// Code highlighting of a fenced block (chat.code_highlight): the language
	// of its fence picks the rules that color it (mdcode.go).
	highlight bool
	// maxCode folds a code block after this many lines (chat.code_max_lines, 0:
	// no fold); the hidden lines are only counted and reported when it ends.
	maxCode    int
	width      int
	code       *mdCode
	codeLang   string
	codeText   []string
	codeLines  int
	codeFolded int
	// blocks are the code blocks of the current message; lastBlocks keeps those
	// of the most recent message that had one, for chat's :code command.
	blocks     []CodeBlock
	lastBlocks []CodeBlock
	// table buffers the lines of a pending GFM table (mdtable.go) until its end.
	table []string
	// extra holds further lines one input line produces (the end of a table, a
	// fold notice); they are printed right after it.
	extra []string
	// prefix, when set, is printed at the start of every message's first line: a
	// caller that cannot write the prefix itself (it would be a write without a
	// newline while an input line is on screen, see LiveText.SetWholeLines) has the
	// renderer, which knows where a line starts, do it.
	prefix  string
	started bool // the current message already printed its prefix
}

// NewMDTerm returns an MDTerm writing rendered lines to w; color styles them.
func NewMDTerm(w io.Writer, color bool) *MDTerm {
	return &MDTerm{w: w, color: color}
}

// SetCode configures fenced code blocks: highlight colors them by their
// language (chat.code_highlight; it needs color) and maxLines folds a block
// after that many lines (chat.code_max_lines, 0: no fold).
func (t *MDTerm) SetCode(highlight bool, maxLines int) {
	t.highlight, t.maxCode = highlight, maxLines
}

// SetWidth sets the terminal width used to leave a too wide table as it came
// (0: any width is accepted).
func (t *MDTerm) SetWidth(width int) { t.width = width }

// CodeBlocks returns the code blocks of the most recent message that had one, in
// the order they appeared (:code).
func (t *MDTerm) CodeBlocks() []CodeBlock { return t.lastBlocks }

// SetLinePrefix sets the text printed at the start of every message's first line
// ("" removes it).
func (t *MDTerm) SetLinePrefix(prefix string) { t.prefix = prefix }

// Write takes the next chunk of markdown text and prints every line it
// completes; the rest waits in the buffer for its newline.
func (t *MDTerm) Write(text string) {
	if text == "" {
		return
	}
	t.pending += text
	for {
		i := strings.IndexByte(t.pending, '\n')
		if i < 0 {
			return
		}
		line := t.pending[:i]
		t.pending = t.pending[i+1:]
		t.print(line)
	}
}

// Print renders text that arrives as one piece (a final answer that was not
// streamed) and ends it on a line of its own. Empty text only ends the line its
// prefix was written on.
func (t *MDTerm) Print(text string) {
	if text == "" {
		fmt.Fprintln(t.w)
		return
	}
	t.Write(text)
	t.Flush()
}

// Flush renders the buffered, still incomplete line and ends the message: a
// code block left open does not carry into the next message.
func (t *MDTerm) Flush() {
	if line := t.pending; line != "" {
		t.pending = ""
		t.print(line)
	}
	if t.block {
		// An unclosed block ends with the message: a folded one still reports the
		// lines it hid.
		t.endCode()
		t.block = false
	}
	t.putExtras()
	for _, line := range t.flushTable() {
		t.put(line)
	}
	if len(t.blocks) > 0 {
		t.lastBlocks = t.blocks
	}
	t.blocks = nil
	// The next message gets the line prefix again.
	t.started = false
}

// put prints one rendered line, with the message prefix in front of the first
// line of a message.
func (t *MDTerm) put(line string) {
	if t.prefix != "" && !t.started {
		line, t.started = t.prefix+line, true
	}
	fmt.Fprintln(t.w, line)
}

// putExtras prints the extra lines the last input line produced.
func (t *MDTerm) putExtras() {
	for _, line := range t.extra {
		t.put(line)
	}
	t.extra = nil
}

// print renders one input line; emit is false for a line the renderer hides
// (a buffered table row, a code line past the fold limit).
func (t *MDTerm) print(line string) {
	rendered, emit := t.renderLine(line)
	if emit {
		t.put(rendered)
	}
	t.putExtras()
}

// renderLine renders one complete markdown line, without its newline; emit is
// false for a line that waits in a buffer or is hidden by folding.
func (t *MDTerm) renderLine(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	// Fences first: inside a code block nothing else is interpreted.
	if strings.HasPrefix(trimmed, "```") {
		info := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
		switch {
		case !t.block:
			t.block = true
			t.startCode(info)
			if info != "" {
				return t.paint(mdDimStyle, mdFence+" "+info), true
			}
			return t.paint(mdDimStyle, mdFence), true
		case info == "":
			t.endCode()
			t.block = false
			return t.paint(mdDimStyle, mdFence), true
		}
		// A "```lang" line inside the block is content, not a closing fence.
	}
	if t.block {
		return t.codeLine(s)
	}
	// A buffered table ends at the first line that is not a table row.
	if t.table != nil {
		if strings.HasPrefix(trimmed, "|") {
			t.table = append(t.table, s)
			return "", false
		}
		t.extra = nil
		// The table's lines go out before this one.
		for _, line := range t.flushTable() {
			t.put(line)
		}
	}
	if mdRule(trimmed) {
		return t.paint(mdDimStyle, strings.Repeat("─", mdRuleWidth)), true
	}
	if _, text, ok := mdHeading(trimmed); ok {
		return t.inline(text, mdHeadingStyle), true
	}
	if strings.HasPrefix(trimmed, "|") {
		// The line may open a GFM table: its next line (the delimiter row) decides,
		// so it waits with the rows that follow it (mdtable.go).
		t.table = []string{s}
		return "", false
	}
	if text, depth, ok := mdQuote(s); ok {
		return t.paint(mdDimStyle, strings.Repeat("│ ", depth)) + t.inline(text, mdDimStyle), true
	}
	if m := mdBulletItem.FindStringSubmatch(s); m != nil {
		return m[1] + "•" + m[3] + t.inline(m[4], nil), true
	}
	if m := mdNumberedItem.FindStringSubmatch(s); m != nil {
		return m[1] + m[2] + m[3] + t.inline(m[4], nil), true
	}
	return t.inline(s, nil), true
}

// startCode prepares the code block opened by a fence with info: its language,
// a highlighter when highlighting is on (a colored terminal and
// chat.code_highlight) and its line counters of the fold limit.
func (t *MDTerm) startCode(info string) {
	t.codeLang = mdCodeLang(info)
	t.codeText, t.codeLines, t.codeFolded = nil, 0, 0
	t.code = nil
	if t.highlight && t.color {
		t.code = newMDCode(info)
	}
}

// codeLine renders one line inside a fenced code block: highlighted for a known
// language, dim otherwise. Past chat.code_max_lines the line is only counted:
// the block's end reports how many were hidden.
func (t *MDTerm) codeLine(s string) (string, bool) {
	t.codeText = append(t.codeText, s)
	t.codeLines++
	if t.maxCode > 0 && t.codeLines > t.maxCode {
		t.codeFolded++
		return "", false
	}
	if t.code != nil {
		return t.code.line(s), true
	}
	return t.paint(mdDimStyle, s), true // code keeps its exact text and is dimmed
}

// endCode ends the open code block: its lines are kept for :code and a folded
// block gets the notice line naming that command.
func (t *MDTerm) endCode() {
	if len(t.codeText) > 0 || t.codeFolded > 0 {
		t.blocks = append(t.blocks, CodeBlock{Lang: t.codeLang, Lines: t.codeText, Folded: t.codeFolded})
	}
	if t.codeFolded > 0 {
		t.extra = append(t.extra, t.paint(mdDimStyle,
			fmt.Sprintf("… 还有 %d 行（:code 查看全部）", t.codeFolded)))
	}
	t.code, t.codeLang, t.codeText, t.codeLines, t.codeFolded = nil, "", nil, 0, 0
}

// inline renders the inline markers of one line: `code`, **bold**, *italic*,
// _italic_ and [text](url). A marker without its partner is printed as it is,
// and code keeps its content untouched. base is the style of the plain text
// around the markers (the dim of a quote line, the heading style, ...); every
// styled part carries base plus its own marker style, so nesting keeps working.
func (t *MDTerm) inline(s string, base color.Style) string {
	var out, plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			out.WriteString(t.paint(base, plain.String()))
			plain.Reset()
		}
	}
	for i := 0; i < len(s); {
		rest := s[i:]
		switch {
		case rest[0] == '`':
			if j := strings.IndexByte(rest[1:], '`'); j >= 0 {
				flush()
				out.WriteString(t.paint(mdCodeStyle, rest[1:1+j]))
				i += j + 2
				continue
			}
		case strings.HasPrefix(rest, "**"):
			if j := strings.Index(rest[2:], "**"); j > 0 {
				flush()
				out.WriteString(t.inline(rest[2:2+j], mdWithStyle(base, color.OpBold)))
				i += j + 4
				continue
			}
		case rest[0] == '*':
			if j := strings.IndexByte(rest[1:], '*'); j > 0 {
				flush()
				out.WriteString(t.inline(rest[1:1+j], mdWithStyle(base, color.OpItalic)))
				i += j + 2
				continue
			}
		case rest[0] == '_':
			if end := mdUnderscoreClose(s, i); end > 0 {
				flush()
				out.WriteString(t.inline(s[i+1:end], mdWithStyle(base, color.OpItalic)))
				i = end + 1
				continue
			}
		case rest[0] == '[':
			if text, url, n, ok := mdLink(rest); ok {
				flush()
				out.WriteString(t.paint(base, text) + " (" + t.paint(base, url) + ")")
				i += n
				continue
			}
		}
		plain.WriteByte(s[i])
		i++
	}
	flush()
	return out.String()
}

// paint styles s with style, or returns it plain when colors are off (an empty
// style is plain as well).
func (t *MDTerm) paint(style color.Style, s string) string {
	if !t.color || len(style) == 0 {
		return s
	}
	return style.Render(s)
}

// mdWithStyle returns base with extra styles added (a new slice: base may be
// shared between the parts of a line).
func mdWithStyle(base color.Style, extra ...color.Color) color.Style {
	style := make(color.Style, 0, len(base)+len(extra))
	style = append(style, base...)
	return append(style, extra...)
}

// mdHeading reports the level of an ATX heading ("## Title") and its text.
// "#x" is not a heading.
func mdHeading(s string) (level int, text string, ok bool) {
	for level < len(s) && level < 6 && s[level] == '#' {
		level++
	}
	if level == 0 || (level < len(s) && s[level] != ' ' && s[level] != '\t') {
		return 0, "", false
	}
	return level, strings.TrimSpace(s[level:]), true
}

// mdRule reports whether the trimmed line is a horizontal rule ("---", "***").
func mdRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return s[0] == '-' || s[0] == '*' || s[0] == '_'
}

// mdQuote splits a blockquote line into its text and how many ">" markers it
// has (they may nest and may be indented).
func mdQuote(s string) (text string, depth int, ok bool) {
	rest := s
	for {
		trimmed := strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(trimmed, ">") {
			break
		}
		rest = strings.TrimPrefix(strings.TrimPrefix(trimmed, ">"), " ")
		depth++
	}
	if depth == 0 {
		return "", 0, false
	}
	return rest, depth, true
}

// mdUnderscoreClose returns the index (in s) of the "_" closing an emphasis that
// opens at s[i], or -1. Underscores inside a word (foo_bar_baz) are no emphasis,
// so identifiers in an answer are not italicized.
func mdUnderscoreClose(s string, i int) int {
	if i > 0 && mdWordByte(s[i-1]) {
		return -1
	}
	j := strings.IndexByte(s[i+1:], '_')
	if j <= 0 { // no "…_", or an empty "__" pair
		return -1
	}
	end := i + 1 + j
	if end+1 < len(s) && mdWordByte(s[end+1]) {
		return -1
	}
	return end
}

// mdWordByte reports whether b can be part of a word.
func mdWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// mdLink parses "[text](url)" at the start of s and reports how many bytes it
// takes; ok is false when the line has no complete link there.
func mdLink(s string) (text, url string, n int, ok bool) {
	if len(s) < 4 || s[0] != '[' {
		return "", "", 0, false
	}
	end := strings.IndexByte(s, ']')
	if end < 0 || end+1 >= len(s) || s[end+1] != '(' {
		return "", "", 0, false
	}
	close := strings.IndexByte(s[end+2:], ')')
	if close < 0 {
		return "", "", 0, false
	}
	return s[1:end], s[end+2 : end+2+close], end + 2 + close + 1, true
}
