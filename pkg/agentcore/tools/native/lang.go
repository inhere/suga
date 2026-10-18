package native

import (
	"os"
	"path/filepath"
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

	// dependencyRoots adds this language's local dependency caches; nil when
	// the language has none outside the workspace.
	dependencyRoots func(env langEnv, add *rootCollector)
}

// languages is the registry, in the order the dependency roots appear in the
// environment prompt and the prompt clauses are read: Go, Rust, Java, JS,
// Python, PHP. That order is the historical one (the roots were detected in it
// before the split, suga-xt0o), so both the read root list and the prompt stay
// byte-identical; a new language is appended at the end and the existing ones
// stay where they are (the prompt itself does not change: PHP declares no
// DependencyAPI clause, so the sentence reads as before).
var languages = []Language{goLanguage, rustLanguage, javaLanguage, jsLanguage, pythonLanguage, phpLanguage}

// Languages returns the language registry in its fixed order.
func Languages() []Language { return languages }

// otherTestCommands are the test commands of tools that are no language of
// their own here: GNU make's test target, CMake's ctest, RSpec and dotnet
// test. They are recognised exactly as before the split, kept together instead
// of inventing a language entry for each. PHPUnit left the list when PHP became
// a language (lang_php.go); the aggregate pattern matches the same commands as
// before.
const otherTestCommands = `\b(make test|rspec|ctest|dotnet test)\b`

// langEnv is what the dependency root probes read: the user home directory and
// the Go environment (the process environment over the go env file).
type langEnv struct {
	home  string
	goVar func(key string) string
}

// newLangEnv gathers the environment once for all the probes.
func newLangEnv() langEnv {
	home, _ := os.UserHomeDir()
	goEnv := readGoEnvFile()
	return langEnv{home: home, goVar: func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return goEnv[key]
	}}
}

// rootCollector gathers the dependency roots of the languages of one detection
// run. A directory that is empty, relative or missing is left out, exactly as
// the per-language detection did before the split.
type rootCollector struct{ out []ReadRoot }

// add appends label: dir when dir is an absolute path of an existing directory.
func (c *rootCollector) add(label, dir string) {
	if dir == "" || !filepath.IsAbs(dir) {
		return
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return
	}
	c.out = append(c.out, ReadRoot{Label: label, Path: filepath.Clean(dir)})
}

// DependencyRoots detects the local dependency caches and repositories of every
// declared language, in Languages() order: the Go module cache, the Go standard
// library sources, the Rust crate sources, the Maven local repository and the
// Gradle module cache. Only directories holding dependency files are returned,
// never a whole tool home (~/.cargo, ~/.gradle and ~/.m2 hold credentials or
// build state too). Directories that do not exist are left out. It runs no
// external command: the go env file, environment variables and the go binary
// location in PATH are enough.
//
// A pathless file_grep / file_find only searches the workspace, so these large
// roots are walked only when the model passes one of them as the search path
// (such a walk stops at searchTimeout and at the tool's result limit).
func DependencyRoots() []ReadRoot {
	env, collector := newLangEnv(), &rootCollector{}
	for _, lang := range Languages() {
		if lang.dependencyRoots != nil {
			lang.dependencyRoots(env, collector)
		}
	}
	return collector.out
}

// testCommandExpr joins the languages' test command regexes into the pattern
// that recognises a test run (trace --analyze). Every language declares a
// complete regex, so the aggregate matches what the languages matched before.
func testCommandExpr() string {
	var parts []string
	for _, lang := range Languages() {
		if lang.TestCommands != "" {
			parts = append(parts, lang.TestCommands)
		}
	}
	return strings.Join(append(parts, otherTestCommands), "|")
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

var (
	testCommandRegexp = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(testCommandExpr()) })
	focusedTestRegexp = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(focusedTestExpr()) })
)

// TestCommandPattern matches the shell commands that run tests, from the
// language registry; the trace analysis counts and rates them.
func TestCommandPattern() *regexp.Regexp { return testCommandRegexp() }

// FocusedTestPattern matches the shell commands of a test run limited to some
// tests (go test -run, pytest -k, maven -Dtest, gradle --tests, ...); such a
// run is no proof that the suite passes, so the trace analysis does not take
// one for the first green run.
func FocusedTestPattern() *regexp.Regexp { return focusedTestRegexp() }
