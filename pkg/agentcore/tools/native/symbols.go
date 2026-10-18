package native

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// The symbol extraction layer (design docs/design/2026-10-18-explore-efficiency-design.md,
// section "0.2 审核修订"): one extractor per language, shared by the
// file_symbols tool and, later, by file_read's outline. Three sources, tried in
// this order: universal-ctags when it is installed (precise, and it covers
// languages no built-in extractor knows), the built-in extractor of the file's
// extension, and a refusal that names the supported languages for a file type
// no source covers (never a silent empty result).

// The kinds the extractors report. Go keeps its own vocabulary
// (func/method/type/const/var), the other languages theirs (class, interface,
// enum, record, trait); file_symbols' kind filter accepts either.
const (
	SymbolFunc      = "func"
	SymbolMethod    = "method"
	SymbolType      = "type"
	SymbolConst     = "const"
	SymbolVar       = "var"
	SymbolClass     = "class"
	SymbolInterface = "interface"
	SymbolEnum      = "enum"
	SymbolRecord    = "record"
	// SymbolTrait is PHP's trait, which is neither a class nor an interface: its
	// own kind keeps a listing honest (a language-specific kind, like the ones
	// ctags reports and the vocabulary does not know).
	SymbolTrait = "trait"
)

// SymbolKinds is the whole kind vocabulary, in the order the messages list it.
var SymbolKinds = []string{SymbolFunc, SymbolMethod, SymbolType, SymbolConst, SymbolVar,
	SymbolClass, SymbolInterface, SymbolEnum, SymbolRecord, SymbolTrait}

// The names of the extraction sources (SymbolSet.Source).
const (
	sourceGoParser  = "go/parser"
	sourceCtags     = "universal-ctags"
	sourceDeclLines = "declaration lines"
)

// Symbol is one declaration of a source file: where it is, what its language
// calls it, its name, and the trimmed text of its declaration line (so a Go
// method shows its receiver and a Java method its parameters).
type Symbol struct {
	File      string // as the caller names the file: relative to the workspace, absolute under a read root
	Line      int    // 1-based line of the declaration
	Kind      string // one of SymbolKinds (a language-specific kind when ctags reports one the tool does not know)
	Name      string
	Signature string // the declaration's first line, trimmed
}

// SymbolSet is the result of extracting one file's declarations.
type SymbolSet struct {
	Symbols []Symbol
	// Approx marks a lightweight, line-based recognition instead of a parser:
	// the names and kinds are close, but they are not proof, so the tool says
	// so and a file_grep confirms a name that matters.
	Approx bool
	// Source names what did the extraction; the tests assert it.
	Source string
}

// symbolLanguage is one language of the extraction registry: the extensions it
// owns, how its declarations are found, and what universal-ctags calls it.
type symbolLanguage struct {
	name string   // spelled as the language registry (lang.go) spells the language
	exts []string // lower-case extensions including the dot, in the order messages list them
	// approx marks the line-based recogniser: it never fails, it just knows
	// less than a parser.
	approx bool
	// source is what SymbolSet.Source reports for this language.
	source string
	// ctagsLanguage maps an extension to universal-ctags' --language-force
	// value (missing = ctags is not used for it).
	ctagsLanguage map[string]string
	// extract returns the declarations of one file, in line order.
	extract func(file string, src []byte) ([]Symbol, error)
}

// symbolLanguages is the registry, in the order the refusal message and the
// tool description list the languages. A new language is appended: the order of
// the existing ones is what the tests and the reminders expect.
var symbolLanguages = []symbolLanguage{goSymbolLanguage, jsSymbolLanguage, pythonSymbolLanguage, javaSymbolLanguage, phpSymbolLanguage}

// symbolLanguageFor returns the extractor that owns file's extension.
func symbolLanguageFor(file string) (symbolLanguage, bool) {
	ext := strings.ToLower(filepath.Ext(file))
	for _, lang := range symbolLanguages {
		if slices.Contains(lang.exts, ext) {
			return lang, true
		}
	}
	return symbolLanguage{}, false
}

// SupportsSymbols reports whether a file of this name or extension has an
// extractor. A file of an unknown type is not analysed, but a caller asking
// for one path is told so instead of getting an empty list.
func SupportsSymbols(file string) bool {
	_, ok := symbolLanguageFor(file)
	return ok
}

// SupportedSymbolLanguages names every language of the extractor with its
// extensions: what a refusal and the tool description tell the model.
func SupportedSymbolLanguages() string {
	parts := make([]string, 0, len(symbolLanguages))
	for _, lang := range symbolLanguages {
		parts = append(parts, fmt.Sprintf("%s (%s)", lang.name, strings.Join(lang.exts, ", ")))
	}
	return strings.Join(parts, ", ")
}

// ExtractSymbols returns the declarations of one source file: file is both how
// the results name it and the path universal-ctags parses when it is
// installed, src is the file's content. A file type no source covers is
// refused with the list of supported languages.
func ExtractSymbols(file string, src []byte) (SymbolSet, error) {
	return ExtractSymbolsAt(file, file, src)
}

// ExtractSymbolsAt is ExtractSymbols for a file whose display name is not a
// usable argument of an external tool (a path relative to the workspace):
// file names the results, onDisk is where the content can be read again.
func ExtractSymbolsAt(file, onDisk string, src []byte) (SymbolSet, error) {
	lang, ok := symbolLanguageFor(file)
	if !ok {
		return SymbolSet{}, fmt.Errorf("unsupported file type %q; supported: %s", filepath.Ext(file), SupportedSymbolLanguages())
	}
	if set, found := ctagsSymbols(lang, file, onDisk, src); found {
		return set, nil
	}
	syms, err := lang.extract(file, src)
	if err != nil {
		return SymbolSet{}, err
	}
	slices.SortStableFunc(syms, func(a, b Symbol) int { return a.Line - b.Line })
	return SymbolSet{Symbols: syms, Approx: lang.approx, Source: lang.source}, nil
}

// sourceLines is one file's lines, split once so a signature lookup per symbol
// is an index. Line numbers are 1-based; "\r\n" ends are trimmed like the file
// tools do.
type sourceLines struct{ lines []string }

func newSourceLines(src []byte) sourceLines {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	return sourceLines{lines: strings.Split(text, "\n")}
}

// at returns the trimmed line n, "" when the number is out of range.
func (s sourceLines) at(n int) string {
	if n < 1 || n > len(s.lines) {
		return ""
	}
	return strings.TrimSpace(s.lines[n-1])
}
