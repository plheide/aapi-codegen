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
	// Operations is the ordered list of operations to emit (both
	// `action: send` and `action: receive` from v0.2).
	Operations []*Operation
	// ParameterEnums collects every distinct typed-enum parameter the
	// templates need to render as a Go enum type + constants. Populated
	// by the lowerer from channels.X.parameters.Y.schema when type
	// is string and an `enum: [...]` list is declared. v0.4+.
	ParameterEnums []*ParameterEnum
	// ParameterPatterns collects every distinct pattern-validated
	// parameter the templates need to render as a typed-wrapper +
	// New<T>/Must<T> constructor pair. Populated by the lowerer from
	// channels.X.parameters.Y.schema when type is string and a
	// `pattern` is declared (and the parameter doesn't already have an
	// enum — enum wins the closed-set check). v0.4.1+.
	ParameterPatterns []*ParameterPattern
}

// ParameterEnum captures one typed-enum channel parameter — what the
// publisher / subscriber template needs to emit a Go enum (named type +
// const block). Two distinct channel parameters that resolve to the
// same GoTypeName + Values get deduped by the lowerer; collisions on
// the GoTypeName with mismatching values raise an error.
type ParameterEnum struct {
	// GoTypeName is the exported Go type name (derived from the
	// parameter key, pascalized: jobType → JobType).
	GoTypeName string
	// Values is the ordered list of valid string values; the const
	// names are `<GoTypeName><Pascalize(value)>`, e.g. JobTypeBuild.
	Values []string
}

// ParameterPattern captures one pattern-validated channel parameter —
// what the publisher / subscriber template needs to emit a typed
// wrapper (`type X string`) plus New<T>/Must<T> constructors that
// regex-check the input before returning. Dedup + collision rules
// mirror ParameterEnum: same GoTypeName must declare the same Pattern.
// Suppressed (parameter falls back to plain string) when the spec sets
// `x-aapi-codegen.omit-validation: true`.
type ParameterPattern struct {
	// GoTypeName is the exported Go type name (derived from the
	// parameter key, pascalized: dataPartitionID → DataPartitionID).
	GoTypeName string
	// Pattern is the spec-declared regex, in the syntax the Go stdlib
	// `regexp` package accepts (RE2). Spec authors who use
	// non-RE2 features (lookaround, backreferences) get a clear
	// `regexp.MustCompile` failure at generated-package load time —
	// catchable in the unit test that imports the package.
	Pattern string
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
	// PublishDefaults carries AMQP message + operation binding values
	// the publisher template wires into the default PublishProperties.
	// Nil when the operation has no relevant bindings; the generated
	// Send method still accepts SendOption opts but starts from a
	// zero-value PublishProperties. v0.3+.
	PublishDefaults *PublishDefaults
}

// PublishDefaults holds the AMQP message-level + operation-level binding
// fields the publisher template materialises as default
// PublishProperties values. Each field is a pointer so the template can
// distinguish "spec didn't declare this" (nil → don't emit a default)
// from "spec declared zero" (non-nil → emit zero).
type PublishDefaults struct {
	// ContentType from defaultContentType or messages.X.contentType.
	// Always set when at least one of the spec sources declares it.
	ContentType string
	// ContentEncoding from messages.X.bindings.amqp.contentEncoding.
	ContentEncoding string
	// MessageType from messages.X.bindings.amqp.messageType.
	MessageType string
	// Priority from operations.X.bindings.amqp.priority (0-9 in AMQP).
	Priority *uint8
	// Expiration from operations.X.bindings.amqp.expiration. Per the
	// AMQP wire format this is a string-encoded number of milliseconds;
	// the IR keeps it as a string to preserve the spec author's exact
	// declaration.
	Expiration string
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

// Message is the resolved payload type the operation publishes/consumes.
//
// Two flavours, distinguished by ImportedPackage:
//   - Local: GoTypeName names a type declared in the generated package
//     (default — the spec defines the payload inline or via a $ref the
//     lowerer materializes).
//   - Imported (v0.5+): the message is a cross-file $ref into another
//     spec's message; the producer's already-generated package supplies
//     the Go type. ImportedPackage is the producer's Go import path,
//     ImportedAlias is the local import alias, GoTypeName is the type
//     name in that package. Generated code references
//     `<ImportedAlias>.<GoTypeName>`.
type Message struct {
	// Name is the AsyncAPI message key, e.g. "WidgetMessage".
	Name string
	// GoTypeName is the exported Go struct type the payload generates
	// to (matches the JSON Schema's `title`, since
	// --struct-name-from-title is on, for local messages; the message
	// name for imported messages).
	GoTypeName string
	// ImportedPackage and ImportedAlias are set when the message was
	// resolved via x-aapi-codegen.message-packages (cross-file $ref).
	// Empty for local messages. v0.5+.
	ImportedPackage string
	ImportedAlias   string
}

// QualifiedGoType returns the type name as the templates should emit it.
// For local messages this is just GoTypeName. For imported messages it
// is the alias.TypeName form. Centralising the rendering here keeps
// templates from having to branch on the imported-vs-local distinction.
func (m *Message) QualifiedGoType() string {
	if m.ImportedAlias != "" {
		return m.ImportedAlias + "." + m.GoTypeName
	}
	return m.GoTypeName
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
