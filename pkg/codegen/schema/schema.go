// Package schema models the slice of JSON Schema 2020-12 that the
// compat-shim emitter cares about. Most of JSON Schema is irrelevant for
// shim emission — we only need the type, properties, required list, and
// $defs of every object. Anything else (numeric ranges, format,
// patternProperties, …) is left to go-jsonschema.
package schema

import (
	"encoding/json"
	"fmt"
	"os"
)

// Schema is the typed representation of a JSON Schema file. Fields not
// needed for shim emission are intentionally omitted; unknown properties
// in the input are dropped during decoding.
type Schema struct {
	// ID corresponds to the JSON Schema `$id` keyword. Used as the match
	// key for cross-tree package mappings (plan §6 schema-packages): when
	// a schema's $id matches an entry in Configuration.SchemaPackages,
	// its types live in another Go package and the compat-shim emitter
	// must skip them (Go forbids methods on imported types).
	ID         string             `json:"$id"`
	Title      string             `json:"title"`
	Type       string             `json:"type"`
	Required   []string           `json:"required"`
	Properties map[string]any     `json:"properties"`
	Defs       map[string]*Schema `json:"$defs"`
	// SourcePath is the absolute path the schema was loaded from. Used
	// only for error messages.
	SourcePath string `json:"-"`
}

// Load parses a JSON Schema file from disk.
func Load(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", path, err)
	}
	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse schema %s: %w", path, err)
	}
	s.SourcePath = path
	return &s, nil
}

// IsObject reports whether the schema describes a JSON object with named
// properties.
func (s *Schema) IsObject() bool {
	return s.Type == "object" && len(s.Properties) > 0
}
