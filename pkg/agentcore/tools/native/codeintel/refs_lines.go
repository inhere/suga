package codeintel

import (
	"path/filepath"
	"regexp"
	"strings"
)

// The approximate reference matcher: the languages without a parser
// (JavaScript/TypeScript, Python, Java, PHP) are searched for a word-bounded
// occurrence of the name after masking the comments and string literals, which
// is what file_symbols' line recognisers do for declarations. Names and lines
// are trustworthy, "it is the same object" is not, so the tool marks the
// result approx.

// RefWordRe compiles the matcher for one name: the name surrounded by
// characters that cannot be part of an identifier in the supported languages
// (letters, digits, _ and $; PHP's variables write the name with a leading $,
// which makes "foo" not match "$foo"). Go's \b is not used because it counts $
// and - as boundaries, so $name and foo-name would match.
func RefWordRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^0-9A-Za-z_$])` + regexp.QuoteMeta(name) + `([^0-9A-Za-z_$]|$)`)
}

// refMaskStyle is the mask style of a language, from its file extension: the
// same split as the declaration recognisers (symbols_lines.go), plus the two
// configuration formats, whose comments are "#" (maskHash).
func refMaskStyle(file string) maskStyle {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".py", ".pyi":
		return maskPython
	case ".php":
		return maskPhp
	case ".yaml", ".yml", ".toml":
		return maskHash
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
func scanLineRefs(file string, src []byte, name, qual string) ([]Ref, *RefDrops) {
	style := refMaskStyle(file)
	masked := NewSourceLines(maskInline(maskSpans(src, style), style))
	original := NewSourceLines(src)
	re := RefWordRe(name)
	var out []Ref
	dropped := NewRefDrops()
	for i, line := range masked.Lines {
		if !re.MatchString(line) {
			continue
		}
		if !approxQualifierMatches(line, qual, name) {
			dropped.addText() // a text match has no parsed qualifier to name
			continue
		}
		// The line is shown as it is written: the masked line is not readable.
		class := RefRef
		if isConfigSymbolFile(file) {
			// A configuration key's defining line is labelled def, like the
			// fallback does it for the declaration keywords of other languages.
			class = configRefClass(line, name)
		}
		out = append(out, Ref{File: file, Line: i + 1, Class: class, Text: RefLineText(original.At(i + 1))})
	}
	return out, dropped
}

// isConfigSymbolFile reports whether file is one of the two configuration formats
// (YAML and TOML): the only files whose key lines are labelled def.
func isConfigSymbolFile(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".yaml", ".yml", ".toml":
		return true
	default:
		return false
	}
}

// configRefClass labels one line of a YAML/TOML file: def when the line defines
// the name as a key ("port: 8080", "- port: 8080", "port = 8080"), so "where is
// this config key declared" is visible at a glance. Every other line is a plain
// ref: how a config file uses a name (usually as a value) is text, not a call.
func configRefClass(line, name string) string {
	key := regexp.QuoteMeta(name)
	if regexp.MustCompile(`^\s*(?:-\s+)?["']?` + key + `["']?\s*(?::(?:\s|$)|=)`).MatchString(line) {
		return RefDef
	}
	return RefRef
}

// approxQualifierMatches reports whether a masked line holds the qualified name
// Q.name in one of the shapes the approximate languages write it: Q.name,
// Q::name (PHP's static access and the TypeScript fixture the tests use) or
// Q->name (PHP's object access), or Q and the name glued together. A text test
// on purpose: no parser.
func approxQualifierMatches(line, qual, name string) bool {
	if qual == "" {
		return true
	}
	for _, sep := range []string{"", ".", "::", "->"} {
		if strings.Contains(line, qual+sep+name) {
			return true
		}
	}
	return false
}
