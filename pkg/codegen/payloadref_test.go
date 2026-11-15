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

// TestGenerate_PayloadSchemaThatIsARef guards the error for a payload schema
// whose root is a $ref: go-jsonschema generates the type of the schema it
// points at, so a type named after the payload schema's title would be
// declared nowhere, and the generated code wouldn't compile.
func TestGenerate_PayloadSchemaThatIsARef(t *testing.T) {
	const fix = "point the payload's $ref at the referenced schema instead"
	for name, c := range map[string]struct {
		payload, extension string
		files              map[string]string
		wantErr            string // empty: Generate succeeds
	}{
		"a components schema that is a $ref": {
			payload: "          $ref: '#/components/schemas/OrderAlias'\n",
			wantErr: `message "OrderPlaced": its payload schema OrderAlias is a $ref to another schema`,
		},
		"a schema file whose root is a $ref": {
			payload: "          $ref: './alias.schema.json'\n",
			files:   map[string]string{"alias.schema.json": `{"title": "AliasOrder", "$ref": "./order.schema.json"}`},
			wantErr: `message "OrderPlaced": its payload schema AliasOrder is a $ref to another schema`,
		},
		// The type named after the title is the one generated, so the code
		// compiles.
		"a schema file whose root is a $ref to a schema of its title": {
			payload: "          $ref: './alias.schema.json'\n",
			files:   map[string]string{"alias.schema.json": `{"title": "FileOrder", "$ref": "./order.schema.json"}`},
		},
		// Without a publisher or subscriber, nothing uses the type.
		"a components schema that is a $ref, with no publisher": {
			payload:   "          $ref: '#/components/schemas/OrderAlias'\n",
			extension: "x-aapi-codegen:\n  omit-publishers: true\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			files := c.files
			if files == nil {
				files = map[string]string{}
			}
			_, err := generatePayloadRefSpec(t, c.payload, c.extension, files)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Generate succeeded, want an error containing %q", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) || !strings.Contains(err.Error(), fix) {
				t.Errorf("error %q should contain %q and %q", err, c.wantErr, fix)
			}
		})
	}
}
