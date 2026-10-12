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
//   - for a queue shared by several receive ops (v0.7), one
//     <MessageName>Handler interface per message type, one combined
//     <QueueName>Handler interface embedding them, and one
//     Subscribe<QueueName> method that binds all keys and dispatches by
//     routing key (.MultiGroups). Routing keys that carry the same
//     message type share one switch case.
//
// Each <MessageName>Handler interface is declared once per file, at its
// first use in render order, however many operations receive that type.
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
{{- if .EmitHandler }}
// {{.HandlerTypeName}} consumes {{.Message.QualifiedGoType}} messages.
// Implement on your consumer; the generated Subscribe wrapper takes care
// of JSON-decoding the body and mapping handler errors to ack semantics
// per ErrDrop.
type {{.HandlerTypeName}} interface {
	{{.HandlerMethodName}}(ctx context.Context, msg {{.Message.QualifiedGoType}}) error
}
{{ end }}
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
{{- range .HandlerDecls }}
// {{.HandlerTypeName}} consumes {{.QualifiedGoType}} messages (routing {{.RoutingKeyWord}}
// {{.RoutingKeysDoc}}) delivered to the shared queue. Implement on your
// consumer; the generated dispatch decodes the body and maps handler
// errors to ack semantics per ErrDrop.
type {{.HandlerTypeName}} interface {
	{{.HandlerMethodName}}(ctx context.Context, msg {{.QualifiedGoType}}) error
}
{{ end }}
// {{.HandlerTypeName}} aggregates the per-message handlers for the shared
// queue "{{.QueueNameExpr}}". {{.CarriesDoc}}; {{.GoFuncName}} routes each delivery to the matching
// method below by its routing key.
type {{.HandlerTypeName}} interface {
{{- range .Embeds }}
	{{.}}
{{- end }}
}

