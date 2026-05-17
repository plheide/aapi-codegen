// Package lower turns a parsed AsyncAPI document (loader.Document) into
// the binding-agnostic IR (ir.Spec) the publisher template iterates
// over. Lowering does the work the templates shouldn't: AsyncAPI-
// internal $ref resolution, address-template parsing, AMQP binding
// extraction, and message-Go-type-name derivation from each payload
// schema's title.
package lower

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
	"github.com/plheide/aapi-codegen/pkg/codegen/loader"
	"github.com/plheide/aapi-codegen/pkg/codegen/schema"
)

// Lower produces the IR spec for one AsyncAPI document. v0.2 lowers
// both `action: send` (publisher) and `action: receive` (subscriber)
// operations. Operation iteration order is alphabetical by operation
// key for deterministic output (Go map iteration is randomised).
func Lower(packageName string, doc *loader.Document) (*ir.Spec, error) {
	spec := &ir.Spec{
		PackageName: packageName,
		DocTitle:    doc.Info.Title,
	}
	for _, opName := range sortedKeys(doc.Operations) {
		op := doc.Operations[opName]
		if op.Action != "send" && op.Action != "receive" {
			return nil, fmt.Errorf("operation %q: unsupported action %q (expected \"send\" or \"receive\")", opName, op.Action)
		}
		lowered, err := lowerOperation(doc, opName, op)
		if err != nil {
			return nil, fmt.Errorf("operation %q: %w", opName, err)
		}
		spec.Operations = append(spec.Operations, lowered)
	}
	return spec, nil
}

func lowerOperation(doc *loader.Document, opName string, op *loader.Operation) (*ir.Operation, error) {
	chName, err := refLastSegment(op.Channel.Ref, "#/channels/")
	if err != nil {
		return nil, fmt.Errorf("channel ref: %w", err)
	}
	rawCh, ok := doc.Channels[chName]
	if !ok {
		return nil, fmt.Errorf("channel %q not declared", chName)
	}
	ch, err := lowerChannel(chName, rawCh)
	if err != nil {
		return nil, fmt.Errorf("channel %q: %w", chName, err)
	}
	if len(op.Messages) != 1 {
		return nil, fmt.Errorf("v0.2 supports exactly 1 message per operation, got %d", len(op.Messages))
	}
	msgName, err := refLastSegment(op.Messages[0].Ref, "#/channels/"+chName+"/messages/")
	if err != nil {
		return nil, fmt.Errorf("message ref: %w", err)
	}
	rawMsg, ok := rawCh.Messages[msgName]
	if !ok {
		return nil, fmt.Errorf("message %q not declared on channel %q", msgName, chName)
	}
	msg, err := lowerMessage(doc.SourcePath, msgName, rawMsg)
	if err != nil {
		return nil, fmt.Errorf("message %q: %w", msgName, err)
	}
	out := &ir.Operation{
		Name:    opName,
		Action:  ir.Action(op.Action),
		Channel: ch,
		Message: msg,
	}
	if op.Action == "send" {
		// Publisher convention: GoFuncName = pascalize(opName). Operation
		// keys like "sendWidgetMessage" already encode the verb; mapping
		// them to "SendWidgetMessage" is a single capitalisation.
		out.GoFuncName = pascalize(opName)
	} else {
		// Subscriber convention: GoFuncName = "Subscribe" + <MessageName>.
		// Decouples the method name from the spec's operation key (which
		// might be "consumeJobMessage", "onJobMessage", etc. — whatever
		// the spec author chose) so consumers always discover
		// SubscribeJobMessage by message name. Handler interface +
		// method follow the same message-name-centric convention.
		out.GoFuncName = "Subscribe" + msg.GoTypeName
		out.HandlerTypeName = msg.GoTypeName + "Handler"
		out.HandlerMethodName = "Handle" + msg.GoTypeName
		// Receive operations require a queue (we need a queue name to
		// subscribe to). bindings.amqp.is must be "queue" (the address
		// IS the queue name) OR bindings.amqp.queue.name must be set.
		amqp, ok := ch.Binding.(*ir.AMQPBinding)
		if !ok {
			return nil, fmt.Errorf("receive op %q: only AMQP binding is supported in v0.2", opName)
		}
		if amqp.Queue == nil && amqp.ChannelKind != "queue" {
			return nil, fmt.Errorf("receive op %q: channel %q has no queue topology — set `bindings.amqp.is: queue` (address becomes the queue name) or declare `bindings.amqp.queue.name`", opName, chName)
		}
		// When ChannelKind is "queue" but `bindings.amqp.queue` block is
		// absent, synthesise a Queue with the address as the name so the
		// templates have a single field to read from.
		if amqp.Queue == nil {
			amqp.Queue = &ir.AMQPQueue{Name: ch.Address.Raw}
		}
	}
	return out, nil
}

