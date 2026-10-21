package codeintel

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// luaRuleFile is a rule file of a language that ships with no recogniser: the
// end_keyword style (design docs/design/2026-10-22-lang-rules-design.md,
// section 3), a line and a block comment and two quote styles.
const luaRuleFile = `languages:
  - name: Lua
    exts: [.lua]
    test_commands: '\bbusted\b'
    syntax:
      line_comment: ['--']
      block_comment: [['--[[', ']]']]
      strings: ['"', "'"]
    blocks: end_keyword
    end_keyword:
      open: '^\s*(function|if|for|while|do)\b'
      close: '^\s*end\b'
    decls:
      - {pattern: '^\s*function\s+([A-Za-z_][\w.:]*)', kind: func, name: 1}
      - {pattern: '^\s*local\s+function\s+([A-Za-z_]\w*)', kind: func, name: 1}
`

// withLangRules writes the given rule files into a temporary directory and
// returns their paths in order.
func withLangRules(t *testing.T, contents ...string) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i, content := range contents {
		path := filepath.Join(dir, "layer"+string(rune('a'+i))+".yaml")
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
		paths = append(paths, path)
	}
	return paths
}

// TestLoadLangRulesLayering checks the three layers: a new language is appended,
// a same-name rule replaces the built-in one whole, and enabled: false switches
// a built-in language off.
func TestLoadLangRulesLayering(t *testing.T) {
	paths := withLangRules(t, luaRuleFile+`
  - name: Ruby
    enabled: false
  - name: Kotlin
    enabled: false
`+`  - name: Python
    exts: [.py]
    test_commands: '\bpytest\b'
    syntax:
      line_comment: ['#']
    blocks: indent
    decls:
      - {pattern: '^def\s+([A-Za-z_]\w*)', kind: func, name: 1}
`)

	reg, errs := LoadLangRules(paths...)
	assert.Eq(t, 0, len(errs), "errs=%v", errs)
	assert.Eq(t, 0, len(reg.Skipped), "skipped=%v", reg.Skipped)

	names := make([]string, 0, len(reg.symbols))
	for _, lang := range reg.symbols {
		names = append(names, lang.name)
	}
	assert.True(t, slicesContains(names, "Lua"), "Lua is a new language and is appended")
	assert.False(t, slicesContains(names, "Ruby"), "enabled: false removes the built-in Ruby")
	assert.False(t, slicesContains(names, "Kotlin"), "enabled: false removes the built-in Kotlin")

	// The same-name Python replaces the built-in rule whole: its new test command
	// is what the language registry reports.
	for _, lang := range reg.langs {
		if lang.Name != "Python" {
			continue
		}
		assert.Eq(t, `\bpytest\b`, lang.TestCommands)
	}
	// Lua is a language of the symbol registry now, after the built-in ones.
	assert.True(t, supportsIn(reg.symbols, ".lua"), "Lua owns .lua")
	assert.Eq(t, "Go", reg.symbols[0].name, "the code languages keep their positions")
}

// TestLoadLangRulesErrors checks that a broken file is skipped as a whole and
// reported with its path and the broken language: a rule file with a typo must
// not silently do nothing.
func TestLoadLangRulesErrors(t *testing.T) {
	cases := map[string]string{
		"yaml syntax": "languages: [",
		"unknown field": `languages:
  - {name: Foo, exts: [.foo], synta: {line_comment: ['#']}, decls: [{pattern: 'x', kind: func, name: 0}]}
`,
		"bad regex": `languages:
  - {name: Foo, exts: [.foo], decls: [{pattern: '([', kind: func, name: 1}]}
`,
		"unknown blocks": `languages:
  - {name: Foo, exts: [.foo], blocks: braceless, decls: [{pattern: '(\w+)', kind: func, name: 1}]}
`,
		"kind and kind_group": `languages:
  - {name: Foo, exts: [.foo], decls: [{pattern: '(\w+) (\w+)', kind: func, kind_group: 1, name: 2}]}
`,
		"missing name group": `languages:
  - {name: Foo, exts: [.foo], decls: [{pattern: '(a)(b)', kind: func, name: 3}]}
`,
		"code language extension": `languages:
  - {name: Foo, exts: [.go], decls: [{pattern: '(\w+)', kind: func, name: 1}]}
`,
		"no languages": "languages: []\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := withLangRules(t, content)[0]
			reg, errs := LoadLangRules(path)
			assert.Eq(t, 1, len(errs))
			assert.StrContains(t, errs[0].Error(), path)
			assert.Eq(t, 1, len(reg.Skipped))
			assert.Eq(t, path, reg.Skipped[0].File)
			// The built-in languages are untouched by a broken file.
			assert.Eq(t, len(BuiltinLangRegistry().Languages), len(reg.Languages))
		})
	}
}

