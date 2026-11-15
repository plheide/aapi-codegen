package codegen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// payloadRefSpec has one send operation whose message payload is %PAYLOAD%,
// next to an Order schema in components and in order.schema.json.
const payloadRefSpec = `asyncapi: 3.1.0
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
%PAYLOAD%operations:
  sendOrderPlaced:
    action: send
    channel: { $ref: '#/channels/orders' }
    messages:
      - $ref: '#/channels/orders/messages/OrderPlaced'
%EXTENSION%components:
  schemas:
    Order:
      type: object
      properties:
        id: { type: string }
    OrderAlias:
      $ref: '#/components/schemas/Order'
`

// generatePayloadRefSpec writes payloadRefSpec with the given payload and
// x-aapi-codegen lines, plus the schema files, and runs Generate on it.
func generatePayloadRefSpec(t *testing.T, payload, extension string, files map[string]string) ([]string, error) {
	t.Helper()
	dir := t.TempDir()
	files["order.schema.json"] = `{"title": "FileOrder", "type": "object", "properties": {"id": {"type": "string"}}}`
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := strings.NewReplacer("%PAYLOAD%", payload, "%EXTENSION%", extension).Replace(payloadRefSpec)
	specPath := filepath.Join(dir, "spec.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	cfg := Config{Package: "x", Output: filepath.Join(dir, "out.go"), Warn: func(msg string) { warnings = append(warnings, msg) }}
	_, err := Generate(specPath, cfg)
	return warnings, err
}

// TestGenerate_WarnsAboutKeywordsBesidePayloadRef guards the warning for
// keywords beside a message payload's own $ref, which aapi-codegen resolves
// itself, so go-jsonschema never sees them.
func TestGenerate_WarnsAboutKeywordsBesidePayloadRef(t *testing.T) {
	const where = "schema fidelity: channels.orders.messages.OrderPlaced.payload does not enforce all it declares: "
	for name, c := range map[string]struct {
		payload string
		want    []string
	}{
		"keywords that constrain": {
			payload: "          $ref: '#/components/schemas/Order'\n          required: [id]\n          minProperties: 1\n",
			want:    []string{where + "minProperties, required beside $ref not checked"},
		},
		"annotations and the type the target declares": {
			payload: "          $ref: '#/components/schemas/Order'\n          description: An order.\n          x-owner: orders-team\n          type: object\n",
		},
		"a type the target doesn't declare": {
			payload: "          $ref: '#/components/schemas/Order'\n          type: string\n",
			want:    []string{where + "type beside $ref not checked"},
		},
		"the type a schema file declares": {
			payload: "          $ref: './order.schema.json'\n          type: object\n",
		},
		"a $ref alone": {
			payload: "          $ref: '#/components/schemas/Order'\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := generatePayloadRefSpec(t, c.payload, "", map[string]string{})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("warnings = %q, want %q", got, c.want)
			}
		})
	}
}

// TestGenerate_WarnsOnceAboutASharedMessage checks that a components
// message that several channels use is reported once, at its component.
func TestGenerate_WarnsOnceAboutASharedMessage(t *testing.T) {
	const spec = `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
channels:
  placed:
    address: orders.placed
    bindings:
      amqp: { is: routingKey, exchange: { name: Orders, type: direct }, bindingVersion: 0.3.0 }
    messages:
      Order: { $ref: '#/components/messages/Order' }
  amended:
    address: orders.amended
    bindings:
      amqp: { is: routingKey, exchange: { name: Orders, type: direct }, bindingVersion: 0.3.0 }
    messages:
      Order: { $ref: '#/components/messages/Order' }
operations:
  sendPlaced:
    action: send
    channel: { $ref: '#/channels/placed' }
    messages: [{ $ref: '#/channels/placed/messages/Order' }]
  sendAmended:
    action: send
    channel: { $ref: '#/channels/amended' }
    messages: [{ $ref: '#/channels/amended/messages/Order' }]
components:
  messages:
    Order:
      payload:
        $ref: '#/components/schemas/Order'
        required: [id]
  schemas:
    Order:
      type: object
      properties:
        id: { type: string }
`
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	cfg := Config{Package: "x", Output: filepath.Join(dir, "out.go"), Warn: func(msg string) { got = append(got, msg) }}
	if _, err := Generate(specPath, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := []string{"schema fidelity: components.messages.Order.payload does not enforce all it declares: required beside $ref not checked"}
	if !slices.Equal(got, want) {
		t.Errorf("warnings = %q, want %q", got, want)
	}
}
