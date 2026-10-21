package codeintel

// The general form of a language's comment and string syntax (design
// docs/design/2026-10-22-lang-rules-design.md, section 4.1): a rule language
// declares it in its `syntax:` block, and the built-in styles are spelled as
// one of these too, so the recognisers have one masking path for both.
type maskSpec struct {
	// LineComment lists the openers of a comment that ends with the line.
	LineComment []string `yaml:"line_comment"`
	// BlockComment lists [opener, closer] pairs of a comment that may span
	// lines.
	BlockComment [][]string `yaml:"block_comment"`
	// Strings lists the quote characters of a string that ends with the line.
	Strings []string `yaml:"strings"`
	// MultilineStrings lists the delimiters of a string that may span lines
	// (Python's ''' and """, C#'s """, a backtick template string).
	MultilineStrings []string `yaml:"multiline_strings"`
	// LineStart lists the block comment openers that only open at a line start
	// (Ruby's =begin), with the closer required at a line start as well.
	LineStart []string `yaml:"line_start"`

	// BlankLineComments blanks a line comment in maskSpans instead of skipping
	// it: the configuration formats and PHP need it, because their recognisers
	// read whole declaration lines.
	BlankLineComments bool `yaml:"-"`
	// KeepStrings leaves single-line strings visible in maskInline: a
	// configuration file names things by its values, and the two formats keep
	// the degraded text match they had before they had an extractor.
	KeepStrings bool `yaml:"-"`
}

// The built-in syntaxes: the equivalents of the former maskBraces, maskPython,
// maskHash and maskToml styles, with exactly the same effect.
var (
	bracesMaskSpec = maskSpec{
		LineComment:      []string{"//"},
		BlockComment:     [][]string{{"/*", "*/"}},
		Strings:          []string{`"`, `'`},
		MultilineStrings: []string{`"""`, "`"},
	}
	pythonMaskSpec = maskSpec{
		LineComment:      []string{"#"},
		Strings:          []string{`"`, `'`},
		MultilineStrings: []string{`"""`, `'''`},
	}
	hashMaskSpec = maskSpec{
		LineComment:       []string{"#"},
		BlankLineComments: true,
		KeepStrings:       true,
	}
	tomlMaskSpec = maskSpec{
		LineComment:       []string{"#"},
		Strings:           []string{`"`, `'`},
		MultilineStrings:  []string{`"""`, `'''`},
		BlankLineComments: true,
	}
)

// spec returns the syntax of a built-in mask style; ok is false for PHP, whose
// masking (heredoc/nowdoc bodies, #[ attributes) stays in its own code.
func (s maskStyle) spec() (maskSpec, bool) {
	switch s {
	case maskBraces:
		return bracesMaskSpec, true
	case maskPython:
		return pythonMaskSpec, true
	case maskHash:
		return hashMaskSpec, true
	case maskToml:
		return tomlMaskSpec, true
	}
	return maskSpec{}, false
}

// hasPrefix reports whether src[i:] starts with s.
func hasPrefix(src []byte, i int, s string) bool {
	return i+len(s) <= len(src) && string(src[i:i+len(s)]) == s
}

// lineCommentAt reports whether a line comment starts at i.
func (m maskSpec) lineCommentAt(src []byte, i int) bool {
	for _, opener := range m.LineComment {
		if hasPrefix(src, i, opener) {
			return true
		}
	}
	return false
}

// blockAt returns the block comment pair open at i (the longest opener wins, so
// a three-character opener is preferred over a two-character one).
func (m maskSpec) blockAt(src []byte, i int) (open, closer string, ok bool) {
	for _, pair := range m.BlockComment {
		if len(pair) == 2 && hasPrefix(src, i, pair[0]) && len(pair[0]) > len(open) {
			open, closer, ok = pair[0], pair[1], true
		}
	}
	return open, closer, ok
}

// lineStartOnly reports whether opener may only open a block at a line start.
func (m maskSpec) lineStartOnly(opener string) bool {
	for _, s := range m.LineStart {
		if s == opener {
			return true
		}
	}
	return false
}

// multilineAt returns the multi-line string delimiter starting at i (the
// longest wins).
func (m maskSpec) multilineAt(src []byte, i int) (string, bool) {
	best := ""
	for _, q := range m.MultilineStrings {
		if hasPrefix(src, i, q) && len(q) > len(best) {
			best = q
		}
	}
	return best, best != ""
}

