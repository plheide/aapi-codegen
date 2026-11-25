package codegen

import (
	"maps"
	"slices"

	"github.com/plheide/aapi-codegen/pkg/codegen/loader"
)

// parameterSchemaWarnings returns a warning for each channel parameter
// that declares a schema, the AsyncAPI 2.x shape of a Parameter Object.
// Its enum or pattern still types the parameter, but a 3.x document
// can't have it, and asyncapi validate rejects the spec, so the warning
// names the 3.x way to declare them.
func parameterSchemaWarnings(doc *loader.Document) []string {
	var warnings []string
	for _, chName := range slices.Sorted(maps.Keys(doc.Channels)) {
		ch := doc.Channels[chName]
		if ch == nil {
			continue
		}
		for _, pName := range slices.Sorted(maps.Keys(ch.Parameters)) {
			if p := ch.Parameters[pName]; p != nil && p.Schema != nil {
				warnings = append(warnings, "channels."+chName+".parameters."+pName+": schema is the AsyncAPI 2.x form, which a 3.x document can't have; declare enum on the parameter, or x-aapi-codegen.pattern for a pattern")
			}
		}
	}
	return warnings
}
