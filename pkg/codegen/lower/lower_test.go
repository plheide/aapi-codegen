package lower

import (
	"slices"
	"strings"
	"testing"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
	"github.com/plheide/aapi-codegen/pkg/codegen/loader"
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

// routingKeyBindings is a minimal valid AMQP channel binding.
var routingKeyBindings = map[string]any{
	"amqp": map[string]any{
		"is":             "routingKey",
		"exchange":       map[string]any{"name": "Orders", "type": "direct"},
		"bindingVersion": "0.3.0",
	},
}

// TestLowerChannel_ParameterTypes covers where a parameter's Go type
// comes from: the AsyncAPI 3.x `enum` on the parameter or a pattern in
// its `x-aapi-codegen`, or the 2.x `schema` with an `enum` or a
// `pattern`, which a 3.x document can't have but older specs use.
func TestLowerChannel_ParameterTypes(t *testing.T) {
	pattern := &loader.ParameterExtension{Pattern: "^[a-z]+$"}
	for name, c := range map[string]struct {
		param          *loader.Parameter
		omitValidation bool
		wantType       string   // the parameter's Go type
		wantValues     []string // the values of the enum named wantType, if any
		wantPattern    string   // the pattern of the type named wantType, if any
		wantChecked    bool     // whether Send and Subscribe check the value
	}{
		"3.x enum": {
			param:       &loader.Parameter{Enum: []any{"eu", "us"}},
			wantType:    "Region",
			wantValues:  []string{"eu", "us"},
			wantChecked: true,
		},
		"2.x schema.enum": {
			param:       &loader.Parameter{Schema: map[string]any{"type": "string", "enum": []any{"eu", "us"}}},
			wantType:    "Region",
			wantValues:  []string{"eu", "us"},
			wantChecked: true,
		},
		"both, where the 3.x enum wins": {
			param:       &loader.Parameter{Enum: []any{"eu", "us"}, Schema: map[string]any{"type": "string", "enum": []any{"apac"}}},
			wantType:    "Region",
			wantValues:  []string{"eu", "us"},
			wantChecked: true,
		},
		"an enum of numbers": {
			param:    &loader.Parameter{Enum: []any{1, 2}},
			wantType: "string",
		},
		"no enum": {
			param:    &loader.Parameter{Description: "Region."},
			wantType: "string",
		},
		"x-aapi-codegen.pattern": {
			param:       &loader.Parameter{XAAPICodegen: pattern},
			wantType:    "Region",
			wantPattern: "^[a-z]+$",
			wantChecked: true,
		},
		"2.x schema.pattern": {
			param:       &loader.Parameter{Schema: map[string]any{"type": "string", "pattern": "^[a-z]+$"}},
			wantType:    "Region",
			wantPattern: "^[a-z]+$",
			wantChecked: true,
		},
		"both patterns, where x-aapi-codegen wins": {
			param:       &loader.Parameter{XAAPICodegen: pattern, Schema: map[string]any{"type": "string", "pattern": "^[0-9]+$"}},
			wantType:    "Region",
			wantPattern: "^[a-z]+$",
			wantChecked: true,
		},
		"an enum and a pattern, where the enum wins": {
			param:       &loader.Parameter{Enum: []any{"eu", "us"}, XAAPICodegen: pattern},
			wantType:    "Region",
			wantValues:  []string{"eu", "us"},
			wantChecked: true,
		},
		"a pattern under omit-validation": {
			param:          &loader.Parameter{XAAPICodegen: pattern},
			omitValidation: true,
			wantType:       "string",
		},
		"an enum under omit-validation": {
			param:          &loader.Parameter{Enum: []any{"eu", "us"}},
			omitValidation: true,
			wantType:       "Region",
			wantValues:     []string{"eu", "us"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := &loader.Channel{
				Address:    "orders.{region}",
				Parameters: map[string]*loader.Parameter{"region": c.param},
				Bindings:   routingKeyBindings,
			}
			spec := &ir.Spec{}
			ch, err := lowerChannel("orders", raw, spec, c.omitValidation)
			if err != nil {
				t.Fatalf("lowerChannel: %v", err)
			}
			if got := ch.Address.Params[0].GoType; got != c.wantType {
				t.Errorf("GoType = %q, want %q", got, c.wantType)
			}
			var gotValues []string
			for _, e := range spec.ParameterEnums {
				if e.GoTypeName == c.wantType {
					gotValues = e.Values
				}
			}
			if !slices.Equal(gotValues, c.wantValues) {
				t.Errorf("enum values = %q, want %q", gotValues, c.wantValues)
			}
			var gotPattern string
			for _, p := range spec.ParameterPatterns {
				if p.GoTypeName == c.wantType {
					gotPattern = p.Pattern
				}
			}
			if gotPattern != c.wantPattern {
				t.Errorf("pattern = %q, want %q", gotPattern, c.wantPattern)
			}
			if got := ch.Address.Params[0].Checked; got != c.wantChecked {
				t.Errorf("Checked = %v, want %v", got, c.wantChecked)
			}
		})
	}
}
