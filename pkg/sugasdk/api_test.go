package sugasdk

// The API guard: the exported surface of this package never names an internal
// package, so an embedding host compiles against public types only.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestExportedAPINamesNoInternal(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	assert.Require(t, assert.NoErr(t, err))

	texts := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		assert.Require(t, assert.NoErr(t, err))
		collectExportedAPI(fset, file, texts)
	}
	assert.Require(t, assert.True(t, len(texts) > 10), "the walk found the exported declarations")
	// The types the SDK is about are part of the surface the walk reads.
	for _, name := range []string{"Engine", "Session", "EngineOptions", "SessionOptions"} {
		assert.True(t, hasDeclaredName(texts, name), "%s must be exported", name)
	}
	for name, text := range texts {
		assert.False(t, strings.Contains(text, "internal/"), "%s names an internal package: %s", name, text)
		for _, pkg := range []string{"wiring.", "personal."} {
			assert.False(t, strings.Contains(text, pkg), "%s names %s: %s", name, pkg, text)
		}
	}
}

// hasDeclaredName reports whether name was recorded, either as its own
// declaration or as a field/method of it.
func hasDeclaredName(texts map[string]string, name string) bool {
	if _, ok := texts[name]; ok {
		return true
	}
	for key := range texts {
		if strings.HasPrefix(key, name+".") {
			return true
		}
	}
	return false
}

// collectExportedAPI records the rendered type of every exported declaration of
// file: funcs and methods by signature, types by their type expression (a
// struct by the types of its exported fields, an interface by its methods).
// Function bodies and unexported fields stay out, so the check sees only what a
// host can name.
func collectExportedAPI(fset *token.FileSet, file *ast.File, out map[string]string) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			collectFunc(fset, d, out)
		case *ast.GenDecl:
			collectGenDecl(fset, d, out)
		}
	}
}

// collectFunc records an exported function or an exported method.
func collectFunc(fset *token.FileSet, d *ast.FuncDecl, out map[string]string) {
	if d.Recv == nil {
		if d.Name.IsExported() {
			out[d.Name.Name] = render(fset, d.Type)
		}
		return
	}
	if !d.Name.IsExported() || len(d.Recv.List) == 0 {
		return
	}
	recv := exportedName(d.Recv.List[0].Type)
	if recv == "" {
		return
	}
	out[recv+"."+d.Name.Name] = render(fset, d.Type)
}

// collectGenDecl records the exported types and values of one declaration group.
func collectGenDecl(fset *token.FileSet, d *ast.GenDecl, out map[string]string) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if s.Name.IsExported() {
				collectType(fset, s, out)
			}
		case *ast.ValueSpec:
			for _, name := range s.Names {
				if name.IsExported() {
					out[name.Name] = render(fset, s.Type)
				}
			}
		}
	}
}

// collectType records one exported type: a struct by its exported fields, an
// interface by its methods, anything else by its whole type expression.
func collectType(fset *token.FileSet, s *ast.TypeSpec, out map[string]string) {
	switch t := s.Type.(type) {
	case *ast.StructType:
		recorded := 0
		for _, field := range t.Fields.List {
			if !exportedField(field) {
				continue
			}
			out[s.Name.Name+"."+fieldName(field)] = render(fset, field.Type)
			recorded++
		}
		// A struct with no exported field is still part of the API (through its
		// methods); the marker keeps its name in the walk.
		if recorded == 0 {
			out[s.Name.Name] = "struct{}"
		}
	case *ast.InterfaceType:
		for _, method := range t.Methods.List {
			if !exportedField(method) {
				continue
			}
			out[s.Name.Name+"."+fieldName(method)] = render(fset, method.Type)
		}
	default:
		out[s.Name.Name] = render(fset, s.Type)
	}
}

// exportedField reports whether a field or method is part of the public API.
func exportedField(field *ast.Field) bool {
	if len(field.Names) == 0 {
		return exportedName(field.Type) != ""
	}
	for _, name := range field.Names {
		if name.IsExported() {
			return true
		}
	}
	return false
}

// fieldName is the name recorded for a field or method.
func fieldName(field *ast.Field) string {
	if len(field.Names) == 0 {
		return exportedName(field.Type)
	}
	return field.Names[0].Name
}

// exportedName returns the exported name of a type expression, or "".
func exportedName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return exportedName(t.X)
	case *ast.Ident:
		if t.IsExported() {
			return t.Name
		}
	case *ast.SelectorExpr:
		if t.Sel.IsExported() {
			return t.Sel.Name
		}
	}
	return ""
}

// render prints one syntax node as source text.
func render(fset *token.FileSet, node any) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, node)
	return buf.String()
}
