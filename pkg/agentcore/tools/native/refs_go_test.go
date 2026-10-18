package native

import (
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
