package ir

// AMQPBinding is the typed view of an AsyncAPI channel's `bindings.amqp`
// stanza. v0.1.x captured only the publisher-relevant fields (exchange);
// v0.2 adds Queue + ChannelKind to drive subscriber emission.
type AMQPBinding struct {
	// ChannelKind is the AsyncAPI `bindings.amqp.is` value. "routingKey"
	// (publisher mode — address is the routing key on Exchange) or
	// "queue" (consumer mode — address is the queue name; the queue is
	// bound to Exchange separately). Empty when `bindings.amqp.is` is
	// not declared (legacy v0.1.x specs imply routingKey for publishers).
	ChannelKind string
	// Exchange is the AMQP exchange name from `bindings.amqp.exchange.name`.
	// Required for publishers; for consumer-mode channels it's the
	// exchange the queue is bound to (informational).
	Exchange string
	// ExchangeType is "direct", "topic", "fanout", or "headers". From
	// `bindings.amqp.exchange.type`.
	ExchangeType string
	// Queue is the consumer-side queue topology when ChannelKind == "queue"
	// or when bindings.amqp.queue is declared. Nil for pure publisher
	// channels (v0.1.x default).
	Queue *AMQPQueue
	// BindingVersion mirrors `bindings.amqp.bindingVersion`.
	BindingVersion string
}

// AMQPQueue mirrors `bindings.amqp.queue` for consumer-mode channels.
// When ChannelKind == "queue", Name is the same template as the channel
// address (the address IS the queue name in AsyncAPI 3.x queue mode);
// the field is preserved here so generated code can reference either.
type AMQPQueue struct {
	// Name is the queue name (may be a parameter template — see
	// Channel.Address.Params for the typed parameter list).
	Name string
	// Durable / AutoDelete / Exclusive mirror the AsyncAPI fields.
	// Defaults are zero-values (false) which matches the AsyncAPI
	// default when the field is omitted.
	Durable    bool
	AutoDelete bool
	Exclusive  bool
}

// Kind satisfies Binding.
func (*AMQPBinding) Kind() string { return "amqp" }
