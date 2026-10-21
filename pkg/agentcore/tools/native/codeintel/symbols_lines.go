package codeintel

import (
	"regexp"
	"strings"
)

// The lightweight recognisers: JavaScript/TypeScript, Python, Java and PHP get
// a declaration-line recogniser instead of a parser (design 0.2), so their
// results are marked approx. They never fail on a broken file - they read what
// a declaration line looks like - which is enough to answer "which
// declarations are in this file, on which line" while file_grep stays the way
// to confirm an exact name.

// braceDecl is one declaration pattern of a brace-delimited language.
type braceDecl struct {
	re *regexp.Regexp
	// kind is the kind the rule reports, or "" when kindGroup names it.
	kind string
	// kindGroup is the 1-based capture group holding the kind (0 = use kind).
	kindGroup int
	// nameGroup is the 1-based capture group holding the declaration's name.
	nameGroup int
	// member rules match only inside a type body: a class member or a nested
	// type, never a statement of a function body.
	member bool
	// body tells that the rule opens a type body (class/interface/enum/record),
	// whose brace depth member rules are limited to.
	body bool
}

// jsSymbolLanguage is JavaScript and TypeScript: two languages, one
// recogniser, one entry per ctags language.
var jsSymbolLanguage = symbolLanguage{
	name:   "JavaScript/TypeScript",
	exts:   []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
	approx: true, source: sourceDeclLines,
	ctagsLanguage: map[string]string{
		".ts": "TypeScript", ".tsx": "TypeScript", ".mts": "TypeScript", ".cts": "TypeScript",
		".js": "JavaScript", ".jsx": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript",
	},
	extract: func(file string, src []byte) ([]Symbol, error) {
		return braceSymbols(file, src, jsDecls, braceStyle{mask: maskBraces})
	},
}

