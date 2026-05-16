package lower

import (
	"strings"
	"testing"
)

// TestLowerAMQPBinding_ErrorMessages locks down each of the three
// distinct failure modes for AMQP binding extraction. Before this
// distinction landed, all three reported "missing or non-object
// `bindings.amqp`" — which is misleading for the "no bindings at all"
// case (implies the AMQP entry is malformed when actually no bindings
// were declared).
func TestLowerAMQPBinding_ErrorMessages(t *testing.T) {
	cases := []struct {
		name     string
		bindings map[string]any
		wantSub  string
	}{
		{
			name:     "nil bindings block",
			bindings: nil,
			wantSub:  "channel has no `bindings` block",
		},
		{
			name:     "bindings present but no amqp entry",
			bindings: map[string]any{"kafka": map[string]any{}},
			wantSub:  "channel has `bindings` but no `amqp` entry",
		},
		{
			name:     "bindings.amqp is not an object",
			bindings: map[string]any{"amqp": "not-an-object"},
			wantSub:  "`bindings.amqp` is not an object",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := lowerAMQPBinding(c.bindings)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), c.wantSub)
			}
		})
	}
}

// TestLowerAMQPBinding_HappyPath confirms the success path still works
// — the new error branches above are easy to over-tighten.
func TestLowerAMQPBinding_HappyPath(t *testing.T) {
	bindings := map[string]any{
		"amqp": map[string]any{
			"exchange": map[string]any{
				"name": "TestExchange",
				"type": "direct",
			},
			"bindingVersion": "0.3.0",
		},
	}
	b, err := lowerAMQPBinding(bindings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Exchange != "TestExchange" {
		t.Errorf("Exchange: got %q, want TestExchange", b.Exchange)
	}
	if b.ExchangeType != "direct" {
		t.Errorf("ExchangeType: got %q, want direct", b.ExchangeType)
	}
	if b.BindingVersion != "0.3.0" {
		t.Errorf("BindingVersion: got %q, want 0.3.0", b.BindingVersion)
	}
}
