package codeintel

import "regexp"

// phpSymbolLanguage is PHP: types, functions, methods and class constants,
// recognised from their declaration lines like JavaScript/TypeScript and Java.
var phpSymbolLanguage = symbolLanguage{
	name: "PHP", exts: []string{".php"}, approx: true, source: sourceDeclLines,
	ctagsLanguage: map[string]string{".php": "PHP"},
	extract: func(file string, src []byte) ([]Symbol, error) {
		return braceSymbols(file, src, phpDecls, phpBraceStyle)
	},
}

// phpBraceStyle is how the PHP recogniser reads a file: PHP's own masking (//
// and # comments, /* */ blocks, heredoc bodies) and PSR-12's type body, whose
// opening brace stands on the line after the declaration ("class Foo" then
// "{").
var phpBraceStyle = braceStyle{mask: maskPhp, bodyBraceNextLine: true}

// phpName is a PHP identifier: a letter or "_", then letters, digits, "_" or a
// byte of a non-ASCII name (PHP allows 0x80-0xff in an identifier).
const phpName = `[A-Za-z_\x80-\xff][0-9A-Za-z_\x80-\xff]*`

// phpConstType is the optional type of a typed class constant (PHP 8.3, "const
// int LIMIT = 10"): it is only accepted when a name and a "=" follow, so a
// plain "const LIMIT = 10" does not read the name as a type.
const phpConstType = `(?:[\w\\?\[\]|]+\s+)?`

// phpDecls are the PHP declaration lines. A namespace is reported as a type: it
// names a scope, which is the closest kind of the shared vocabulary. A trait
// keeps its own kind (SymbolTrait) because it is neither a class nor an
// interface.
var phpDecls = []braceDecl{
	// namespace App\Service; - the file's namespace, with its full name.
	{re: regexp.MustCompile(`^namespace\s+([^\s;{]+)`), kind: SymbolType, nameGroup: 1},
	// A class (with the modifiers PHP allows on it), an interface, a trait or an
	// enum (PHP 8.1). Each opens a type body whose members the rules below match.
	{re: regexp.MustCompile(`^(?:(?:abstract|final|readonly)\s+)*class\s+(` + phpName + `)`),
		kind: SymbolClass, nameGroup: 1, body: true},
	{re: regexp.MustCompile(`^interface\s+(` + phpName + `)`), kind: SymbolInterface, nameGroup: 1, body: true},
	{re: regexp.MustCompile(`^trait\s+(` + phpName + `)`), kind: SymbolTrait, nameGroup: 1, body: true},
	{re: regexp.MustCompile(`^enum\s+(` + phpName + `)`), kind: SymbolEnum, nameGroup: 1, body: true},
	// A method: any combination of the modifiers PHP allows, then "function"
	// (no modifier at all is a public method). The rule comes before the
	// function rule so a bare "function foo()" inside a type body is a method,
	// not a function.
	{re: regexp.MustCompile(`^(?:(?:public|protected|private|static|abstract|final)\s+)*function\s*&?\s*(` + phpName + `)`),
		kind: SymbolMethod, nameGroup: 1, member: true},
	// A class constant. PHP has no const outside a class, so the rule is a
	// member rule: it matches inside a type body only.
	{re: regexp.MustCompile(`^(?:(?:public|protected|private|final)\s+)*const\s+` + phpConstType + `(` + phpName + `)\s*=`),
		kind: SymbolConst, nameGroup: 1, member: true},
	// A function declared outside a type body, by reference too ("function
	// &build()"); a closure ("$f = function () {}") has no name and matches
	// nothing here.
	{re: regexp.MustCompile(`^function\s*&?\s*(` + phpName + `)`), kind: SymbolFunc, nameGroup: 1},
}