// jsDecls are the JavaScript/TypeScript declaration lines: classes,
// interfaces, type aliases, enums, functions and const/let/var bindings, plus
// the members of a class body.
var jsDecls = []braceDecl{
	{re: regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`),
		kind: SymbolClass, nameGroup: 1, body: true},
	{re: regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?interface\s+([A-Za-z_$][\w$]*)`),
		kind: SymbolInterface, nameGroup: 1, body: true},
	{re: regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?type\s+([A-Za-z_$][\w$]*)\s*[=<]`),
		kind: SymbolType, nameGroup: 1},
	{re: regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:const\s+)?enum\s+([A-Za-z_$][\w$]*)`),
		kind: SymbolEnum, nameGroup: 1, body: true},
	{re: regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`),
		kind: SymbolFunc, nameGroup: 1},
	{re: regexp.MustCompile(`^(?:export\s+)?const\s+([A-Za-z_$][\w$]*)`), kind: SymbolConst, nameGroup: 1},
	{re: regexp.MustCompile(`^(?:export\s+)?(?:let|var)\s+([A-Za-z_$][\w$]*)`), kind: SymbolVar, nameGroup: 1},
	// A class member: modifiers, then the name, then its parameter list.
	{re: regexp.MustCompile(`^(?:(?:public|private|protected|static|async|readonly|override|declare|abstract|get|set)\s+)*([A-Za-z_$][\w$]*)\s*(?:<[^<>()]*>)?\s*\(`),
		kind: SymbolMethod, nameGroup: 1, member: true},
}

// javaSymbolLanguage is Java: types and their members.
var javaSymbolLanguage = symbolLanguage{
	name: "Java", exts: []string{".java"}, approx: true, source: sourceDeclLines,
	ctagsLanguage: map[string]string{".java": "Java"},
	extract: func(file string, src []byte) ([]Symbol, error) {
		return braceSymbols(file, src, javaDecls, braceStyle{mask: maskBraces})
	},
}

// javaDecls are the Java declaration lines: classes, interfaces, enums and
// records (also nested ones) and the methods of a type body. Field declarations
// are left out on purpose: a field is not what a "where is this used" question
// looks for, and keeping them would bury the methods.
var javaDecls = []braceDecl{
	{re: regexp.MustCompile(`^(?:(?:public|protected|private|static|final|abstract|sealed|non-sealed|strictfp)\s+)*(class|interface|enum|record)\s+([A-Za-z_$][\w$]*)`),
		kindGroup: 1, nameGroup: 2, body: true},
	// A method: modifiers, an optional return type, the name, then parameters.
	// The return type excludes "." and "=" on purpose: a qualified type or a
	// field initializer then does not look like a method.
	{re: regexp.MustCompile(`^\s*(?:(?:public|protected|private|static|final|abstract|synchronized|native|default|strictfp)\s+)*(?:[\w$<>\[\],\s?]+\s+)?([A-Za-z_$][\w$]*)\s*\(`),
		kind: SymbolMethod, nameGroup: 1, member: true},
}

// javaNonDeclNames are the words that can sit where a method's name sits in a
// statement of a method body ("return foo(", "if (x)"): they are not
// declarations, so the member rules skip them.
var javaNonDeclNames = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true,
	"new": true, "do": true, "else": true, "case": true, "throw": true, "assert": true,
	"super": true, "this": true, "try": true, "finally": true, "break": true, "continue": true,
	"instanceof": true, "typeof": true, "delete": true, "void": true, "await": true, "yield": true,
	"print": true, "synchronized": true,
}

// braceSymbols recognises the declarations of a brace-delimited language line
// by line. The brace depth tells the file's own declarations and the members of
// a type body apart from the statements of a function body, where a local const
// or a bare call looks like a declaration. Braces inside '...', "..." and
// `...` strings and after // are ignored, and /* */ comments, multi-line
// template strings and """ text blocks are blanked first (maskSpans, with the
// style of the language).
func braceSymbols(file string, src []byte, decls []braceDecl, style braceStyle) ([]Symbol, error) {
	lines := NewSourceLines(maskSpans(src, style.mask))
	var out []Symbol
	// braces is the brace depth, bodyStack the brace depths of the type bodies
	// currently open: a member rule matches only directly inside one of them, so
	// a statement of a method body is never taken for a declaration.
	braces, bodyStack := 0, []int(nil)
	// pendingBody is a type body whose opening brace has not been read yet (the
	// PSR-12 style of PHP writes it on the next line).
	pendingBody := false
	for i, raw := range lines.Lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		for len(bodyStack) > 0 && braces < bodyStack[len(bodyStack)-1] {
			bodyStack = bodyStack[:len(bodyStack)-1]
		}
		inBody := len(bodyStack) > 0 && braces == bodyStack[len(bodyStack)-1]
		opened := false
		if braces == 0 || inBody { // a function body is neither
			for _, d := range decls {
				if d.member && !inBody {
					continue
				}
				m := d.re.FindStringSubmatchIndex(line)
				if m == nil {
					continue
				}
				start, end := m[2*d.nameGroup], m[2*d.nameGroup+1]
				name := line[start:end]
				if javaNonDeclNames[name] {
					continue
				}
				// "name = value" is a field or a variable, not a method.
				if d.member {
					if eq := strings.IndexByte(line, '='); eq >= 0 && eq < start {
						continue
					}
				}
				kind := d.kind
				if d.kindGroup > 0 {
					kind = line[m[2*d.kindGroup]:m[2*d.kindGroup+1]]
				}
				out = append(out, Symbol{File: file, Line: i + 1, Kind: kind, Name: name, Signature: line})
				opened = d.body
				break
			}
		}
		delta := braceDelta(raw)
		braces += delta
		if braces < 0 {
			braces = 0
		}
		switch {
		case opened && delta > 0:
			bodyStack = append(bodyStack, braces) // the type body just opened
			pendingBody = false
		case opened:
			pendingBody = style.bodyBraceNextLine
		case pendingBody && delta > 0:
			bodyStack = append(bodyStack, braces) // the "{" of a declaration-line body
			pendingBody = false
		}
	}
	return out, nil
}

// braceStyle is how one brace-delimited language's recogniser reads a file: the
// style of its comments and of its multi-line strings, and where the opening
// brace of a type body may sit.
type braceStyle struct {
	mask maskStyle
	// bodyBraceNextLine accepts that brace on a follow-up line (PHP's PSR-12
	// writes "class Foo" and then "{"): the body is pushed at the next line that
	// opens a brace. It is off for JavaScript/TypeScript and Java, whose style
	// puts the brace on the declaration line, so their results are exactly what
	// they were.
	bodyBraceNextLine bool
}

// braceDelta is the change in brace depth of one line, ignoring braces inside
// quoted strings and after // (see braceSymbols for what stays approximate).
func braceDelta(line string) int {
	delta := 0
	quote, escaped := byte(0), false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '/':
			if i+1 < len(line) && line[i+1] == '/' {
				return delta // the rest of the line is a comment
			}
		case '{':
			delta++
		case '}':
			delta--
		}
	}
	return delta
}

// pythonSymbolLanguage is Python: class and def lines, with the indentation
// deciding whether a def is a method.
var pythonSymbolLanguage = symbolLanguage{
	name: "Python", exts: []string{".py", ".pyi"}, approx: true, source: sourceDeclLines,
	ctagsLanguage: map[string]string{".py": "Python", ".pyi": "Python"},
	extract:       pythonSymbols,
}

var (
	pyClassRe = regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`)
	pyDefRe   = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_]\w*)`)
)

// pythonSymbols recognises Python's class and def lines. A def is a method when
// an enclosing class is open, which the indentation decides: a block ends where
// a line is no deeper than its first line, Python's own rule. Only the class
// and def lines themselves are looked at, so a call inside a function body is
// never mistaken for a declaration.
func pythonSymbols(file string, src []byte) ([]Symbol, error) {
	// Docstrings are blanked first: an example "def x():" in one is no def.
	lines := NewSourceLines(maskSpans(src, maskPython))
	// frames are the open blocks: a def or a class, by their indentation.
	type frame struct {
		indent int
		class  bool
	}
	var frames []frame
	var out []Symbol
	add := func(i int, kind, name, signature string) {
		out = append(out, Symbol{File: file, Line: i + 1, Kind: kind, Name: name, Signature: signature})
	}
	for i, raw := range lines.Lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		indent := indentWidth(raw)
		for len(frames) > 0 && indent <= frames[len(frames)-1].indent {
			frames = frames[:len(frames)-1]
		}
		inClass := len(frames) > 0 && frames[len(frames)-1].class
		switch {
		case pyClassRe.MatchString(line):
			add(i, SymbolClass, pyClassRe.FindStringSubmatch(line)[1], line)
			frames = append(frames, frame{indent: indent, class: true})
		case pyDefRe.MatchString(line):
			kind := SymbolFunc
			if inClass {
				kind = SymbolMethod
			}
			add(i, kind, pyDefRe.FindStringSubmatch(line)[1], line)
			frames = append(frames, frame{indent: indent})
		}
	}
	return out, nil
}

// indentWidth counts a line's leading whitespace in characters (a tab counts as
// one): only the order of the indentation levels matters, not their width.
func indentWidth(line string) int {
	n := 0
	for _, r := range line {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}
