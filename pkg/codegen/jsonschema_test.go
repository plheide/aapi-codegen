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