// {{.GoFuncName}} starts consuming the shared queue "{{.QueueNameExpr}}",
// {{.BoundDoc}}, and dispatches each delivery
// by its routing key to the matching handler method. Blocks until ctx is
// cancelled or a fatal transport error occurs.
func (s *Subscriber) {{.GoFuncName}}(ctx context.Context, handler {{.HandlerTypeName}}) error {
	queueName := {{.QueueNameExprGo}}
	bindingKeys := []string{ {{.BindingKeysExprGo}} }
	return s.transport.Subscribe(ctx, queueName, bindingKeys, func(ctx context.Context, routingKey string, body []byte) error {
		switch routingKey {
{{- range .Cases }}
		case {{.KeysGo}}:
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
	// EmitHandler is true when this operation declares its handler
	// interface: the first use of the interface in render order.
	EmitHandler bool
}

type subscriberQueueView struct {
	// NameExpr is the queue name template in human-readable form for
	// the generated-code comment ("{tenant}.OrderProcessor").
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
	// Go identifier (e.g. queue "order-events-raw" → SubscribeOrderEventsRaw).
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
	// operation's binding key (each channel's address routing-key literal),
	// in operation (alphabetical) order.
	BindingKeysExprGo string
	// CarriesDoc and BoundDoc are the doc-comment phrases that change when
	// a message type arrives on more than one of the queue's routing keys.
	CarriesDoc string
	BoundDoc   string
	// HandlerDecls are the per-message handler interfaces this group
	// declares: one per distinct message type, minus any already declared
	// earlier in render order.
	HandlerDecls []groupHandlerDecl
	// Embeds are the distinct per-message handler interfaces the combined
	// <Queue>Handler embeds, in first-seen order.
	Embeds []string
	// Cases are the routing-key switch cases: one per distinct message
	// type, listing every routing key that carries it.
	Cases []groupCase
}

// groupHandlerDecl is one per-message handler interface a shared-queue
// group declares.
type groupHandlerDecl struct {
	// OpName is the first operation in the group that receives this
	// message type; error messages name it.
	OpName            string
	HandlerTypeName   string
	HandlerMethodName string
	QualifiedGoType   string
	// RoutingKeyWord is "key" or "keys"; RoutingKeysDoc lists the quoted
	// routing keys that carry this type, for the doc comment.
	RoutingKeyWord string
	RoutingKeysDoc string
}

// groupCase is one case of a shared-queue subscriber's routing-key switch.
type groupCase struct {
	// KeysGo is the comma-joined quoted Go literals of every routing key
	// that carries this message type (fixed addresses only — multi-message
	// grouping requires fixed routing keys).
	KeysGo            string
	QualifiedGoType   string
	HandlerMethodName string
}

// Doc-comment phrases for a shared-queue group. The "distinct" pair keeps
// the v0.7 wording, so groups whose routing keys each carry a different
// message type generate byte-for-byte as before.
const (
	carriesDocDistinct = "The queue carries several message types (one\n// per binding key)"
	carriesDocShared   = "The queue is bound to several routing keys, some\n// carrying the same message type"
	boundDocDistinct   = "bound with one binding key per message type"
	boundDocShared     = "bound with each operation's routing key"
)

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
	if err := assignHandlerDecls(&view); err != nil {
		return "", err
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

// assignHandlerDecls makes each <MessageName>Handler interface appear once
// per file: at its first use in render order (single-queue operations,
// then shared-queue groups), however many operations receive that message
// type. It rejects the specs that would otherwise generate Go that doesn't
// compile:
//   - two operations whose message types differ but map to the same
//     handler name (e.g. one type name imported from two packages);
//   - two single-queue operations on the same message type, which would
//     both need Subscribe<MessageName>.
func assignHandlerDecls(view *subscriberView) error {
	type origin struct{ op, goType string }
	declared := make(map[string]origin)
	byFunc := make(map[string]subscriberOpView)
	for i := range view.Operations {
		o := &view.Operations[i]
		goType := o.Message.QualifiedGoType()
		if prev, ok := declared[o.HandlerTypeName]; ok && prev.goType != goType {
			return sameHandlerNameError(o.HandlerTypeName, prev.op, prev.goType, o.Name, goType)
		}
		if prev, ok := byFunc[o.GoFuncName]; ok {
			return fmt.Errorf("operations %q and %q both receive %s from separate queues (%q and %q), so both would need %s; bind their routing keys to one shared queue (the same bindings.amqp.queue.name or bindings.x-aapi-codegen.queue.name) to get one combined subscriber", prev.Name, o.Name, goType, prev.Queue.NameExpr, o.Queue.NameExpr, o.GoFuncName)
		}
		byFunc[o.GoFuncName] = *o
		declared[o.HandlerTypeName] = origin{op: o.Name, goType: goType}
		o.EmitHandler = true
	}
	for gi := range view.MultiGroups {
		g := &view.MultiGroups[gi]
		kept := g.HandlerDecls[:0]
		for _, d := range g.HandlerDecls {
			if prev, ok := declared[d.HandlerTypeName]; ok {
				if prev.goType != d.QualifiedGoType {
					return sameHandlerNameError(d.HandlerTypeName, prev.op, prev.goType, d.OpName, d.QualifiedGoType)
				}
				continue // declared earlier in the file
			}
			declared[d.HandlerTypeName] = origin{op: d.OpName, goType: d.QualifiedGoType}
			kept = append(kept, d)
		}
		g.HandlerDecls = kept
	}
	return nil
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
	// One slot per distinct message type (keyed by handler name), in
	// first-seen order, collecting every routing key that carries it.
	type typeSlot struct {
		decl groupHandlerDecl
		raw  []string // addresses, for the doc comment
		lits []string // quoted literals, for the switch case
	}
	var slots []*typeSlot
	byHandler := make(map[string]*typeSlot, len(group))
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

		goType := e.op.Message.QualifiedGoType()
		if s, ok := byHandler[e.op.HandlerTypeName]; ok {
			if s.decl.QualifiedGoType != goType {
				return subscriberGroupView{}, sameHandlerNameError(e.op.HandlerTypeName, s.decl.OpName, s.decl.QualifiedGoType, e.op.Name, goType)
			}
			s.raw = append(s.raw, e.op.Channel.Address.Raw)
			s.lits = append(s.lits, addrLiteral)
			continue
		}
		s := &typeSlot{
			decl: groupHandlerDecl{
				OpName:            e.op.Name,
				HandlerTypeName:   e.op.HandlerTypeName,
				HandlerMethodName: e.op.HandlerMethodName,
				QualifiedGoType:   goType,
			},
			raw:  []string{e.op.Channel.Address.Raw},
			lits: []string{addrLiteral},
		}
		byHandler[e.op.HandlerTypeName] = s
		slots = append(slots, s)
	}
	gv.BindingKeysExprGo = strings.Join(keys, ", ")

	gv.CarriesDoc, gv.BoundDoc = carriesDocDistinct, boundDocDistinct
	for _, s := range slots {
		s.decl.RoutingKeyWord = "key"
		if len(s.raw) > 1 {
			s.decl.RoutingKeyWord = "keys"
			gv.CarriesDoc, gv.BoundDoc = carriesDocShared, boundDocShared
		}
		quoted := make([]string, len(s.raw))
		for i, r := range s.raw {
			quoted[i] = `"` + r + `"`
		}
		s.decl.RoutingKeysDoc = strings.Join(quoted, ", ")
		gv.HandlerDecls = append(gv.HandlerDecls, s.decl)
		gv.Embeds = append(gv.Embeds, s.decl.HandlerTypeName)
		gv.Cases = append(gv.Cases, groupCase{
			KeysGo:            strings.Join(s.lits, ", "),
			QualifiedGoType:   s.decl.QualifiedGoType,
			HandlerMethodName: s.decl.HandlerMethodName,
		})
	}
	return gv, nil
}

// sameHandlerNameError reports two receive operations whose message types
// differ but map to the same handler interface name (for example the same
// type name imported from two packages), which one file can't declare twice.
func sameHandlerNameError(handler, opA, typeA, opB, typeB string) error {
	return fmt.Errorf("operations %q and %q receive different message types (%s and %s) that both map to the handler interface %s; aapi-codegen names handlers after the message type name, so it can't generate both", opA, opB, typeA, typeB, handler)
}

// goExportIdentFromQueue derives an exported Go identifier from a queue
// name, for the combined multi-message Subscriber's method/interface name.
// Splits on any run of non-alphanumeric characters and upper-cases the
// first rune of each segment: "order-events-raw" → "OrderEventsRaw",
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
