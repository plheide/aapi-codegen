package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
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

// checkPayloadTypesDeclared rejects generated source that lacks the Go type
// of a local message's payload, where a rendered publisher or subscriber
// uses it. The type name is the payload schema's title, but for a schema
// whose root is a $ref, go-jsonschema generates the type of the schema the
// $ref points at, so a title that names neither leaves the type undeclared.
// Without this check the generator would write code that doesn't compile.
func checkPayloadTypesDeclared(src []byte, spec *ir.Spec, publishers, subscribers bool) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", src, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parse generated source: %w", err)
	}
	declared := make(map[string]bool)
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.GenDecl); ok {
			for _, s := range d.Specs {
				if ts, ok := s.(*ast.TypeSpec); ok {
					declared[ts.Name.Name] = true
				}
			}
		}
	}
	for _, op := range spec.Operations {
		m := op.Message
		if m == nil || m.ImportedAlias != "" || declared[m.GoTypeName] {
			continue
		}
		if (op.Action == ir.ActionSend && !publishers) || (op.Action == ir.ActionReceive && !subscribers) {
			continue
		}
		if m.PayloadIsRef {
			return fmt.Errorf("message %q: its payload schema %s is a $ref to another schema, so go-jsonschema generates that schema's type and no type %s; point the payload's $ref at the referenced schema instead", m.Name, m.GoTypeName, m.GoTypeName)
		}
		return fmt.Errorf("message %q: the generated code has no type %s for its payload", m.Name, m.GoTypeName)
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
