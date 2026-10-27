package chatui

import (
	"strings"

	"github.com/gookit/color"
)

// Code highlighting of chat's fenced code blocks (chat.code_highlight, on by
// default): the language named by the fence picks a small set of rules and every
// complete line is colored as it arrives, so a long block still streams. The
// highlighter is our own rather than a syntax highlighting library: a library of
// lexers and themes cost 4 MB of binary for four colors, and chat only needs to
// tell comments, strings, numbers and keywords apart - everything else keeps the
// terminal's own color. Coloring a line on its own cannot know a multi-line
// string, so only a block comment carries a state from one line to the next.

// The four styles of a highlighted line are those of the theme's palette
// (mdThemes in mdterm.go): 16 color ANSI ones, so they stay legible with any
// terminal palette.

// mdLang is what the highlighter knows about one language: the words it colors
// as keywords, the markers starting a comment and the quote characters opening a
// string.
type mdLang struct {
	keywords map[string]bool
	line     []string  // markers of a comment that ends with its line
	block    [2]string // opening and closing marker of a block comment (empty: none)
	quotes   string    // characters opening a string
	fold     bool      // keywords are matched case insensitively (SQL)
}

// The rules of the languages chat colors; an alias names the same rules.
var (
	mdLangGo = &mdLang{
		keywords: mdWordSet(`break case chan const continue default defer else
			fallthrough for func go goto if import interface map package range return
			select struct switch type var true false nil iota`),
		line:   []string{"//"},
		block:  [2]string{"/*", "*/"},
		quotes: "\"'`",
	}
	mdLangPython = &mdLang{
		keywords: mdWordSet(`and as assert async await break class continue def del
			elif else except finally for from global if import in is lambda nonlocal
			not or pass raise return try while with yield True False None`),
		line:   []string{"#"},
		quotes: "\"'",
	}
	mdLangJS = &mdLang{
		keywords: mdWordSet(`async await break case catch class const continue
			debugger default delete do else export extends false finally for from
			function get if implements import in instanceof interface let new null of
			private protected public readonly return set static super switch this
			throw true try type typeof undefined var void while yield`),
		line:   []string{"//"},
		block:  [2]string{"/*", "*/"},
		quotes: "\"'`",
	}
	mdLangShell = &mdLang{
		keywords: mdWordSet(`case declare do done elif else esac export fi for
			function if in local readonly return select shift source then time until
			unset while true false`),
		line:   []string{"#"},
		quotes: "\"'`",
	}
	mdLangJSON = &mdLang{
		keywords: mdWordSet(`true false null`),
		quotes:   "\"",
	}
	mdLangYAML = &mdLang{
		keywords: mdWordSet(`true false null yes no on off`),
		line:     []string{"#"},
		quotes:   "\"'",
	}
	mdLangSQL = &mdLang{
		keywords: mdWordSet(`add all alter and as asc between by case cast column
			commit constraint create cross default delete desc distinct drop else end
			exists foreign from full group having in index inner insert intersect into
			is join key left like limit not null offset on or order outer primary
			references right rollback select set table then union unique update values
			view when where with`),
		line:   []string{"--"},
		block:  [2]string{"/*", "*/"},
		quotes: "\"'",
		fold:   true,
	}
	mdLangRust = &mdLang{
		keywords: mdWordSet(`as async await break const continue crate dyn else enum
			extern false fn for if impl in let loop match mod move mut pub ref return
			self static struct super trait true type unsafe use where while`),
		line:   []string{"//"},
		block:  [2]string{"/*", "*/"},
		quotes: "\"'",
	}
	mdLangJava = &mdLang{
		keywords: mdWordSet(`abstract assert boolean break byte case catch char class
			const continue default do double else enum extends false final finally
			float for if implements import instanceof int interface long native new
			null package private protected public record return short static strictfp
			super switch synchronized this throw throws transient true try var void
			volatile while`),
		line:   []string{"//"},
		block:  [2]string{"/*", "*/"},
		quotes: "\"'",
	}
	mdLangC = &mdLang{
		keywords: mdWordSet(`auto bool break case catch char class const constexpr
			continue decltype default delete do double else enum explicit extern false
			float for goto if inline int long namespace new noexcept nullptr operator
			override private protected public register return short signed sizeof static
			struct switch template this throw true try typedef typename union unsigned
			using virtual void volatile while`),
		line:   []string{"//"},
		block:  [2]string{"/*", "*/"},
		quotes: "\"'",
	}
)

