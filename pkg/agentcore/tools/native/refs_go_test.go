package native

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// goRefsFixture is the Go fixture of the classification cases; the line
// numbers in the cases below refer to it.
const goRefsFixture = `package sample

import (
	"fmt"
	other "example.com/other/pkg"
)

type Loader struct {
	Name string
}

func Run(x int) {
	v := 1
	_ = v
	fmt.Println(x)
	other.Do()
	label:
	goto label
}

func (l *Loader) Use() string {
	return l.Name
}

var LoaderName = "Run"
`

// goRefs scans one source for one name.
func goRefs(t *testing.T, src, name string) []Ref {
	t.Helper()
	refs, err := ScanGoRefs("fixture.go", []byte(src), name)
	assert.Require(t, assert.NoErr(t, err))
	return refs
}

// refPositions renders one "line class(qualifier)" per reference, so a case can
// assert the whole classification of a name.
func refPositions(refs []Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		desc := r.Class
		switch {
		case r.Qualifier == "":
		case r.Pkg:
			desc += "(pkg:" + r.Qualifier + ")"
		default:
			desc += "(" + r.Qualifier + ")"
		}
		out = append(out, desc)
	}
	return out
}

// TestGoRefsClasses covers the four classes on the fixture: where the name is
// bound, called, selected and only mentioned.
func TestGoRefsClasses(t *testing.T) {
	cases := []struct {
		what string
		name string
		want []string
	}{
		{"def: func declaration", "Run", []string{RefDef}},
		{"def: parameter, then a use", "x", []string{RefDef, RefRef}},
		{"def: := left value, then a use", "v", []string{RefDef, RefRef}},
		{"def: struct field, then a selector use", "Name", []string{RefDef, "sel(l)"}},
		{"def: receiver, then a selector use", "l", []string{RefDef, RefRef}},
		{"def: label, then a goto", "label", []string{RefDef, RefRef}},
		{"def: package level var", "LoaderName", []string{RefDef}},
		{"call: through an imported package", "Println", []string{"call(pkg:fmt)"}},
		{"call: through an import alias", "Do", []string{"call(pkg:other)"}},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			refs := goRefs(t, goRefsFixture, c.name)
			assert.Eq(t, c.want, refPositions(refs))
			if len(c.want) > 0 {
				assert.StrContains(t, refs[0].File, "fixture.go")
			}
		})
	}
}

// TestGoRefsSelectorAndRef covers the qualifier text, the line text and the
// plain references.
func TestGoRefsSelectorAndRef(t *testing.T) {
	t.Run("a selector carries its qualifier and the line text", func(t *testing.T) {
		refs := goRefs(t, goRefsFixture, "Name")
		assert.Require(t, assert.Eq(t, 2, len(refs)))
		assert.Eq(t, RefDef, refs[0].Class)
		assert.Eq(t, "Name string", refs[0].Text)
		assert.Eq(t, RefSel, refs[1].Class)
		assert.Eq(t, "l", refs[1].Qualifier)
		assert.Eq(t, "return l.Name", refs[1].Text)
		assert.Eq(t, 22, refs[1].Line)
	})

	t.Run("a bare use is ref, a different name matches nothing", func(t *testing.T) {
		refs := goRefs(t, "package p\n\nfunc a() Loader { return Loader{} }\n", "Loader")
		assert.Require(t, assert.Eq(t, 2, len(refs)))
		assert.Eq(t, []string{RefRef, RefRef}, refPositions(refs))

		assert.Eq(t, 0, len(goRefs(t, goRefsFixture, "Load")))
		assert.Eq(t, 0, len(goRefs(t, goRefsFixture, "Loader2")))
	})
}

