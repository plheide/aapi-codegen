package codegen

import (
	"fmt"
	"strings"
	"text/template"
	"unicode"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
)

// subscriberTemplate emits, per generated package: the ErrDrop sentinel,
// a Transport interface fragment (added by combineSections when receive
// ops exist), the Subscriber struct + constructor, and then EITHER:
//   - one <MessageName>Handler interface + one Subscribe<MessageName>
//     method per receive op whose queue carries a single message type
//     (.Operations), OR
//   - for a queue shared by several receive ops (several message types,
//     one per routing key — v0.7), one <MessageName>Handler interface
//     per message, one combined <QueueName>Handler interface embedding
//     them, and one Subscribe<QueueName> method that binds all keys and
//     dispatches by routing key (.MultiGroups).
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
// queueName is the queue to declare/consume; bindingKeys are the
// routing keys to bind that queue to the channel's exchange with. They
// come from the spec independently (bindings.amqp.queue.name vs the
// channel address), so a consumer whose queue name differs from its
// binding key — e.g. a shared queue bound to a fixed routing key on a
// direct exchange — is expressible. For queue-mode channels the two
// are equal. A queue shared by several message types (v0.7) is bound to
// several binding keys in one Subscribe call and the handler dispatches
// on routingKey.
//
// Subscribe blocks until ctx is cancelled or a fatal transport error
// occurs. It invokes handler once per delivery; the handler's return
// drives ack semantics:
//   - nil                         → ack
//   - errors.Is(err, ErrDrop)     → nack, do not requeue (poison)
//   - any other non-nil           → nack, requeue (transient)
type SubscribeTransport interface {
	Subscribe(ctx context.Context, queueName string, bindingKeys []string, handler func(ctx context.Context, routingKey string, body []byte) error) error
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
// the queue named "{{.Queue.NameExpr}}" (from bindings.amqp.queue.name),
// bound with routing key "{{.Channel.Address.Raw}}" (the channel address).
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
	bindingKeys := []string{ {{.BindingKeysExprGo}} }
	return s.transport.Subscribe(ctx, queueName, bindingKeys, func(ctx context.Context, routingKey string, body []byte) error {
		var msg {{.Message.QualifiedGoType}}
		if err := json.Unmarshal(body, &msg); err != nil {
			// A payload that doesn't unmarshal will never unmarshal —
			// drop it rather than requeue forever.
			return fmt.Errorf("decode {{.Message.QualifiedGoType}} (dropping): %w", errors.Join(ErrDrop, err))
		}
		return handler.{{.HandlerMethodName}}(ctx, msg)
	})
}
{{ end }}
{{- range .MultiGroups }}
{{- range .Messages }}
// {{.HandlerTypeName}} consumes {{.QualifiedGoType}} messages (routing key
// "{{.AddressRaw}}") delivered to the shared queue. Implement on your
// consumer; the generated dispatch decodes the body and maps handler
// errors to ack semantics per ErrDrop.
type {{.HandlerTypeName}} interface {
	{{.HandlerMethodName}}(ctx context.Context, msg {{.QualifiedGoType}}) error
}
{{ end }}
// {{.HandlerTypeName}} aggregates the per-message handlers for the shared
// queue "{{.QueueNameExpr}}". The queue carries several message types (one
// per binding key); {{.GoFuncName}} routes each delivery to the matching
// method below by its routing key.
type {{.HandlerTypeName}} interface {
{{- range .Messages }}
	{{.HandlerTypeName}}
{{- end }}
}

