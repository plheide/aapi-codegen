package codegen

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
)

// publisherTemplate emits, per generated package: the PublishProperties
// struct, the Transport interface (single Publish method taking
// properties), the SendOption functional-option type + With* helpers,
// the Publisher struct + constructor, and one Send<MessageName> method
// per `action: send` operation.
//
// Per-call SendOption args override spec-declared defaults from
// messages.X.bindings.amqp / operations.X.bindings.amqp. The defaults
// are baked into each Send method as a literal PublishProperties
// initializer; opts mutate it before the transport call. v0.3+.
//
// **Breaking change vs v0.1.x/v0.2.0**: Transport.Publish gains a 5th
// `props PublishProperties` argument. Adapters wrapping
// *amqp091.Channel grow one line per binding field they translate.
const publisherTemplate = `
// ---------- aapi-codegen publisher (generated, DO NOT EDIT) ----------

// PublishProperties carries AMQP message-level metadata the publisher
// passes through to the broker. Spec authors declare defaults via
// messages.<X>.bindings.amqp.{contentEncoding, messageType} and
// operations.<X>.bindings.amqp.{priority, expiration}; callers
// override per-publish via SendOption args (WithPriority,
// WithExpirationMillis, WithContentType, ...).
//
// Pointer-typed fields distinguish "not set" (nil → adapter uses the
// AMQP default / leaves the wire field blank) from "set to zero"
// (non-nil zero → adapter writes the zero value). Strings use "" for
// "not set" because the AMQP wire format does the same.
type PublishProperties struct {
	ContentType     string
	ContentEncoding string
	MessageType     string
	Priority        *uint8
	Expiration      string // milliseconds as a decimal string per AMQP 0.9.1
}

// Transport is the minimal AMQP publish surface aapi-codegen-generated
// publishers need. Adapt your *amqp091.Channel by wrapping it; tests
// can substitute a fake. The generated package never imports an AMQP
// client library.
//
// v0.3 breaking change: Publish gains a 5th props arg. Existing
// adapters add a one-liner that copies the relevant fields into the
// amqp091.Publishing struct.
type Transport interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte, props PublishProperties) error
}

// SendOption mutates per-call PublishProperties. Spec-declared
// bindings are baked into the default value; opts layer on top in
// declaration order (later opts win on the same field).
type SendOption func(*PublishProperties)

// WithContentType overrides the per-call content type. Defaults come
// from messages.<X>.contentType or defaultContentType.
func WithContentType(v string) SendOption {
	return func(p *PublishProperties) { p.ContentType = v }
}

// WithContentEncoding overrides messages.<X>.bindings.amqp.contentEncoding.
func WithContentEncoding(v string) SendOption {
	return func(p *PublishProperties) { p.ContentEncoding = v }
}

// WithMessageType overrides messages.<X>.bindings.amqp.messageType.
func WithMessageType(v string) SendOption {
	return func(p *PublishProperties) { p.MessageType = v }
}

// WithPriority overrides operations.<X>.bindings.amqp.priority.
// AMQP priority is 0–9; values above 9 are clamped by the broker.
func WithPriority(v uint8) SendOption {
	return func(p *PublishProperties) { p.Priority = &v }
}

// WithExpirationMillis sets the per-message TTL in milliseconds.
// Overrides operations.<X>.bindings.amqp.expiration.
func WithExpirationMillis(ms int) SendOption {
	return func(p *PublishProperties) { p.Expiration = fmt.Sprintf("%d", ms) }
}

// Publisher publishes {{.DocTitle}} AsyncAPI messages.
type Publisher struct {
	transport Transport
}

func NewPublisher(transport Transport) *Publisher {
	return &Publisher{transport: transport}
}
{{ range .Operations }}
// {{.GoFuncName}} publishes a {{.Message.GoTypeName}} to the {{.Channel.Binding.AMQP.Exchange}}
// {{.Channel.Binding.AMQP.ExchangeType}} exchange with routing key "{{.Channel.Address.Raw}}".
// Generated from operations.{{.Name}} (channel {{.Channel.Name}}).
func (p *Publisher) {{.GoFuncName}}(
	ctx context.Context,
{{- range .Channel.Address.Params }}
	{{.GoArgName}} {{.GoType}},
{{- end }}
	msg {{.Message.GoTypeName}},
	opts ...SendOption,
) error {
	props := PublishProperties{
{{- if .Defaults }}
{{- if .Defaults.ContentType }}
		ContentType: {{printf "%q" .Defaults.ContentType}},
{{- end }}
{{- if .Defaults.ContentEncoding }}
		ContentEncoding: {{printf "%q" .Defaults.ContentEncoding}},
{{- end }}
{{- if .Defaults.MessageType }}
		MessageType: {{printf "%q" .Defaults.MessageType}},
{{- end }}
{{- if .Defaults.Priority }}
		Priority: priorityPtr({{.Defaults.Priority}}),
{{- end }}
{{- if .Defaults.Expiration }}
		Expiration: {{printf "%q" .Defaults.Expiration}},
{{- end }}
{{- end }}
	}
	for _, opt := range opts {
		opt(&props)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal {{.Message.GoTypeName}}: %w", err)
	}
	routingKey := {{.Channel.Address.RoutingKeyExpr}}
	return p.transport.Publish(ctx, {{printf "%q" .Channel.Binding.AMQP.Exchange}}, routingKey, body, props)
}
{{ end }}
{{ if .EmitPriorityPtr }}
// priorityPtr is a private helper that lets the spec-declared priority
// default appear as a compile-time-constant initialiser. Avoids the
// "cannot take address of constant" Go restriction.
func priorityPtr(v uint8) *uint8 { return &v }
{{ end }}`