// mdCodeLangs maps the language a fence names (lower case) to its rules; an
// unknown language is not highlighted at all.
var mdCodeLangs = map[string]*mdLang{
	"go": mdLangGo, "golang": mdLangGo,
	"python": mdLangPython, "py": mdLangPython, "python3": mdLangPython,
	"javascript": mdLangJS, "js": mdLangJS, "jsx": mdLangJS, "mjs": mdLangJS, "cjs": mdLangJS,
	"typescript": mdLangJS, "ts": mdLangJS, "tsx": mdLangJS,
	"sh": mdLangShell, "bash": mdLangShell, "zsh": mdLangShell, "shell": mdLangShell,
	"json": mdLangJSON, "jsonc": mdLangJSON,
	"yaml": mdLangYAML, "yml": mdLangYAML,
	"sql":  mdLangSQL,
	"rust": mdLangRust, "rs": mdLangRust,
	"java": mdLangJava,
	"c":    mdLangC, "h": mdLangC, "cpp": mdLangC, "c++": mdLangC, "cc": mdLangC,
	"cxx": mdLangC, "hpp": mdLangC, "hxx": mdLangC, "objc": mdLangC,
}

// mdWordSet is the keyword set of a language, written as one blank separated
// string.
func mdWordSet(words string) map[string]bool {
	set := make(map[string]bool)
	for _, word := range strings.Fields(words) {
		set[word] = true
	}
	return set
}

// mdCode highlights the lines of one fenced code block: the rules of its language
// and whether a block comment is still open.
type mdCode struct {
	lang    string
	rules   *mdLang
	styles  *mdPalette // the theme's code highlight colors (chat.theme)
	inBlock bool       // inside a block comment that started on an earlier line
}

// newMDCode returns the highlighter of the block opened by a fence with info, or
// nil when the language is unknown (the block then keeps the plain dim text).
func newMDCode(info string, styles *mdPalette) *mdCode {
	lang := mdCodeLang(info)
	if lang == "" {
		return nil
	}
	rules := mdCodeLangs[lang]
	if rules == nil {
		return nil
	}
	return &mdCode{lang: lang, rules: rules, styles: styles}
}

// mdCodeLang is the language a fence names: its first word, without braces or a
// leading dot ("```go", "```{.go}", "```go title=x").
func mdCodeLang(info string) string {
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return ""
	}
	lang := strings.Trim(strings.ToLower(fields[0]), "{}.")
	return lang
}

// line colors one complete code line, without its newline; the text of the line
// is kept byte for byte, only SGR styles are added around what it recognizes.
func (c *mdCode) line(s string) string {
	if c == nil || c.rules == nil {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		// A block comment opened on an earlier line runs to its end, or over the
		// end of this line when it is still not closed.
		if c.inBlock {
			end, closed := mdCodeBlockEnd(s, i, c.rules.block[1])
			c.inBlock = !closed
			out.WriteString(mdCodePaint(c.styles.comment, s[i:end]))
			i = end
			continue
		}
		if open := c.rules.block[0]; open != "" && strings.HasPrefix(s[i:], open) {
			end, closed := mdCodeBlockEnd(s, i+len(open), c.rules.block[1])
			c.inBlock = !closed
			out.WriteString(mdCodePaint(c.styles.comment, s[i:end]))
			i = end
			continue
		}
		if c.rules.lineCommentAt(s, i) {
			out.WriteString(mdCodePaint(c.styles.comment, s[i:]))
			return out.String()
		}
		if strings.IndexByte(c.rules.quotes, s[i]) >= 0 {
			end := mdCodeStringEnd(s, i, s[i])
			out.WriteString(mdCodePaint(c.styles.codeStr, s[i:end]))
			i = end
			continue
		}
		if mdCodeDigit(s[i]) && (i == 0 || !mdWordByte(s[i-1])) {
			end := mdCodeNumberEnd(s, i)
			out.WriteString(mdCodePaint(c.styles.number, s[i:end]))
			i = end
			continue
		}
		if mdWordByte(s[i]) {
			end := i + 1
			for end < len(s) && mdWordByte(s[end]) {
				end++
			}
			word := s[i:end]
			if c.rules.keyword(word) {
				out.WriteString(mdCodePaint(c.styles.keyword, word))
			} else {
				out.WriteString(word)
			}
			i = end
			continue
		}
		out.WriteString(s[i : i+1])
		i++
	}
	return out.String()
}

