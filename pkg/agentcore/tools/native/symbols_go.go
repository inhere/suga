package native

import (
	"go/ast"
	"go/parser"
	"go/token"
)

// goSymbolLanguage is Go: it gets the one exact extractor, go/parser.
var goSymbolLanguage = symbolLanguage{
	name:          "Go",
	exts:          []string{".go"},
	source:        sourceGoParser,
	ctagsLanguage: map[string]string{".go": "Go"},
	extract:       goSymbols,
}

// goSymbols parses one Go file's declarations with go/parser: functions and
// methods (kind func/method, so a receiver is visible), types (type) and the
// names of const and var declarations. Only the declarations are read
// (parser.SkipObjectResolution, no type checking, no imported package), so it
// stays fast and cannot fail on a missing dependency. The signature is the
// declaration's own line: for a func that is its name, receiver, parameters and
// result, for a name inside a const block just that line.
func goSymbols(file string, src []byte) ([]Symbol, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	lines := newSourceLines(src)
	var out []Symbol
	add := func(pos token.Pos, kind, name string) {
		line := fset.Position(pos).Line
		out = append(out, Symbol{File: file, Line: line, Kind: kind, Name: name, Signature: lines.at(line)})
	}
	for _, decl := range parsed.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := SymbolFunc
			if d.Recv != nil {
				kind = SymbolMethod
			}
			add(d.Pos(), kind, d.Name.Name)
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						add(ts.Pos(), SymbolType, ts.Name.Name)
					}
				}
			case token.CONST, token.VAR:
				kind := SymbolConst
				if d.Tok == token.VAR {
					kind = SymbolVar
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					// One line per name: a grouped block declares several.
					for _, name := range vs.Names {
						add(name.Pos(), kind, name.Name)
					}
				}
			}
		}
	}
	return out, nil
}
