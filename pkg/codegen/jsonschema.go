package codegen

import (
	"fmt"
	"path/filepath"

	"github.com/atombender/go-jsonschema/pkg/generator"
)

// runJSONSchemaInProcess invokes go-jsonschema as a library, replacing
// the M1–M4 subprocess shell-out to /tmp/go-jsonschema-local. Plan §3
// settled this in M5: the sibling fork exposes a stable
// generator.New(Config) → DoFile → Sources() API, so vendoring or
// shelling out are both unnecessary.
//
// The Config below mirrors the canonical CLI flag set the per-schema-
// dir generate.sh files passed to the binary. Drift between this and
// the previous CLI invocation would silently change generated output
// (the regression would surface as a wire-compat or publisher test
// failure, but the failure mode would be confusing) — keep the two
// aligned when adding new flags.
//
// Cross-tree --schema-package mappings turn into SchemaMappings
// entries. The library still emits a Go file per mapped schema (same
// "wart" go-jsonschema/notest.md flags upstream); we route those
// outputs into a per-call discard directory the caller cleans up.
func runJSONSchemaInProcess(pkg string, initialisms []string, output string, schemas []string, schemaPackages []SchemaPackageMapping, discardDir string, omitValidation bool) error {
	cfg := generator.Config{
		DefaultPackageName:         pkg,
		DefaultOutputName:          output,
		Capitalizations:            initialisms,
		StructNameFromTitle:        true,
		OnlyModels:                 omitValidation, // false (default): emit UnmarshalJSON with required-field + additionalProperties + format checks. True (opt-out): plain structs only — consumer brings their own UnmarshalJSON (e.g. wire-compat shim).
		Tags:                       []string{"json"},
		StrictAdditionalProperties: generator.StrictAdditionalPropertiesRespectSchema,
		FormatValidation:           generator.FormatValidationConfig{Enabled: true}, // nil AllowList = all formats
		Warner:                     func(string) {},                                  // discard warnings — surface via Sources() if needed later
	}
	for i, sp := range schemaPackages {
		cfg.SchemaMappings = append(cfg.SchemaMappings, generator.SchemaMapping{
			SchemaID:    sp.ID,
			PackageName: sp.Package,
			ImportAlias: sp.Alias,
			OutputName:  filepath.Join(discardDir, fmt.Sprintf("_discard_%d.go", i)),
		})
	}

	gen, err := generator.New(cfg)
	if err != nil {
		return fmt.Errorf("go-jsonschema New: %w", err)
	}
	for _, s := range schemas {
		if err := gen.DoFile(s); err != nil {
			return fmt.Errorf("go-jsonschema DoFile(%s): %w", s, err)
		}
	}

	sources, err := gen.Sources()
	if err != nil {
		return fmt.Errorf("go-jsonschema Sources: %w", err)
	}
	body, ok := sources[output]
	if !ok {
		// Surface the keys that *did* come back so a path-mismatch
		// regression is debuggable. The most common cause is a relative
		// vs absolute path discrepancy between DefaultOutputName and the
		// key Sources() returns.
		var keys []string
		for k := range sources {
			keys = append(keys, k)
		}
		return fmt.Errorf("go-jsonschema produced no output for %q; got: %v", output, keys)
	}
	return writeFile(output, body)
}
