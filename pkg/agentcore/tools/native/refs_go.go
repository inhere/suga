package native

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// The reference extraction layer of file_refs (design
// docs/design/2026-10-18-symbol-refs-design.md): where a name is *used*, as
// opposed to the declaration extractors in symbols*.go, which answer where it
// is declared. Go gets the exact treatment with go/parser - but no type
// checking, so a match may be another object with the same name; the other
// languages get the word-bounded text match of refs_lines.go.

// The classes a reference is reported as. They tell a definition from a use
// without type information: "def" is a binding position, "call" a call of the
// name, "sel" a selector use of it (x.Name, pkg.Name) and "ref" anything else.
const (
	RefDef  = "def"
	RefCall = "call"
	RefSel  = "sel"
	RefRef  = "ref"
)

// Ref is one occurrence of a searched name in a source file.
type Ref struct {
	File  string // as the caller names the file: relative to the workspace, absolute under a read root
	Line  int    // 1-based line of the occurrence
	Class string // one of RefDef, RefCall, RefSel, RefRef
	// Qualifier is the X of an X.Name occurrence (call/sel): a package name, a
	// value, a field or a receiver. Empty for def/ref.
	Qualifier string
	// Pkg marks a qualifier that the same file imports as a package name, so
	// runtime.ReadOnly (a package symbol) is told from cfg.ReadOnly (a field).
	Pkg bool
	// Text is the trimmed source line, at most maxGrepLineBytes like file_grep.
	Text string
}

// ScanGoRefs returns every occurrence of name in one Go file, classified by
// go/parser + go/ast without type checking (parser.SkipObjectResolution, the
// same minimal parse the declaration extractor uses). Occurrences inside
// comments and string literals are not identifiers, so they never match. A
// file that does not parse returns its error to the caller.
func ScanGoRefs(file string, src []byte, name string) ([]Ref, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	lines := newSourceLines(src)
	imports := goImportNames(parsed)
	// nodeText is the source text of one node: how the qualifier of X.Name is
	// shown, without any type information.
	nodeText := func(n ast.Node) string {
		from, to := fset.Position(n.Pos()).Offset, fset.Position(n.End()).Offset
		if from < 0 || to > len(src) || from >= to {
			return ""
		}
		return string(src[from:to])
	}
	var out []Ref
	// stack is the path to the node being visited: the parent decides how an
	// identifier is used, which is all the classification needs.
	var stack []ast.Node
	ast.Inspect(parsed, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		var parent, grandparent ast.Node
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		if len(stack) > 1 {
			grandparent = stack[len(stack)-2]
		}
		stack = append(stack, n)
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name != name {
			return true
		}
		ref := Ref{File: file, Line: fset.Position(ident.Pos()).Line}
		switch p := parent.(type) {
		case *ast.FuncDecl:
			if p.Name == ident {
				ref.Class = RefDef
			}
		case *ast.TypeSpec:
			if p.Name == ident {
				ref.Class = RefDef
			}
		case *ast.ValueSpec:
			if identIn(p.Names, ident) {
				ref.Class = RefDef
			}
		case *ast.Field:
			// A struct field, an interface method, a parameter, a result or a
			// receiver: all of them are binding positions here, because the
			// name is bound rather than used.
			if identIn(p.Names, ident) {
				ref.Class = RefDef
			}
		case *ast.ImportSpec:
			// An explicit import name ("import x \"path\"").
			if p.Name == ident {
				ref.Class = RefDef
			}
		case *ast.LabeledStmt:
			if p.Label == ident {
				ref.Class = RefDef
			}
		case *ast.AssignStmt:
			// Only := binds; "x = v" assigns to something bound elsewhere. A
			// left side that is not a plain identifier (m[k] := v) is not a
			// binding position of this name.
			if p.Tok == token.DEFINE {
				for _, lhs := range p.Lhs {
					if lhs == ident {
						ref.Class = RefDef
						break
					}
				}
			}
		case *ast.RangeStmt:
			if p.Tok == token.DEFINE && (p.Key == ident || p.Value == ident) {
				ref.Class = RefDef
			}
		case *ast.SelectorExpr:
			if p.Sel == ident {
				ref.Class = RefSel
				ref.Qualifier = nodeText(p.X)
				ref.Pkg = imports[ref.Qualifier]
				// X.Name(...) is a call of the name rather than a field read.
				if call, ok := grandparent.(*ast.CallExpr); ok && call.Fun == p {
					ref.Class = RefCall
				}
			}
		case *ast.CallExpr:
			if p.Fun == ident {
				ref.Class = RefCall
			}
		}
		if ref.Class == "" {
			ref.Class = RefRef // a bare use: a type, an operand, an embedded name
		}
		ref.Text = refLineText(lines.at(ref.Line))
		out = append(out, ref)
		return true
	})
	// go/ast walks in tree order, which is not quite line order (a func
	// declaration's body is visited after its signature): sort so that one
	// line's occurrences are stable between runs.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Class < out[j].Class
	})
	return out, nil
}

// scanRefs-style helpers shared by the Go extractor and the approximate
// matcher of refs_lines.go live here: both report the trimmed line of a match.

// refLineText trims a matching line and cuts it at maxGrepLineBytes, the limit
// file_grep applies to the lines it shows.
func refLineText(line string) string {
	if len(line) > maxGrepLineBytes {
		return line[:utf8Cut(line, maxGrepLineBytes)] + "…"
	}
	return line
}

// identIn reports whether ids holds ident.
func identIn(ids []*ast.Ident, ident *ast.Ident) bool {
	for _, id := range ids {
		if id == ident {
			return true
		}
	}
	return false
}

// goImportNames collects the package names a file's imports bind: the explicit
// alias when there is one, otherwise the last element of the import path (a
// major-version element like /v2 dropped, since the package under it is not
// called "v2"). Blank and dot imports bind no name. The last element is an
// approximation: a package whose name differs from its directory (gopkg.in/yaml.v3
// is package yaml) is not detected as a package qualifier.
func goImportNames(f *ast.File) map[string]bool {
	names := make(map[string]bool, len(f.Imports))
	for _, spec := range f.Imports {
		if spec.Name != nil {
			if spec.Name.Name != "_" && spec.Name.Name != "." {
				names[spec.Name.Name] = true
			}
			continue
		}
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			base := path[i+1:]
			if isMajorVersionElement(base) {
				if j := strings.LastIndexByte(path[:i], '/'); j >= 0 {
					path = path[:i]
					base = path[j+1:]
				}
				names[base] = true
				continue
			}
			names[base] = true
			continue
		}
		names[path] = true
	}
	return names
}

// isMajorVersionElement reports whether an import path's last element is a
// major-version marker (v2, v3, ...), not a package name.
func isMajorVersionElement(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
