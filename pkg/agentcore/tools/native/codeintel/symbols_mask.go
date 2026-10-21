package codeintel

import (
	"bytes"
	"regexp"
)

// maskStyle selects which spans maskSpans blanks for a line recogniser.
type maskStyle int

const (
	// maskBraces is JavaScript/TypeScript and Java: /* */ comments, `...`
	// template strings and """ text blocks.
	maskBraces maskStyle = iota
	// maskPython is Python: '''...''' and """...""" strings (docstrings).
	maskPython
	// maskPhp is PHP: // and # line comments (#[ opens an attribute, not a
	// comment), /* */ block comments and heredoc/nowdoc bodies (<<<EOT ... EOT;,
	// whose closing label may be indented). The HTML a ?> switches to is not
	// parsed: it is masked like code, so an apostrophe in it can blank the rest
	// of that line - never more than one line, and the design accepts it.
	maskPhp
	// maskHash is YAML and TOML for the reference search: "#" line comments.
	// String literals stay visible on purpose (see maskInline): a configuration
	// file names things by their values (stylesheets = "target.css"), and the two
	// formats keep the degraded text match they had before they had an extractor,
	// so masking a value would hide the reference the model asked for.
	maskHash
	// maskToml is TOML for the declaration recogniser: "#" line comments and the
	// multi-line strings (""" and ''') whose body must not read as a key.
	maskToml
)

// maskLineComment reports whether a line comment starts at i: // for the brace
// languages and PHP, # for Python and PHP - but not #[, which opens an
// attribute.
func maskLineComment(src []byte, i int, style maskStyle) bool {
	hasAt := func(s string) bool {
		return i+len(s) <= len(src) && string(src[i:i+len(s)]) == s
	}
	switch style {
	case maskPython:
		return src[i] == '#'
	case maskPhp:
		return hasAt("//") || src[i] == '#' && !hasAt("#[")
	case maskHash, maskToml:
		return src[i] == '#'
	default:
		return hasAt("//")
	}
}

// maskSpans blanks the spans that can run over several lines - block comments
// and multi-line strings - so a line recogniser neither takes an example in a
// docstring or a comment for a declaration nor counts the braces inside them.
// Every masked byte becomes a space except newlines, so line numbers and the
// positions on unmasked lines stay as they were. Single-line strings and line
// comments are left alone: the recognisers already skip them. PHP's line
// comments are an exception: they are blanked here, because its recogniser
// reads whole declaration lines and a brace inside a "# ..." comment must not
// shift the depth of what follows.
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
		case maskHash, maskToml:
			switch {
			case maskLineComment(src, i, style):
				end := i
				for end < len(src) && src[end] != '\n' {
					end++
				}
				blank(i, end)
				i = end - 1
			case style == maskToml && (hasAt(i, `"""`) || hasAt(i, "'''")):
				quote := string(src[i : i+3])
				end := closing(i+3, quote, true)
				blank(i+3, end-3)
				i = end - 1
			case style == maskToml && (c == '"' || c == '\''):
				// A one-line string: skipped so a # inside it opens no comment.
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
		case maskPhp:
			switch {
			case maskLineComment(src, i, maskPhp):
				end := i
				for end < len(src) && src[end] != '\n' {
					end++
				}
				blank(i, end)
				i = end - 1
			case hasAt(i, "/*"):
				end := closing(i+2, "*/", false)
				blank(i, end)
				i = end - 1
			case hasAt(i, "<<<"):
				// A heredoc or nowdoc: only blanked when its closing label is
				// found, so an unterminated opener never swallows the file (e.g. a
				// "<<<" in the HTML after a ?>).
				if end, ok := phpHeredocEnd(src, i); ok {
					blank(i, end)
					i = end - 1
				}
			case c == '"' || c == '\'':
				// A one-line string: skipped so a //, # or <<< inside it opens
				// nothing.
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

// phpLabelRe is the label of a heredoc/nowdoc opener (<<<EOT, <<<'EOT',
// <<<"EOT"), read from the text behind the "<<<".
var phpLabelRe = regexp.MustCompile(`^[ \t]*["']?([A-Za-z_\x80-\xff][0-9A-Za-z_\x80-\xff]*)["']?`)

// phpHeredocEnd finds the end of a heredoc/nowdoc whose opener starts at i
// (src[i:] begins with "<<<") and returns the byte just after its closing
// label. That label may be indented (PHP 7.3+) and is the first later line
// holding the label followed by a character that cannot extend it; ok is false
// when the opener carries no label or when no closing label follows, so a shift
// operator or a broken opener masks nothing.
func phpHeredocEnd(src []byte, i int) (int, bool) {
	m := phpLabelRe.FindSubmatchIndex(src[i+len("<<<"):])
	if m == nil {
		return 0, false
	}
	label := string(src[i+len("<<<")+m[2] : i+len("<<<")+m[3]])
	// The body starts on the line after the opener.
	start := i + len("<<<") + m[1]
	nl := bytes.IndexByte(src[start:], '\n')
	if nl < 0 {
		return 0, false
	}
	for line := start + nl + 1; line <= len(src); {
		end := line
		for end < len(src) && src[end] != '\n' {
			end++
		}
		text := src[line:end]
		indent := len(text) - len(bytes.TrimLeft(text, " \t"))
		if rest := text[indent:]; len(rest) >= len(label) && string(rest[:len(label)]) == label &&
			(len(rest) == len(label) || !phpIdentByte(rest[len(label)])) {
			return line + indent + len(label), true
		}
		if end >= len(src) {
			break
		}
		line = end + 1
	}
	return 0, false
}

// phpIdentByte reports whether c can extend a PHP identifier.
func phpIdentByte(c byte) bool {
	return c == '_' || c >= 0x80 ||
		'0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// maskInline blanks what maskSpans leaves alone on purpose: the content of the
// single-line string literals and the tail of a line comment (// for the brace
// languages, # for Python, // and # for PHP). A declaration recogniser skips
// both by itself, but a reference search matches every line, so "ReadOnly" in a
// literal or "// ReadOnly is ..." would otherwise be reported as a use. Every
// masked byte becomes a space except newlines, so line numbers, column
// positions and the unmasked part of each line stay as they were. Backtick
// template strings are not looked at here: maskSpans already blanks them,
// single-line or not.
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
		if maskLineComment(src, i, style) {
			end := lineEnd(i)
			blank(i, end)
			i = end - 1
			continue
		}
		if style == maskHash {
			// A configuration file's value is how it names things, and these two
			// formats keep the degraded text match they had before they had an
			// extractor: their string literals stay visible here.
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
