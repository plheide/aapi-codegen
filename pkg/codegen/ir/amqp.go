package ir

// AMQPBinding is the typed view of an AsyncAPI channel's `bindings.amqp`
// stanza, restricted to the fields v1's publisher template uses. Today
// that means: which exchange to publish to, what type that exchange is
// (so consumers can read it from the IR if they grow to validate routing
// shape against type), and a routing-key shape flag derived from
// Address.Params.
type AMQPBinding struct {
	// Exchange is the AMQP exchange name from `bindings.amqp.exchange.name`.
	Exchange string
	// ExchangeType is "direct", "topic", "fanout", or "headers". From
	// `bindings.amqp.exchange.type`.
	ExchangeType string
	// BindingVersion mirrors `bindings.amqp.bindingVersion` — preserved
	// so generated-code comments can name the spec version that drove
	// emission. Not used in code generation logic itself.
	BindingVersion string
}

// Kind satisfies Binding.
func (*AMQPBinding) Kind() string { return "amqp" }
