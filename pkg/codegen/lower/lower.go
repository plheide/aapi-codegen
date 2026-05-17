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
//
// omitValidation, when true, suppresses runtime parameter pattern
// validation (parameters with `schema.pattern` fall back to plain
// `string`). Enum parameters are unaffected — they're compile-time
// type safety, not runtime validation. The flag mirrors the
// `x-aapi-codegen.omit-validation` spec extension semantics: when the
// consumer has opted out of generated validation, parameter pattern
// checks are skipped along with the payload UnmarshalJSON checks.
// MessagePackage is one entry from the resolved
// `x-aapi-codegen.message-packages` mapping the lowerer consults when
// it encounters a cross-file message $ref. Mirror of
// codegen.MessagePackageMapping kept local to the lower package so
// importers don't need to depend on codegen.
type MessagePackage struct {
	File    string // path relative to the consuming spec's directory
	Package string // Go import path
	Alias   string // Go import alias
}

func Lower(packageName string, doc *loader.Document, omitValidation bool, messagePackages []MessagePackage) (*ir.Spec, error) {
	spec := &ir.Spec{
		PackageName: packageName,
		DocTitle:    doc.Info.Title,
	}
	for _, opName := range sortedKeys(doc.Operations) {
		op := doc.Operations[opName]
		if op.Action != "send" && op.Action != "receive" {
			return nil, fmt.Errorf("operation %q: unsupported action %q (expected \"send\" or \"receive\")", opName, op.Action)
		}
		lowered, err := lowerOperation(doc, opName, op, spec, omitValidation, messagePackages)
		if err != nil {
			return nil, fmt.Errorf("operation %q: %w", opName, err)
		}
		spec.Operations = append(spec.Operations, lowered)
	}
	return spec, nil
}