// mdCodeBlockEnd returns the end of a block comment whose body starts at s[i],
// and whether its closing marker was found on this line (the end of the line
// otherwise).
func mdCodeBlockEnd(s string, i int, close string) (int, bool) {
	if j := strings.Index(s[i:], close); j >= 0 {
		return i + j + len(close), true
	}
	return len(s), false
}

// mdCodeStringEnd returns the end of the string opening at s[i], with quote as
// its opening quote: the closing quote, or the end of the line for one that stays
// open (the string does not carry over to the next line, which may open its own
// again). A backslash escapes the next byte, except in a raw string (`).
func mdCodeStringEnd(s string, i int, quote byte) int {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if quote != '`' {
				j++ // the escaped byte is not the closing quote
			}
		case quote:
			return j + 1
		}
	}
	return len(s)
}

// mdCodeNumberEnd returns the end of the number starting at s[i]: its digits with
// a 0x hexadecimal prefix, a fractional part and an exponent.
func mdCodeNumberEnd(s string, i int) int {
	j := i
	if s[j] == '0' && j+1 < len(s) && (s[j+1] == 'x' || s[j+1] == 'X') {
		for j += 2; j < len(s) && mdCodeHexDigit(s[j]); j++ {
		}
		return j
	}
	for ; j < len(s) && mdCodeDigit(s[j]); j++ {
	}
	if j+1 < len(s) && s[j] == '.' && mdCodeDigit(s[j+1]) {
		for j++; j < len(s) && mdCodeDigit(s[j]); j++ {
		}
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		if k < len(s) && mdCodeDigit(s[k]) {
			for j = k; j < len(s) && mdCodeDigit(s[j]); j++ {
			}
		}
	}
	return j
}

// lineCommentAt reports whether a comment of the language starts at s[i]. A "#"
// or "--" only starts one at the start of the line or behind a blank, so that a
// word like "a#b" or a value like "1--2" is not taken for one.
func (l *mdLang) lineCommentAt(s string, i int) bool {
	for _, marker := range l.line {
		if !strings.HasPrefix(s[i:], marker) {
			continue
		}
		if (marker[0] == '#' || marker[0] == '-') && i > 0 && s[i-1] != ' ' && s[i-1] != '\t' {
			continue
		}
		return true
	}
	return false
}

// keyword reports whether word is a keyword of the language (matched without case
// for SQL).
func (l *mdLang) keyword(word string) bool {
	if l.fold {
		return l.keywords[strings.ToLower(word)]
	}
	return l.keywords[word]
}

// mdCodePaint wraps s in the SGR style; the highlighter is only built for a
// colored terminal, so there is no plain path of its own.
func mdCodePaint(style color.Style, s string) string {
	if s == "" {
		return ""
	}
	return style.Render(s)
}

// mdCodeDigit reports whether b is a decimal digit.
func mdCodeDigit(b byte) bool { return b >= '0' && b <= '9' }

// mdCodeHexDigit reports whether b is a hexadecimal digit.
func mdCodeHexDigit(b byte) bool {
	return mdCodeDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// CodeBlock is one fenced code block of a rendered message: the language of its
// fence, every line as the model wrote it and how many lines the fold limit hid
// (chat.code_max_lines). chat's :code command shows one of them again in full.
type CodeBlock struct {
	Lang   string
	Lines  []string
	Folded int
}
