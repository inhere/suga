package codeintel

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/goccy/go-yaml"
)

// The language rule files (design docs/design/2026-10-22-lang-rules-design.md):
// one embedded YAML per language describes its extensions, declaration lines,
// comment and string syntax, ctags language and test command. The layering
// matches the output rules and the nudge rules: built-in, then
// $SUGA_HOME/lang-rules.yaml, then <workspace>/.suga/lang-rules.yaml, where a
// same-name rule replaces an earlier one, a new name is appended and
// enabled: false removes a rule.
//
//go:embed langrules/*.yaml
var builtinLangRuleFiles embed.FS

// builtinLangRuleOrder is the order the embedded rule files are read in: the
// three migrated languages first (their registry position is the historical one
// of the Go recognisers they replace), then the languages added by rule.
var builtinLangRuleOrder = []string{
	"langrules/javascript.yaml",
	"langrules/python.yaml",
	"langrules/java.yaml",
	"langrules/csharp.yaml",
	"langrules/kotlin.yaml",
	"langrules/ruby.yaml",
	"langrules/cpp.yaml",
}

// codeLanguageExts are the extensions of the languages whose extractor stays in
// Go code (Go, PHP, YAML, TOML): a rule may not take one of them away, so a
// stray lang-rules.yaml cannot silently replace the exact Go parser or the
// configuration key listing (design section 4.1).
var codeLanguageExts = []string{".go", ".php", ".yaml", ".yml", ".toml"}

// LangRuleSkip is one rule file that was skipped, with the reason: a manifest
// shows it, because a rule file with a typo would otherwise silently not apply.
type LangRuleSkip struct {
	File string `json:"file"`
	Err  string `json:"err"`
}

// LangRegistry is the effective language registry: the built-in rules merged
// with the loaded files, plus what the load skipped.
type LangRegistry struct {
	// Languages are the enabled rules in registry order (built-in first).
	Languages []LangRule
	// Skipped are the files that did not load, with their reason.
	Skipped []LangRuleSkip
	// Warnings are the non-fatal problems of a load (an extension a later rule
	// took from an earlier, differently named one).
	Warnings []string
	// Disabled lists the code languages a rule file switched off.
	Disabled []string

	// symbols and langs are the composed registries of the tool layer.
	symbols []symbolLanguage
	langs   []Language
}

// LangManifest is one language as `suga inspect manifest` shows it.
type LangManifest struct {
	Name string   `json:"name"`
	Exts []string `json:"exts"`
	// Source is the layer the rule came from: "builtin" or the file's path.
	Source string `json:"source"`
	// Blocks is the declaration style (Go/PHY/YAML/TOML report their own
	// recogniser instead).
	Blocks string `json:"blocks"`
	// Ctags tells whether universal-ctags can extract the language's files too.
	Ctags bool `json:"ctags"`
	// Approx marks a declaration-line recogniser rather than a parser.
	Approx bool `json:"approx"`
}

// langState is the installed registry; nil means the built-in rules.
var langState atomic.Pointer[LangRegistry]

var (
	builtinRegistryOnce sync.Once
	builtinRegistry     LangRegistry
)

// BuiltinLangRegistry returns the registry of the embedded rules alone.
func BuiltinLangRegistry() LangRegistry {
	builtinRegistryOnce.Do(func() {
		var rules []LangRule
		for _, name := range builtinLangRuleOrder {
			data, err := builtinLangRuleFiles.ReadFile(name)
			if err != nil {
				panic("codeintel: built-in lang rules: " + err.Error())
			}
			file, err := parseLangRules(data, RuleSourceBuiltin)
			if err != nil {
				panic("codeintel: built-in lang rules " + name + ": " + err.Error())
			}
			rules = append(rules, file...)
		}
		reg := LangRegistry{Languages: rules}
		builtinRegistry = finishLangRegistry(reg)
	})
	return builtinRegistry
}

// ActiveLangRules returns the installed registry, or the built-in one when no
// rule file has been loaded (tests and tools built without wiring).
func ActiveLangRules() LangRegistry {
	if p := langState.Load(); p != nil {
		return *p
	}
	return BuiltinLangRegistry()
}

// InstallLangRules makes reg the process's language registry: the symbol
// extractors, the language registry (test commands) and the manifest read it.
// The wiring calls it once at startup, after loading the layered files.
func InstallLangRules(reg LangRegistry) {
	installed := finishLangRegistry(reg)
	langState.Store(&installed)
}

// finishLangRegistry composes the symbol and language registries of reg.
func finishLangRegistry(reg LangRegistry) LangRegistry {
	reg.symbols = composeSymbolLanguages(reg)
	reg.langs = composeLanguages(reg)
	return reg
}

// DefaultLangRules returns the built-in rules (no rule file loaded).
func DefaultLangRules() LangRegistry { return BuiltinLangRegistry() }

