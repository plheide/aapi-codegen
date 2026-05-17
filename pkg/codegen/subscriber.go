package codegen

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
)

// subscriberTemplate emits, per generated package: the ErrDrop sentinel,
// a Transport interface fragment (added by combineSections when receive
// ops exist), the Subscriber struct + constructor, one <MessageName>Handler
// interface per receive op, and one Subscribe<MessageName> method per
// receive op.
//
// Handler signature: HandleX(ctx, msg X) error. Ack semantics:
//   - nil               → ack
//   - errors.Is(err, ErrDrop) → nack without requeue (poison message)
//   - any other err     → nack with requeue (transient — retry)
//
// JSON-unmarshal failure inside the generated dispatch wrapper is joined
// with ErrDrop, because a payload that doesn't parse will never parse no
// matter how many times you requeue it.
const subscriberTemplate = `
// SubscribeTransport is the consumer half of the AMQP surface
// aapi-codegen-generated subscribers need. It is intentionally separate
// from the (publisher) Transport so a package that only consumes can
// satisfy this interface alone. The generated package never imports an
// AMQP client library.
//
// Subscribe blocks until ctx is cancelled or a fatal transport error
// occurs. It invokes handler once per delivery; the handler's return
// drives ack semantics:
//   - nil                         → ack
//   - errors.Is(err, ErrDrop)     → nack, do not requeue (poison)
//   - any other non-nil           → nack, requeue (transient)
type SubscribeTransport interface {
	Subscribe(ctx context.Context, queueName string, handler func(ctx context.Context, routingKey string, body []byte) error) error
}

// ErrDrop signals that the current message should be acknowledged-and-
// dropped (nack-without-requeue) rather than retried. Handlers wrap or
// join this sentinel into their error return when they detect a poison
// message; the SubscribeTransport implementation should map any error
// satisfying errors.Is(err, ErrDrop) to nack with requeue=false.
var ErrDrop = errors.New("aapi-codegen: drop message (nack without requeue)")

// Subscriber wires {{.DocTitle}} AsyncAPI message handlers to a
// SubscribeTransport.
type Subscriber struct {
	transport SubscribeTransport
}

func NewSubscriber(transport SubscribeTransport) *Subscriber {
	return &Subscriber{transport: transport}
}
{{ range .Operations }}
// {{.HandlerTypeName}} consumes {{.Message.QualifiedGoType}} messages.
// Implement on your consumer; the generated Subscribe wrapper takes care
// of JSON-decoding the body and mapping handler errors to ack semantics
// per ErrDrop.
type {{.HandlerTypeName}} interface {
	{{.HandlerMethodName}}(ctx context.Context, msg {{.Message.QualifiedGoType}}) error
}

// {{.GoFuncName}} starts consuming {{.Message.QualifiedGoType}} messages from
// the queue named "{{.Queue.NameExpr}}" (from bindings.amqp.queue.name).
// Blocks until ctx is cancelled or a fatal transport error occurs.
// Generated from operations.{{.Name}} (channel {{.Channel.Name}}).
func (s *Subscriber) {{.GoFuncName}}(
	ctx context.Context,
{{- range .Channel.Address.Params }}
	{{.GoArgName}} {{.GoType}},
{{- end }}
	handler {{.HandlerTypeName}},
) error {
	queueName := {{.Queue.NameExprGo}}
	return s.transport.Subscribe(ctx, queueName, func(ctx context.Context, routingKey string, body []byte) error {
		var msg {{.Message.QualifiedGoType}}
		if err := json.Unmarshal(body, &msg); err != nil {
			// A payload that doesn't unmarshal will never unmarshal —
			// drop it rather than requeue forever.
			return fmt.Errorf("decode {{.Message.QualifiedGoType}} (dropping): %w", errors.Join(ErrDrop, err))
		}
		return handler.{{.HandlerMethodName}}(ctx, msg)
	})
}
{{ end }}`

type subscriberView struct {
	DocTitle   string
	Operations []subscriberOpView
}

type subscriberOpView struct {
	Name              string
	GoFuncName        string
	HandlerTypeName   string
	HandlerMethodName string
	Channel           publisherChannelView
	Message           *ir.Message
	Queue             subscriberQueueView
}

type subscriberQueueView struct {
	// NameExpr is the queue name template in human-readable form for
	// the generated-code comment ("{partitionID}.PetexResultParser").
	NameExpr string
	// NameExprGo is the Go expression that computes the queue name at
	// runtime from the address parameters. For a literal queue name it's
	// a quoted string; for a parameterised one it's a Sprintf or string
	// concatenation expression.
	NameExprGo string
}

// RenderSubscriber emits the subscriber Go source for every
// `action: receive` operation. Returns "" when the spec has none —
// caller should skip emission and not pull in context/json/fmt/errors.
func RenderSubscriber(spec *ir.Spec) (string, error) {
	view := subscriberView{DocTitle: spec.DocTitle}
	for _, op := range spec.Operations {
		if op.Action != ir.ActionReceive {
			continue
		}
		amqp, ok := op.Channel.Binding.(*ir.AMQPBinding)
		if !ok {
			return "", fmt.Errorf("operation %q: only AMQP bindings are supported, got %q", op.Name, op.Channel.Binding.Kind())
		}
		queueExpr := queueNameExpr(amqp, op.Channel.Address)
		view.Operations = append(view.Operations, subscriberOpView{
			Name:              op.Name,
			GoFuncName:        op.GoFuncName,
			HandlerTypeName:   op.HandlerTypeName,
			HandlerMethodName: op.HandlerMethodName,
			Channel: publisherChannelView{
				Name:    op.Channel.Name,
				Address: op.Channel.Address,
				Binding: publisherBindingView{AMQP: amqp},
			},
			Message: op.Message,
			Queue: subscriberQueueView{
				NameExpr:   queueNameTemplate(amqp, op.Channel.Address),
				NameExprGo: queueExpr,
			},
		})
	}
	if len(view.Operations) == 0 {
		return "", nil
	}
	tmpl, err := template.New("subscriber").Parse(subscriberTemplate)
	if err != nil {
		return "", fmt.Errorf("parse subscriber template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, view); err != nil {
		return "", fmt.Errorf("execute subscriber template: %w", err)
	}
	return b.String(), nil
}

// queueNameTemplate returns the queue-name template string for use in
// generated-code comments. Prefers bindings.amqp.queue.name when set;
// falls back to the channel address (queue mode) which IS the queue name.
func queueNameTemplate(amqp *ir.AMQPBinding, addr ir.Address) string {
	if amqp.Queue != nil && amqp.Queue.Name != "" {
		return amqp.Queue.Name
	}
	return addr.Raw
}

// queueNameExpr returns the Go expression that builds the queue name at
// runtime. When the queue name matches the channel address (queue-mode
// channels typically declare them equal), reuses Address.RoutingKeyExpr
// which already knows how to interpolate the parameters. When the queue
// name is a static string distinct from the address, emits a quoted
// literal.
func queueNameExpr(amqp *ir.AMQPBinding, addr ir.Address) string {
	name := queueNameTemplate(amqp, addr)
	if name == addr.Raw {
		return addr.RoutingKeyExpr()
	}
	return fmt.Sprintf("%q", name)
}
