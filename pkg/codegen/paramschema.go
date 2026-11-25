package codegen

import (
	"maps"
	"slices"

	"github.com/plheide/aapi-codegen/pkg/codegen/loader"
)

// parameterSchemaWarnings returns a warning for each channel parameter
// that declares a schema, the AsyncAPI 2.x shape of a Parameter Object.
// A 3.x document can't have it, and asyncapi validate rejects the spec.
// Its enum or pattern still types a parameter that declares neither the
// 3.x way, so the warning names the 3.x way; a parameter that does
// declare one ignores its schema, and the warning says so.
func parameterSchemaWarnings(doc *loader.Document) []string {
	var warnings []string
	for _, chName := range slices.Sorted(maps.Keys(doc.Channels)) {
		ch := doc.Channels[chName]
		if ch == nil {
			continue
		}
		for _, pName := range slices.Sorted(maps.Keys(ch.Parameters)) {
			p := ch.Parameters[pName]
			if p == nil || p.Schema == nil {
				continue
			}
			what := "declare enum on the parameter, or x-aapi-codegen.pattern for a pattern"
			if p.HasOwnType() {
				what = "it is ignored, since the parameter declares its own enum or x-aapi-codegen.pattern"
			}
			warnings = append(warnings, "channels."+chName+".parameters."+pName+": schema is the AsyncAPI 2.x form, which a 3.x document can't have; "+what)
		}
	}
	return warnings
}
