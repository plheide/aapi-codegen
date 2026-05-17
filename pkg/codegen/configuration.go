package codegen

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Configuration is the YAML-driven config aapi-codegen reads via the
// CLI's -config flag. Mirrors oapi-codegen's pattern: top-level package
// + output, plus an aapi-codegen-specific schema-packages block that
// absorbs the cross-tree $ref mapping today's per-schema-dir generate.sh
// files express as repeated --schema-package CLI args. See plan §6.
//
// Fields not set in YAML default to zero; CLI flags supersede YAML
// values when both are present (the precedence is established by
// MergeFlags).
type Configuration struct {
	Package         string                  `yaml:"package"`
	Output          string                  `yaml:"output"`
	SchemaPackages  []SchemaPackageMapping  `yaml:"schema-packages"`
	MessagePackages []MessagePackageMapping `yaml:"message-packages"`
	// Capitalization mirrors go-jsonschema's --capitalization flag list.
	// When empty, DefaultInitialisms is applied.
	Capitalization []string `yaml:"capitalization"`
	// OmitValidation opts out of go-jsonschema's generated UnmarshalJSON
	// methods. Default false (validation included). Set true only when
	// consumers will hand-write their own UnmarshalJSON (e.g. for the
	// camelCase ↔ legacy PascalCase wire-compat pattern); a generated
	// UnmarshalJSON and a hand-written one on the same type is a
	// compile error.
	OmitValidation bool `yaml:"omit-validation"`
}

// SchemaPackageMapping ties a JSON-Schema $id to the Go package its types
// live in. Aliasing is mandatory because two canonical schemas commonly
// share a last path segment (`v1`/`v1`) and Go's import-alias derivation
// would otherwise collide.
type SchemaPackageMapping struct {
	ID      string `yaml:"id"`
	Package string `yaml:"package"`
	Alias   string `yaml:"alias"`
}

// MessagePackageMapping ties a referenced AsyncAPI spec file (consumed
// via cross-file message $ref) to the Go package the producer's
// already-generated message types live in. See
// loader.MessagePackageMapping for the field semantics — this type is
// the codegen-internal mirror (loader can't import codegen without a
// cycle; Generate converts at the boundary). v0.5+.
type MessagePackageMapping struct {
	File    string `yaml:"file"`
	Package string `yaml:"package"`
	Alias   string `yaml:"alias"`
}

// LoadConfiguration parses a YAML config file. Returns an error rather
// than silently filling in defaults — a typo in the YAML key should
// surface, not be papered over.
func LoadConfiguration(path string) (*Configuration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Configuration
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &c, nil
}

// MergeFlags overlays CLI-flag values onto a config loaded from YAML. A
// non-empty flag value wins; this lets the CLI run a config file as the
// base while still supporting one-off overrides without editing YAML.
func (c *Configuration) MergeFlags(pkg, output string, capitalization []string) {
	if pkg != "" {
		c.Package = pkg
	}
	if output != "" {
		c.Output = output
	}
	if len(capitalization) > 0 {
		c.Capitalization = capitalization
	}
}

// ToCodegenConfig translates a Configuration into the codegen.Config the
// internal pipeline consumes. Kept as a translation step so the YAML
// schema can evolve independently of the internal struct's fields.
func (c *Configuration) ToCodegenConfig() Config {
	return Config{
		Package:         c.Package,
		Output:          c.Output,
		Initialisms:     c.Capitalization,
		SchemaPackages:  c.SchemaPackages,
		MessagePackages: c.MessagePackages,
		OmitValidation:  c.OmitValidation,
	}
}

