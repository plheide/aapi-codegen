package codegen

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
)

// RenderParameterEnums emits one `type <T> string` + `const ( ... )`
// block per channel-parameter enum the spec declared, plus one `type
// <T> string` + `var <t>Pattern = regexp.MustCompile(...)` + `New<T>` +
// `Must<T>` block per pattern-validated parameter (v0.4.1+).
//
// Returns "" when the spec has no parameter types — caller skips
// emission. The block intentionally precedes the publisher / subscriber
// sections so generated Send / Subscribe signatures can reference the
// types without forward declarations.
//
// Naming: GoTypeName comes from the lowerer (pascalized parameter
// name). Enum constants are <TypeName><Pascalized(value)>. Pattern
// types ship a regex var (private, named `<lowercasedType>Pattern`),
// a returning constructor (`New<TypeName>` — error on mismatch), and
// a panicking convenience (`Must<TypeName>` — for compile-time-known
// constants).
func RenderParameterEnums(spec *ir.Spec) (string, error) {
	if len(spec.ParameterEnums) == 0 && len(spec.ParameterPatterns) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, e := range spec.ParameterEnums {
		fmt.Fprintf(&b, "// %s is the typed value for the AsyncAPI channel parameter\n", e.GoTypeName)
		fmt.Fprintf(&b, "// declared with an `enum` list. It is the Go argument type on every\n")
		fmt.Fprintf(&b, "// Publisher / Subscriber method that takes this parameter.\n")
		fmt.Fprintf(&b, "type %s string\n\n", e.GoTypeName)
		b.WriteString("const (\n")
		for _, v := range e.Values {
			fmt.Fprintf(&b, "\t%s%s %s = %q\n", e.GoTypeName, pascalizeEnumValue(v), e.GoTypeName, v)
		}
		b.WriteString(")\n\n")
		fmt.Fprintf(&b, "// %sValues returns every %s value, in the order the spec lists them.\n", e.GoTypeName, e.GoTypeName)
		fmt.Fprintf(&b, "func %sValues() []%s {\n", e.GoTypeName, e.GoTypeName)
		fmt.Fprintf(&b, "\treturn []%s{\n", e.GoTypeName)
		for _, v := range e.Values {
			fmt.Fprintf(&b, "\t\t%s%s,\n", e.GoTypeName, pascalizeEnumValue(v))
		}
		b.WriteString("\t}\n}\n\n")
		fmt.Fprintf(&b, "// Valid reports whether v is one of the %s values. A conversion such\n", e.GoTypeName)
		fmt.Fprintf(&b, "// as %s(s) compiles for any string, so the Publisher and Subscriber\n", e.GoTypeName)
		fmt.Fprintf(&b, "// methods that take a %s check it, unless the spec omits validation.\n", e.GoTypeName)
		fmt.Fprintf(&b, "func (v %s) Valid() bool {\n", e.GoTypeName)
		b.WriteString("\tswitch v {\n\tcase ")
		for i, v := range e.Values {
			if i > 0 {
				b.WriteString(",\n\t\t")
			}
			fmt.Fprintf(&b, "%s%s", e.GoTypeName, pascalizeEnumValue(v))
		}
		b.WriteString(":\n\t\treturn true\n\t}\n\treturn false\n}\n\n")
	}
	for _, p := range spec.ParameterPatterns {
		varName := lowercaseFirst(p.GoTypeName) + "Pattern"
		fmt.Fprintf(&b, "// %s is the typed value for the AsyncAPI channel parameter\n", p.GoTypeName)
		fmt.Fprintf(&b, "// declared with the pattern `%s`. Construct via New%s\n", goCommentEscape(p.Pattern), p.GoTypeName)
		fmt.Fprintf(&b, "// (returns an error on pattern mismatch) or Must%s (panics — for\n", p.GoTypeName)
		fmt.Fprintf(&b, "// compile-time-known constants such as tests or boot-time config).\n")
		fmt.Fprintf(&b, "type %s string\n\n", p.GoTypeName)
		fmt.Fprintf(&b, "var %s = regexp.MustCompile(%s)\n\n", varName, backtickQuote(p.Pattern))
		fmt.Fprintf(&b, "// New%s checks v against the spec's pattern and returns it as a\n", p.GoTypeName)
		fmt.Fprintf(&b, "// %s. A conversion such as %s(s) skips the check, but the\n", p.GoTypeName, p.GoTypeName)
		fmt.Fprintf(&b, "// Publisher and Subscriber methods that take a %s check it again.\n", p.GoTypeName)
		fmt.Fprintf(&b, "func New%s(v string) (%s, error) {\n", p.GoTypeName, p.GoTypeName)
		fmt.Fprintf(&b, "\tif !%s.MatchString(v) {\n", varName)
		fmt.Fprintf(&b, "\t\treturn \"\", fmt.Errorf(\"%s: %%q does not match pattern %%q\", v, %s.String())\n", p.GoTypeName, varName)
		fmt.Fprintf(&b, "\t}\n")
		fmt.Fprintf(&b, "\treturn %s(v), nil\n", p.GoTypeName)
		fmt.Fprintf(&b, "}\n\n")
		fmt.Fprintf(&b, "// Must%s is the panic-on-error sibling of New%s, named after the\n", p.GoTypeName, p.GoTypeName)
		fmt.Fprintf(&b, "// stdlib convention (regexp.MustCompile, template.Must). Use only\n")
		fmt.Fprintf(&b, "// for inputs known to be valid at compile time.\n")
		fmt.Fprintf(&b, "func Must%s(v string) %s {\n", p.GoTypeName, p.GoTypeName)
		fmt.Fprintf(&b, "\tout, err := New%s(v)\n", p.GoTypeName)
		fmt.Fprintf(&b, "\tif err != nil { panic(err) }\n")
		fmt.Fprintf(&b, "\treturn out\n")
		fmt.Fprintf(&b, "}\n\n")
		fmt.Fprintf(&b, "// Valid reports whether v matches the spec's pattern.\n")
		fmt.Fprintf(&b, "func (v %s) Valid() bool {\n", p.GoTypeName)
		fmt.Fprintf(&b, "\treturn %s.MatchString(string(v))\n", varName)
		fmt.Fprintf(&b, "}\n\n")
	}
	return b.String(), nil
}

// goCommentEscape neutralises sequences that would close a Go comment.
// AsyncAPI patterns occasionally use `*/` inside character classes;
// surfacing the raw value in a `//` comment is fine, but `/* … */`
// blocks would break. We only emit `//` comments here, so the only
// concern is preventing the user's eyes from parsing a stray `*/` as
// significant. Belt-and-braces: drop tabs/newlines too.
func goCommentEscape(s string) string {
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// backtickQuote returns a Go raw-string literal of pat. AsyncAPI regex
// patterns commonly contain backslashes (`\d`, `\w`) that would need
// double-escaping inside a "..." string literal. Raw strings sidestep
// that. The only character that can't appear inside a raw string is
// the backtick itself; fall back to a strconv.Quote'd form when pat
// contains one (rare enough to be worth the fallback rather than the
// cost of perpetually-escaped patterns).
func backtickQuote(pat string) string {
	if !strings.Contains(pat, "`") {
		return "`" + pat + "`"
	}
	return strconv.Quote(pat)
}

func lowercaseFirst(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
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
