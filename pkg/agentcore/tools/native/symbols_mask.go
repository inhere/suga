package native

// maskStyle selects which spans maskSpans blanks for a line recogniser.
type maskStyle int

const (
	// maskBraces is JavaScript/TypeScript and Java: /* */ comments, `...`
	// template strings and """ text blocks.
	maskBraces maskStyle = iota
	// maskPython is Python: '''...''' and """...""" strings (docstrings).
	maskPython
)

// maskSpans blanks the spans that can run over several lines - block comments
// and multi-line strings - so a line recogniser neither takes an example in a
// docstring or a comment for a declaration nor counts the braces inside them.
// Every masked byte becomes a space except newlines, so line numbers and the
// positions on unmasked lines stay as they were. Single-line strings and line
// comments are left alone: the recognisers already skip them.
func maskSpans(src []byte, style maskStyle) []byte {
	out := append([]byte(nil), src...)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	hasAt := func(i int, s string) bool {
		return i+len(s) <= len(src) && string(src[i:i+len(s)]) == s
	}
	// closing finds the end of a span opened at i (after the opener) that ends
	// with closer, honouring backslash escapes when escapes is true.
	closing := func(i int, closer string, escapes bool) int {
		for j := i; j < len(src); j++ {
			if escapes && src[j] == '\\' {
				j++
				continue
			}
			if hasAt(j, closer) {
				return j + len(closer)
			}
		}
		return len(src)
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch style {
		case maskBraces:
			switch {
			case hasAt(i, "//"):
				for i < len(src) && src[i] != '\n' {
					i++
				}
			case hasAt(i, "/*"):
				end := closing(i+2, "*/", false)
				blank(i, end)
				i = end - 1
			case hasAt(i, `"""`):
				end := closing(i+3, `"""`, true)
				blank(i+3, end-3)
				i = end - 1
			case c == '`':
				end := closing(i+1, "`", true)
				blank(i+1, end-1)
				i = end - 1
			case c == '"' || c == '\'':
				// A one-line string: skipped so a /* or a backtick inside it
				// opens nothing.
				for j := i + 1; j < len(src) && src[j] != '\n'; j++ {
					if src[j] == '\\' {
						j++
						continue
					}
					if src[j] == c {
						i = j
						break
					}
				}
			}
		case maskPython:
			switch {
			case c == '#':
				for i < len(src) && src[i] != '\n' {
					i++
				}
			case hasAt(i, `"""`) || hasAt(i, "'''"):
				quote := string(src[i : i+3])
				end := closing(i+3, quote, true)
				blank(i+3, end-3)
				i = end - 1
			case c == '"' || c == '\'':
				for j := i + 1; j < len(src) && src[j] != '\n'; j++ {
					if src[j] == '\\' {
						j++
						continue
					}
					if src[j] == c {
						i = j
						break
					}
				}
			}
		}
	}
	return out
}

// maskInline blanks what maskSpans leaves alone on purpose: the content of the
// single-line string literals and the tail of a line comment (// for the brace
// languages, # for Python). A declaration recogniser skips both by itself, but
// a reference search matches every line, so "ReadOnly" in a literal or
// "// ReadOnly is ..." would otherwise be reported as a use. Every masked byte
// becomes a space except newlines, so line numbers, column positions and the
// unmasked part of each line stay as they were. Backtick template strings are
// not looked at here: maskSpans already blanks them, single-line or not.
func maskInline(src []byte, style maskStyle) []byte {
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
	hasAt := func(i int, s string) bool {
		return i+len(s) <= len(src) && string(src[i:i+len(s)]) == s
	}
	lineEnd := func(i int) int {
		for ; i < len(src) && src[i] != '\n'; i++ {
		}
		return i
	}
	// closing finds the end of a span opened at i (after the opener) that ends
	// with closer, or the end of the line when it does not close there.
	closing := func(i int, closer string) int {
		for j := i; j < len(src) && src[j] != '\n'; j++ {
			if src[j] == '\\' {
				j++
				continue
			}
			if hasAt(j, closer) {
				return j + len(closer)
			}
		}
		return lineEnd(i)
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		if style == maskPython && c == '#' || style == maskBraces && hasAt(i, "//") {
			end := lineEnd(i)
			blank(i, end)
			i = end - 1
			continue
		}
		if c != '"' && c != '\'' {
			continue
		}
		// A triple-quoted string is already blanked by maskSpans, single-line or
		// not: only " and ' open a one-line string here.
		end := closing(i+1, string(c))
		blank(i, end)
		i = end - 1
	}
	return out
}
