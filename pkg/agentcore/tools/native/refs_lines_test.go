package native

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestMaskInline covers the line-scoped masking the reference search needs:
// maskSpans leaves one-line strings and line comments alone on purpose, and a
// name inside them must not look like a use.
func TestMaskInline(t *testing.T) {
	t.Run("brace languages mask strings, '...' and the // tail", func(t *testing.T) {
		src := "const a = 1\nconst s = \"foo\"\nconst b = 'foo'\nconst c = 2 // foo here\n"
		masked := string(maskInline([]byte(src), maskBraces))
		assert.Eq(t, strings.Count(src, "\n"), strings.Count(masked, "\n"))
		assert.Eq(t, "const a = 1", strings.Split(masked, "\n")[0]) // an untouched line stays as it is
		assert.NotContains(t, masked, "foo")
		assert.StrContains(t, masked, "const s = ")
	})

	t.Run("a style keeps its own comment marker", func(t *testing.T) {
		// "//" is no comment in Python, "#" no comment in the brace languages.
		py := string(maskInline([]byte("x = 1  # foo\n"), maskPython))
		assert.NotContains(t, py, "foo")
		brace := string(maskInline([]byte("x = 1  # foo\n"), maskBraces))
		assert.StrContains(t, brace, "# foo")
	})

	t.Run("an unterminated string runs to the end of its line only", func(t *testing.T) {
		masked := string(maskInline([]byte("a = \"foo\nfoo = 2\n"), maskBraces))
		assert.NotContains(t, strings.Split(masked, "\n")[0], "foo")
		assert.StrContains(t, masked, "foo = 2") // the next line is untouched
	})

	t.Run("the masked source is a copy: the input is not modified", func(t *testing.T) {
		src := []byte("s = \"foo\"\n")
		_ = maskInline(src, maskBraces)
		assert.Eq(t, "s = \"foo\"\n", string(src))
	})
}

// tsRefsFixture is the TypeScript fixture of the approximate matcher.
const tsRefsFixture = "const foo = 1\n" + // 1
	"function bar() { return foo }\n" + // 2
	"const s = \"foo\"\n" + // 3
	"const s2 = 'foo'\n" + // 4
	"// foo mentioned in a comment\n" + // 5
	"/* foo in a block comment */\n" + // 6
	"const t = `foo`\n" + // 7
	"const multi = `\n" + // 8
	"foo\n" + // 9
	"`\n" + // 10
	"const foobar = 1\n" + // 11
	"const y = obj.foo\n" + // 12
	"const z = $foo\n" // 13

// pyRefsFixture is the Python fixture: a # comment, a one-line docstring and a
// multi-line one.
const pyRefsFixture = "foo = 1\n" + // 1
	"def bar():\n" + // 2
	"    return foo\n" + // 3
	"s = \"foo\"\n" + // 4
	"# foo mentioned in a comment\n" + // 5
	"'''foo in a docstring'''\n" + // 6
	"d = '''\n" + // 7
	"foo\n" + // 8
	"'''\n" + // 9
	"foobar = 1\n" // 10

// refLines renders the line numbers of one result.
func refLines(refs []Ref) []int {
	out := make([]int, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Line)
	}
	return out
}

// TestLineRefs covers the word-bounded approximate match: what counts as a use,
// and what the masking keeps out.
func TestLineRefs(t *testing.T) {
	t.Run("TypeScript: code lines match, literals and comments do not", func(t *testing.T) {
		refs := ScanLineRefs("fixture.ts", []byte(tsRefsFixture), "foo")
		assert.Eq(t, []int{1, 2, 12}, refLines(refs))
		assert.Eq(t, RefRef, refs[0].Class)
		assert.Eq(t, "const foo = 1", refs[0].Text) // the unmasked line text
		assert.Eq(t, "const y = obj.foo", refs[2].Text)
	})

	t.Run("Python: code lines match, a # comment and docstrings do not", func(t *testing.T) {
		refs := ScanLineRefs("fixture.py", []byte(pyRefsFixture), "foo")
		assert.Eq(t, []int{1, 3}, refLines(refs))
	})

	t.Run("word boundaries: a longer identifier or a $ is no match", func(t *testing.T) {
		assert.Eq(t, 0, len(ScanLineRefs("f.ts", []byte("const foobar = 1\nconst a = $foo\nconst c = my_foo\n"), "foo")))
		// The other direction: the name as a whole word, in code positions.
		assert.Eq(t, []int{1}, refLines(ScanLineRefs("f.ts", []byte("foo()\n"), "foo")))
		assert.Eq(t, []int{1}, refLines(ScanLineRefs("f.ts", []byte("foo.bar()\n"), "foo")))
		assert.Eq(t, []int{1}, refLines(ScanLineRefs("f.ts", []byte("const n = foo - 1\n"), "foo")))
	})

	t.Run("a name that appears nowhere yields nothing", func(t *testing.T) {
		assert.Eq(t, 0, len(ScanLineRefs("f.ts", []byte(tsRefsFixture), "other")))
	})

	t.Run("a long line is trimmed to the grep limit", func(t *testing.T) {
		src := "foo() // " + strings.Repeat("x", 400) + "\n"
		refs := ScanLineRefs("f.ts", []byte(src), "foo")
		assert.Require(t, assert.Eq(t, 1, len(refs)))
		assert.True(t, len(refs[0].Text) <= maxGrepLineBytes+len("…"), refs[0].Text)
	})
}

// TestRefWordRe covers the matcher itself, whose boundaries decide what the
// approximate languages report.
func TestRefWordRe(t *testing.T) {
	re := refWordRe("foo")
	cases := map[string]bool{
		"foo":            true,
		"foo()":          true,
		"x = foo":        true,
		"foo - 1":        true, // "foo - name" is a use: only identifier characters make a longer name
		"foobar":         false,
		"$foo":           false,
		"my_foo":         false,
		"a$foo$b":        false,
		"foo.and.more":   true,
		"const foo = 1 ": true,
	}
	for line, want := range cases {
		t.Run(line, func(t *testing.T) {
			assert.Eq(t, want, re.MatchString(line))
		})
	}

	t.Run("a name with regexp metacharacters is quoted", func(t *testing.T) {
		quoted := refWordRe("a.b")
		assert.True(t, quoted.MatchString("a.b()"))
		assert.False(t, quoted.MatchString("axb()"))
	})
}
