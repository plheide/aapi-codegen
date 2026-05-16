package ir

import (
	"fmt"
	"regexp"
	"strings"
)

// paramNameRegexp validates channel-parameter names. The published Go
// publisher signature uses each parameter name verbatim as a function
// argument (`func SendX(ctx, tenant string, ...)`); without
// validation, names like `tenant.id` would emit a Go field-access
// expression and names like `1bad` would produce an illegal
// identifier — both compile errors downstream that surface as
// confusing gofmt failures rather than a clear spec-level error.
var paramNameRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseAddress parses an AsyncAPI channel address template into an
// Address. Parameters use `{name}` syntax; everything else is literal.
//
// Examples:
//
//	"widget.cancellation"     → Parts: [Literal("widget.cancellation")]
//	"{tenant}.{widgetType}"   → Parts: [Param("tenant"), Literal("."), Param("widgetType")]
//	"prefix.{a}.suffix.{b}"   → Parts: [Literal("prefix."), Param("a"), Literal(".suffix."), Param("b")]
//
// Nested braces and escaping are not supported — AsyncAPI 3.x doesn't
// define them. An unmatched `{` is an error.
func ParseAddress(s string) (Address, error) {
	addr := Address{Raw: s}
	var lit strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '{' {
			lit.WriteByte(c)
			continue
		}
		// Flush any literal accumulated up to this point.
		if lit.Len() > 0 {
			addr.Parts = append(addr.Parts, AddressPart{Literal: lit.String()})
			lit.Reset()
		}
		end := strings.IndexByte(s[i+1:], '}')
		if end < 0 {
			return Address{}, fmt.Errorf("address %q: unterminated `{`", s)
		}
		end += i + 1
		name := s[i+1 : end]
		if name == "" {
			return Address{}, fmt.Errorf("address %q: empty parameter name at offset %d", s, i)
		}
		if !paramNameRegexp.MatchString(name) {
			return Address{}, fmt.Errorf("address %q: parameter %q is not a valid Go identifier (must match %s)",
				s, name, paramNameRegexp.String())
		}
		addr.Parts = append(addr.Parts, AddressPart{Param: name})
		addr.Params = append(addr.Params, AddressParam{
			JSONName:  name,
			GoArgName: name,
			GoType:    "string",
		})
		i = end
	}
	if lit.Len() > 0 {
		addr.Parts = append(addr.Parts, AddressPart{Literal: lit.String()})
	}
	if len(addr.Parts) == 0 {
		return Address{}, fmt.Errorf("address %q: empty", s)
	}
	// Reject duplicate parameter names — Go forbids duplicate function
	// arguments, so `{a}.{a}` would emit `func(a string, a string)`.
	// Catching this here gives a clear spec-level error vs a gofmt one.
	seen := make(map[string]struct{}, len(addr.Params))
	for _, p := range addr.Params {
		if _, dup := seen[p.JSONName]; dup {
			return Address{}, fmt.Errorf("address %q: parameter %q declared more than once", s, p.JSONName)
		}
		seen[p.JSONName] = struct{}{}
	}
	return addr, nil
}

// RoutingKeyExpr renders the Go expression that constructs the routing
// key from the parsed address. For all-literal addresses, returns a Go
// string literal ("widget.cancellation"). For templated addresses,
// returns a `+`-concatenation of arg names and quoted literals (a +
// "." + b). Single-parameter addresses become just the arg name.
func (a Address) RoutingKeyExpr() string {
	if len(a.Parts) == 1 && !a.Parts[0].IsParam() {
		return fmt.Sprintf("%q", a.Parts[0].Literal)
	}
	if len(a.Parts) == 1 && a.Parts[0].IsParam() {
		return a.Parts[0].Param
	}
	pieces := make([]string, 0, len(a.Parts))
	for _, p := range a.Parts {
		if p.IsParam() {
			pieces = append(pieces, p.Param)
		} else {
			pieces = append(pieces, fmt.Sprintf("%q", p.Literal))
		}
	}
	return strings.Join(pieces, " + ")
}
