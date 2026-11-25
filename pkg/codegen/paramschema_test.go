package codegen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGenerate_ParameterSchemaShape checks that channel parameters
// declared in the AsyncAPI 2.x shape, with a schema, generate the same
// code as ones declared the 3.x way, with an enum or an x-aapi-codegen
// pattern on the parameter, and that only the 2.x shape warns.
func TestGenerate_ParameterSchemaShape(t *testing.T) {
	const spec = `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
channels:
  orders:
    address: 'orders.{region}.{shard}'
    parameters:
%PARAMETERS%    bindings:
      amqp: { is: routingKey, exchange: { name: Orders, type: direct }, bindingVersion: 0.3.0 }
    messages:
      Order:
        payload: { title: Order, type: object, properties: { id: { type: string } } }
operations:
  sendOrder:
    action: send
    channel: { $ref: '#/channels/orders' }
    messages: [{ $ref: '#/channels/orders/messages/Order' }]
`
	generate := func(t *testing.T, parameters string) (string, []string) {
		t.Helper()
		dir := t.TempDir()
		specPath := filepath.Join(dir, "spec.asyncapi.yaml")
		if err := os.WriteFile(specPath, []byte(strings.ReplaceAll(spec, "%PARAMETERS%", parameters)), 0o600); err != nil {
			t.Fatal(err)
		}
		var warnings []string
		out := filepath.Join(dir, "out.go")
		cfg := Config{Package: "x", Output: out, Warn: func(msg string) { warnings = append(warnings, msg) }}
		if _, err := Generate(specPath, cfg); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		src, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return string(src), warnings
	}

	v3, v3Warnings := generate(t, `      region: { enum: [eu, us] }
      shard: { x-aapi-codegen: { pattern: '^s-[0-9]+$' } }
`)
	v2, v2Warnings := generate(t, `      region: { schema: { type: string, enum: [eu, us] } }
      shard: { schema: { type: string, pattern: '^s-[0-9]+$' } }
`)

	for _, want := range []string{"type Region string", `RegionEu Region = "eu"`, "type Shard string", "func NewShard(v string) (Shard, error)", "region Region,", "shard Shard,"} {
		if !strings.Contains(v3, want) {
			t.Errorf("the 3.x shape's code lacks %q\n--- generated ---\n%s", want, v3)
		}
	}
	if v2 != v3 {
		t.Errorf("the 2.x shape generates other code than the 3.x shape\n--- 2.x ---\n%s\n--- 3.x ---\n%s", v2, v3)
	}
	if len(v3Warnings) != 0 {
		t.Errorf("the 3.x shape warns: %q", v3Warnings)
	}
	const why = ": schema is the AsyncAPI 2.x form, which a 3.x document can't have; declare enum on the parameter, or x-aapi-codegen.pattern for a pattern"
	if want := []string{"channels.orders.parameters.region" + why, "channels.orders.parameters.shard" + why}; !slices.Equal(v2Warnings, want) {
		t.Errorf("the 2.x shape's warnings = %q, want %q", v2Warnings, want)
	}
}