// stringAt returns the single-line string quote at i.
func (m maskSpec) stringAt(src []byte, i int) (byte, bool) {
	for _, q := range m.Strings {
		if q != "" && hasPrefix(src, i, q) {
			return src[i], true
		}
	}
	return 0, false
}

// atLineStart reports whether only whitespace precedes i on its line.
func atLineStart(src []byte, i int) bool {
	for j := i - 1; j >= 0 && src[j] != '\n'; j-- {
		if src[j] != ' ' && src[j] != '\t' {
			return false
		}
	}
	return true
}

// lineStartCloser finds the end of a block whose closer must sit at a line
// start (Ruby's =end); len(src) when it does not close.
func lineStartCloser(src []byte, from int, closer string) int {
	for j := from; j < len(src); j++ {
		if src[j] != '\n' || j+1+len(closer) > len(src) {
			continue
		}
		if hasPrefix(src, j+1, closer) {
			return j + 1 + len(closer)
		}
	}
	return len(src)
}

// maskSpansSpec is maskSpans for a declared syntax: it blanks the spans that
// can run over several lines - block comments and multi-line strings - so a
// line recogniser neither takes an example in a docstring or a comment for a
// declaration nor counts the braces inside them. Every masked byte becomes a
// space except newlines, so line numbers and the positions on unmasked lines
// stay as they were.
func maskSpansSpec(src []byte, spec maskSpec) []byte {
	out := append([]byte(nil), src...)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	// closing finds the end of a span opened at i (after the opener) that ends
	// with closer, honouring backslash escapes when escapes is true.
	closing := func(i int, closer string, escapes bool) int {
		for j := i; j < len(src); j++ {
			if escapes && src[j] == '\\' {
				j++
				continue
			}
			if hasPrefix(src, j, closer) {
				return j + len(closer)
			}
		}
		return len(src)
	}
	for i := 0; i < len(src); i++ {
		if open, closer, ok := spec.blockAt(src, i); ok {
			var end int
			if spec.lineStartOnly(open) {
				if !atLineStart(src, i) {
					continue // "=begin" is only a comment at a line start
				}
				end = lineStartCloser(src, i+len(open), closer)
			} else {
				end = closing(i+len(open), closer, false)
			}
			blank(i, end)
			i = end - 1
			continue
		}
		if spec.lineCommentAt(src, i) {
			end := i
			for end < len(src) && src[end] != '\n' {
				end++
			}
			if spec.BlankLineComments {
				blank(i, end)
			}
			i = end - 1
			continue
		}
		if q, ok := spec.multilineAt(src, i); ok {
			end := closing(i+len(q), q, true)
			blank(i+len(q), end-len(q))
			i = end - 1
			continue
		}
		if _, ok := spec.stringAt(src, i); ok {
			// A one-line string: skipped so a /* or a # inside it opens nothing.
			for j := i + 1; j < len(src) && src[j] != '\n'; j++ {
				if src[j] == '\\' {
					j++
					continue
				}
				if src[j] == src[i] {
					i = j
					break
				}
			}
		}
	}
	return out
}

// maskInlineSpec is maskInline for a declared syntax: it blanks the line
// comments and the content of the single-line string literals, which maskSpans
// leaves alone on purpose. A reference search matches every line, so "ReadOnly"
// in a literal or after a comment opener would otherwise be reported as a use.
func maskInlineSpec(src []byte, spec maskSpec) []byte {
	out := append([]byte(nil), src...)
	blank := func(from, to int) {
		if to > len(out) {
			to = len(out)
		}
		for i := from; i < to; i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	lineEnd := func(i int) int {
		for ; i < len(src) && src[i] != '\n'; i++ {
		}
		return i
	}
	// closing finds the end of the literal opened at i, or the end of its line
	// when it does not close there.
	closing := func(i int, quote byte) int {
		for j := i; j < len(src) && src[j] != '\n'; j++ {
			if src[j] == '\\' {
				j++
				continue
			}
			if src[j] == quote {
				return j + 1
			}
		}
		return lineEnd(i)
	}
	for i := 0; i < len(src); i++ {
		if spec.lineCommentAt(src, i) {
			end := lineEnd(i)
			blank(i, end)
			i = end - 1
			continue
		}
		if spec.KeepStrings {
			continue
		}
		if q, ok := spec.stringAt(src, i); ok {
			end := closing(i+1, q)
			blank(i, end)
			i = end - 1
		}
	}
	return out
}
