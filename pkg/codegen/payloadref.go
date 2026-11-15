package codegen

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/plheide/aapi-codegen/pkg/codegen/loader"
)

// annotationKeywords don't constrain a value, so nothing is lost when they
// stand beside a $ref. go-jsonschema leaves the same kind of keyword out of
// its warning about keywords beside a $ref.
var annotationKeywords = map[string]bool{
	"$comment":    true,
	"$defs":       true,
	"$id":         true,
	"$schema":     true,
	"default":     true,
	"definitions": true,
	"deprecated":  true,
	"description": true,
	"examples":    true,
	"readOnly":    true,
	"title":       true,
	"writeOnly":   true,
}

// payloadRefSiblingWarnings returns a warning for each message payload that
// is a $ref with keywords beside it that may reject a value, such as
// required. They are ignored: the payload's type is the type the $ref
// points at. aapi-codegen resolves a payload's own $ref itself, so these
// keywords never reach go-jsonschema, which warns about keywords beside a
// $ref everywhere else. A component message that several channels use is
// reported once.
func payloadRefSiblingWarnings(doc *loader.Document) []string {
	var warnings []string
	seen := make(map[string]bool)
	for _, chName := range slices.Sorted(maps.Keys(doc.Channels)) {
		ch := doc.Channels[chName]
		if ch == nil {
			continue
		}
		for _, msgName := range slices.Sorted(maps.Keys(ch.Messages)) {
			msg := ch.Messages[msgName]
			if msg == nil || msg.Ref != "" || msg.Payload.Ref == "" {
				continue
			}
			where := "channels." + chName + ".messages." + msgName + ".payload"
			if msg.CanonicalName != "" {
				where = "components.messages." + msg.CanonicalName + ".payload"
			}
			if seen[where] {
				continue
			}
			seen[where] = true
			if keys := constrainingSiblings(doc, msg.Payload); len(keys) > 0 {
				warnings = append(warnings, "schema fidelity: "+where+" does not enforce all it declares: "+strings.Join(keys, ", ")+" beside $ref not checked")
			}
		}
	}
	return warnings
}

// constrainingSiblings returns, sorted, the keywords beside p's $ref that
// may reject a value. A type that the $ref's target declares as well adds
// nothing and is left out, as go-jsonschema does.
func constrainingSiblings(doc *loader.Document, p loader.Payload) []string {
	var keys []string
	for k, v := range p.Inline {
		if annotationKeywords[k] || strings.HasPrefix(k, "x-") {
			continue
		}
		if k == "type" && reflect.DeepEqual(v, refTargetType(doc, p.Ref)) {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// refTargetType returns the type declared by the schema that ref points at:
// a components schema, or a schema file relative to the spec. It returns nil
// where it can't tell, such as for a ref into part of a file.
func refTargetType(doc *loader.Document, ref string) any {
	if name, ok := strings.CutPrefix(ref, "#/components/schemas/"); ok {
		if doc.Components == nil {
			return nil
		}
		target, _ := doc.Components.Schemas[name].(map[string]any)
		return target["type"]
	}
	if strings.Contains(ref, "#") {
		return nil
	}
	path := ref
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(doc.SourcePath), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var target map[string]any
	if err := yaml.Unmarshal(data, &target); err != nil {
		return nil
	}
	return target["type"]
}