// TestGoRefsLimitsAndErrors covers what counts as no match, the accepted false
// positives and how a broken file is reported.
func TestGoRefsLimitsAndErrors(t *testing.T) {
	t.Run("comments and string literals are not identifiers", func(t *testing.T) {
		src := "package p\n\n// Run is documented here.\nvar s = \"Run\"\n"
		assert.Eq(t, 0, len(goRefs(t, src, "Run")))
	})

	t.Run("a same-named local and a same-named field are reported as def, with their line", func(t *testing.T) {
		// The false positives go/parser cannot tell apart without type
		// information are reported as def with the line text, which is what the
		// model decides on.
		src := "package p\n\ntype A struct {\n\tKey string\n}\n\nfunc a() {\n\tKey := 1\n\t_ = Key\n}\n"
		refs := goRefs(t, src, "Key")
		assert.Require(t, assert.Eq(t, 3, len(refs)))
		assert.Eq(t, []string{RefDef, RefDef, RefRef}, refPositions(refs))
		assert.Eq(t, "Key string", refs[0].Text)
		assert.Eq(t, "Key := 1", refs[1].Text)
	})

	t.Run("a file that does not parse returns the error instead of a panic", func(t *testing.T) {
		_, err := ScanGoRefs("broken.go", []byte("package broken\n\nfunc Broken( {\n"), "Broken")
		assert.Err(t, err)
	})

	t.Run("a long line is trimmed to the grep limit", func(t *testing.T) {
		src := "package p\n\nvar long = 1 // " + strings.Repeat("x", 400) + "\n"
		refs := goRefs(t, src, "long")
		assert.Require(t, assert.Eq(t, 1, len(refs)))
		assert.True(t, len(refs[0].Text) <= maxGrepLineBytes+len("…"), refs[0].Text)
	})
}

// TestGoImportNames covers the package-name collection the qualifier marking
// uses: an alias, a version suffix and the imports that bind no name.
func TestGoImportNames(t *testing.T) {
	src := "package p\n\nimport (\n\t\"fmt\"\n\t\"github.com/x/y/v2\"\n\taliased \"github.com/x/z\"\n" +
		"\t_ \"github.com/x/blank\"\n\t. \"github.com/x/dot\"\n)\n"
	names := goImportNames(parseGoForTest(t, src))
	assert.True(t, names["fmt"])
	assert.True(t, names["y"]) // the package under a /v2 element keeps its name
	assert.True(t, names["aliased"])
	assert.False(t, names["v2"])
	assert.False(t, names["blank"])
	assert.False(t, names["dot"])
}

// parseGoForTest parses one source file for the import-name cases.
func parseGoForTest(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, parser.SkipObjectResolution)
	assert.Require(t, assert.NoErr(t, err))
	return f
}

// goRefsQualifiedFixture puts the name Run on five objects the filter has to tell
// apart by text: Runner's field, Scaler's field, the package's function, Runner's
// method and a use through another package.
const goRefsQualifiedFixture = `package sample

import other "example.com/other/pkg"

type Runner struct {
	Run bool
}

type Scaler struct {
	Run bool
}

func Run(n int) int {
	return n
}

func (r *Runner) Run(n int) int {
	return n
}

type App struct {
	runner *Runner
}

func (a *App) Start() {
	a.runner.Run(1)
	other.Run()
	Run(2)
	x := Runner{}
	_ = x.Run
}
`

// refLineClasses renders "line class(qualifier)" per reference: the line tells
// which object a qualified search kept.
func refLineClasses(refs []Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, fmt.Sprintf("%d %s", r.Line, refClass(r)))
	}
	return out
}

// TestGoRefsQualified covers the qualified-name filter of one scan: a "Q.Run"
// query keeps the occurrences that can belong to Q and counts the rest as
// dropped, so the result can say what the same name is used on elsewhere.
func TestGoRefsQualified(t *testing.T) {
	// A package qualifier keeps every definition written in a file of that package
	// (a local binding included): without type information the filter errs on
	// keeping, and the note says what it kept.
	cases := []struct {
		what    string
		qual    string
		want    []string
		dropped int
	}{
		{"the method and the field of the type, and a use through a receiver field",
			"Runner", []string{"6 def", "17 def", "26 call(a.runner)"}, 5},
		{"the plain function, the bare call and the definitions of the package's own file",
			"sample", []string{"6 def", "10 def", "13 def", "17 def", "28 call"}, 3},
		{"a call through an imported package alias",
			"other", []string{"27 call(pkg:other)"}, 7},
		{"the field of another type is not the field of the type",
			"Scaler", []string{"10 def"}, 7},
		{"a selector of a receiver variable",
			"x", []string{"30 sel(x)"}, 7},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			refs, dropped, err := scanGoRefs("fixture.go", []byte(goRefsQualifiedFixture), "Run", c.qual)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, c.want, refLineClasses(refs))
			assert.Eq(t, c.dropped, dropped.total())
		})
	}

	t.Run("without a qualifier nothing is dropped", func(t *testing.T) {
		refs, dropped, err := scanGoRefs("fixture.go", []byte(goRefsQualifiedFixture), "Run", "")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 8, len(refs))
		assert.Eq(t, 0, dropped.total())
	})
}

