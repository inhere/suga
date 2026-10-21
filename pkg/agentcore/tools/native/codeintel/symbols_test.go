package codeintel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// pinBuiltinExtractors keeps universal-ctags out of the extractor tests of this
// package: the expectations are the built-in recognisers' output.
func pinBuiltinExtractors(t *testing.T) {
	t.Helper()
	t.Cleanup(PinBuiltinExtractors())
}

// writeSourceFixture writes one fixture file into dir and returns its path: the
// extractors run on real files, not only on in-memory strings.
func writeSourceFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	return path
}

// extractFixture writes one fixture, extracts its symbols and returns them with
// the whole extraction (source and approx flag).
func extractFixture(t *testing.T, dir, name, content string) ([]Symbol, SymbolSet) {
	t.Helper()
	path := writeSourceFixture(t, dir, name, content)
	src, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	set, err := ExtractSymbols(path, src)
	assert.Require(t, assert.NoErr(t, err))
	return set.Symbols, set
}

// symbolsAsText renders "file:line kind name" per symbol, so one extraction is
// one comparable value.
func symbolsAsText(syms []Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = fmt.Sprintf("%s:%d %s %s", filepath.Base(s.File), s.Line, s.Kind, s.Name)
	}
	return out
}

// symbolByName returns the symbol called name; fails the test when there is
// none, so a signature assertion cannot silently pass on a missing symbol.
func symbolByName(t *testing.T, syms []Symbol, name string) Symbol {
	t.Helper()
	for _, s := range syms {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("%s is not among the extracted symbols", name)
	return Symbol{}
}

// TestExtractSymbolsGo is the exact extractor: go/parser, so the results carry
// no approx marker.
func TestExtractSymbolsGo(t *testing.T) {
	syms, set := extractFixture(t, t.TempDir(), "sample.go", goFixture)
	assert.Eq(t, sourceGoParser, set.Source)
	assert.False(t, set.Approx)
	assert.Eq(t, []string{
		"sample.go:7 const MaxItems",
		"sample.go:8 const prefix",
		"sample.go:11 var ErrFull",
		"sample.go:14 type Item",
		"sample.go:19 type Finder",
		"sample.go:24 type Store",
		"sample.go:29 func New",
		"sample.go:34 method Get",
	}, symbolsAsText(syms))

	t.Run("a declaration line is the signature", func(t *testing.T) {
		assert.Eq(t, "MaxItems = 10", symbolByName(t, syms, "MaxItems").Signature)
		assert.Eq(t, "func (s *Store) Get(name string) (Item, bool) {", symbolByName(t, syms, "Get").Signature)
	})
}

// goFixture is the Go extractor's fixture: a const block, a var, a struct, an
// interface, a function and a method (the line numbers in the test are this
// file's).
const goFixture = `package sample

import "fmt"

const (
	// MaxItems caps the number of items.
	MaxItems = 10
	prefix = "item"
)

var ErrFull = fmt.Errorf("store full")

// Item is one stored item.
type Item struct {
	Name string
}

// Finder finds items.
type Finder interface {
	Find(name string) (Item, bool)
}

// Store keeps the items.
type Store struct {
	items []Item
}

// New builds an empty store.
func New() *Store {
	return &Store{}
}

// Get returns the item called name.
func (s *Store) Get(name string) (Item, bool) {
	local := Item{Name: name}
	return local, true
}
`

// TestExtractSymbolsJavaScriptTypeScript checks the line-based recogniser for
// JS/TS: the declarations of the file and of a class body, but no statement of
// a function body.
func TestExtractSymbolsJavaScriptTypeScript(t *testing.T) {
	syms, set := extractFixture(t, t.TempDir(), "sample.ts", tsFixture)
	assert.Eq(t, sourceDeclLines, set.Source)
	assert.True(t, set.Approx, "a line-based result is marked approx")
	assert.Eq(t, []string{
		"sample.ts:1 interface Options",
		"sample.ts:5 type Handler",
		"sample.ts:7 enum Level",
		"sample.ts:12 class Service",
		"sample.ts:15 method constructor",
		"sample.ts:19 method start",
		"sample.ts:25 func build",
		"sample.ts:29 const defaultLevel",
		"sample.ts:30 var counter",
	}, symbolsAsText(syms))

	t.Run("a .js file is the same language", func(t *testing.T) {
		jsSyms, set := extractFixture(t, t.TempDir(), "sample.js", "export function run() {}\nconst local = 1;\n")
		assert.Eq(t, sourceDeclLines, set.Source)
		assert.Eq(t, []string{"sample.js:1 func run", "sample.js:2 const local"}, symbolsAsText(jsSyms))
	})
}

// tsFixture is the JavaScript/TypeScript fixture: a local const and a call
// inside a function body must not become declarations.
const tsFixture = `export interface Options {
  name: string;
}

export type Handler = (value: string) => void;

export enum Level {
  Low,
  High,
}

export class Service {
  private options: Options;

  constructor(options: Options) {
    this.options = options;
  }

  async start(): Promise<void> {
    const local = 1;
    run(local);
  }
}

export function build(options: Options): Service {
  return new Service(options);
}

export const defaultLevel = Level.Low;
export let counter = 0;
`

// TestExtractSymbolsPython checks the indentation-based recogniser: a def in a
// class body is a method, a def elsewhere a function.
func TestExtractSymbolsPython(t *testing.T) {
	syms, set := extractFixture(t, t.TempDir(), "sample.py", pythonFixture)
	assert.Eq(t, sourceDeclLines, set.Source)
	assert.True(t, set.Approx)
	assert.Eq(t, []string{
		"sample.py:4 func module_func",
		"sample.py:8 class Service",
		"sample.py:11 method __init__",
		"sample.py:15 method name_value",
		"sample.py:18 class Nested",
		"sample.py:19 method nested_method",
		"sample.py:23 func other",
		"sample.py:24 func local",
	}, symbolsAsText(syms))
}

// pythonFixture is the Python fixture: a docstring, a decorator, a nested class
// and a nested function (a function, not a method).
const pythonFixture = `import os


def module_func(value):
    return value


class Service:
    """A service."""

    def __init__(self, name):
        self.name = name

    @property
    def name_value(self):
        return self.name

    class Nested:
        def nested_method(self):
            return 1


def other():
    def local():
        return 2
    return local
`

// TestExtractSymbolsJava checks the Java recogniser: types, a nested interface
// and enum, and the methods of a type body (with a control statement and a
// field initializer left out).
func TestExtractSymbolsJava(t *testing.T) {
	syms, set := extractFixture(t, t.TempDir(), "Sample.java", javaFixture)
	assert.Eq(t, sourceDeclLines, set.Source)
	assert.True(t, set.Approx)
	assert.Eq(t, []string{
		"Sample.java:5 class Sample",
		"Sample.java:8 method Sample",
		"Sample.java:12 method name",
		"Sample.java:16 method check",
		"Sample.java:23 interface Callback",
		"Sample.java:24 method done",
		"Sample.java:27 enum State",
		"Sample.java:32 record Point",
	}, symbolsAsText(syms))
}

// javaFixture is the Java fixture: a field, a constructor, methods (one holding
// an if and a return), a nested interface and enum, and a record.
const javaFixture = `package sample;

import java.util.List;

public class Sample {
    private static final int LIMIT = 10;

    public Sample(String name) {
        this.name = name;
    }

    public String name() {
        return check(this.name);
    }

    private String check(String value) {
        if (value == null) {
            return "";
        }
        return value;
    }

    interface Callback {
        void done(int code);
    }

    enum State {
        OPEN,
        CLOSED
    }

    record Point(int x, int y) {}
}
`

// TestExtractSymbolsUnsupportedType: a file type no extractor covers is refused
// with the supported languages, never answered with an empty list.
func TestExtractSymbolsUnsupportedType(t *testing.T) {
	dir := t.TempDir()
	path := writeSourceFixture(t, dir, "notes.md", "# notes\n\n## Symbols\n")
	_, err := ExtractSymbols(path, []byte("# notes\n"))
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), `unsupported file type ".md"`)
	assert.StrContains(t, err.Error(), "Go (.go)")
	assert.False(t, SupportsSymbols("notes.md"))
	assert.True(t, SupportsSymbols("a/b/c.go"))
	assert.True(t, SupportsSymbols("a/b/c.TS"), "the extension is compared case-insensitively")
	assert.StrContains(t, SupportedSymbolLanguages(), "Python (.py, .pyi)")
}