// TestLoadLangRulesExtConflict checks that a later rule of another name takes an
// extension from an earlier one with a warning, so the conflict is never a
// silent shadow.
func TestLoadLangRulesExtConflict(t *testing.T) {
	path := withLangRules(t, `languages:
  - {name: Foo, exts: [.rb], test_commands: '\bfoo\b', decls: [{pattern: '(\w+)', kind: func, name: 1}]}
`)[0]
	reg, errs := LoadLangRules(path)
	assert.Eq(t, 0, len(errs))
	assert.Eq(t, 1, len(reg.Warnings))
	assert.StrContains(t, reg.Warnings[0], ".rb")
	lang, ok := symbolLanguageIn(reg.symbols, ".rb")
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, "Foo", lang.name)
	// Ruby lost the extension and the built-in language keeps its other one.
	assert.True(t, supportsIn(reg.symbols, ".rake"), "Ruby keeps its other extension")
}

// TestLangRulesBuiltinLanguages checks the migrated and the new built-in
// languages: the registry lists them, and each one extracts a declaration from a
// source of its own style.
func TestLangRulesBuiltinLanguages(t *testing.T) {
	reg := BuiltinLangRegistry()
	for _, name := range []string{"JavaScript/TypeScript", "Python", "Java", "C#", "Kotlin", "Ruby", "C/C++"} {
		assert.True(t, ruleNamed(reg.Languages, name), name+" is a built-in rule")
	}
	cases := []struct {
		file string
		src  string
		want string
	}{
		{"a.ts", "export class Loader {\n}\n", "a.ts:1 class Loader"},
		{"a.py", "class Store:\n    def reload(self):\n        pass\n", "a.py:2 method reload"},
		{"a.java", "public class Store {\n  void reload() {}\n}\n", "a.java:2 method reload"},
		{"a.cs", "public class Store\n{\n    public void Reload()\n    {\n    }\n}\n", "a.cs:3 method Reload"},
		{"a.kt", "class Store {\n    fun reload(): Int = 1\n}\n", "a.kt:2 method reload"},
		{"a.rb", "class Store\n  def reload\n  end\nend\n", "a.rb:2 method reload"},
		{"a.cpp", "int load(const char *path) {\n  return 1;\n}\n", "a.cpp:1 func load"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			set, err := ExtractSymbols(c.file, []byte(c.src))
			assert.Require(t, assert.NoErr(t, err))
			assert.True(t, set.Approx)
			assert.Eq(t, sourceDeclLines, set.Source)
			assert.True(t, slices.Contains(symbolsText(set.Symbols), c.want), "want %q in %v", c.want, symbolsText(set.Symbols))
		})
	}
}

// TestLangRulesMaskedReferences checks the masking of the four new languages:
// the same name in a comment or in a string is no reference, and a name in code
// is one.
func TestLangRulesMaskedReferences(t *testing.T) {
	cases := []struct {
		name string
		file string
		src  string
	}{
		{"csharp", "a.cs", "// reload\nvar s = \"reload\";\nvoid f() { reload(); }\n"},
		{"kotlin", "a.kt", "// reload\nval s = \"reload\"\nfun f() = reload()\n"},
		{"ruby", "a.rb", "# reload\ns = 'reload'\nf = reload\n"},
		{"cpp", "a.cpp", "// reload\nconst char *s = \"reload\";\nf = reload();\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refs := ScanLineRefs(c.file, []byte(c.src), "reload")
			assert.Eq(t, 1, len(refs))
			assert.Eq(t, 3, refs[0].Line)
		})
	}
}

// TestLangRulesEndKeywordBlocks checks the block tracking of an end_keyword
// language: a def inside a class body is a method, one after the class is a
// function, and an "end" in a string or a block comment closes nothing.
func TestLangRulesEndKeywordBlocks(t *testing.T) {
	src := `local function helper(x)
  return x
end

function Store.reload(path)
  local label = "end"
  return path
end

--[[
function ghost()
end
]]
`
	reg := BuiltinLangRegistry()
	rule, ok := reg.activeRule("Lua")
	if ok {
		t.Fatalf("Lua is no built-in language: %v", rule)
	}
	rules, err := parseLangRules([]byte(luaRuleFile), "test")
	assert.Require(t, assert.NoErr(t, err))
	syms, err := rules[0].extractSymbols("a.lua", []byte(src))
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, []string{"a.lua:1 func helper", "a.lua:5 func Store.reload"}, symbolsText(syms))
}

// symbolsText renders the symbols as the characterization tests do.
func symbolsText(syms []Symbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, fmt.Sprintf("%s:%d %s %s", s.File, s.Line, s.Kind, s.Name))
	}
	return out
}

func ruleNamed(rules []LangRule, name string) bool {
	for _, r := range rules {
		if r.Name == name {
			return true
		}
	}
	return false
}

func supportsIn(langs []symbolLanguage, ext string) bool {
	_, ok := symbolLanguageIn(langs, ext)
	return ok
}

func symbolLanguageIn(langs []symbolLanguage, ext string) (symbolLanguage, bool) {
	for _, lang := range langs {
		for _, e := range lang.exts {
			if e == ext {
				return lang, true
			}
		}
	}
	return symbolLanguage{}, false
}

func slicesContains(values []string, want string) bool {
	return slices.Contains(values, want)
}
