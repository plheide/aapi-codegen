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

func TestAddress_TemplateExpr(t *testing.T) {
	addr, err := ParseAddress("{tenant}.{jobType}.orders")
	if err != nil {
		t.Fatal(err)
	}
	addr.Params[1].GoType = "JobType" // typed, as lowering does for an enum parameter
	cases := []struct {
		tmpl string
		want string
	}{
		// No parameters: a quoted literal, as before.
		{"audit-log", `"audit-log"`},
		// The address itself: the routing-key expression.
		{"{tenant}.{jobType}.orders", addr.RoutingKeyExpr()},
		// A subset of the parameters, with their own literals.
		{"{tenant}.audit-log", `tenant + ".audit-log"`},
		// A typed parameter keeps its string() cast.
		{"archive.{jobType}", `"archive." + string(jobType)`},
		// A parameter may appear more than once in a template.
		{"{tenant}.{tenant}", `tenant + "." + tenant`},
	}
	for _, c := range cases {
		got, err := addr.TemplateExpr(c.tmpl)
		if err != nil {
			t.Errorf("TemplateExpr(%q) error: %v", c.tmpl, err)
			continue
		}
		if got != c.want {
			t.Errorf("TemplateExpr(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

func TestAddress_TemplateExpr_Errors(t *testing.T) {
	addr, err := ParseAddress("{tenant}.orders")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		tmpl    string
		wantSub string
	}{
		{"{region}.audit-log", `parameter "region" is not in address "{tenant}.orders"`},
		{"{tenant.audit-log", "unterminated"},
		{"{}.audit-log", "empty parameter name"},
	}
	for _, c := range cases {
		_, err := addr.TemplateExpr(c.tmpl)
		if err == nil {
			t.Errorf("TemplateExpr(%q): expected error, got nil", c.tmpl)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("TemplateExpr(%q): error %q does not contain %q", c.tmpl, err.Error(), c.wantSub)
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