// TestExtractSymbolsGoParseError: a Go file that does not parse reports the
// parser's error instead of an empty or a half result.
func TestExtractSymbolsGoParseError(t *testing.T) {
	dir := t.TempDir()
	path := writeSourceFixture(t, dir, "broken.go", "package broken\n\nfunc Broken( {\n")
	_, err := ExtractSymbols(path, []byte("package broken\n\nfunc Broken( {\n"))
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), "broken.go:3")
}

// TestExtractSymbolsPrefersUniversalCtags checks the priority rule of design
// 0.2: an installed universal-ctags extracts, its absence and every kind of
// failure fall back to the built-in extractor. The ctags process is stubbed
// (the host has no ctags), so the test can also assert the arguments.
func TestExtractSymbolsPrefersUniversalCtags(t *testing.T) {
	dir := t.TempDir()
	path := writeSourceFixture(t, dir, "sample.go", "package sample\n\nfunc Read() {}\n")
	src, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))

	originalCommand, originalRun := ctagsCommand, ctagsRun
	t.Cleanup(func() { ctagsCommand, ctagsRun = originalCommand, originalRun })
	ctagsCommand = func() string { return "ctags" }

	t.Run("ctags output wins and is not approx", func(t *testing.T) {
		var gotArgs []string
		ctagsRun = func(command, language, path string) ([]byte, error) {
			gotArgs = []string{command, language, path}
			// A pseudo tag and a tag without a name are not symbols.
			return []byte(`{"_type": "tag", "name": "Read", "path": "sample.go", "line": 3, "kind": "function"}` + "\n" +
				`{"_type": "ptag", "name": "!_TAG_FILE_FORMAT"}` + "\n" +
				`{"_type": "tag", "line": 9}` + "\n"), nil
		}
		set, err := ExtractSymbols(path, src)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, sourceCtags, set.Source)
		assert.False(t, set.Approx, "ctags parses, so the result is not approx")
		assert.Eq(t, []string{"sample.go:3 func Read"}, symbolsAsText(set.Symbols))
		assert.Eq(t, []string{"ctags", "Go", path}, gotArgs)
		assert.Eq(t, "func Read() {}", symbolByName(t, set.Symbols, "Read").Signature)
	})

	t.Run("a failing ctags falls back", func(t *testing.T) {
		ctagsRun = func(string, string, string) ([]byte, error) { return nil, errors.New("ctags broke") }
		set, err := ExtractSymbols(path, src)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, sourceGoParser, set.Source)
		assert.Eq(t, []string{"sample.go:3 func Read"}, symbolsAsText(set.Symbols))
	})

	t.Run("a ctags that finds nothing falls back", func(t *testing.T) {
		ctagsRun = func(string, string, string) ([]byte, error) { return nil, nil }
		set, err := ExtractSymbols(path, src)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, sourceGoParser, set.Source)
	})

	t.Run("no ctags in PATH", func(t *testing.T) {
		ctagsCommand = func() string { return "" }
		ctagsRun = func(string, string, string) ([]byte, error) { return nil, errors.New("must not run") }
		set, err := ExtractSymbols(path, src)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, sourceGoParser, set.Source)
	})
}

