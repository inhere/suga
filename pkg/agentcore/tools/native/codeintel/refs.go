package codeintel

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// The reference scanning itself: which lines of one file hold the searched name
// and how each match is classified. It takes the file's name and content and
// returns the matches; reading the files, walking a directory and rendering the
// result stay in the tool (package native).

// ScanBareLineRefs is the fallback for readable text with no extractor. Unlike
// the registered approximate languages, it deliberately does not mask anything.
func ScanBareLineRefs(file string, src []byte, name, qual string, re *regexp.Regexp) ([]Ref, *RefDrops) {
	original := NewSourceLines(src)
	dropped := NewRefDrops()
	var refs []Ref
	for i, line := range original.Lines {
		if !re.MatchString(line) {
			continue
		}
		if !bareQualifierMatches(line, qual, name) {
			dropped.addText()
			continue
		}
		refs = append(refs, Ref{File: file, Line: i + 1, Class: BareRefClass(line, name), Text: RefLineText(line)})
	}
	return refs, dropped
}

// bareDeclKeywords are the words that introduce a declaration in the common
// languages without an extractor (Rust fn/struct/trait/mod, Ruby/Python def,
// Kotlin/Swift/C# class, Tera macro, ...): "<keyword> name" marks the line as
// the definition. A guess from text, like the rest of the fallback.
var bareDeclKeywords = `fn|def|func|function|fun|class|struct|enum|trait|interface|type|mod|module|macro|component|object|record`

// BareRefClass labels a fallback line from its text: def after a declaration
// keyword, call when the name is followed by "(", else ref. The classes keep
// the result readable (a Rust "pub fn file_type" is not one of its callers);
// they are not a parse.
func BareRefClass(line, name string) string {
	quoted := regexp.QuoteMeta(name)
	if regexp.MustCompile(`\b(?:` + bareDeclKeywords + `)\s+` + quoted + `\b`).MatchString(line) {
		return RefDef
	}
	if regexp.MustCompile(`(?:^|[^0-9A-Za-z_$])` + quoted + `\s*(?:::<[^>]*>)?\(`).MatchString(line) {
		return RefCall
	}
	return RefRef
}

// InTestDir reports whether a fallback file sits under a test directory
// (tests/, test/, __tests__/, spec/): languages without an extractor have no
// test file naming the tool knows, and Rust keeps its tests in such folders.
func InTestDir(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		switch part {
		case "tests", "test", "__tests__", "spec":
			return true
		}
	}
	return false
}

// bareQualifierMatches follows the fallback contract: a qualified query must
// appear as Q.name, Q::name, or Q->name in the unmasked source line.
func bareQualifierMatches(line, qual, name string) bool {
	if qual == "" {
		return true
	}
	for _, sep := range []string{".", "::", "->"} {
		token := qual + sep + name
		for start := 0; start < len(line); {
			i := strings.Index(line[start:], token)
			if i < 0 {
				break
			}
			i += start
			end := i + len(token)
			beforeOK := i == 0 || !isBareIdentByte(line[i-1])
			afterOK := end == len(line) || !isBareIdentByte(line[end])
			if beforeOK && afterOK {
				return true
			}
			start = i + 1
		}
	}
	return false
}

func isBareIdentByte(b byte) bool {
	return b == '_' || b == '$' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// IsRefDocumentation keeps prose files out of reference results.
func IsRefDocumentation(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".md", ".txt", ".rst":
		return true
	default:
		return false
	}
}

// ScanRefs extracts the references of one file: Go by parser (exact), the other
// languages of the registry by word-bounded text match (approx). A file type
// the registry does not know is an error; a file without the name yields
// nothing, which is why the caller filters on the same word-bounded pattern
// first. qual narrows the result to the Q of a qualified name and dropped is how
// many occurrences of the name qualified something else.
func ScanRefs(file string, src []byte, name, qual string, re *regexp.Regexp) ([]Ref, bool, *RefDrops, error) {
	lang, ok := symbolLanguageFor(file)
	if !ok {
		return nil, false, nil, fmt.Errorf("unsupported file type %q; supported: %s",
			filepath.Ext(file), SupportedSymbolLanguages())
	}
	if lang.referenceParser { // Go is the only language with a parsed reference search
		if !re.Match(src) {
			return nil, false, nil, nil
		}
		refs, dropped, err := scanGoRefs(file, src, name, qual)
		return refs, false, dropped, err
	}
	refs, dropped := scanLineRefs(file, src, name, qual)
	return refs, true, dropped, nil
}

// SplitRefName splits a possibly qualified name argument: the last dot segment
// is the name to search and the segment before it the qualifier Q that narrows
// the result, so "a.b.Run" searches Run with Q=b and "Run" searches it
// unqualified. A trailing or doubled dot falls back to the text as it is.
func SplitRefName(raw string) (name, qual string) {
	separator, i := ".", strings.LastIndexByte(raw, '.')
	for _, candidate := range []string{"::", "->"} {
		if j := strings.LastIndex(raw, candidate); j > i {
			separator, i = candidate, j
		}
	}
	if i <= 0 || i == len(raw)-len(separator) {
		return raw, ""
	}
	name, qual = raw[i+len(separator):], raw[:i]
	if separator == "." {
		if j := strings.LastIndexByte(qual, '.'); j >= 0 {
			qual = qual[j+1:]
		}
	}
	if qual == "" {
		return name, ""
	}
	return name, qual
}

// RefClass is how one match is labelled: the class, plus the qualifier of a
// selector or call, with the pkg: prefix when the qualifier is an imported
// package name.
func RefClass(r Ref) string {
	switch {
	case r.Qualifier == "":
		return r.Class
	case r.Pkg:
		return r.Class + "(pkg:" + r.Qualifier + ")"
	default:
		return r.Class + "(" + r.Qualifier + ")"
	}
}
