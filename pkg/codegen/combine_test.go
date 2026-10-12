package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerate_KeepsCommentsAndHeader guards the generated file's comments:
// aapi-codegen's own header first, schema descriptions on payload types and
// fields, the templates' doc comments, and nothing stranded inside the
// import block. An earlier import rewrite kept only the first comment of the
// file, so every other doc comment was silently dropped.
func TestGenerate_KeepsCommentsAndHeader(t *testing.T) {
	dir := t.TempDir()
	spec := `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
channels:
  orders:
    address: orders
    bindings:
      amqp:
        is: routingKey
        exchange: { name: Orders, type: direct }
        bindingVersion: 0.3.0
    messages:
      OrderPlaced:
        payload:
          title: OrderPlaced
          description: An order a customer has just placed.
          type: object
          required: [id]
          properties:
            id:
              type: string
              description: Identifier of the order.
operations:
  sendOrderPlaced:
    action: send
    channel: { $ref: '#/channels/orders' }
    messages:
      - $ref: '#/channels/orders/messages/OrderPlaced'
`
	specPath := filepath.Join(dir, "spec.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.go")
	if _, err := Generate(specPath, Config{Package: "x", Output: out}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srcBytes, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)

	if first, _, _ := strings.Cut(src, "\n"); first != generatedHeader {
		t.Errorf("first line = %q, want %q", first, generatedHeader)
	}
	if strings.Contains(src, "go-jsonschema, DO NOT EDIT") {
		t.Errorf("go-jsonschema's header survived; the file should carry only aapi-codegen's\n--- generated ---\n%s", src)
	}
	for _, want := range []string{
		"// An order a customer has just placed.",          // schema description on the type
		"// Identifier of the order.",                      // schema description on a field
		"// Transport is the minimal AMQP publish surface", // template doc comment
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file lost comment %q\n--- generated ---\n%s", want, src)
		}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "out.go", srcBytes, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse generated file: %v", err)
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, cg := range f.Comments {
			if cg.Pos() > gd.Pos() && cg.End() < gd.End() {
				t.Errorf("comment %q ended up inside the import block", cg.Text())
			}
		}
	}
}