// TestQualifierMatches covers the text comparison the filter decides on: the
// qualifier itself, its last segment case-insensitively, and what it must not
// match.
func TestQualifierMatches(t *testing.T) {
	cases := map[string]bool{
		"Runner|Runner":     true,
		"Runner|runner":     true, // a receiver variable named after the type
		"Runner|a.runner":   true, // the last segment of the qualifier decides
		"Runner|a.b.Runner": true,
		"Runner|other":      false,
		"Runner|":           false,
		"Runner|r":          false,
		"Runner|s.runner2":  false,
	}
	for pair, want := range cases {
		q, qual, _ := strings.Cut(pair, "|")
		t.Run(pair, func(t *testing.T) {
			assert.Eq(t, want, qualifierMatches(q, qual))
		})
	}

	t.Run("an empty filter keeps everything", func(t *testing.T) {
		assert.True(t, qualifierMatches("", "anything"))
		assert.True(t, qualifierMatches("", ""))
	})
}

// TestSplitRefName covers the qualified name argument: the last segment is the
// name, the one before it the qualifier, and a name without a dot has none.
func TestSplitRefName(t *testing.T) {
	cases := []struct {
		raw  string
		name string
		qual string
	}{
		{"Run", "Run", ""},
		{"Runner.Run", "Run", "Runner"},
		{"appconfig.ReadModelSettings", "ReadModelSettings", "appconfig"},
		{"s.runner.Run", "Run", "runner"}, // only the last segment of the path is the filter
		{"a.b.Run", "Run", "b"},
		{"Run.", "Run.", ""}, // a trailing dot is no qualifier
		{".Run", ".Run", ""}, // and neither is a leading one
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			name, qual := splitRefName(c.raw)
			assert.Eq(t, c.name, name)
			assert.Eq(t, c.qual, qual)
		})
	}
}

// TestRefDropsDescribe pins how the dropped qualifiers are named and which
// lines are shown: variables sorted with their counts, packages and bare uses
// only summed, and the lines of rarely used variables kept.
func TestRefDropsDescribe(t *testing.T) {
	drops := func(counts map[string]int) *refDrops {
		d := newRefDrops()
		for k, n := range counts {
			for i := 0; i < n; i++ {
				switch {
				case k == "":
					d.addText()
				case strings.HasPrefix(k, "pkg:"):
					d.add(Ref{File: "a.go", Line: i + 1, Qualifier: strings.TrimPrefix(k, "pkg:"), Pkg: true})
				default:
					d.add(Ref{File: k + ".go", Line: i + 1, Qualifier: k})
				}
			}
		}
		return d
	}

	t.Run("variables are named, packages and bare uses counted", func(t *testing.T) {
		d := drops(map[string]int{"t": 40, "cmd": 3, "child": 1, "pkg:exec": 2, "": 1})
		assert.Eq(t, 47, d.total())
		assert.Eq(t, " (variables: child 1, cmd 3, t 40; 2 through imported packages; 1 unqualified or text matches)", d.describe())
	})

	t.Run("only rarely used variables keep their lines", func(t *testing.T) {
		d := drops(map[string]int{"t": 40, "cmd": 3, "child": 1, "x": 2, "pkg:exec": 1})
		rare, more := d.rare()
		assert.Eq(t, 0, more)
		assert.Eq(t, []string{"child.go:1", "x.go:1", "x.go:2"}, refFileLines(rare))
	})

	t.Run("nothing dropped describes nothing", func(t *testing.T) {
		assert.Eq(t, "", newRefDrops().describe())
		rare, _ := newRefDrops().rare()
		assert.Empty(t, rare)
	})

	t.Run("long lists are capped", func(t *testing.T) {
		counts := map[string]int{}
		for i := 0; i < maxDropNames+3; i++ {
			counts[fmt.Sprintf("v%02d", i)] = 1
		}
		d := drops(counts)
		assert.StrContains(t, d.describe(), "and 3 more)")
		rare, more := d.rare()
		assert.Len(t, rare, maxRareDropLines)
		assert.Eq(t, maxDropNames+3-maxRareDropLines, more)
	})
}

// refFileLines is the path:line of each ref.
func refFileLines(refs []Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, fmt.Sprintf("%s:%d", r.File, r.Line))
	}
	return out
}