func lowerOperation(doc *loader.Document, opName string, op *loader.Operation, spec *ir.Spec, omitValidation bool, messagePackages []MessagePackage) (*ir.Operation, error) {
	chName, err := refLastSegment(op.Channel.Ref, "#/channels/")
	if err != nil {
		return nil, fmt.Errorf("channel ref: %w", err)
	}
	rawCh, ok := doc.Channels[chName]
	if !ok {
		return nil, fmt.Errorf("channel %q not declared", chName)
	}
	ch, err := lowerChannel(chName, rawCh, spec, omitValidation)
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
	msg, err := lowerMessage(doc.SourcePath, msgName, rawMsg, messagePackages)
	if err != nil {
		return nil, fmt.Errorf("message %q: %w", msgName, err)
	}
	out := &ir.Operation{
		Name:    opName,
		Action:  ir.Action(op.Action),
		Channel: ch,
		Message: msg,
	}
	// Publish defaults: spec-declared AMQP message + operation bindings
	// the Send method materialises into the default PublishProperties.
	// Only applies to send operations — receive doesn't publish.
	if op.Action == "send" {
		defaults := lowerPublishDefaults(doc.DefaultContentType, rawMsg, op)
		if defaults != nil {
			out.PublishDefaults = defaults
		}
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

func lowerChannel(name string, raw *loader.Channel, spec *ir.Spec, omitValidation bool) (*ir.Channel, error) {
	addr, err := ir.ParseAddress(raw.Address)
	if err != nil {
		return nil, err
	}
	binding, err := lowerAMQPBinding(raw.Bindings)
	if err != nil {
		return nil, err
	}
	// v0.4 / v0.4.1: walk each address parameter; if the spec declares
	// an enum or pattern schema for it, override the AddressParam's
	// GoType and register the type globally on the spec. Enum wins over
	// pattern (a closed enum makes the pattern redundant); pattern
	// wrapping is skipped entirely when omitValidation is on.
	for i, p := range addr.Params {
		paramDef, ok := raw.Parameters[p.JSONName]
		if !ok || paramDef == nil {
			continue
		}
		typeName := pascalize(p.JSONName)
		if enum, ok := parameterEnum(paramDef.Schema); ok {
			if err := registerParameterEnum(spec, typeName, enum); err != nil {
				return nil, fmt.Errorf("parameter %q: %w", p.JSONName, err)
			}
			addr.Params[i].GoType = typeName
			continue
		}
		if omitValidation {
			continue
		}
		if pattern, ok := parameterPattern(paramDef.Schema); ok {
			if err := registerParameterPattern(spec, typeName, pattern); err != nil {
				return nil, fmt.Errorf("parameter %q: %w", p.JSONName, err)
			}
			addr.Params[i].GoType = typeName
		}
	}
	return &ir.Channel{
		Name:    name,
		Address: addr,
		Binding: binding,
	}, nil
}

// parameterPattern returns the regex from a `{type: string, pattern: ...}`
// parameter schema, or ("", false) when the schema isn't that shape.
// $ref-based schemas are deliberately not followed — v0.4.1 only handles
// inline patterns to keep the surface narrow.
func parameterPattern(schema map[string]any) (string, bool) {
	if schema == nil {
		return "", false
	}
	if t, _ := schema["type"].(string); t != "string" {
		return "", false
	}
	pat, _ := schema["pattern"].(string)
	if pat == "" {
		return "", false
	}
	return pat, true
}

// registerParameterPattern adds the typed-pattern wrapper to
// spec.ParameterPatterns, deduping on GoTypeName. Two parameters that
// lower to the same type name must declare the same pattern; otherwise
// the spec contradicts itself and emission would yield two
// conflicting regex constants on one type.
func registerParameterPattern(spec *ir.Spec, typeName, pattern string) error {
	for _, existing := range spec.ParameterPatterns {
		if existing.GoTypeName != typeName {
			continue
		}
		if existing.Pattern != pattern {
			return fmt.Errorf("pattern type %q declared with conflicting regexes (%q vs %q) — rename one parameter or align the patterns", typeName, existing.Pattern, pattern)
		}
		return nil
	}
	// Same GoTypeName already used by an enum parameter is a clearer
	// problem to surface than letting both blocks emit and conflict.
	for _, e := range spec.ParameterEnums {
		if e.GoTypeName == typeName {
			return fmt.Errorf("type %q is already declared as an enum — a parameter cannot be typed as both enum and pattern on the same name", typeName)
		}
	}
	spec.ParameterPatterns = append(spec.ParameterPatterns, &ir.ParameterPattern{
		GoTypeName: typeName,
		Pattern:    pattern,
	})
	return nil
}

// parameterEnum returns the string-enum values declared on a parameter
// schema, or (nil, false) if the schema isn't `{type: string, enum: [...]}`.
// $ref-based schemas are deliberately not followed — v0.4 only handles
// inline enums to keep the surface narrow.
func parameterEnum(schema map[string]any) ([]string, bool) {
	if schema == nil {
		return nil, false
	}
	if t, _ := schema["type"].(string); t != "string" {
		return nil, false
	}
	rawList, ok := schema["enum"].([]any)
	if !ok || len(rawList) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(rawList))
	for _, v := range rawList {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// registerParameterEnum adds the enum to spec.ParameterEnums, deduping
// on GoTypeName. Two parameters that lower to the same type name must
// declare the same value list; otherwise the spec is contradicting
// itself and emission would yield two conflicting `type X string`
// declarations.
func registerParameterEnum(spec *ir.Spec, typeName string, values []string) error {
	for _, existing := range spec.ParameterEnums {
		if existing.GoTypeName != typeName {
			continue
		}
		if !stringSlicesEqual(existing.Values, values) {
			return fmt.Errorf("enum type %q declared with conflicting values (%v vs %v) — rename one parameter or align the enum values", typeName, existing.Values, values)
		}
		return nil
	}
	spec.ParameterEnums = append(spec.ParameterEnums, &ir.ParameterEnum{
		GoTypeName: typeName,
		Values:     append([]string(nil), values...),
	})
	return nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lowerPublishDefaults pulls AMQP message + operation binding values
// into the typed IR shape the publisher template materialises as
// default PublishProperties. Returns nil when no field is set so the
// generated Send can keep the simplest possible default-zero block.
//
// Per AsyncAPI 3.x AMQP binding spec:
//   - messages.<X>.bindings.amqp.{contentEncoding, messageType}
//   - operations.<X>.bindings.amqp.{expiration, priority}
//
// ContentType falls back to defaultContentType (top-level spec field)
// when not explicitly set on the message.
func lowerPublishDefaults(defaultContentType string, msg *loader.Message, op *loader.Operation) *ir.PublishDefaults {
	out := &ir.PublishDefaults{}
	any := false

	if msg.ContentType != "" {
		out.ContentType = msg.ContentType
		any = true
	} else if defaultContentType != "" {
		out.ContentType = defaultContentType
		any = true
	}

	if msgAMQP := amqpBinding(msg.Bindings); msgAMQP != nil {
		if v, _ := msgAMQP["contentEncoding"].(string); v != "" {
			out.ContentEncoding = v
			any = true
		}
		if v, _ := msgAMQP["messageType"].(string); v != "" {
			out.MessageType = v
			any = true
		}
	}
	if opAMQP := amqpBinding(op.Bindings); opAMQP != nil {
		if p, ok := opAMQP["priority"]; ok {
			if u, ok := coerceUint8(p); ok {
				out.Priority = &u
				any = true
			}
		}
		// Expiration may be a string ("60000") or a number (60000) in
		// the source YAML; the AMQP wire format is a string. Coerce both.
		if e, ok := opAMQP["expiration"]; ok {
			out.Expiration = coerceString(e)
			if out.Expiration != "" {
				any = true
			}
		}
	}
	if !any {
		return nil
	}
	return out
}

// amqpBinding extracts the `amqp` entry from a bindings map, returning
// nil when the bindings block is absent or the amqp entry is missing or
// wrong-shaped. Centralised so message-level and operation-level
// bindings share one lookup with consistent tolerance.
func amqpBinding(bindings map[string]any) map[string]any {
	if bindings == nil {
		return nil
	}
	raw, ok := bindings["amqp"]
	if !ok {
		return nil
	}
	m, _ := raw.(map[string]any)
	return m
}

func coerceUint8(v any) (uint8, bool) {
	switch n := v.(type) {
	case int:
		if n >= 0 && n <= 255 {
			return uint8(n), true
		}
	case int64:
		if n >= 0 && n <= 255 {
			return uint8(n), true
		}
	case uint64:
		if n <= 255 {
			return uint8(n), true
		}
	case float64:
		if n >= 0 && n <= 255 && float64(uint8(n)) == n {
			return uint8(n), true
		}
	}
	return 0, false
}

func coerceString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case int:
		return fmt.Sprintf("%d", n)
	case int64:
		return fmt.Sprintf("%d", n)
	case uint64:
		return fmt.Sprintf("%d", n)
	case float64:
		// AMQP expirations are always integer milliseconds; truncate.
		return fmt.Sprintf("%d", int64(n))
	}
	return ""
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
	// v0.5.1+: also accept the queue declaration under
	// `bindings.x-aapi-codegen.queue`. AsyncAPI AMQP 0.3.0 disallows the
	// `queue` block when `is: routingKey` (the validator rejects it),
	// so consumers that declare their own queue in routingKey-mode
	// channels need an escape hatch. The extension namespace IS that
	// hatch. Standard binding takes precedence when both are present.
	if out.Queue == nil {
		if xExt, ok := bindings["x-aapi-codegen"].(map[string]any); ok {
			if queue, ok := xExt["queue"].(map[string]any); ok {
				out.Queue = &ir.AMQPQueue{}
				out.Queue.Name, _ = queue["name"].(string)
				out.Queue.Durable, _ = queue["durable"].(bool)
				out.Queue.AutoDelete, _ = queue["autoDelete"].(bool)
				out.Queue.Exclusive, _ = queue["exclusive"].(bool)
			}
		}
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

// lowerMessage resolves the message into an *ir.Message.
//
// Two paths:
//   - Cross-file message $ref (v0.5+): raw.Ref is set to something like
//     `../path/spec.yaml#/channels/X/messages/Y`. The lowerer looks up
//     the file part in messagePackages and returns an Imported message
//     — the Go type comes from the mapped producer package; no payload
//     schema is read from disk.
//   - Local message: lowerer follows raw.Payload.Ref (already
//     absolutised by the materializer or pointing at an external file)
//     and reads the schema title for the Go type name. This is the
//     v0.1.x+ path.
func lowerMessage(specPath, name string, raw *loader.Message, messagePackages []MessagePackage) (*ir.Message, error) {
	if raw.Ref != "" {
		return lowerImportedMessage(specPath, name, raw.Ref, messagePackages)
	}
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

// lowerImportedMessage resolves a cross-file message $ref to an
// imported Go type via x-aapi-codegen.message-packages. The file path
// part of the ref is resolved relative to the consuming spec's
// directory; the message-name segment of the fragment is the Go type
// name in the mapped package. v0.5+.
func lowerImportedMessage(specPath, name, ref string, messagePackages []MessagePackage) (*ir.Message, error) {
	hash := strings.Index(ref, "#")
	if hash < 0 || hash == len(ref)-1 {
		return nil, fmt.Errorf("cross-file message $ref %q has no fragment — expected `<path>#/channels/<ch>/messages/<key>`", ref)
	}
	pathPart := ref[:hash]
	fragment := ref[hash+1:]
	if pathPart == "" {
		return nil, fmt.Errorf("message $ref %q is same-file but not `#/components/messages/...` — unsupported", ref)
	}
	// Last fragment segment is the message key on the producer side;
	// it doubles as the Go type name (--struct-name-from-title makes
	// go-jsonschema emit the type with that exact name).
	segments := strings.Split(fragment, "/")
	typeName := segments[len(segments)-1]
	if typeName == "" {
		return nil, fmt.Errorf("cross-file message $ref %q fragment has no trailing segment", ref)
	}

	// Resolve the path relative to the consuming spec's directory, the
	// same rule the materializer uses for cross-tree payload $refs.
	resolved := pathPart
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(specPath), resolved)
	}
	resolvedAbs, err := filepath.Abs(resolved)
	if err != nil {
		return nil, fmt.Errorf("resolve message $ref path %q: %w", pathPart, err)
	}

	for _, mp := range messagePackages {
		mpPath := mp.File
		if !filepath.IsAbs(mpPath) {
			mpPath = filepath.Join(filepath.Dir(specPath), mpPath)
		}
		mpAbs, _ := filepath.Abs(mpPath)
		if mpAbs != resolvedAbs {
			continue
		}
		return &ir.Message{
			Name:            name,
			GoTypeName:      typeName,
			ImportedPackage: mp.Package,
			ImportedAlias:   mp.Alias,
		}, nil
	}
	return nil, fmt.Errorf("cross-file message $ref %q has no matching `x-aapi-codegen.message-packages` mapping — add an entry with file: %q to import the type from the producer's already-generated Go package",
		ref, pathPart)
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
