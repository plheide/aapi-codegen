package ir

import (
	"strings"
	"testing"
)

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in        string
		wantParts int
		wantExpr  string
	}{
		// Single literal — fixed routing key.
		{"widget.cancellation", 1, `"widget.cancellation"`},
		// Two-param templated — the M3 marquee case.
		{"{tenant}.{widgetType}", 3, `tenant + "." + widgetType`},
		// Single param — degenerate templated.
		{"{workflowName}", 1, `workflowName`},
		// Mixed prefix + param + suffix + param.
		{"prefix.{a}.suffix.{b}", 4, `"prefix." + a + ".suffix." + b`},
	}
	for _, c := range cases {
		got, err := ParseAddress(c.in)
		if err != nil {
			t.Errorf("ParseAddress(%q) error: %v", c.in, err)
			continue
		}
		if len(got.Parts) != c.wantParts {
			t.Errorf("ParseAddress(%q): want %d parts, got %d (%+v)", c.in, c.wantParts, len(got.Parts), got.Parts)
		}
		if expr := got.RoutingKeyExpr(); expr != c.wantExpr {
			t.Errorf("RoutingKeyExpr(%q) = %q, want %q", c.in, expr, c.wantExpr)
		}
	}
}

func TestParseAddress_Errors(t *testing.T) {
	// wantSub is a substring the returned error must contain. Catches a
	// regression where the WRONG error message gets returned for a given
	// input (e.g. unterminated-{ case returning "empty parameter name").
	cases := []struct {
		in      string
		wantSub string
	}{
		{"", "empty"},
		{"{", "unterminated"},
		{"foo.{}", "empty parameter name"},
		{"{abc", "unterminated"},
		// Parameter names that aren't Go identifiers — these would
		// emit broken Go source if not caught at parse time.
		{"{tenant.id}", "not a valid Go identifier"}, // dot → field-access expr
		{"{1bad}", "not a valid Go identifier"},      // leading digit
		{"{a-b}", "not a valid Go identifier"},       // dash → subtraction expr
		{"{}", "empty parameter name"},               // duplicate of foo.{} root cause but tighter
		// Duplicate parameter names — Go forbids duplicate function args.
		{"{a}.{a}", "declared more than once"},
		{"{a}.{b}.{a}", "declared more than once"},
	}
	for _, c := range cases {
		_, err := ParseAddress(c.in)
		if err == nil {
			t.Errorf("ParseAddress(%q): expected error, got nil", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("ParseAddress(%q): error %q does not contain %q", c.in, err.Error(), c.wantSub)
		}
	}
}