// parseLangRules decodes and compiles one rule file; source names the layer in
// each rule (builtin, or the file's path).
func parseLangRules(data []byte, source string) ([]LangRule, error) {
	var file struct {
		Languages []LangRule `yaml:"languages"`
	}
	// An unknown field is an error: a misspelt "synta:" would otherwise silently
	// mask nothing.
	if err := yaml.UnmarshalWithOptions(data, &file, yaml.DisallowUnknownField()); err != nil {
		return nil, err
	}
	if len(file.Languages) == 0 {
		return nil, errors.New("languages is required and must list at least one language")
	}
	for i := range file.Languages {
		r := &file.Languages[i]
		r.Source = source
		if r.Enabled != nil && !*r.Enabled {
			// enabled: false only switches the rule of that name off, so it needs
			// neither extention nor declarations.
			if strings.TrimSpace(r.Name) == "" {
				return nil, fmt.Errorf("languages[%d]: name is required", i)
			}
			continue
		}
		if err := r.compile(); err != nil {
			return nil, fmt.Errorf("language %q: %w", r.Name, err)
		}
		for _, ext := range r.Exts {
			if slices.Contains(codeLanguageExts, ext) {
				return nil, fmt.Errorf("language %q: extension %s belongs to a language whose extractor stays in code", r.Name, ext)
			}
		}
	}
	return file.Languages, nil
}

// LoadLangRules returns the built-in rules merged with the rule files in order
// (later files win): a same-name rule replaces an earlier one whole, a new name
// is appended, enabled: false removes the rule of that name. A missing file is
// skipped quietly; a file that does not parse or holds a broken rule is
// reported (and listed in Skipped) and skipped as a whole, like an
// output-rules file. The returned errors are exactly the skipped files'
// reasons; Warnings holds the non-fatal extension conflicts.
func LoadLangRules(paths ...string) (LangRegistry, []error) {
	base := BuiltinLangRegistry()
	base.Languages = slices.Clone(base.Languages)
	reg := LangRegistry{Languages: base.Languages}
	var errs []error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			reg.Skipped = append(reg.Skipped, LangRuleSkip{File: path, Err: err.Error()})
			continue
		}
		rules, err := parseLangRules(data, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			reg.Skipped = append(reg.Skipped, LangRuleSkip{File: path, Err: err.Error()})
			continue
		}
		var warnings []string
		reg.Languages, warnings = mergeLangRules(reg.Languages, rules)
		reg.Warnings = append(reg.Warnings, warnings...)
	}
	for _, r := range reg.Languages {
		if r.Enabled != nil && !*r.Enabled {
			reg.Disabled = append(reg.Disabled, r.Name)
		}
	}
	return finishLangRegistry(reg), errs
}

// mergeLangRules merges a file's rules into the loaded ones. A same-name rule
// replaces the earlier rule whole; enabled: false removes it (a code language
// then stays disabled, see LangRegistry.Disabled). A rule of another name
// holding the same extension takes it: the later rule wins and the conflict is
// a warning, never a silent shadow.
func mergeLangRules(base, extra []LangRule) ([]LangRule, []string) {
	var warnings []string
	for _, r := range extra {
		if r.Enabled != nil && !*r.Enabled {
			base = slices.DeleteFunc(base, func(old LangRule) bool { return old.Name == r.Name })
			base = append(base, r) // kept, so the disabled code language stays known
			continue
		}
		replaced := false
		for i := range base {
			if base[i].Name == r.Name {
				base[i], replaced = r, true
				break
			}
		}
		if !replaced {
			base = append(base, r)
		}
		for i := range base {
			if base[i].Name == r.Name {
				continue
			}
			for _, ext := range r.Exts {
				if !slices.Contains(base[i].Exts, ext) {
					continue
				}
				warnings = append(warnings, fmt.Sprintf("%s: language %q takes %s from %q", r.Source, r.Name, ext, base[i].Name))
				base[i].Exts = slices.DeleteFunc(base[i].Exts, func(old string) bool { return old == ext })
			}
		}
	}
	return base, warnings
}

// activeRule returns the enabled rule of a name.
func (r LangRegistry) activeRule(name string) (LangRule, bool) {
	for _, rule := range r.Languages {
		if rule.Name != name {
			continue
		}
		if rule.Enabled != nil && !*rule.Enabled {
			return LangRule{}, false
		}
		return rule, true
	}
	return LangRule{}, false
}

// disabled reports whether a code language of that name was switched off.
func (r LangRegistry) disabled(name string) bool { return slices.Contains(r.Disabled, name) }

