package codeintel

import (
	"fmt"
	"regexp"
	"strings"
)

// One language of the rule-file registry (design
// docs/design/2026-10-22-lang-rules-design.md, section 3): a declaration-line
// language described by data instead of Go code, so a new language costs one
// YAML entry. The built-in languages JavaScript/TypeScript, Python and Java are
// spelled this way too; Go, PHP, YAML and TOML keep their Go implementation
// (design section 2, non-goals).

// The block styles a rule language declares.
const (
	// BlocksBrace is a brace-delimited language (C#, Kotlin, C/C++,
	// JavaScript/TypeScript, Java): the brace depth tells a type body from a
	// function body.
	BlocksBrace = "brace"
	// BlocksIndent is an indentation-delimited language (Python): a block ends
	// where a line is no deeper than its first line.
	BlocksIndent = "indent"
	// BlocksEndKeyword is a language whose blocks a keyword closes (Ruby's end):
	// an opener line deepens the stack, the keyword pops it.
	BlocksEndKeyword = "end_keyword"
)

// DeclRule is one `decls:` entry: a declaration-line pattern and what it
// reports. It compiles into a braceDecl, the matcher every block style shares.
type DeclRule struct {
	// Pattern is a Go regular expression matched against the trimmed line.
	Pattern string `yaml:"pattern"`
	// Kind is the kind reported; KindGroup names the capture group holding it
	// instead (a Java "class|interface|enum|record" line).
	Kind      string `yaml:"kind"`
	KindGroup int    `yaml:"kind_group"`
	// Name is the capture group holding the declaration's name.
	Name int `yaml:"name"`
	// Member limits the rule to a type body: a class member or a nested type,
	// never a statement of a function body.
	Member bool `yaml:"member"`
	// Body tells that the rule opens a type body (class/interface/enum), whose
	// nesting member rules are limited to.
	Body bool `yaml:"body"`
}

// EndKeywordRule says which lines open a block and which keyword closes it.
type EndKeywordRule struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

// LangRule is one language of the rule file. Its compiled forms (the regexps,
// the mask spec, the ctags map) are filled by compile, the same way an
// OutputRule and a NudgeRule are.
type LangRule struct {
	Name         string   `yaml:"name"`
	Enabled      *bool    `yaml:"enabled"` // nil = on
	Exts         []string `yaml:"exts"`
	Markers      []string `yaml:"markers"`
	TestCommands string   `yaml:"test_commands"`
	FocusedFlags []string `yaml:"focused_flags"`
	// Ctags is the universal-ctags language: one name for every extension
	// (ctags: Ruby) or one per extension (ctags: {.h: C++}).
	Ctags any `yaml:"ctags"`
	// Syntax is the masking of the language's comments and strings, shared by
	// the declaration recogniser and file_refs.
	Syntax maskSpec `yaml:"syntax"`
	// Blocks is brace, indent or end_keyword; empty = brace.
	Blocks string `yaml:"blocks"`
	// EndKeyword is read when Blocks is end_keyword.
	EndKeyword EndKeywordRule `yaml:"end_keyword"`
	// BodyBraceNextLine accepts the opening brace of a type body on the line
	// after its declaration (the Allman style of C#).
	BodyBraceNextLine bool       `yaml:"body_brace_next_line"`
	Decls             []DeclRule `yaml:"decls"`

	// Source is where the rule came from (builtin, or the loaded file's path);
	// the loader fills it.
	Source string `yaml:"-"`

	// Compiled forms.
	mask    maskSpec
	decls   []braceDecl
	ctags   map[string]string
	openRe  *regexp.Regexp
	closeRe *regexp.Regexp
}

// symbolLanguage turns a rule into an entry of the symbol registry: it is
// always approx (a declaration-line recogniser, not a parser), its source is
// sourceDeclLines, and ctags may accelerate it.
func (r *LangRule) symbolLanguage() symbolLanguage {
	rule := r
	return symbolLanguage{
		name:          rule.Name,
		exts:          append([]string(nil), rule.Exts...),
		approx:        true,
		source:        sourceDeclLines,
		ctagsLanguage: rule.ctags,
		mask:          &rule.mask,
		rule:          true,
		extract: func(file string, src []byte) ([]Symbol, error) {
			return rule.extractSymbols(file, src)
		},
	}
}

// language turns a rule into an entry of the language registry (lang.go): the
// trace analysis reads its test commands and the tool layer its markers. The
// dependency-API clause is not in the rule file (design section 2: the base
// prompt's sentence is part of the provider's cached prefix), so it comes from
// dependencyAPIClaases when the language has one.
func (r *LangRule) language() Language {
	return Language{
		Name:          r.Name,
		Markers:       append([]string(nil), r.Markers...),
		DependencyAPI: dependencyAPIClaases[r.Name],
		TestCommands:  r.TestCommands,
		FocusedFlags:  append([]string(nil), r.FocusedFlags...),
	}
}

