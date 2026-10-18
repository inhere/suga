package native

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestExtractSymbolsPHP checks the PHP recogniser: the namespace, the types
// (with PHP's modifiers and PSR-12's opening brace on the line after the
// declaration), their members and a function outside a type - and that a
// declaration written in a comment or a heredoc body is none.
func TestExtractSymbolsPHP(t *testing.T) {
	pinBuiltinExtractors(t) // the expectations are the built-in recogniser's
	syms, set := extractFixture(t, t.TempDir(), "sample.php", phpFixture)
	assert.Eq(t, sourceDeclLines, set.Source)
	assert.True(t, set.Approx, "a line-based result is marked approx")
	assert.Eq(t, []string{
		"sample.php:3 type App\\Service",
		"sample.php:11 class Greeter",
		"sample.php:13 const DEFAULT_GREETING",
		"sample.php:14 const LIMIT",
		"sample.php:18 method __construct",
		"sample.php:26 method build",
		"sample.php:36 interface GreeterInterface",
		"sample.php:38 method greet",
		"sample.php:41 trait Logs",
		"sample.php:43 method log",
		"sample.php:50 enum Suit",
		"sample.php:54 method color",
		"sample.php:60 func helper",
	}, symbolsAsText(syms))

	t.Run("a method's signature is its declaration line", func(t *testing.T) {
		build := symbolByName(t, syms, "build")
		assert.Eq(t, "protected static function &build(): self", build.Signature)
		assert.Eq(t, SymbolMethod, build.Kind)
	})

	t.Run("a property is not a declaration of the listing", func(t *testing.T) {
		for _, s := range syms {
			assert.NotEq(t, "name", s.Name, "a property is no member of the listing")
		}
	})
}

// phpFixture is the PHP fixture: a namespace, a doc comment and a block comment
// holding a declaration, an attribute, an abstract class (PSR-12 brace), class
// constants (plain and typed), a property, a method, a heredoc body holding a
// declaration, an interface, a trait, an enum and a function. The line numbers
// in the test are this file's.
const phpFixture = `<?php

namespace App\Service;

use App\Store\Repository;

/**
 * A short doc comment: class Ghost {
 */
#[Attribute]
abstract class Greeter implements GreeterInterface
{
    public const DEFAULT_GREETING = 'Hello';
    const int LIMIT = 10;

    private string $name;

    public function __construct(private string $name)
    {
        $this->name = $name;
    }

    /*
     * class Ghost {
     */
    protected static function &build(): self
    {
        $sql = <<<SQL
class Ghost {
SQL;

        return new self($sql);
    }
}

interface GreeterInterface
{
    public function greet(): string;
}

trait Logs
{
    public function log(string $line): void
    {
        // class Ghost {
        file_put_contents('/tmp/x', $line);
    }
}

enum Suit: string
{
    case Hearts = 'H';

    public function color(): string
    {
        return 'red';
    }
}

function helper(): string
{
    return 'help';
}
`

// TestPhpMasking covers what the PHP mask keeps out of the reference search and
// out of the declaration recogniser: // and # comments, /* */ blocks, string
// literals, heredoc/nowdoc bodies and the HTML after a ?>. #[ is an attribute,
// not a comment, and an opener without a closing label masks nothing.
func TestPhpMasking(t *testing.T) {
	t.Run("the spans PHP needs are blanked, the line count stays", func(t *testing.T) {
		src := "<?php\n" + // 1
			"# a # comment\n" + // 2
			"// a // comment\n" + // 3
			"/* a\nblock comment */\n" + // 4-5
			"$a = 'single';\n" + // 6
			"$b = \"double\";\n" + // 7
			"$c = <<<EOT\n" + // 8
			"heredoc body\n" + // 9
			"    EOT;\n" + // 10 the closing label may be indented (PHP 7.3+)
			"$d = <<<'EOT'\n" + // 11
			"nowdoc body\n" + // 12
			"EOT;\n" + // 13
			"$e = 1;\n" // 14
		// The reference search's pipeline (scanLineRefs): maskSpans blanks the
		// spans that run over lines, maskInline the one-line strings and comments.
		masked := string(maskInline(maskSpans([]byte(src), maskPhp), maskPhp))
		assert.Eq(t, 14, len(newSourceLines([]byte(masked)).lines)-1, "the masked source keeps its lines")
		for _, gone := range []string{"comment", "single", "double", "heredoc body", "nowdoc body", "<<<"} {
			assert.NotContains(t, masked, gone)
		}
		assert.StrContains(t, masked, "$e = 1;")
	})

	t.Run("#[Attr] is an attribute, not a comment", func(t *testing.T) {
		src := "#[Route('/x')]\n#[Attr]\n# a # comment\n"
		masked := string(maskSpans([]byte(src), maskPhp))
		assert.StrContains(t, masked, "#[Route")
		assert.StrContains(t, masked, "#[Attr]")
		assert.NotContains(t, masked, "a # comment")
	})

	t.Run("an opener without a closing label masks nothing", func(t *testing.T) {
		// A shift or a broken opener must not swallow the rest of the file.
		src := "<?php\n$x = 1 <<< 2;\nfoo();\n"
		masked := string(maskSpans([]byte(src), maskPhp))
		assert.StrContains(t, masked, "foo();")
	})

	t.Run("the HTML after ?> is no comment", func(t *testing.T) {
		src := "<?php if ($x): ?>\n<div class=\"box\">text</div>\n<?php endif; $y = 1; ?>\n"
		masked := string(maskSpans([]byte(src), maskPhp))
		assert.StrContains(t, masked, "<?php endif; $y = 1; ?>")
	})

	t.Run("the input is not modified", func(t *testing.T) {
		src := []byte("<?php\n// one\n")
		_ = maskSpans(src, maskPhp)
		assert.Eq(t, "<?php\n// one\n", string(src))
	})
}

// TestPhpDeclarationsInHeredocAndComments ties the mask to the recogniser: what
// sits inside a heredoc, a # comment or an unterminated one is no declaration.
func TestPhpDeclarationsInHeredocAndComments(t *testing.T) {
	pinBuiltinExtractors(t)
	t.Run("a heredoc body and its indented closing label", func(t *testing.T) {
		src := "<?php\n$s = <<<EOT\nclass Ghost {\n    EOT;\nclass Real {\n}\n"
		set, err := ExtractSymbols("a.php", []byte(src))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"Real"}, symbolNames(set.Symbols))
	})

	t.Run("a # comment and a // comment", func(t *testing.T) {
		src := "<?php\n# class Ghost {\n// class Other {\nclass Real\n{\n}\n"
		set, err := ExtractSymbols("a.php", []byte(src))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"Real"}, symbolNames(set.Symbols))
	})

	t.Run("an opener without a closing label masks nothing", func(t *testing.T) {
		// A broken opener must not swallow the file: the lines after it are read
		// as the recogniser always read them.
		src := "<?php\n$s = <<<EOT\nclass Real\n{\n    public function run(): void {}\n}\n"
		set, err := ExtractSymbols("a.php", []byte(src))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"Real", "run"}, symbolNames(set.Symbols))
	})
}

// symbolNames renders the names of one extraction in line order.
func symbolNames(syms []Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Name
	}
	return out
}
