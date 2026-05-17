package codegen

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
)

// RenderParameterEnums emits one `type <T> string` + `const ( ... )`
// block per channel-parameter enum the spec declared. Returns "" when
// the spec has none — caller skips emission. The block intentionally
// precedes the publisher / subscriber sections so generated Send /
// Subscribe signatures can reference the enum types without forward
// declarations (Go allows it either way, but ordering helps readers).
//
// Naming: GoTypeName comes from the lowerer (pascalized parameter
// name); constants are <TypeName><Pascalized(value)>. Values that
// contain runes invalid in Go identifiers are sanitised to '_'.
func RenderParameterEnums(spec *ir.Spec) (string, error) {
	if len(spec.ParameterEnums) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("\n// ---------- aapi-codegen parameter enums (generated, DO NOT EDIT) ----------\n\n")
	for _, e := range spec.ParameterEnums {
		fmt.Fprintf(&b, "// %s is the typed value for the AsyncAPI channel parameter\n", e.GoTypeName)
		fmt.Fprintf(&b, "// declared with `schema.type: string` and an `enum` list — used as the\n")
		fmt.Fprintf(&b, "// Go argument type on every Publisher / Subscriber method that takes\n")
		fmt.Fprintf(&b, "// this parameter, so callers cannot pass an unrecognised value.\n")
		fmt.Fprintf(&b, "type %s string\n\n", e.GoTypeName)
		b.WriteString("const (\n")
		for _, v := range e.Values {
			fmt.Fprintf(&b, "\t%s%s %s = %q\n", e.GoTypeName, pascalizeEnumValue(v), e.GoTypeName, v)
		}
		b.WriteString(")\n\n")
	}
	return b.String(), nil
}

// pascalizeEnumValue turns an enum value like "build" / "deploy-prod" /
// "v2.0" into the pascal-case suffix Go const naming wants
// ("Build", "DeployProd", "V20"). Non-identifier runes become word
// boundaries; the result is guaranteed to start with an uppercase
// ASCII letter (prepending 'X' if the cleaned value would otherwise
// start with a digit).
func pascalizeEnumValue(v string) string {
	var b strings.Builder
	upper := true
	for _, r := range v {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if upper {
				b.WriteRune(unicode.ToUpper(r))
				upper = false
			} else {
				b.WriteRune(r)
			}
		default:
			upper = true
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	out := b.String()
	if r := []rune(out)[0]; r >= '0' && r <= '9' {
		return "X" + out
	}
	return out
}