type publisherView struct {
	DocTitle        string
	Operations      []publisherOpView
	EmitPriorityPtr bool
}

type publisherOpView struct {
	Name       string
	GoFuncName string
	Channel    publisherChannelView
	Message    *ir.Message
	Defaults   *publisherDefaultsView
}

type publisherDefaultsView struct {
	ContentType     string
	ContentEncoding string
	MessageType     string
	// Priority is rendered as its uint8 literal; empty when no default.
	Priority   string
	Expiration string
}

type publisherChannelView struct {
	Name    string
	Address ir.Address
	Binding publisherBindingView
}

type publisherBindingView struct {
	AMQP *ir.AMQPBinding
}

// RenderPublisher emits the publisher Go source for every `action: send`
// operation in spec. Returns "" when the spec has no send operations —
// caller should skip emission entirely and not pull in
// context/json/fmt imports.
func RenderPublisher(spec *ir.Spec) (string, error) {
	view := publisherView{DocTitle: spec.DocTitle}
	for _, op := range spec.Operations {
		if op.Action != ir.ActionSend {
			continue
		}
		amqp, ok := op.Channel.Binding.(*ir.AMQPBinding)
		if !ok {
			return "", fmt.Errorf("operation %q: only AMQP bindings are supported, got %q", op.Name, op.Channel.Binding.Kind())
		}
		defaults := toPublisherDefaults(op.PublishDefaults)
		if defaults != nil && defaults.Priority != "" {
			view.EmitPriorityPtr = true
		}
		view.Operations = append(view.Operations, publisherOpView{
			Name:       op.Name,
			GoFuncName: op.GoFuncName,
			Channel: publisherChannelView{
				Name:    op.Channel.Name,
				Address: op.Channel.Address,
				Binding: publisherBindingView{AMQP: amqp},
			},
			Message:  op.Message,
			Defaults: defaults,
		})
	}
	if len(view.Operations) == 0 {
		return "", nil
	}
	tmpl, err := template.New("publisher").Parse(publisherTemplate)
	if err != nil {
		return "", fmt.Errorf("parse publisher template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, view); err != nil {
		return "", fmt.Errorf("execute publisher template: %w", err)
	}
	return b.String(), nil
}

func toPublisherDefaults(d *ir.PublishDefaults) *publisherDefaultsView {
	if d == nil {
		return nil
	}
	out := &publisherDefaultsView{
		ContentType:     d.ContentType,
		ContentEncoding: d.ContentEncoding,
		MessageType:     d.MessageType,
		Expiration:      d.Expiration,
	}
	if d.Priority != nil {
		out.Priority = fmt.Sprintf("%d", *d.Priority)
	}
	return out
}
