package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// phpRefsFixture is the PHP fixture of the approximate reference matcher: the
// uses of a name in code, and the occurrences the masking must keep out (a //
// comment, a # comment, a /* */ block, single- and double-quoted strings and a
// heredoc body).
const phpRefsFixture = "<?php\n" + // 1
	"$service = new Greeter();\n" + // 2
	"$service->greet();\n" + // 3
	"Greeter::create();\n" + // 4
	"// Greeter is a comment\n" + // 5
	"# Greeter is a comment too\n" + // 6
	"/* Greeter in a block comment */\n" + // 7
	"$s = 'Greeter in a string';\n" + // 8
	"$t = \"Greeter in a string\";\n" + // 9
	"$sql = <<<SQL\n" + // 10
	"SELECT * FROM Greeter\n" + // 11
	"SQL;\n" + // 12
	"function notGreeter(): void {}\n" // 13

// TestLineRefsPHP covers the PHP reference matcher: a new, a static call and an
// object call are uses, everything the mask covers is not.
func TestLineRefsPHP(t *testing.T) {
	t.Run("code lines match, comments, strings and a heredoc body do not", func(t *testing.T) {
		assert.Eq(t, []int{2, 4}, refLines(ScanLineRefs("fixture.php", []byte(phpRefsFixture), "Greeter")))
		assert.Eq(t, []int{3}, refLines(ScanLineRefs("fixture.php", []byte(phpRefsFixture), "greet")))
		assert.Eq(t, 0, len(ScanLineRefs("fixture.php", []byte(phpRefsFixture), "SELECT")))
	})

	t.Run("a variable's $ keeps it out of the way of the bare name", func(t *testing.T) {
		assert.Eq(t, 0, len(ScanLineRefs("a.php", []byte("$foo = 1;\n$bar = $foo;\n"), "foo")))
		assert.Eq(t, []int{1}, refLines(ScanLineRefs("a.php", []byte("foo($foo);\n"), "foo")))
		assert.Eq(t, []int{1}, refLines(ScanLineRefs("a.php", []byte("$x = foo($y);\n"), "foo")))
	})

	t.Run("#[Attr] is an attribute, not a comment", func(t *testing.T) {
		src := "#[Route('/x')] // no Route here\npublic function run(): void\n{\n    Route::get('/x');\n}\n"
		assert.Eq(t, []int{1, 4}, refLines(ScanLineRefs("a.php", []byte(src), "Route")))
	})

	t.Run("the HTML after ?> is searched like the rest", func(t *testing.T) {
		src := "<?php if ($x): ?>\n<div class=\"box\">text</div>\n<?php endif; $y = foo(); ?>\n"
		assert.Eq(t, []int{3}, refLines(ScanLineRefs("tpl.php", []byte(src), "foo")))
	})

	t.Run("an opener without a closing label masks nothing", func(t *testing.T) {
		src := "<?php\n$s = <<<EOT\nfoo();\n"
		assert.Eq(t, []int{3}, refLines(ScanLineRefs("a.php", []byte(src), "foo")))
	})

	t.Run("an indented closing label ends the heredoc", func(t *testing.T) {
		src := "<?php\n$s = <<<EOT\nfoo\n    EOT;\nbar(foo);\n"
		assert.Eq(t, []int{5}, refLines(ScanLineRefs("a.php", []byte(src), "foo")))
	})
}

// TestLineRefsPHPQualified covers the qualified-name filter of a PHP query: the
// object (->) and the static (::) access are the shapes it accepts.
func TestLineRefsPHPQualified(t *testing.T) {
	t.Run("$this->name is kept, another object's use is dropped", func(t *testing.T) {
		src := "$this->greet();\n$other->greet();\n"
		refs, dropped := scanLineRefs("a.php", []byte(src), "greet", "this")
		assert.Eq(t, []int{1}, refLines(refs))
		assert.Eq(t, 1, dropped.total())
	})

	t.Run("Q::name is the other shape", func(t *testing.T) {
		src := "Config::ReadOnly;\n$cfg->ReadOnly;\n"
		refs, dropped := scanLineRefs("a.php", []byte(src), "ReadOnly", "Config")
		assert.Eq(t, []int{1}, refLines(refs))
		assert.Eq(t, 1, dropped.total())
	})

	t.Run("the separators of a query", func(t *testing.T) {
		assert.True(t, approxQualifierMatches("$this->greet();", "this", "greet"))
		assert.True(t, approxQualifierMatches("Greeter::greet();", "Greeter", "greet"))
		assert.False(t, approxQualifierMatches("$other->greet();", "this", "greet"))
	})
}

// TestFileRefsPHP is the tool over a small PHP workspace: the directory query
// reports the definition and the calls, a qualified query keeps what $this
// calls and the result carries the approx marker.
func TestFileRefsPHP(t *testing.T) {
	ws := t.TempDir()
	files := map[string]string{
		"src/Greeter.php": "<?php\n\nnamespace App;\n\nclass Greeter\n{\n    public function greet(): string\n    {\n        return 'hello';\n    }\n}\n",
		"src/Runner.php": "<?php\n\nnamespace App;\n\nclass Runner\n{\n    public function run(Greeter $greeter): string\n    {\n" +
			"        $this->log();\n        return $greeter->greet();\n    }\n\n    private function log(): void\n    {\n    }\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}
	tool := &FileRefsTool{workspace: ws}

	t.Run("a directory reports the definition and the calls", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "greet", "path": "src"})
		assert.StrContains(t, got, "src/Greeter.php:7 ref public function greet(): string")
		assert.StrContains(t, got, "src/Runner.php:10 ref return $greeter->greet();")
		assert.Eq(t, 2, strings.Count(got, " ref "), got)
		assert.StrContains(t, got, "[approx: ")
		assert.StrContains(t, got, "PHP")
	})

	t.Run("a qualified query keeps what $this calls", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "this.log", "path": "src"})
		assert.StrContains(t, got, "src/Runner.php:9 ref $this->log();")
		assert.NotContains(t, got, "Runner.php:13")
		assert.StrContains(t, got, `[qualified by "this"`)
	})

	t.Run("one PHP file alone is searched as that file", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "greet", "path": "src/Greeter.php"})
		assert.StrContains(t, got, "src/Greeter.php:7 ref public function greet(): string")
		assert.StrContains(t, got, "[approx: ")
	})

	t.Run("a PHP file is a supported type for the file query", func(t *testing.T) {
		assert.True(t, SupportsSymbols("src/Greeter.php"))
		assert.StrContains(t, SupportedSymbolLanguages(), "PHP (.php)")
	})
}
