package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// receiveChannel is a fixed-address routing-key channel whose consumer
// reads from queue, carrying one message under msgKey.
func receiveChannel(address, queue, msgKey, msgYAML string) string {
	return `
    address: ` + address + `
    bindings:
      amqp:
        is: routingKey
        exchange: { name: Events, type: direct }
        bindingVersion: 0.3.0
      x-aapi-codegen:
        queue: { name: ` + queue + ` }
    messages:
      ` + msgKey + `: ` + msgYAML
}

func receiveOp(channel, msgKey string) string {
	return `
    action: receive
    channel: { $ref: '#/channels/` + channel + `' }
    messages:
      - $ref: '#/channels/` + channel + `/messages/` + msgKey + `'`
}

// generateSpec writes spec into a temp dir (plus any extra files) and runs
// Generate on it.
func generateSpec(t *testing.T, spec string, extra map[string]string) error {
	t.Helper()
	dir := t.TempDir()
	for name, content := range extra {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	specPath := filepath.Join(dir, "spec.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Generate(specPath, Config{Package: "x", Output: filepath.Join(dir, "out.go")})
	return err
}

const statusComponent = `
components:
  messages:
    Status:
      payload:
        title: Status
        type: object
        properties:
          id: { type: string }
`

func TestGenerate_SameMessageOnSeparateQueues(t *testing.T) {
	spec := `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
channels:
  alpha:` + receiveChannel("key-alpha", "q1", "Status", `{ $ref: '#/components/messages/Status' }`) + `
  beta:` + receiveChannel("key-beta", "q2", "Status", `{ $ref: '#/components/messages/Status' }`) + `
operations:
  consumeAlpha:` + receiveOp("alpha", "Status") + `
  consumeBeta:` + receiveOp("beta", "Status") + statusComponent

	err := generateSpec(t, spec, nil)
	if err == nil {
		t.Fatal("Generate succeeded; want an error for one message type received from two separate queues")
	}
	for _, want := range []string{`"consumeAlpha" and "consumeBeta"`, "separate queues", "SubscribeStatus", "one shared queue"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestGenerate_QueueNameParameterNotInAddress covers queue names whose
// parameters the generated Subscribe method can't fill in: the method takes
// the address's parameters, so the queue name may use only those.
func TestGenerate_QueueNameParameterNotInAddress(t *testing.T) {
	const tenantAddress = `
    address: '{tenant}.orders.created'
    parameters:
      tenant: { description: Tenant id. }`
	cases := map[string]struct {
		channel string // the channel's address and parameters
		queue   string
		wantSub string
	}{
		"parameter the address lacks": {
			channel: tenantAddress,
			queue:   `'{region}.audit-log'`,
			wantSub: `queue name "{region}.audit-log": parameter "region" is not in address "{tenant}.orders.created"`,
		},
		"fixed address": {
			channel: `
    address: orders.created`,
			queue:   `'{tenant}.audit-log'`,
			wantSub: `queue name "{tenant}.audit-log": parameter "tenant" is not in address "orders.created"`,
		},
		"unterminated brace": {
			channel: tenantAddress,
			queue:   `'{tenant.audit-log'`,
			wantSub: `queue name "{tenant.audit-log": unterminated`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			spec := `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
channels:
  orders:` + c.channel + `
    bindings:
      amqp:
        is: routingKey
        exchange: { name: Events, type: direct }
        bindingVersion: 0.3.0
      x-aapi-codegen:
        queue: { name: ` + c.queue + ` }
    messages:
      Status: { $ref: '#/components/messages/Status' }
operations:
  consumeStatus:` + receiveOp("orders", "Status") + statusComponent

			err := generateSpec(t, spec, nil)
			if err == nil {
				t.Fatal("Generate succeeded; want an error for the queue name")
			}
			for _, want := range []string{`operation "consumeStatus"`, c.wantSub} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestGenerate_SameHandlerNameDifferentTypes(t *testing.T) {
	// OrderMessage imported from two producer packages maps to one
	// handler name, OrderMessageHandler, with two parameter types.
	header := `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
x-aapi-codegen:
  omit-publishers: true
  message-packages:
    - { file: producer-a.yaml, package: example.com/a, alias: a }
    - { file: producer-b.yaml, package: example.com/b, alias: b }
channels:
  fromA:` + receiveChannel("orders.a", "%QA%", "OrderMessage", `{ $ref: 'producer-a.yaml#/channels/orders/messages/OrderMessage' }`) + `
  fromB:` + receiveChannel("orders.b", "%QB%", "OrderMessage", `{ $ref: 'producer-b.yaml#/channels/orders/messages/OrderMessage' }`) + `
operations:
  consumeA:` + receiveOp("fromA", "OrderMessage") + `
  consumeB:` + receiveOp("fromB", "OrderMessage") + "\n"
	producers := map[string]string{
		"producer-a.yaml": "asyncapi: 3.1.0\n",
		"producer-b.yaml": "asyncapi: 3.1.0\n",
	}
	for name, queues := range map[string][2]string{
		"separate queues": {"qa", "qb"},
		"shared queue":    {"orders", "orders"},
	} {
		t.Run(name, func(t *testing.T) {
			spec := strings.NewReplacer("%QA%", queues[0], "%QB%", queues[1]).Replace(header)
			err := generateSpec(t, spec, producers)
			if err == nil {
				t.Fatal("Generate succeeded; want an error for two message types mapping to one handler name")
			}
			for _, want := range []string{"a.OrderMessage", "b.OrderMessage", "OrderMessageHandler"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestGenerate_PayloadNamedLikeGeneratedIdentifier(t *testing.T) {
	// A payload titled Subscriber collides with the generated Subscriber
	// struct; the declaration check reports it instead of writing a file
	// that doesn't compile.
	spec := `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
channels:
  alpha:` + receiveChannel("key-alpha", "q1", "Subscriber", `
        payload:
          title: Subscriber
          type: object
          properties:
            id: { type: string }`) + `
operations:
  consumeAlpha:` + receiveOp("alpha", "Subscriber") + "\n"

	err := generateSpec(t, spec, nil)
	if err == nil {
		t.Fatal("Generate succeeded; want an error for a payload named like a generated identifier")
	}
	if !strings.Contains(err.Error(), "Subscriber") || !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error %q should name Subscriber as declared more than once", err)
	}
}
