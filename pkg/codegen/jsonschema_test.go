package codegen

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestGenerate_SizedIntegerFormats guards go-jsonschema's mapping of
// integer `format: int32` / `format: int64` to int32 / int64. From
// go-jsonschema v0.25.0-rc.7 that mapping becomes opt-in
// (generator.Config.OpenAPIIntFormats, default off), and without it these
// fields would silently become int. When a go-jsonschema bump makes this
// test fail, set OpenAPIIntFormats in runJSONSchemaInProcess.
func TestGenerate_SizedIntegerFormats(t *testing.T) {
	dir := t.TempDir()
	spec := `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
channels:
  counters:
    address: counters
    bindings:
      amqp:
        is: routingKey
        exchange: { name: Counters, type: direct }
        bindingVersion: 0.3.0
    messages:
      Counter:
        payload:
          title: Counter
          type: object
          properties:
            plain: { type: integer }
            small: { type: integer, format: int32 }
            large: { type: integer, format: int64 }
operations:
  sendCounter:
    action: send
    channel: { $ref: '#/channels/counters' }
    messages:
      - $ref: '#/channels/counters/messages/Counter'
`
	specPath := filepath.Join(dir, "spec.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.go")
	if _, err := Generate(specPath, Config{Package: "x", Output: out}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	src, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for field, goType := range map[string]string{"Plain": "int", "Small": "int32", "Large": "int64"} {
		re := regexp.MustCompile(`(?m)^\s+` + field + `\s+\*?` + goType + `\s`)
		if !re.Match(src) {
			t.Errorf("field %s should be %s (or *%s); go-jsonschema's integer-format mapping changed\n--- generated ---\n%s", field, goType, goType, src)
		}
	}
}

// TestGenerate_NullableComponentRef guards how a nullable reference to a
// components schema is generated: a `oneOf` or `anyOf` of the `$ref` and
// `{type: null}` becomes a pointer to the referenced type. aapi-codegen
// writes each components schema to a file of its own, so go-jsonschema sees
// a `$ref` into another file. Before go-jsonschema v0.25.0-rc.9 the `oneOf`
// form fell back to interface{}.
func TestGenerate_NullableComponentRef(t *testing.T) {
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
          $ref: '#/components/schemas/OrderPlaced'
operations:
  sendOrderPlaced:
    action: send
    channel: { $ref: '#/channels/orders' }
    messages:
      - $ref: '#/channels/orders/messages/OrderPlaced'
components:
  schemas:
    OrderPlaced:
      title: OrderPlaced
      type: object
      properties:
        shipping:
          oneOf:
            - $ref: '#/components/schemas/Address'
            - type: 'null'
        billing:
          anyOf:
            - $ref: '#/components/schemas/Address'
            - type: 'null'
    Address:
      title: Address
      type: object
      properties:
        city: { type: string }
`
	specPath := filepath.Join(dir, "spec.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.go")
	if _, err := Generate(specPath, Config{Package: "x", Output: out}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	src, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Shipping", "Billing"} {
		re := regexp.MustCompile(`(?m)^\s+` + field + `\s+\*Address\s`)
		if !re.Match(src) {
			t.Errorf("field %s should be *Address; go-jsonschema's handling of a nullable $ref changed\n--- generated ---\n%s", field, src)
		}
	}
}