func lowerChannel(name string, raw *loader.Channel) (*ir.Channel, error) {
	addr, err := ir.ParseAddress(raw.Address)
	if err != nil {
		return nil, err
	}
	binding, err := lowerAMQPBinding(raw.Bindings)
	if err != nil {
		return nil, err
	}
	return &ir.Channel{
		Name:    name,
		Address: addr,
		Binding: binding,
	}, nil
}

// lowerAMQPBinding reads the `bindings.amqp` stanza out of the loader's
// generic map representation. Unmodelled fields are tolerated (preserved
// in the original map but not surfaced) so future AMQP fields don't
// break the loader.
//
// Validation differs by channel mode (bindings.amqp.is):
//   - "queue" (consumer): exchange optional (queues exist independently);
//     queue block synthesised from address if not declared.
//   - "routingKey" or unset (publisher): exchange.name + exchange.type
//     required — that's where messages get published.
func lowerAMQPBinding(bindings map[string]any) (*ir.AMQPBinding, error) {
	if bindings == nil {
		return nil, fmt.Errorf("channel has no `bindings` block; aapi-codegen requires `bindings.amqp`")
	}
	amqpRaw, hasAMQP := bindings["amqp"]
	if !hasAMQP {
		return nil, fmt.Errorf("channel has `bindings` but no `amqp` entry; aapi-codegen supports only the AMQP binding")
	}
	raw, ok := amqpRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`bindings.amqp` is not an object (got %T)", amqpRaw)
	}
	channelKind, _ := raw["is"].(string)

	out := &ir.AMQPBinding{ChannelKind: channelKind}
	out.BindingVersion, _ = raw["bindingVersion"].(string)

	if exchange, ok := raw["exchange"].(map[string]any); ok {
		out.Exchange, _ = exchange["name"].(string)
		out.ExchangeType, _ = exchange["type"].(string)
	}
	if queue, ok := raw["queue"].(map[string]any); ok {
		out.Queue = &ir.AMQPQueue{}
		out.Queue.Name, _ = queue["name"].(string)
		out.Queue.Durable, _ = queue["durable"].(bool)
		out.Queue.AutoDelete, _ = queue["autoDelete"].(bool)
		out.Queue.Exclusive, _ = queue["exclusive"].(bool)
	}

	// Publisher-mode validation: exchange.name + .type required because
	// the publisher template uses them as positional arguments to
	// transport.Publish. Consumer-mode (queue) channels publish nothing
	// and may legitimately omit exchange details.
	if channelKind != "queue" {
		if out.Exchange == "" {
			return nil, fmt.Errorf("`bindings.amqp.exchange.name` missing or empty (required for publisher channels)")
		}
		if out.ExchangeType == "" {
			return nil, fmt.Errorf("`bindings.amqp.exchange.type` missing or empty (required for publisher channels)")
		}
	}
	return out, nil
}

// lowerMessage resolves the message's payload $ref to a schema file and
// reads the title. The schema title is the Go type name go-jsonschema
// emits under --struct-name-from-title; matching it exactly is what
// makes the generated publisher reference a real type.
func lowerMessage(specPath, name string, raw *loader.Message) (*ir.Message, error) {
	if raw.Payload.Ref == "" {
		return nil, fmt.Errorf("inline payload schemas not yet supported (M3+ scope)")
	}
	if strings.HasPrefix(raw.Payload.Ref, "#/") {
		return nil, fmt.Errorf("internal payload refs not yet supported")
	}
	path := raw.Payload.Ref
	if i := strings.Index(path, "#"); i >= 0 {
		path = path[:i]
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(specPath), path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve payload ref %q: %w", raw.Payload.Ref, err)
	}
	s, err := schema.Load(abs)
	if err != nil {
		return nil, fmt.Errorf("load payload schema %s: %w", abs, err)
	}
	if s.Title == "" {
		return nil, fmt.Errorf("payload schema %s has no `title` (required so the Go type name matches go-jsonschema's --struct-name-from-title)", abs)
	}
	return &ir.Message{
		Name:       name,
		GoTypeName: s.Title,
	}, nil
}

// refLastSegment validates that ref starts with prefix and returns the
// segment after it. Used to resolve simple internal refs like
// `#/channels/widgetDispatch` to the channel key. Doesn't try to be a
// general JSON Pointer implementation — v1 only needs three exact
// shapes (#/channels/X, #/channels/X/messages/Y, #/components/...).
func refLastSegment(ref, prefix string) (string, error) {
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("expected ref to start with %q, got %q", prefix, ref)
	}
	tail := ref[len(prefix):]
	if tail == "" || strings.Contains(tail, "/") {
		return "", fmt.Errorf("ref %q has unexpected trailing path after %q", ref, prefix)
	}
	return tail, nil
}

// pascalize uppercases the first rune. Operation keys in AsyncAPI are
// lowerCamelCase by convention (`sendWidgetMessage`); the Go method
// name is the same with a capital first letter (`SendWidgetMessage`).
// More elaborate name mangling (initialisms etc.) lives in
// pkg/codegen.goFieldName but isn't needed here — operation keys don't
// contain initialisms in any spec we've seen.
func pascalize(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// stable alphabetical
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
