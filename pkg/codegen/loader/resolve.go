package loader

import (
	"fmt"
	"strings"
)

const componentMessageRefPrefix = "#/components/messages/"

// ResolveMessageRefs walks every channel-level message slot and follows
// any `$ref` pointing into `components.messages`, inlining the
// referenced component into the slot. After this call, every
// channel.messages[X] is a fully-populated Message struct;
// CanonicalName is set on slots that were resolved from a component,
// so the materializer can dedupe shared messages (multiple channels
// referencing the same component produce one synthetic schema file
// and therefore one Go type).
//
// Idempotent: calling twice is a no-op because Ref is cleared after
// inlining and a slot with empty Ref is skipped.
//
// Operation-level message refs (`operations.X.messages[].$ref`)
// support both forms after this resolver runs:
//
//   - channel-scoped:   `#/channels/<chan>/messages/<key>` (passes through)
//   - component-scoped: `#/components/messages/<Name>`
//
// Component-scoped refs are rewritten to channel-scoped form by
// finding the unique channel-level slot whose CanonicalName matches.
// The operation's own `channel.$ref` constrains which channel to look
// in, so a component referenced from multiple channels resolves
// unambiguously per operation.
func (d *Document) ResolveMessageRefs() error {
	// Channel-level refs first — sets CanonicalName on every slot
	// resolved from a component, which the operation-level pass below
	// uses to disambiguate.
	for chName, ch := range d.Channels {
		for msgKey, msg := range ch.Messages {
			if msg.Ref == "" {
				continue
			}
			if err := d.inlineMessageRef(chName, msgKey, msg); err != nil {
				return err
			}
		}
	}
	// Operation-level refs: rewrite component-scoped to channel-scoped.
	for opName, op := range d.Operations {
		for i, mr := range op.Messages {
			if !strings.HasPrefix(mr.Ref, componentMessageRefPrefix) {
				continue
			}
			rewritten, err := d.rewriteOpMessageRef(opName, op, mr.Ref)
			if err != nil {
				return err
			}
			op.Messages[i].Ref = rewritten
		}
	}
	return nil
}

// rewriteOpMessageRef finds the channel-scoped equivalent of a
// component-scoped operation message ref. The operation's
// `channel.$ref` constrains the search space — exactly one of that
// channel's message slots must have CanonicalName matching the
// referenced component, otherwise the resolver errors clearly so the
// spec author can disambiguate.
func (d *Document) rewriteOpMessageRef(opName string, op *Operation, ref string) (string, error) {
	const channelRefPrefix = "#/channels/"
	if !strings.HasPrefix(op.Channel.Ref, channelRefPrefix) {
		return "", fmt.Errorf("operation %q: channel.$ref %q must be of the form %s<name>", opName, op.Channel.Ref, channelRefPrefix)
	}
	chName := strings.TrimPrefix(op.Channel.Ref, channelRefPrefix)
	if strings.Contains(chName, "/") || chName == "" {
		return "", fmt.Errorf("operation %q: channel.$ref %q has unexpected trailing path", opName, op.Channel.Ref)
	}
	ch, ok := d.Channels[chName]
	if !ok {
		return "", fmt.Errorf("operation %q: channel %q not declared", opName, chName)
	}

	componentName := strings.TrimPrefix(ref, componentMessageRefPrefix)
	var matchKey string
	for slotKey, slot := range ch.Messages {
		if slot.CanonicalName != componentName {
			continue
		}
		if matchKey != "" {
			return "", fmt.Errorf("operation %q: component-scoped $ref %q is ambiguous on channel %q (multiple slots resolve to the same component: %q and %q); use the channel-scoped form to disambiguate",
				opName, ref, chName, matchKey, slotKey)
		}
		matchKey = slotKey
	}
	if matchKey == "" {
		return "", fmt.Errorf("operation %q: component-scoped $ref %q is not declared on channel %q (no message slot resolves to this component); add a channel-level alias or use the channel-scoped form",
			opName, ref, chName)
	}
	return channelRefPrefix + chName + "/messages/" + matchKey, nil
}

func (d *Document) inlineMessageRef(chName, msgKey string, msg *Message) error {
	if !strings.HasPrefix(msg.Ref, componentMessageRefPrefix) {
		// Cross-file refs (e.g. `../path/spec.yaml#/channels/X/messages/Y`)
		// and any other shape pass through to the lowerer, which
		// consults x-aapi-codegen.message-packages to resolve them to
		// an imported Go type. v0.5+.
		return nil
	}
	name := msg.Ref[len(componentMessageRefPrefix):]
	if d.Components == nil || d.Components.Messages == nil {
		return fmt.Errorf("channel %q message %q: $ref %q but no components.messages declared",
			chName, msgKey, msg.Ref)
	}
	component, ok := d.Components.Messages[name]
	if !ok {
		return fmt.Errorf("channel %q message %q: $ref %q targets undeclared components.messages.%s",
			chName, msgKey, msg.Ref, name)
	}
	// Reject the "partial override" case — a slot with $ref shouldn't
	// also carry inline fields. Allowing it would silently merge in
	// confusing ways (which fields win?), and the AsyncAPI spec itself
	// is ambiguous about it. Force the author to pick one form.
	if msg.Name != "" || msg.Title != "" || msg.Summary != "" || msg.ContentType != "" || msg.Payload.Ref != "" || len(msg.Payload.Inline) > 0 {
		return fmt.Errorf("channel %q message %q: $ref combined with inline fields is ambiguous; pick one form",
			chName, msgKey)
	}
	// Components are leaves — a component referencing another
	// component would be valid AsyncAPI but adds resolution complexity
	// we don't need yet.
	if component.Ref != "" {
		return fmt.Errorf("components.messages.%s: nested $ref not supported", name)
	}
	msg.Name = component.Name
	msg.Title = component.Title
	msg.Summary = component.Summary
	msg.ContentType = component.ContentType
	msg.Payload = component.Payload
	msg.Ref = ""             // mark resolved (idempotency)
	msg.CanonicalName = name // drives materializer dedup
	return nil
}