// extractSymbols runs the recogniser of the rule's block style.
func (r *LangRule) extractSymbols(file string, src []byte) ([]Symbol, error) {
	switch r.Blocks {
	case BlocksIndent:
		return indentSymbols(file, src, r.decls, r.mask)
	case BlocksEndKeyword:
		return endKeywordSymbols(file, src, r.decls, r.mask, r.openRe, r.closeRe)
	default:
		return braceSymbols(file, src, r.decls, braceStyle{spec: &r.mask, bodyBraceNextLine: r.BodyBraceNextLine})
	}
}

// compile validates one rule and fills its compiled forms. A broken rule is a
// whole-file error: a language that silently lost a declaration rule would
// answer with a misleadingly short list.
func (r *LangRule) compile() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(r.Exts) == 0 {
		return fmt.Errorf("exts is required")
	}
	for i, ext := range r.Exts {
		if !strings.HasPrefix(ext, ".") || len(ext) < 2 {
			return fmt.Errorf("ext %q: an extension starts with '.' (.rb)", ext)
		}
		r.Exts[i] = strings.ToLower(ext)
	}
	switch r.Blocks {
	case "":
		r.Blocks = BlocksBrace
	case BlocksBrace, BlocksIndent, BlocksEndKeyword:
	default:
		return fmt.Errorf("blocks %q: use brace, indent or end_keyword", r.Blocks)
	}
	if r.Blocks == BlocksEndKeyword {
		if r.EndKeyword.Open == "" || r.EndKeyword.Close == "" {
			return fmt.Errorf("end_keyword: open and close are required")
		}
		var err error
		if r.openRe, err = regexp.Compile(r.EndKeyword.Open); err != nil {
			return fmt.Errorf("end_keyword.open %q: %w", r.EndKeyword.Open, err)
		}
		if r.closeRe, err = regexp.Compile(r.EndKeyword.Close); err != nil {
			return fmt.Errorf("end_keyword.close %q: %w", r.EndKeyword.Close, err)
		}
	}
	if len(r.Decls) == 0 {
		return fmt.Errorf("decls is required")
	}
	for i, d := range r.Decls {
		re, err := regexp.Compile(d.Pattern)
		if err != nil {
			return fmt.Errorf("decls[%d] pattern %q: %w", i, d.Pattern, err)
		}
		if d.Kind == "" && d.KindGroup == 0 {
			return fmt.Errorf("decls[%d]: kind or kind_group is required", i)
		}
		if d.Kind != "" && d.KindGroup != 0 {
			return fmt.Errorf("decls[%d]: kind and kind_group exclude each other", i)
		}
		if d.Name < 1 || d.Name > re.NumSubexp() {
			return fmt.Errorf("decls[%d]: name group %d does not exist (the pattern captures %d)", i, d.Name, re.NumSubexp())
		}
		if d.KindGroup < 0 || d.KindGroup > re.NumSubexp() {
			return fmt.Errorf("decls[%d]: kind_group %d does not exist (the pattern captures %d)", i, d.KindGroup, re.NumSubexp())
		}
		r.decls = append(r.decls, braceDecl{
			re: re, kind: d.Kind, kindGroup: d.KindGroup, nameGroup: d.Name,
			member: d.Member, body: d.Body,
		})
	}
	if r.TestCommands != "" {
		if _, err := regexp.Compile(r.TestCommands); err != nil {
			return fmt.Errorf("test_commands %q: %w", r.TestCommands, err)
		}
	}
	for _, flag := range r.FocusedFlags {
		if _, err := regexp.Compile(flag); err != nil {
			return fmt.Errorf("focused_flags %q: %w", flag, err)
		}
	}
	for _, pair := range r.Syntax.BlockComment {
		if len(pair) != 2 {
			return fmt.Errorf("syntax.block_comment: every pair needs an opener and a closer")
		}
	}
	if err := r.compileCtags(); err != nil {
		return err
	}
	r.mask = r.Syntax
	return nil
}

// compileCtags fills the extension -> ctags language map from the two spellings
// a rule may write.
func (r *LangRule) compileCtags() error {
	r.ctags = map[string]string{}
	switch v := r.Ctags.(type) {
	case nil:
	case string:
		for _, ext := range r.Exts {
			r.ctags[ext] = v
		}
	case map[string]any:
		for ext, lang := range v {
			name, ok := lang.(string)
			if !ok {
				return fmt.Errorf("ctags %q: the language must be a string", ext)
			}
			r.ctags[strings.ToLower(ext)] = name
		}
	case map[any]any:
		for ext, lang := range v {
			name, ok := lang.(string)
			if !ok {
				return fmt.Errorf("ctags %q: the language must be a string", ext)
			}
			r.ctags[strings.ToLower(fmt.Sprint(ext))] = name
		}
	default:
		return fmt.Errorf("ctags: use a language name (ctags: Ruby) or an extension map (ctags: {.h: C++})")
	}
	return nil
}
