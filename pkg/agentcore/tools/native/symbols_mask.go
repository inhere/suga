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
