package codegen

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
)

// publisherTemplate emits the Transport interface, the Publisher
// struct, its constructor, and one Send<MessageName> method per
// `action: send` operation. The context / encoding/json / fmt imports
// the body needs are injected separately by the caller (see Generate's
// combine pass) so the template stays free of import bookkeeping.
//
// Routing-key construction is delegated to ir.Address.RoutingKeyExpr so
// the template stays free of Go-expression building. Likewise, exchange
// name comes from the AMQP binding the lowerer already validated.
const publisherTemplate = `
// ---------- aapi-codegen publisher (generated, DO NOT EDIT) ----------

// Transport is the minimal AMQP publish surface aapi-codegen-generated
// publishers need. Adapt your *amqp091.Channel by wrapping it; tests
// can substitute a fake. The generated package never imports an AMQP
// client library.
type Transport interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte) error
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
) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal {{.Message.GoTypeName}}: %w", err)
	}
	routingKey := {{.Channel.Address.RoutingKeyExpr}}
	return p.transport.Publish(ctx, {{printf "%q" .Channel.Binding.AMQP.Exchange}}, routingKey, body)
}
{{ end }}`

// publisherView is the template-friendly view of the IR. We add an AMQP
// accessor so the template can write `.Channel.Binding.AMQP.Exchange`
// without having to do an interface type switch in `text/template`,
// which doesn't support type assertions.
type publisherView struct {
	DocTitle   string
	Operations []publisherOpView
}

type publisherOpView struct {
	Name       string
	GoFuncName string
	Channel    publisherChannelView
	Message    *ir.Message
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
		view.Operations = append(view.Operations, publisherOpView{
			Name:       op.Name,
			GoFuncName: op.GoFuncName,
			Channel: publisherChannelView{
				Name:    op.Channel.Name,
				Address: op.Channel.Address,
				Binding: publisherBindingView{AMQP: amqp},
			},
			Message: op.Message,
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