// {{.GoFuncName}} starts consuming the shared queue "{{.QueueNameExpr}}",
// bound with one binding key per message type, and dispatches each delivery
// by its routing key to the matching handler method. Blocks until ctx is
// cancelled or a fatal transport error occurs.
func (s *Subscriber) {{.GoFuncName}}(ctx context.Context, handler {{.HandlerTypeName}}) error {
	queueName := {{.QueueNameExprGo}}
	bindingKeys := []string{ {{.BindingKeysExprGo}} }
	return s.transport.Subscribe(ctx, queueName, bindingKeys, func(ctx context.Context, routingKey string, body []byte) error {
		switch routingKey {
{{- range .Messages }}
		case {{.AddressLiteralGo}}:
			var msg {{.QualifiedGoType}}
			if err := json.Unmarshal(body, &msg); err != nil {
				return fmt.Errorf("decode {{.QualifiedGoType}} (dropping): %w", errors.Join(ErrDrop, err))
			}
			return handler.{{.HandlerMethodName}}(ctx, msg)
{{- end }}
		default:
			return fmt.Errorf("routing key %q has no handler on queue {{.QueueNameExpr}} (dropping): %w", routingKey, errors.Join(ErrDrop, errors.New("unbound routing key")))
		}
	})
}
{{ end }}`

type subscriberView struct {
	DocTitle string
	// Operations holds receive ops whose queue carries exactly one
	// message type. Rendered with the v0.2–v0.6 single-message block,
	// so existing single-message specs generate byte-for-byte as before.
	Operations []subscriberOpView
	// MultiGroups holds queues shared by several receive ops (several
	// message types). Rendered with the v0.7 combined-Subscriber block.
	MultiGroups []subscriberGroupView
}

type subscriberOpView struct {
	Name              string
	GoFuncName        string
	HandlerTypeName   string
	HandlerMethodName string
	Channel           publisherChannelView
	Message           *ir.Message
	Queue             subscriberQueueView
	// BindingKeysExprGo is the Go expression for the routing key the
	// queue is bound with — always derived from the channel address
	// (Address.RoutingKeyExpr), independent of the queue name. Equal to
	// Queue.NameExprGo for queue-mode channels where address == queue.
	BindingKeysExprGo string
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

// subscriberGroupView is one queue shared by several receive operations
// (v0.7). The combined Subscribe<Queue> binds every message's binding key
// and dispatches deliveries by routing key to the per-message handler.
type subscriberGroupView struct {
	// GoFuncName is "Subscribe" + the queue name turned into an exported
	// Go identifier (e.g. queue "dm-builder-raw" → SubscribeDmBuilderRaw).
	GoFuncName string
	// HandlerTypeName is the combined interface name (<Queue>Handler) that
	// embeds each per-message handler interface.
	HandlerTypeName string
	// QueueNameExpr is the queue name for comments; QueueNameExprGo is the
	// Go expression that yields it at runtime (a quoted literal for a
	// shared fixed queue).
	QueueNameExpr   string
	QueueNameExprGo string
	// BindingKeysExprGo is the comma-joined Go expression listing every
	// message's binding key (each channel's address routing-key literal).
	BindingKeysExprGo string
	// Messages is the per-message dispatch set, in operation (alphabetical)
	// order.
	Messages []subscriberGroupMsgView
}

type subscriberGroupMsgView struct {
	HandlerTypeName   string
	HandlerMethodName string
	QualifiedGoType   string
	// AddressRaw is the channel address (routing key) for the comment.
	AddressRaw string
	// AddressLiteralGo is the quoted Go string literal for the routing-key
	// switch case (fixed addresses only — multi-message grouping requires
	// fixed routing keys).
	AddressLiteralGo string
}

// opEntry is one lowered receive operation plus the bits buildSubscriberGroup
// needs to decide single-vs-shared-queue rendering.
type opEntry struct {
	view     subscriberOpView
	op       *ir.Operation
	amqp     *ir.AMQPBinding
	queue    string
	exchange string
}

// RenderSubscriber emits the subscriber Go source for every
// `action: receive` operation. Receive ops are grouped by their resolved
// queue name: a queue with one op renders the single-message block
// (unchanged since v0.2); a queue shared by several ops renders the v0.7
// combined block. Returns "" when the spec has no receive ops — caller
// should skip emission and not pull in context/json/fmt/errors.
func RenderSubscriber(spec *ir.Spec) (string, error) {
	view := subscriberView{DocTitle: spec.DocTitle}

	// First pass: build a per-op view and tally how many receive ops
	// resolve to each queue name, preserving first-seen queue order for
	// deterministic multi-group emission.
	var entries []opEntry
	queueCount := map[string]int{}
	var queueOrder []string
	for _, op := range spec.Operations {
		if op.Action != ir.ActionReceive {
			continue
		}
		amqp, ok := op.Channel.Binding.(*ir.AMQPBinding)
		if !ok {
			return "", fmt.Errorf("operation %q: only AMQP bindings are supported, got %q", op.Name, op.Channel.Binding.Kind())
		}
		queue := queueNameTemplate(amqp, op.Channel.Address)
		ov := subscriberOpView{
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
				NameExpr:   queue,
				NameExprGo: queueNameExpr(amqp, op.Channel.Address),
			},
			BindingKeysExprGo: op.Channel.Address.RoutingKeyExpr(),
		}
		if queueCount[queue] == 0 {
			queueOrder = append(queueOrder, queue)
		}
		queueCount[queue]++
		entries = append(entries, opEntry{view: ov, op: op, amqp: amqp, queue: queue, exchange: amqp.Exchange})
	}

	// Single-message queues render with the unchanged block, in their
	// original order.
	for _, e := range entries {
		if queueCount[e.queue] == 1 {
			view.Operations = append(view.Operations, e.view)
		}
	}
	// Shared queues (≥2 ops) render with the v0.7 combined block.
	for _, q := range queueOrder {
		if queueCount[q] < 2 {
			continue
		}
		var group []opEntry
		for _, e := range entries {
			if e.queue == q {
				group = append(group, e)
			}
		}
		gv, err := buildSubscriberGroup(q, group)
		if err != nil {
			return "", err
		}
		view.MultiGroups = append(view.MultiGroups, gv)
	}

	if len(view.Operations) == 0 && len(view.MultiGroups) == 0 {
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

// buildSubscriberGroup assembles the combined-Subscriber view for a queue
// shared by several receive operations. Requires: a consistent exchange
// (the transport is constructed per-exchange), fixed (parameterless)
// channel addresses (the routing-key switch needs static case literals),
// distinct addresses (no two messages on the same key), and a consistent
// queue-name expression. group is non-empty and every entry shares queue.
func buildSubscriberGroup(queue string, group []opEntry) (subscriberGroupView, error) {
	ident := goExportIdentFromQueue(queue)
	if ident == "" {
		return subscriberGroupView{}, fmt.Errorf("shared queue %q does not yield a valid Go identifier for the combined Subscriber", queue)
	}
	gv := subscriberGroupView{
		GoFuncName:      "Subscribe" + ident,
		HandlerTypeName: ident + "Handler",
		QueueNameExpr:   queue,
		QueueNameExprGo: group[0].view.Queue.NameExprGo,
	}
	exchange := group[0].exchange
	seenAddr := make(map[string]struct{}, len(group))
	keys := make([]string, 0, len(group))
	for _, e := range group {
		if e.exchange != exchange {
			return subscriberGroupView{}, fmt.Errorf("shared queue %q is bound on two exchanges (%q vs %q); a combined subscriber needs one exchange", queue, exchange, e.exchange)
		}
		if e.view.Queue.NameExprGo != gv.QueueNameExprGo {
			return subscriberGroupView{}, fmt.Errorf("shared queue %q has inconsistent queue-name expressions across its operations", queue)
		}
		if len(e.op.Channel.Address.Params) != 0 {
			return subscriberGroupView{}, fmt.Errorf("shared queue %q: channel %q address %q has parameters; a combined multi-message subscriber requires fixed routing keys", queue, e.op.Channel.Name, e.op.Channel.Address.Raw)
		}
		addrLiteral := e.op.Channel.Address.RoutingKeyExpr() // quoted literal for fixed addresses
		if _, dup := seenAddr[addrLiteral]; dup {
			return subscriberGroupView{}, fmt.Errorf("shared queue %q has two messages on the same routing key %s; routing-key dispatch needs distinct keys", queue, addrLiteral)
		}
		seenAddr[addrLiteral] = struct{}{}
		keys = append(keys, addrLiteral)
		gv.Messages = append(gv.Messages, subscriberGroupMsgView{
			HandlerTypeName:   e.op.HandlerTypeName,
			HandlerMethodName: e.op.HandlerMethodName,
			QualifiedGoType:   e.op.Message.QualifiedGoType(),
			AddressRaw:        e.op.Channel.Address.Raw,
			AddressLiteralGo:  addrLiteral,
		})
	}
	gv.BindingKeysExprGo = strings.Join(keys, ", ")
	return gv, nil
}

// goExportIdentFromQueue derives an exported Go identifier from a queue
// name, for the combined multi-message Subscriber's method/interface name.
// Splits on any run of non-alphanumeric characters and upper-cases the
// first rune of each segment: "dm-builder-raw" → "DmBuilderRaw",
// "shared.status.worker" → "SharedStatusWorker".
func goExportIdentFromQueue(name string) string {
	var b strings.Builder
	newWord := true
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			newWord = true
			continue
		}
		if newWord {
			b.WriteRune(unicode.ToUpper(r))
			newWord = false
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
