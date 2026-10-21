package codeintel

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
	// SymbolSection is a configuration file's table or mapping node: a TOML
	// [table] / [[array.of.tables]] header, or a YAML key whose value is a
	// mapping (a server: block). It is the closest of the vocabulary - a
	// namespace names a scope the same way (see phpSymbols' namespace rule) -, and
	// it is what file_symbols' kind filter can select: "section" answers "which
	// tables/blocks does this config declare".
	SymbolSection = "section"
	// SymbolKey is a configuration file's leaf key: a YAML/TOML entry whose value
	// is a scalar (or an array of scalars), named by its full dotted key path
	// (server.http.port). A config file's keys are its declarations.
	SymbolKey = "key"
)

// SymbolKinds is the whole kind vocabulary, in the order the messages list it.
var SymbolKinds = []string{SymbolFunc, SymbolMethod, SymbolType, SymbolConst, SymbolVar,
	SymbolClass, SymbolInterface, SymbolEnum, SymbolRecord, SymbolTrait, SymbolSection, SymbolKey}

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
	// Notes are extra lines the extractor wants the tool to show besides its
	// approx marker: what its own caps left out (YAML/TOML depth and key count),
	// which the tool alone cannot know. file_symbols and file_read's outline
	// print them after the declarations.
	Notes []string
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
	// referenceParser marks the one language whose references a real parse
	// classifies (Go's go/parser): file_refs uses its word-bounded line matcher
	// for every other file, whatever that file's declaration extractor is.
	referenceParser bool
	// extract returns the declarations of one file, in line order.
	extract func(file string, src []byte) ([]Symbol, error)
	// extractSet, when set, replaces extract for a language whose recogniser
	// builds the whole SymbolSet itself: a file that does not parse falls back to
	// line detection and must say so (YAML), and a recogniser's own caps must
	// travel with the result (YAML and TOML). extract is used for a language that
	// is just a symbol list plus the language's source and approx.
	extractSet func(file string, src []byte) (SymbolSet, error)
}

// symbolLanguages is the registry, in the order the refusal message and the
// tool description list the languages. A new language is appended: the order of
// the existing ones is what the tests and the reminders expect.
var symbolLanguages = []symbolLanguage{goSymbolLanguage, jsSymbolLanguage, pythonSymbolLanguage, javaSymbolLanguage, phpSymbolLanguage, yamlSymbolLanguage, tomlSymbolLanguage}

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
	// A language whose extractor builds the whole set (YAML) reports its own
	// approx flag and notes; the others are just a symbol list plus the
	// language's source and approx.
	if lang.extractSet != nil {
		set, err := lang.extractSet(file, src)
		if err != nil {
			return SymbolSet{}, err
		}
		slices.SortStableFunc(set.Symbols, func(a, b Symbol) int { return a.Line - b.Line })
		return set, nil
	}
	syms, err := lang.extract(file, src)
	if err != nil {
		return SymbolSet{}, err
	}
	slices.SortStableFunc(syms, func(a, b Symbol) int { return a.Line - b.Line })
	return SymbolSet{Symbols: syms, Approx: lang.approx, Source: lang.source}, nil
}

// SourceLines is one file's lines, split once so a signature lookup per symbol
// is an index. Line numbers are 1-based; "\r\n" ends are trimmed like the file
// tools do.
type SourceLines struct{ Lines []string }

func NewSourceLines(src []byte) SourceLines {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	return SourceLines{Lines: strings.Split(text, "\n")}
}

// At returns the trimmed line n, "" when the number is out of range.
func (s SourceLines) At(n int) string {
	if n < 1 || n > len(s.Lines) {
		return ""
	}
	return strings.TrimSpace(s.Lines[n-1])
}
