package codeintel

import (
	"regexp"
	"strings"
)

// The lightweight recognisers: JavaScript/TypeScript, Python, Java, PHP and the
// rule-file languages get a declaration-line recogniser instead of a parser
// (design 0.2), so their results are marked approx. They never fail on a broken
// file - they read what a declaration line looks like - which is enough to
// answer "which declarations are in this file, on which line" while file_grep
// stays the way to confirm an exact name. JavaScript/TypeScript, Python and
// Java describe their declarations in the embedded rule files
// (langrules/*.yaml); PHP keeps its own table here (heredoc masking, #[.

// braceDecl is one declaration pattern of a brace-delimited language. The rule
// languages compile their `decls` entries into it, so one matcher serves them,
// the built-in PHP and every block style (brace, indent, end_keyword).
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
	// whose nesting member rules are limited to.
	body bool
}

// javaNonDeclNames are the words that can sit where a method's name sits in a
// statement of a method body ("return foo(", "if (x)"): they are not
// declarations, so the member rules skip them. The list is shared by every
// brace and end_keyword language (Java, JavaScript, C#, Kotlin, Ruby, C/C++).
var javaNonDeclNames = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true,
	"new": true, "do": true, "else": true, "case": true, "throw": true, "assert": true,
	"super": true, "this": true, "try": true, "finally": true, "break": true, "continue": true,
	"instanceof": true, "typeof": true, "delete": true, "void": true, "await": true, "yield": true,
	"print": true, "synchronized": true, "sizeof": true, "defined": true, "unless": true,
	"until": true, "puts": true, "raise": true, "when": true, "end": true,
}

// matchDeclLine finds the declaration of one line: the first rule that matches
// wins, member rules only inside a type body, and a "name = value" line is a
// field or a variable rather than a method.
func matchDeclLine(line string, decls []braceDecl, inBody bool) (d braceDecl, kind, name string, ok bool) {
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
		return d, kind, name, true
	}
	return braceDecl{}, "", "", false
}

// startsComment reports whether the first non-space byte of a raw line opens a
// line comment of the language.
func startsComment(spec maskSpec, raw string) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] == ' ' || raw[i] == '\t' {
			continue
		}
		return spec.lineCommentAt([]byte(raw), i)
	}
	return false
}

// braceSymbols recognises the declarations of a brace-delimited language line
// by line. The brace depth tells the file's own declarations and the members of
// a type body apart from the statements of a function body, where a local const
// or a bare call looks like a declaration. Braces inside '...', "..." and
// `...` strings and after // are ignored, and /* */ comments, multi-line
// template strings and """ text blocks are blanked first (maskSpans, with the
// style of the language).
func braceSymbols(file string, src []byte, decls []braceDecl, style braceStyle) ([]Symbol, error) {
	lines := NewSourceLines(style.masked(src))
	var out []Symbol
	// braces is the brace depth, bodyStack the brace depths of the type bodies
	// currently open: a member rule matches only directly inside one of them, so
	// a statement of a method body is never taken for a declaration.
	braces, bodyStack := 0, []int(nil)
	// pendingBody is a type body whose opening brace has not been read yet (the
	// PSR-12 style of PHP and the Allman style of C# write it on the next line).
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
			if d, kind, name, ok := matchDeclLine(line, decls, inBody); ok {
				out = append(out, Symbol{File: file, Line: i + 1, Kind: kind, Name: name, Signature: line})
				opened = d.body
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
	// spec is the masking of a rule language (its `syntax:` block); it replaces
	// mask when set.
	spec *maskSpec
	// bodyBraceNextLine accepts that brace on a follow-up line (PHP's PSR-12
	// and C#'s Allman style write "class Foo" and then "{"): the body is pushed
	// at the next line that opens a brace. It is off for JavaScript/TypeScript
	// and Java, whose style puts the brace on the declaration line, so their
	// results are exactly what they were.
	bodyBraceNextLine bool
}

// masked returns the source with the comments and multi-line strings blanked.
func (s braceStyle) masked(src []byte) []byte {
	if s.spec != nil {
		return maskSpansSpec(src, *s.spec)
	}
	return maskSpans(src, s.mask)
}

// indentSymbols recognises the declarations of an indentation-delimited
// language (Python): a declaration's block ends where a line is no deeper than
// its first line, Python's own rule, so only the class and def lines themselves
// are looked at and a call inside a function body is never mistaken for a
// declaration.
func indentSymbols(file string, src []byte, decls []braceDecl, spec maskSpec) ([]Symbol, error) {
	// Docstrings are blanked first: an example "def x():" in one is no def.
	lines := NewSourceLines(maskSpansSpec(src, spec))
	// frames are the open blocks: a declaration with a type body (a class) and
	// one without (a def), by their indentation.
	type frame struct {
		indent int
		body   bool
	}
	var frames []frame
	var out []Symbol
	for i, raw := range lines.Lines {
		line := strings.TrimSpace(raw)
		if line == "" || startsComment(spec, raw) {
			continue
		}
		indent := indentWidth(raw)
		for len(frames) > 0 && indent <= frames[len(frames)-1].indent {
			frames = frames[:len(frames)-1]
		}
		inBody := len(frames) > 0 && frames[len(frames)-1].body
		d, kind, name, ok := matchDeclLine(line, decls, inBody)
		if !ok {
			continue
		}
		out = append(out, Symbol{File: file, Line: i + 1, Kind: kind, Name: name, Signature: line})
		frames = append(frames, frame{indent: indent, body: d.body})
	}
	return out, nil
}

// endKeywordSymbols recognises the declarations of a language whose blocks are
// closed by a keyword instead of a brace (Ruby): a block opener line deepens
// the stack, the closing keyword (end) pops it, and a member rule matches only
// directly inside the body of a type (class, module) - the same semantics as
// braceSymbols' member/body rules.
func endKeywordSymbols(file string, src []byte, decls []braceDecl, spec maskSpec, open, close *regexp.Regexp) ([]Symbol, error) {
	lines := NewSourceLines(maskSpansSpec(src, spec))
	var out []Symbol
	depth := 0
	var bodyStack []int
	for i, raw := range lines.Lines {
		line := strings.TrimSpace(raw)
		if line == "" || startsComment(spec, raw) {
			continue
		}
		for len(bodyStack) > 0 && depth < bodyStack[len(bodyStack)-1] {
			bodyStack = bodyStack[:len(bodyStack)-1]
		}
		inBody := len(bodyStack) > 0 && depth == bodyStack[len(bodyStack)-1]
		opened := false
		if depth == 0 || inBody {
			if d, kind, name, ok := matchDeclLine(line, decls, inBody); ok {
				out = append(out, Symbol{File: file, Line: i + 1, Kind: kind, Name: name, Signature: line})
				opened = d.body
			}
		}
		// Every "end" closes a block and every opener line ("class Foo", "def
		// x", "if ...") opens one; the approximation is that a line holding both
		// (a one-line "def x; end") only closes.
		closed := close != nil && close.MatchString(line)
		if closed {
			depth--
			if depth < 0 {
				depth = 0
			}
			continue
		}
		if open != nil && open.MatchString(line) {
			depth++
			if opened {
				bodyStack = append(bodyStack, depth) // the type body just opened
			}
		}
	}
	return out, nil
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
