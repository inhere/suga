package native

import (
	"path/filepath"
	"regexp"
	"strings"
)

// The approximate reference matcher: the languages without a parser
// (JavaScript/TypeScript, Python, Java) are searched for a word-bounded
// occurrence of the name after masking the comments and string literals, which
// is what file_symbols' line recognisers do for declarations. Names and lines
// are trustworthy, "it is the same object" is not, so the tool marks the
// result approx.

// refWordRe compiles the matcher for one name: the name surrounded by
// characters that cannot be part of an identifier in the supported languages
// (letters, digits, _ and $). Go's \b is not used because it counts $ and - as
// boundaries, so $name and foo-name would match.
func refWordRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^0-9A-Za-z_$])` + regexp.QuoteMeta(name) + `([^0-9A-Za-z_$]|$)`)
}

// refMaskStyle is the mask style of a language, from its file extension: the
// same split as the declaration recognisers (symbols_lines.go).
func refMaskStyle(file string) maskStyle {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".py", ".pyi":
		return maskPython
	default:
		return maskBraces
	}
}

// ScanLineRefs returns the approx references to name in one file: one Ref per
// matching line, class RefRef (a text match cannot tell a definition from a
// use). Comments and string literals are masked first (maskSpans, then
// maskInline), so a name inside them is no reference. The masking is the
// approximation: an interpolation in a template string is masked with the rest
// of the literal, so it is not reported.
func ScanLineRefs(file string, src []byte, name string) []Ref {
	refs, _ := scanLineRefs(file, src, name, "")
	return refs
}

// scanLineRefs is ScanLineRefs with the qualified-name filter of one file_refs
// call: with qual set (the Q of "Q.Name"), only the lines holding Q.name are
// kept and dropped counts the lines that only held other qualifiers.
func scanLineRefs(file string, src []byte, name, qual string) ([]Ref, int) {
	style := refMaskStyle(file)
	masked := newSourceLines(maskInline(maskSpans(src, style), style))
	original := newSourceLines(src)
	re := refWordRe(name)
	var out []Ref
	dropped := 0
	for i, line := range masked.lines {
		if !re.MatchString(line) {
			continue
		}
		if !approxQualifierMatches(line, qual, name) {
			dropped++
			continue
		}
		// The line is shown as it is written: the masked line is not readable.
		out = append(out, Ref{File: file, Line: i + 1, Class: RefRef, Text: refLineText(original.at(i + 1))})
	}
	return out, dropped
}

// approxQualifierMatches reports whether a masked line holds the qualified name
// Q.name in one of the shapes the approximate languages write it: Q.name,
// Q::name or Q and the name glued together. A text test on purpose: no parser.
func approxQualifierMatches(line, qual, name string) bool {
	if qual == "" {
		return true
	}
	for _, sep := range []string{"", ".", "::"} {
		if strings.Contains(line, qual+sep+name) {
			return true
		}
	}
	return false
}
