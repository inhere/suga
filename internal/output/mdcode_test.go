package output

import (
	"bytes"
	gcolor "github.com/gookit/color"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// renderMDWithCode renders text with the code options set; color switches the
// terminal styling on (chat colors a terminal only).
func renderMDWithCode(text string, color, highlight bool, maxLines int) (string, *MDTerm) {
	// The SGR output goes through gookit/color, which turns colors off when it
	// detects no color terminal (CI has no TERM): force it, as mdterm_test does,
	// so the result does not depend on the environment or on test order.
	gcolor.ForceColor()
	var out bytes.Buffer
	term := NewMDTerm(&out, color)
	term.SetCode(highlight, maxLines)
	term.Write(text)
	term.Flush()
	return out.String(), term
}

// stripMDANSI drops the SGR styles, so the rendered text can be compared byte by
// byte with the source.
func stripMDANSI(s string) string { return mdANSIPattern.ReplaceAllString(s, "") }

// The SGR sequence the highlighter wraps one part of a code line in; the styles
// are 16 color ones (see mdcode.go).
var (
	mdCommentSGR = "\x1b[90m"
	mdStringSGR  = "\x1b[32m"
	mdNumberSGR  = "\x1b[35m"
	mdKeywordSGR = "\x1b[36m"
)

// TestMDCodeHighlightLanguages: every supported language colors its keywords,
// strings, comments and numbers, and only styles are added - without them the
// rendered text is the source byte for byte.
func TestMDCodeHighlightLanguages(t *testing.T) {
	all := []string{mdKeywordSGR, mdStringSGR, mdCommentSGR, mdNumberSGR}
	cases := []struct {
		lang  string
		code  string
		wants []string
	}{
		{"go", "func f() { // note\n\ts := \"hi\"\n\tn := 42\n}\n", all},
		{"python", "def f():  # note\n    s = \"hi\"\n    n = 42\n", all},
		{"javascript", "function f() { // note\n  const s = \"hi\";\n  let n = 42;\n}\n", all},
		{"ts", "const s = \"hi\"; // note\nlet n = 42;\n", all},
		{"tsx", "const el = <p title=\"hi\" />; // note\nlet n = 42;\n", all},
		{"shell", "if [ -n \"$x\" ]; then # note\n  n=42\nfi\n", all},
		{"json", "{\"s\": \"hi\", \"n\": 42, \"ok\": true}\n", []string{mdStringSGR, mdNumberSGR, mdKeywordSGR}},
		{"yaml", "name: \"hi\" # note\nn: 42\n", []string{mdStringSGR, mdCommentSGR, mdNumberSGR}},
		{"sql", "SELECT name FROM t WHERE n = 42 -- note\n", []string{mdKeywordSGR, mdNumberSGR, mdCommentSGR}},
		{"rust", "fn f() { // note\n    let s = \"hi\";\n    let n = 42;\n}\n", all},
		{"java", "void f() { // note\n  String s = \"hi\";\n  int n = 42;\n}\n", all},
		{"c", "int f(void) { // note\n  char *s = \"hi\";\n  return 42;\n}\n", all},
		{"cpp", "std::string s = \"hi\"; // note\nint n = 42;\n", all},
	}
	for _, c := range cases {
		t.Run(c.lang, func(t *testing.T) {
			got, _ := renderMDWithCode("```"+c.lang+"\n"+c.code+"```\n", true, true, 0)
			for _, want := range c.wants {
				assert.StrContains(t, got, want)
			}
			assert.Eq(t, "─── "+c.lang+"\n"+c.code+"───\n", stripMDANSI(got))
		})
	}
}

// TestMDCodeSwitch: without a colored terminal, without the setting or with an
// unknown language the block keeps its plain dim text.
func TestMDCodeSwitch(t *testing.T) {
	const body = "func main() {\n\tx := 1 // note\n\tfmt.Println(x)\n}"
	t.Run("colored lines keep the source", func(t *testing.T) {
		got, _ := renderMDWithCode("```go\n"+body+"\n```\n", true, true, 0)
		assert.StrContains(t, got, "\x1b[")
		assert.Eq(t, "─── go\n"+body+"\n───\n", stripMDANSI(got))
	})
	t.Run("without a terminal or with NO_COLOR nothing is colored", func(t *testing.T) {
		got, _ := renderMDWithCode("```go\n"+body+"\n```\n", false, true, 0)
		assert.StrNotContains(t, got, "\x1b[")
	})
	t.Run("the switch turns the coloring off", func(t *testing.T) {
		got, _ := renderMDWithCode("```go\n"+body+"\n```\n", false, false, 0)
		assert.StrNotContains(t, got, "\x1b[")
		assert.Eq(t, "─── go\n"+body+"\n───\n", got)
	})
	t.Run("an unknown language stays plain", func(t *testing.T) {
		got, _ := renderMDWithCode("```not-a-language\nx := 1 // y\n```\n", true, true, 0)
		assert.StrNotContains(t, got, "\x1b[36m")
		assert.Eq(t, "─── not-a-language\nx := 1 // y\n───\n", stripMDANSI(got))
	})
	t.Run("a fence without a language stays plain", func(t *testing.T) {
		got, _ := renderMDWithCode("```\nx := 1\n```\n", true, true, 0)
		assert.StrNotContains(t, got, mdKeywordSGR)
		assert.Eq(t, "───\nx := 1\n───\n", stripMDANSI(got))
	})
}

// TestMDCodeBlockComment: a block comment colors its opening line, every line it
// covers and the line it closes on, and its end is not taken for code again.
func TestMDCodeBlockComment(t *testing.T) {
	const code = "/* first\nstill a comment\nlast */ return 1;\n"
	got, _ := renderMDWithCode("```go\n"+code+"```\n", true, true, 0)
	assert.StrContains(t, got, mdCommentSGR+"/* first")
	assert.StrContains(t, got, mdCommentSGR+"still a comment")
	assert.StrContains(t, got, mdCommentSGR+"last */")
	// The code behind the closing marker is code again.
	assert.StrContains(t, got, mdKeywordSGR+"return")
	assert.StrContains(t, got, mdNumberSGR+"1")
	assert.Eq(t, "─── go\n"+code+"───\n", stripMDANSI(got))
}

// TestMDCodeCommentMarkersInStrings: a comment marker inside a string is part of
// the string, not a comment.
func TestMDCodeCommentMarkersInStrings(t *testing.T) {
	cases := []struct{ lang, code string }{
		{"go", "s := \"http://example.com\" // tail\n"},
		{"python", "s = \"# not a comment\"\n"},
		{"shell", "echo \"a # b\" && echo 'c -- d'\n"},
		{"json", "{\"a\": \"/* not a comment */\"}\n"},
	}
	for _, c := range cases {
		t.Run(c.lang, func(t *testing.T) {
			got, _ := renderMDWithCode("```"+c.lang+"\n"+c.code+"```\n", true, true, 0)
			assert.StrContains(t, got, mdStringSGR)
			// The code lines carry no comment style (the fences are dim lines of
			// their own, hence the middle lines only).
			lines := strings.Split(got, "\n")
			body := strings.Join(lines[1:len(lines)-2], "\n")
			assert.StrContains(t, body, mdStringSGR)
			// The tail of the go case is a real comment; the others have none.
			if c.lang == "go" {
				assert.StrContains(t, body, mdCommentSGR+"// tail")
			} else {
				assert.StrNotContains(t, body, mdCommentSGR)
			}
			assert.Eq(t, "─── "+c.lang+"\n"+c.code+"───\n", stripMDANSI(got))
		})
	}
}

// TestMDCodeStreamsDeltaSplit: a line split over several deltas is colored as a
// whole once its newline arrives, so a marker cut in half is still recognized.
func TestMDCodeStreamsDeltaSplit(t *testing.T) {
	var out bytes.Buffer
	term := NewMDTerm(&out, true)
	term.SetCode(true, 0)
	term.Write("```go\nx := 1 /")
	assert.StrNotContains(t, out.String(), "x :=") // the line waits for its newline
	term.Write("/ note\n")
	term.Write("```\n")
	term.Flush()
	assert.StrContains(t, out.String(), mdCommentSGR+"// note")
	assert.Eq(t, "─── go\nx := 1 // note\n───\n", stripMDANSI(out.String()))
}

// TestMDCodeStreamsPerLine: a code line is colored as soon as its newline
// arrives, not when the block ends (a long block must not wait for its fence).
func TestMDCodeStreamsPerLine(t *testing.T) {
	var out bytes.Buffer
	term := NewMDTerm(&out, true)
	term.SetCode(true, 0)
	term.Write("```go\nfunc a()\n")
	assert.StrContains(t, stripMDANSI(out.String()), "func a()")
	assert.StrNotContains(t, stripMDANSI(out.String()), "───\n") // the closing fence is not there yet
	term.Write("```\n")
	term.Flush()
	assert.Eq(t, "─── go\nfunc a()\n───\n", stripMDANSI(out.String()))
}

// TestMDCodeLang: the language is the first word of the fence info, without its
// braces or leading dot.
func TestMDCodeLang(t *testing.T) {
	for info, want := range map[string]string{
		"":              "",
		"go":            "go",
		"Go":            "go",
		"go title=x":    "go",
		"{.go}":         "go",
		"  python  ":    "python",
		"{.go linenos}": "go",
	} {
		t.Run(info, func(t *testing.T) {
			assert.Eq(t, want, mdCodeLang(info))
		})
	}
}

// TestMDCodeHighlighter: the highlighter is only built for a known language, and
// its colored line is the source plus styles.
func TestMDCodeHighlighter(t *testing.T) {
	assert.True(t, newMDCode("go") != nil)
	assert.True(t, newMDCode("bash") != nil)
	assert.True(t, newMDCode("not-a-language") == nil)
	assert.True(t, newMDCode("") == nil)
	colored := newMDCode("go").line("x := 1")
	assert.Eq(t, "x := 1", stripMDANSI(colored))
	assert.Eq(t, 0, strings.Count(stripMDANSI(colored), "\n"), "a colored line carries no newline of its own")
}
