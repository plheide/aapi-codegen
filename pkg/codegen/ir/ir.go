// Package ir is the binding-agnostic intermediate representation that
// sits between the AsyncAPI loader and the Go-source templates. The
// loader produces a *loader.Document that mirrors the YAML shape; the
// lowerer turns that into an *ir.Spec the templates iterate over.
//
// Keeping the IR binding-agnostic at the top level (channels and
// operations) and per-binding only at the leaves (Channel.Binding) lets
// future bindings (Kafka, WebSocket) slot in without rewriting either
// the templates or the lowerer's spine. v1 ships only AMQP — see amqp.go.
package ir

// Spec is the root of the IR. One *Spec per generated Go package.
type Spec struct {
	// PackageName is the Go package name the templates emit.
	PackageName string
	// DocTitle is doc.Info.Title, used in publisher comments.
	DocTitle string
	// Operations is the ordered list of operations to emit (only
	// `action: send` operations in v1; `action: receive` is M3+).
	Operations []*Operation
}

// Action is the AsyncAPI operation.action value. Determines which
// template emits the operation: send → publisher, receive → subscriber.
type Action string

const (
	// ActionSend marks a publish operation. The publisher template emits
	// a Send<MessageName> method.
	ActionSend Action = "send"
	// ActionReceive marks a consume operation. The subscriber template
	// emits a <MessageName>Handler interface and a Subscribe<MessageName>
	// method (v0.2+).
	ActionReceive Action = "receive"
)

// Operation is one published or consumed operation. Maps 1:1 to
// AsyncAPI `operations.X`. Action distinguishes publisher vs subscriber
// emission; everything else is shared.
type Operation struct {
	// Name is the AsyncAPI operation key, e.g. "sendWidgetMessage" or
	// "consumeWidgetMessage".
	Name string
	// Action is the operation's AsyncAPI action (send or receive).
	Action Action
	// GoFuncName is the exported Go method name. For send: "SendWidgetMessage"
	// (on Publisher). For receive: "SubscribeWidgetMessage" (on Subscriber).
	GoFuncName string
	// HandlerTypeName is the interface name the subscriber generates and
	// the consumer code implements. Only set for ActionReceive; empty for
	// ActionSend. Form: <MessageName>Handler, e.g. "WidgetMessageHandler".
	HandlerTypeName string
	// HandlerMethodName is the single method on HandlerTypeName the
	// consumer implements. Only set for ActionReceive. Form:
	// Handle<MessageName>, e.g. "HandleWidgetMessage".
	HandlerMethodName string
	// Channel is the resolved channel this operation targets.
	Channel *Channel
	// Message is the resolved payload type.
	Message *Message
}

// Channel is the AsyncAPI channel an operation publishes to.
type Channel struct {
	// Name is the AsyncAPI channel key, e.g. "widgetDispatch".
	Name string
	// Address is the parsed channel-address template.
	Address Address
	// Binding is the protocol-specific binding metadata. Concrete type
	// is *AMQPBinding for v1; future bindings extend this interface.
	Binding Binding
}

// Message is the resolved payload type the operation publishes.
type Message struct {
	// Name is the AsyncAPI message key, e.g. "WidgetMessage".
	Name string
	// GoTypeName is the exported Go struct type the payload generates
	// to (matches the JSON Schema's `title`, since
	// --struct-name-from-title is on).
	GoTypeName string
}

// Address is a parsed channel-address template. Templated parameters
// become Param parts; everything else is Literal.
type Address struct {
	// Raw is the original address string from the spec, used in
	// generated-code comments so the reader can correlate to the YAML.
	Raw string
	// Parts is the address split on `{name}` boundaries, in order.
	Parts []AddressPart
	// Params is Parts filtered to just the parameter parts, in
	// declaration order — the order publisher arguments appear in.
	Params []AddressParam
}

// AddressPart is one segment of an address template. Either
// AddressLiteral or AddressParam — never both. (No interface to keep the
// template logic trivially branchable.)
type AddressPart struct {
	// Literal is the raw text segment when this part is a literal,
	// empty when it's a parameter.
	Literal string
	// Param is the parameter name when this part is a parameter,
	// empty when it's a literal.
	Param string
}

// IsParam reports whether the part is a parameter reference.
func (p AddressPart) IsParam() bool { return p.Param != "" }

// AddressParam is a typed view of one channel-address parameter as it
// surfaces on the generated Publisher method signature. Today every
// parameter is `string`; future work can derive types from the
// AsyncAPI parameter schema.
type AddressParam struct {
	// JSONName is the parameter name as it appears in the spec
	// address template, e.g. "tenant".
	JSONName string
	// GoArgName is the Go function-argument name the publisher emits.
	// Equal to JSONName today; reserved for future name-mangling
	// (reserved-keyword escaping etc.).
	GoArgName string
	// GoType is the Go type emitted in the function signature; "string"
	// for v1.
	GoType string
}

// Binding is the marker interface for protocol-specific channel
// metadata. v1 has one implementation: *AMQPBinding (see amqp.go).
type Binding interface {
	// Kind returns the binding's protocol identifier ("amqp", future:
	// "kafka", "ws"). Templates dispatch on this when more than one
	// binding ships.
	Kind() string
}