// TestParseCtagsTags covers the JSON shape and the kind mapping on their own:
// the language only reaches them through ctags' own kind names.
func TestParseCtagsTags(t *testing.T) {
	out := []byte(`{"_type":"tag","name":"Foo","line":2,"kind":"function"}` + "\n" +
		`{"_type":"tag","name":"bar","line":7,"kind":"m"}` + "\n" +
		`{"_type":"tag","name":"Baz","line":9,"kind":"typedef"}` + "\n" +
		`{"_type":"tag","name":"qux","line":11,"kind":"enumerator"}`)
	syms, err := parseCtagsTags(out, "a.c", []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\n"))
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, []string{"a.c:2 func Foo", "a.c:7 method bar", "a.c:9 type Baz", "a.c:11 const qux"}, symbolsAsText(syms))

	t.Run("a malformed line fails the parse", func(t *testing.T) {
		_, err := parseCtagsTags([]byte("{not json}"), "a.c", nil)
		assert.Err(t, err)
	})
}

// TestLineRecognisersSkipCommentsAndMultiLineStrings: declarations written in a
// block comment, a template string or a docstring are not declarations, and
// their braces do not shift the depth of what follows.
func TestLineRecognisersSkipCommentsAndMultiLineStrings(t *testing.T) {
	names := func(set SymbolSet) []string {
		var out []string
		for _, s := range set.Symbols {
			out = append(out, s.Name)
		}
		return out
	}
	t.Run("typescript", func(t *testing.T) {
		src := "/*\nclass Ghost {\n}\n*/\nconst tpl = `\nfunction ghost() {\n`\nexport class Real {\n  run() {}\n}\n"
		set, err := ExtractSymbols("a.ts", []byte(src))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"tpl", "Real", "run"}, names(set))
	})
	t.Run("python", func(t *testing.T) {
		src := "def real():\n    \"\"\"Example:\n\ndef ghost():\n    pass\n\"\"\"\n    return 1\n\nclass Kept:\n    pass\n"
		set, err := ExtractSymbols("a.py", []byte(src))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"real", "Kept"}, names(set))
	})
	t.Run("java text block", func(t *testing.T) {
		src := "class Real {\n  String s = \"\"\"\nclass Ghost {\n\"\"\";\n  void run() {}\n}\n"
		set, err := ExtractSymbols("A.java", []byte(src))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"Real", "run"}, names(set))
	})
}
