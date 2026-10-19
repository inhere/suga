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

// TestConfigKeyRefs pins the one class the two configuration formats add to the
// line matcher: the line that defines the name as a key is a def, so "where is
// this config key declared" is visible at a glance. A commented-out key is no
// line at all, and the rule must not leak into the code languages.
func TestConfigKeyRefs(t *testing.T) {
	t.Run("YAML: a key line is def, a value and a comment are not", func(t *testing.T) {
		src := "server:\n" + // 1
			"  port: 8080\n" + // 2
			"# port: 9090\n" + // 3
			"listen: \"port\"\n" // 4
		refs := ScanLineRefs("app.yaml", []byte(src), "port")
		assert.Eq(t, []int{2, 4}, refLines(refs))
		assert.Eq(t, RefDef, refs[0].Class)
		assert.Eq(t, RefRef, refs[1].Class) // a value is a use, not a definition
	})

	t.Run("YAML: a sequence entry is def too", func(t *testing.T) {
		refs := ScanLineRefs("app.yml", []byte("ports:\n  - port: 8080\n"), "port")
		assert.Require(t, assert.Eq(t, 1, len(refs)))
		assert.Eq(t, RefDef, refs[0].Class)
	})

	t.Run("TOML: an assignment is def, a table header is not", func(t *testing.T) {
		src := "[server]\nport = 8080\nname = \"port\"\n"
		refs := ScanLineRefs("config.toml", []byte(src), "port")
		assert.Eq(t, []int{2, 3}, refLines(refs))
		assert.Eq(t, RefDef, refs[0].Class)
		assert.Eq(t, RefRef, refs[1].Class)
	})

	t.Run("a code language keeps its plain refs", func(t *testing.T) {
		refs := ScanLineRefs("app.js", []byte("port = 1\n"), "port")
		assert.Require(t, assert.Eq(t, 1, len(refs)))
		assert.Eq(t, RefRef, refs[0].Class)
	})
}

// TestLineRefsQualified covers the qualified-name filter of the approximate
// matcher: only the lines writing the qualified name stay, the others are
// counted as dropped.
func TestLineRefsQualified(t *testing.T) {
	t.Run("a Python qualified use keeps its line and counts the others", func(t *testing.T) {
		src := "def load():\n" + // 1
			"    return opts.ReadOnly\n" + // 2
			"def other(cfg):\n" + // 3
			"    return cfg.ReadOnly\n" // 4
		refs, dropped := scanLineRefs("fixture.py", []byte(src), "ReadOnly", "opts")
		assert.Eq(t, []int{2}, refLines(refs))
		assert.Eq(t, 1, dropped.total())
	})

	t.Run("Q::name is the other shape the filter accepts", func(t *testing.T) {
		src := "Config::ReadOnly\nother.ReadOnly\n"
		refs, dropped := scanLineRefs("fixture.ts", []byte(src), "ReadOnly", "Config")
		assert.Eq(t, []int{1}, refLines(refs))
		assert.Eq(t, 1, dropped.total())
	})

	t.Run("a comment keeps no qualified use either", func(t *testing.T) {
		src := "// opts.ReadOnly is a flag\nreturn 1\n"
		refs, dropped := scanLineRefs("fixture.ts", []byte(src), "ReadOnly", "opts")
		assert.Eq(t, 0, len(refs))
		assert.Eq(t, 0, dropped.total())
	})
}

// TestApproxQualifierMatches covers the shapes the filter looks for in one
// masked line, and the qualifier it must not accept.
func TestApproxQualifierMatches(t *testing.T) {
	cases := []struct {
		line string
		qual string
		name string
		want bool
	}{
		{"return opts.ReadOnly", "opts", "ReadOnly", true},
		{"Config::ReadOnly = 1", "Config", "ReadOnly", true},
		{"RunnerRun()", "Runner", "Run", true},
		{"return cfg.ReadOnly", "opts", "ReadOnly", false},
		{"return opts.ReadOnly", "Options", "ReadOnly", false},
		{"return opts.ReadOnly", "", "ReadOnly", true}, // no filter keeps the line
	}
	for _, c := range cases {
		t.Run(c.qual+"|"+c.line, func(t *testing.T) {
			assert.Eq(t, c.want, approxQualifierMatches(c.line, c.qual, c.name))
		})
	}
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
