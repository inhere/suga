package output

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Code highlighting of chat's fenced code blocks (chat.code_highlight, on by
// default): the language named by the fence picks the lexer and every complete
// line is colored as it arrives, so a long block still streams. Coloring a line
// on its own cannot know a multi-line string or comment, so those may be colored
// as plain code - a whole block buffered to its end would delay the answer.
const (
	// MDDefaultCodeTheme is the style chat uses when chat.code_theme is not set.
	// It is a solarized based palette of its own: its hues stay legible on a dark
	// and on a light terminal, and the plain text keeps the terminal's own color,
	// which is the only way a single theme can suit both backgrounds (a style
	// that colors plain text near-white vanishes on a light terminal).
	MDDefaultCodeTheme = "suga"
	// mdCodeTerminalFormat is the ANSI 256 color formatter: it needs no terminal
	// background information and degrades to the nearest 256 color cube entry.
	mdCodeTerminalFormat = "terminal256"
)

// Register the default style by name, so chat.code_theme can name it too.
func init() {
	styles.Register(chroma.MustNewStyle(MDDefaultCodeTheme, chroma.StyleEntries{
		chroma.Text:              "",
		chroma.Error:             "#dc322f",
		chroma.Comment:           "italic #93a1a1",
		chroma.Keyword:           "bold #859900",
		chroma.KeywordType:       "bold #b58900",
		chroma.KeywordConstant:   "bold #cb4b16",
		chroma.Name:              "",
		chroma.NameAttribute:     "#268bd2",
		chroma.NameClass:         "bold #268bd2",
		chroma.NameFunction:      "#268bd2",
		chroma.NameBuiltin:       "#268bd2",
		chroma.NameTag:           "#268bd2",
		chroma.LiteralString:     "#2aa198",
		chroma.LiteralNumber:     "#d33682",
		chroma.Operator:          "#cb4b16",
		chroma.OperatorWord:      "bold #cb4b16",
		chroma.Punctuation:       "",
		chroma.GenericDeleted:    "#dc322f",
		chroma.GenericInserted:   "#859900",
		chroma.GenericHeading:    "bold #268bd2",
		chroma.GenericSubheading: "bold #268bd2",
		chroma.GenericEmph:       "italic",
		chroma.GenericStrong:     "bold",
		chroma.Background:        "",
	}))
}

// mdCode highlights the lines of one fenced code block: its lexer, the shared
// terminal256 formatter and the style the person chose.
type mdCode struct {
	lang   string
	lexer  chroma.Lexer
	style  *chroma.Style
	format chroma.Formatter
}

// newMDCode returns the highlighter of the block opened by a fence with info, or
// nil when the language is unknown (the block then keeps the plain dim text) or
// the theme is unusable.
func newMDCode(info, theme string) *mdCode {
	lang := mdCodeLang(info)
	if lang == "" {
		return nil
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return nil
	}
	if theme == "" {
		theme = MDDefaultCodeTheme
	}
	style := styles.Get(theme)
	if style == nil {
		style = styles.Get(MDDefaultCodeTheme)
	}
	if style == nil {
		return nil
	}
	format := formatters.Get(mdCodeTerminalFormat)
	if format == nil {
		return nil
	}
	return &mdCode{lang: lang, lexer: lexer, style: style, format: format}
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

// line colors one complete code line; ok is false when the lexer or the
// formatter failed, and the caller keeps the plain text.
func (c *mdCode) line(s string) (string, bool) {
	if c == nil || c.lexer == nil || c.style == nil || c.format == nil {
		return "", false
	}
	iterator, err := c.lexer.Tokenise(nil, s)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	if err := c.format.Format(&b, c.style, iterator); err != nil {
		return "", false
	}
	// The formatter may repeat the line ending it was given; the caller prints
	// the newline, so the colored text must not carry one of its own.
	return strings.TrimRight(b.String(), "\r\n"), true
}

// CodeBlock is one fenced code block of a rendered message: the language of its
// fence, every line as the model wrote it and how many lines the fold limit hid
// (chat.code_max_lines). chat's :code command shows one of them again in full.
type CodeBlock struct {
	Lang   string
	Lines  []string
	Folded int
}
