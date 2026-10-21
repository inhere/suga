package codeintel

import (
	"regexp"
	"strings"
	"sync"
)

// Language is how one programming language's special handling is declared
// (suga-xt0o): one file per language (lang_go.go, lang_java.go, ...), the same
// way the LLM layer keeps one file per provider (pkg/agentcore/llm/compat_*.go).
// A language declares where its dependency sources live on this machine, how
// the model reads a dependency's API, which shell commands run its tests and
// which project files identify it.
//
// The declarations feed three consumers:
//   - DependencyRoots: dependencyRoots adds the local caches whose files the
//     read-only file tools may read.
//   - the base prompt: DependencyAPI is one clause of the dependency-API
//     sentence (internal/wiring/prompt.go joins them into one line). The
//     wording is part of the provider's cached prompt prefix, so the clause of
//     an existing language changes only together with its test.
//   - the trace analysis (internal/personal/analyze.go): TestCommands and
//     FocusedFlags recognise test runs and runs limited to some tests.
//
// Markers (go.mod, pom.xml, ...) are declared but not used yet: the base prompt
// carries the clause of every language today.
type Language struct {
	Name    string   // the language's name, as the prompt and the docs spell it
	Markers []string // project files that identify the language

	// DependencyAPI is this language's clause of the base prompt's
	// dependency-API sentence. It includes how the sentence names the language
	// ("<hint> for Go", "for Java, <hint>") because the sentence's own wording
	// is fixed; the clauses are joined with "; " in Languages() order. It is
	// empty for a language whose dependencies need no command of their own.
	DependencyAPI string

	// TestCommands is a complete regex matching a shell command that runs this
	// language's tests; FocusedFlags are the regex fragments of the flags that
	// limit a run to some tests (they are combined into one shared
	// "(^|\s)(<flags>)(\s|=)" pattern). The trace analysis uses both.
	TestCommands string
	FocusedFlags []string
}

// languages is the registry, in the order the dependency roots appear in the
// environment prompt and the prompt clauses are read: Go, Rust, Java, JS,
// Python, PHP. That order is the historical one (the roots were detected in it
// before the split, suga-xt0o), so both the read root list and the prompt stay
// byte-identical; a new language is appended at the end and the existing ones
// stay where they are (the prompt itself does not change: PHP declares no
// DependencyAPI clause, so the sentence reads as before).
//
// It is composed from the code languages (Go, Rust, PHP) and the rule languages
// (Java, JavaScript/TypeScript, Python and the languages a rule file adds), so a
// lang-rules.yaml can tune a language's test commands without a code change.
func Languages() []Language { return ActiveLangRules().langs }

// testCommandExpr joins the languages' test command regexes into the pattern
// that recognises a test run (trace --analyze). Every language declares a
// complete regex, so the aggregate matches what the languages matched before;
// a tool that is no language of its own here (PHPUnit before PHP became a
// language, dotnet test for C#, rspec for Ruby, ctest/make test for C/C++) was
// moved into the rule of the language it belongs to, which keeps the recognition
// unchanged and puts it under the same enabled: false switch.
func testCommandExpr() string {
	var parts []string
	for _, lang := range Languages() {
		if lang.TestCommands != "" {
			parts = append(parts, lang.TestCommands)
		}
	}
	return strings.Join(parts, "|")
}

// focusedTestExpr combines the languages' focused-run flags into the pattern
// that recognises a test run limited to some tests: a flag at a word start
// followed by a space or "=" (so "-Dtest=Foo" matches through "-Dtest").
func focusedTestExpr() string {
	var flags []string
	for _, lang := range Languages() {
		flags = append(flags, lang.FocusedFlags...)
	}
	return `(^|\s)(` + strings.Join(flags, "|") + `)(\s|=)`
}

// cachedRegexp compiles expr once per distinct expression: the language
// registry is only complete after the rule files are loaded, so a pattern must
// not be frozen at the first call (a test builds tools without wiring).
func cachedRegexp(expr string) *regexp.Regexp {
	if re, ok := langRegexpCache.Load(expr); ok {
		return re.(*regexp.Regexp)
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		// A rule file's regexps are compiled when it loads; a broken aggregate
		// is a defect here, so make it match nothing instead of crashing a turn.
		re = regexp.MustCompile(`(?:^\b$)`)
	}
	langRegexpCache.Store(expr, re)
	return re
}

var langRegexpCache sync.Map // expression -> *regexp.Regexp

// TestCommandPattern matches the shell commands that run tests, from the
// language registry; the trace analysis counts and rates them.
func TestCommandPattern() *regexp.Regexp { return cachedRegexp(testCommandExpr()) }

// FocusedTestPattern matches the shell commands of a test run limited to some
// tests (go test -run, pytest -k, maven -Dtest, gradle --tests, ...); such a
// run is no proof that the suite passes, so the trace analysis does not take
// one for the first green run.
func FocusedTestPattern() *regexp.Regexp { return cachedRegexp(focusedTestExpr()) }