// composeSymbolLanguages builds the symbol registry in the order the refusal
// message and the tool description list the languages: the code languages keep
// their historical positions (go, javascript, python, java, php, yaml, toml)
// and a rule language is placed on its position or appended when it is new.
func composeSymbolLanguages(reg LangRegistry) []symbolLanguage {
	code := map[string]symbolLanguage{
		"Go": goSymbolLanguage, "PHP": phpSymbolLanguage,
		"YAML": yamlSymbolLanguage, "TOML": tomlSymbolLanguage,
	}
	order := []string{"Go", "JavaScript/TypeScript", "Python", "Java", "PHP", "YAML", "TOML"}
	var out []symbolLanguage
	for _, name := range order {
		if lang, ok := code[name]; ok {
			if !reg.disabled(name) {
				out = append(out, lang)
			}
			continue
		}
		if rule, ok := reg.activeRule(name); ok {
			out = append(out, rule.symbolLanguage())
		}
	}
	for _, rule := range reg.Languages {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		if slices.Contains(order, rule.Name) {
			continue
		}
		out = append(out, rule.symbolLanguage())
	}
	return out
}

// composeLanguages builds the language registry (lang.go) in its historical
// order (go, rust, java, js, python, php), with a rule language on its position
// and a new one appended: the dependency roots and the base prompt's clauses
// then stay byte-identical.
func composeLanguages(reg LangRegistry) []Language {
	code := map[string]Language{"Go": goLanguage, "Rust": rustLanguage, "PHP": phpLanguage}
	order := []string{"Go", "Rust", "Java", "JavaScript/TypeScript", "Python", "PHP"}
	var out []Language
	for _, name := range order {
		if lang, ok := code[name]; ok {
			if !reg.disabled(name) {
				out = append(out, lang)
			}
			continue
		}
		if rule, ok := reg.activeRule(name); ok {
			out = append(out, rule.language())
		}
	}
	for _, rule := range reg.Languages {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		if slices.Contains(order, rule.Name) {
			continue
		}
		out = append(out, rule.language())
	}
	return out
}

// ManifestLanguages lists the effective languages for suga inspect manifest: the
// code languages with their own recogniser and every enabled rule language.
func (r LangRegistry) ManifestLanguages() []LangManifest {
	reg := r
	if reg.symbols == nil {
		reg = finishLangRegistry(reg)
	}
	var out []LangManifest
	for _, lang := range reg.symbols {
		blocks := lang.rule
		m := LangManifest{Name: lang.name, Exts: append([]string(nil), lang.exts...), Source: RuleSourceBuiltin, Approx: lang.approx, Ctags: len(lang.ctagsLanguage) > 0}
		if blocks {
			if rule, ok := reg.activeRule(lang.name); ok {
				m.Source, m.Blocks = rule.Source, rule.Blocks
			}
		} else {
			m.Source, m.Blocks = RuleSourceCode, RuleSourceCode
		}
		out = append(out, m)
	}
	return out
}

// RuleSourceBuiltin marks a rule that ships with the binary.
const RuleSourceBuiltin = "builtin"

// dependencyAPIClaases are the base prompt's dependency-API clauses of the rule
// languages: the sentence is part of the provider's cached prompt prefix, so a
// rule file cannot change it (design section 2), and the clause of a migrated
// language stays in Go code like the clause of Go, Rust and Java had it.
var dependencyAPIClaases = map[string]string{
	"Java": "for Java, `javap -public -cp <jar> <fully.qualified.Class>` with the jar under " +
		"~/.m2/repository/<group path>/<artifact>/<version>/ (`mvn -o dependency:sources` fetches the sources)",
}

// RuleSourceCode marks a language whose extractor stays in Go code.
const RuleSourceCode = "code"

// DeclLineLanguages names, in registry order, the languages whose declarations
// come from declaration lines rather than a parser: the tool descriptions and
// the approx note list them.
func DeclLineLanguages() string {
	var ruleLangs, codeLangs []string
	for _, lang := range ActiveLangRules().symbols {
		switch {
		case lang.rule:
			ruleLangs = append(ruleLangs, lang.name)
		case lang.name == "PHP" || lang.name == "TOML":
			codeLangs = append(codeLangs, lang.name)
		}
	}
	return joinAnd(append(ruleLangs, codeLangs...))
}

// MaskedLineLanguages names, in registry order, the languages whose references
// file_refs' approx line matcher masks comments and strings for: the rule
// languages and PHP. A configuration file (YAML/TOML) keeps its strings visible,
// so it is not listed.
func MaskedLineLanguages() string { return joinAnd(maskedLineLanguageNames()) }

// MaskedTextLanguages names the languages the approx note of a file_refs result
// lists: the masked ones plus the two configuration formats, whose string values
// stay visible on purpose.
func MaskedTextLanguages() string {
	return joinAnd(append(maskedLineLanguageNames(), "YAML", "TOML"))
}

func maskedLineLanguageNames() []string {
	var names []string
	for _, lang := range ActiveLangRules().symbols {
		if lang.rule || lang.name == "PHP" {
			names = append(names, lang.name)
		}
	}
	return names
}

// joinAnd joins words the way the tool descriptions read: "a, b and c".
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
