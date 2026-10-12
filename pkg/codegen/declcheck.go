package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// checkDuplicateDeclarations rejects generated source that declares a
// top-level identifier, or a method on the same receiver, more than once.
// Such source doesn't compile, yet without this check the generator would
// write it and exit 0: two spec elements that map to one Go name (say, a
// payload titled like a generated identifier such as Subscriber) surface
// only when the consumer builds the package.
func checkDuplicateDeclarations(src []byte) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", src, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parse generated source: %w", err)
	}
	seen := make(map[string]bool)
	var dups []string
	note := func(name string) {
		if seen[name] {
			dups = append(dups, name)
			return
		}
		seen[name] = true
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == "init" || d.Name.Name == "_" {
				continue
			}
			if d.Recv == nil || len(d.Recv.List) == 0 {
				note(d.Name.Name)
				continue
			}
			note(receiverTypeName(d.Recv.List[0].Type) + "." + d.Name.Name)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					note(s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name != "_" {
							note(n.Name)
						}
					}
				}
			}
		}
	}
	if len(dups) > 0 {
		return fmt.Errorf("generated code would declare %s more than once; two spec elements map to the same Go name", strings.Join(dups, ", "))
	}
	return nil
}

// receiverTypeName returns the base type name of a method receiver: T for
// T, *T, T[K] and *T[K].
func receiverTypeName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return fmt.Sprintf("%T", e)
		}
	}
}
