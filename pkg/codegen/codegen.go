// Package codegen orchestrates AsyncAPI → Go generation.
//
// Pipeline:
//   - Load the AsyncAPI spec.
//   - Resolve every message payload's $ref to a JSON Schema file.
//   - Invoke go-jsonschema in-process to emit payload Go types
//     (cross-tree $refs become Go imports per Config.SchemaPackages).
//   - Lower the spec to IR; render typed publishers per `action: send`
//     operation.
//   - Combine, inject imports, gofmt, write one Go file.
//
// Wire-compat (accepting legacy PascalCase JSON alongside the
// canonical camelCase) is intentionally out of scope: aapi-codegen
// does not emit UnmarshalJSON shims. Consumers that need it
// hand-write a small `compat.go` sibling to the generated file and
// retire it once the legacy publishers are gone — the kind of
// transitional debt that doesn't belong in generated code.
package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/plheide/aapi-codegen/pkg/codegen/ir"
	"github.com/plheide/aapi-codegen/pkg/codegen/loader"
	"github.com/plheide/aapi-codegen/pkg/codegen/lower"
)

// DefaultInitialisms is the initialism list applied when Config.Initialisms
// is empty. Mirrors oapi-codegen's pattern: defaults are baked in;
// user-supplied entries are additive.
var DefaultInitialisms = []string{"ID", "URL"}

// Config is the internal codegen surface. The user-facing YAML
// Configuration translates into this via Configuration.ToCodegenConfig.
type Config struct {
	Package string
	Output  string
	// Initialisms supplements DefaultInitialisms when configuring
	// go-jsonschema's --capitalization rules. Empty means defaults only.
	Initialisms []string
	// SchemaPackages drives cross-tree $ref → import behaviour. For each
	// entry, go-jsonschema emits `import alias "package"` at the use
	// site instead of inlining the schema's types.
	SchemaPackages []SchemaPackageMapping
	// MessagePackages drives cross-file MESSAGE $ref → import behaviour.
	// When a message slot is a $ref into another spec file, the lowerer
	// looks up the file in this mapping; the message-name segment of
	// the $ref becomes the Go type name in the mapped package. Generated
	// Subscriber/Publisher signatures reference `<alias>.<TypeName>`
	// from the imported package — no payload schema duplication. v0.5+.
	MessagePackages []MessagePackageMapping
	// OmitValidation suppresses go-jsonschema's generated UnmarshalJSON
	// methods (required-field checks, strict additionalProperties,
	// format/enum/numeric-bound validators, etc.). False (the default)
	// means validation is included; true is the opt-out for consumers
	// that hand-write their own UnmarshalJSON, e.g. for wire-compat
	// (camelCase ↔ legacy PascalCase): two UnmarshalJSON methods on the
	// same type would be a compile error, so wire-compat consumers must
	// opt out here to define their own.
	OmitValidation bool
	// OmitPublishers / OmitSubscribers skip the corresponding generated
	// section even when the spec has matching `action:` operations.
	// OR-merged with the spec extension equivalents. v0.2+.
	OmitPublishers  bool
	OmitSubscribers bool
}

// Generate runs the pipeline end-to-end and writes one Go file at
// cfg.Output. Returns the absolute output path on success.
func Generate(specPath string, cfg Config) (string, error) {
	if cfg.Package == "" {
		return "", errors.New("Config.Package is required")
	}
	if cfg.Output == "" {
		return "", errors.New("Config.Output is required")
	}
	initialisms := mergeInitialisms(cfg.Initialisms)

	doc, err := loader.Load(specPath)
	if err != nil {
		return "", fmt.Errorf("load spec: %w", err)
	}
	// Resolve components.messages refs: inline shared message wrappers
	// into the channel-level slots that reference them. Sets
	// CanonicalName on resolved messages so the materializer can dedupe
	// (multiple channels referencing the same component → one
	// synthetic schema file → one Go type).
	if err := doc.ResolveMessageRefs(); err != nil {
		return "", fmt.Errorf("resolve component message refs: %w", err)
	}
	// Merge spec-extension schema-packages with anything passed via
	// CLI/config. Spec is canonical (Q1); CLI/config entries supplement
	// without overriding when both declare the same $id.
	cfg.SchemaPackages = mergeSchemaPackages(cfg.SchemaPackages, doc.XAAPICodegen)
	cfg.MessagePackages = mergeMessagePackages(cfg.MessagePackages, doc.XAAPICodegen)
	// OR-merge omit-validation: either source can opt out; neither can
	// override the other's opt-out. Spec opts out → omit; else CLI/config
	// opts out → omit; else validation included.
	if doc.XAAPICodegen != nil && doc.XAAPICodegen.OmitValidation {
		cfg.OmitValidation = true
	}
	if doc.XAAPICodegen != nil && doc.XAAPICodegen.OmitPublishers {
		cfg.OmitPublishers = true
	}
	if doc.XAAPICodegen != nil && doc.XAAPICodegen.OmitSubscribers {
		cfg.OmitSubscribers = true
	}

	// Materialize inline payloads + components.schemas into a temp
	// directory of synthetic .schema.json files (Q4). After this call,
	// every message has a Payload.Ref pointing at a real file on disk
	// — so the rest of the pipeline doesn't have to distinguish
	// inline-vs-file-based payloads.
	materializeDir, err := os.MkdirTemp("", "aapi-codegen-inline-")
	if err != nil {
		return "", fmt.Errorf("create materialize tmp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(materializeDir) }()
	if err := doc.Materialize(materializeDir); err != nil {
		return "", fmt.Errorf("materialize inline schemas: %w", err)
	}

	schemaPaths, err := doc.PayloadSchemaPaths()
	if err != nil {
		return "", fmt.Errorf("collect payload schemas: %w", err)
	}

	// Phase 1: payload types (in-process go-jsonschema). Output to a
	// tmp sibling so partial failures don't clobber cfg.Output.
	tmpOut := cfg.Output + ".jsonschema.tmp"
	defer func() { _ = os.Remove(tmpOut) }()
	// Discard sink for cross-tree schemas: go-jsonschema still emits a
	// Go file per mapped schema even when --schema-package routes the
	// types to an import (see go-jsonschema/notest.md wart note;
	// upstream `--known-schema URL=PATH` would let us drop these
	// entirely once it lands).
	discardDir, err := os.MkdirTemp("", "aapi-codegen-discard-")
	if err != nil {
		return "", fmt.Errorf("create discard dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(discardDir) }()

	// v0.5+: a pure consumer-import spec (every message is a cross-file
	// $ref → imported Go type) has no local payload schemas to feed
	// go-jsonschema. Skip the call and synthesize a bare package stub
	// for combineSections to merge sections into.
	if len(schemaPaths) == 0 {
		stub := "// Code generated by aapi-codegen, DO NOT EDIT.\n\npackage " + cfg.Package + "\n"
		if err := writeFile(tmpOut, []byte(stub)); err != nil {
			return "", err
		}
	} else if err := runJSONSchemaInProcess(cfg.Package, initialisms, tmpOut, schemaPaths, cfg.SchemaPackages, discardDir, cfg.OmitValidation); err != nil {
		return "", err
	}

	// Phase 2: lower spec → IR; render publishers (one Send method per
	// `action: send` operation). When the spec has no send operations,
	// the publisher section is empty and no extra imports get pulled in.
	messagePackages := make([]lower.MessagePackage, 0, len(cfg.MessagePackages))
	for _, mp := range cfg.MessagePackages {
		messagePackages = append(messagePackages, lower.MessagePackage{
			File:    mp.File,
			Package: mp.Package,
			Alias:   mp.Alias,
		})
	}
	spec, err := lower.Lower(cfg.Package, doc, cfg.OmitValidation, messagePackages)
	if err != nil {
		return "", fmt.Errorf("lower: %w", err)
	}
	var publisherSection string
	if !cfg.OmitPublishers {
		publisherSection, err = RenderPublisher(spec)
		if err != nil {
			return "", fmt.Errorf("render publisher: %w", err)
		}
	}
	var subscriberSection string
	if !cfg.OmitSubscribers {
		subscriberSection, err = RenderSubscriber(spec)
		if err != nil {
			return "", fmt.Errorf("render subscriber: %w", err)
		}
	}
	// v0.4: parameter-enum types. Render only if something else is also
	// being rendered (the enums are only referenced by Publisher /
	// Subscriber signatures; emitting them in isolation would yield
	// dead code).
	var enumSection string
	if publisherSection != "" || subscriberSection != "" {
		enumSection, err = RenderParameterEnums(spec)
		if err != nil {
			return "", fmt.Errorf("render parameter enums: %w", err)
		}
	}

	// Phase 3: combine, inject imports, gofmt, write.
	jsonschemaOut, err := os.ReadFile(tmpOut)
	if err != nil {
		return "", fmt.Errorf("read go-jsonschema output: %w", err)
	}
	imports := neededImports(publisherSection, subscriberSection)
	if len(spec.ParameterPatterns) > 0 {
		// Pattern wrappers need fmt (constructor's Errorf) and regexp
		// (MustCompile + MatchString). fmt is already pulled in by
		// publisher/subscriber whenever those sections render; add it
		// here too in case the spec is parameters-only (rare but possible).
		imports = append(imports, "fmt", "regexp")
	}
	// Cross-file message-packages contribute aliased imports — collect
	// each distinct (alias, package) pair referenced by the operations'
	// imported messages. Dedup at the call site so combineSections sees
	// each once. v0.5+.
	aliasedImports := collectAliasedImports(spec)
	combined, err := combineSections(string(jsonschemaOut), imports, aliasedImports, []string{enumSection, publisherSection, subscriberSection}, cfg.Package)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(cfg.Output, combined, 0o600); err != nil {
		return "", fmt.Errorf("write output: %w", err)
	}
	return cfg.Output, nil
}

// mergeMessagePackages folds the spec's
// `x-aapi-codegen.message-packages` extension into the CLI/config list.
// Spec wins on file-path collision (same precedence as schema-packages
// merge — the spec is the source of truth).
func mergeMessagePackages(cliPkgs []MessagePackageMapping, ext *loader.XExtension) []MessagePackageMapping {
	if ext == nil || len(ext.MessagePackages) == 0 {
		return cliPkgs
	}
	seenInSpec := make(map[string]struct{}, len(ext.MessagePackages))
	out := make([]MessagePackageMapping, 0, len(cliPkgs)+len(ext.MessagePackages))
	for _, mp := range ext.MessagePackages {
		seenInSpec[mp.File] = struct{}{}
		out = append(out, MessagePackageMapping{File: mp.File, Package: mp.Package, Alias: mp.Alias})
	}
	for _, mp := range cliPkgs {
		if _, dup := seenInSpec[mp.File]; dup {
			continue
		}
		out = append(out, mp)
	}
	return out
}

// mergeSchemaPackages folds the spec's `x-aapi-codegen.schema-packages`
// extension into the CLI/config-provided list. Spec entries take
// priority on $id collision (the spec is the source of truth, plan §6
// Q1); CLI/config entries declared for $ids the spec doesn't mention
// pass through unchanged so a partial override is still possible
// during migration.
func mergeSchemaPackages(cliPkgs []SchemaPackageMapping, ext *loader.XExtension) []SchemaPackageMapping {
	if ext == nil || len(ext.SchemaPackages) == 0 {
		return cliPkgs
	}
	seenInSpec := make(map[string]struct{}, len(ext.SchemaPackages))
	out := make([]SchemaPackageMapping, 0, len(cliPkgs)+len(ext.SchemaPackages))
	for _, sp := range ext.SchemaPackages {
		seenInSpec[sp.ID] = struct{}{}
		out = append(out, SchemaPackageMapping{ID: sp.ID, Package: sp.Package, Alias: sp.Alias})
	}
	for _, sp := range cliPkgs {
		if _, dup := seenInSpec[sp.ID]; dup {
			continue
		}
		out = append(out, sp)
	}
	return out
}

// mergeInitialisms returns DefaultInitialisms plus any user-supplied
// extras, deduped. Additive semantics (oapi-codegen's pattern) — users
// can extend the default list without restating ID/URL.
func mergeInitialisms(extra []string) []string {
	if len(extra) == 0 {
		return DefaultInitialisms
	}
	seen := make(map[string]struct{}, len(DefaultInitialisms)+len(extra))
	out := make([]string, 0, len(DefaultInitialisms)+len(extra))
	for _, init := range DefaultInitialisms {
		seen[strings.ToUpper(init)] = struct{}{}
		out = append(out, init)
	}
	for _, init := range extra {
		key := strings.ToUpper(init)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, init)
	}
	return out
}

// neededImports returns the imports the appended sections collectively
// require. Publisher + subscriber both pull in context/encoding/json/fmt;
// subscriber additionally needs `errors` for the ErrDrop sentinel and
// errors.Join in the JSON-decode wrapper.
func neededImports(publisherSection, subscriberSection string) []string {
	var imports []string
	if publisherSection != "" || subscriberSection != "" {
		imports = append(imports, "context", "encoding/json", "fmt")
	}
	if subscriberSection != "" {
		imports = append(imports, "errors")
	}
	return imports
}

// collectAliasedImports gathers the (alias, package) pairs the IR's
// imported messages require. Deduplicated on (alias, package) — two
// operations consuming messages from the same producer package
// contribute one import, not two. v0.5+.
func collectAliasedImports(spec *ir.Spec) [][2]string {
	if spec == nil {
		return nil
	}
	seen := make(map[[2]string]struct{})
	var out [][2]string
	for _, op := range spec.Operations {
		if op.Message == nil || op.Message.ImportedPackage == "" {
			continue
		}
		key := [2]string{op.Message.ImportedAlias, op.Message.ImportedPackage}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

// writeFile is a tiny helper exposed so the in-process schema generator
// can write the bytes Sources() returned to disk.
func writeFile(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// combineSections concatenates the go-jsonschema output and each
// supplied section, parses the result, and rewrites all import
// declarations as a single deduplicated block. This is robust to
// go-jsonschema emitting its own imports (which it does when
// OnlyModels is off, or when SchemaMappings produce cross-tree alias
// imports) — naively concatenating import blocks would yield
// duplicate-import compile errors.
//
// `imports` are the additional unaliased imports the appended sections
// need (e.g. context/encoding/json/fmt for the publisher block).
// `aliasedImports` are imports with explicit aliases — used by the
// cross-file message-packages path (v0.5+) where each imported
// producer-package needs its alias preserved in the generated source.
// Both lists get unioned with whatever go-jsonschema emitted and any
// aliases inside section sources.
func combineSections(jsonschemaSrc string, imports []string, aliasedImports [][2]string, sections []string, pkg string) ([]byte, error) {
	pkgLine := "package " + pkg
	if !strings.Contains(jsonschemaSrc, pkgLine) {
		return nil, fmt.Errorf("combine: go-jsonschema output missing %q (package mismatch?)", pkgLine)
	}

	// Concatenate everything; let go/parser handle the merge.
	var concat strings.Builder
	concat.WriteString(jsonschemaSrc)
	for _, sec := range sections {
		if sec == "" {
			continue
		}
		concat.WriteString("\n")
		concat.WriteString(sec)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "combined.go", concat.String(), parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse combined source: %w\n--- combined source ---\n%s", err, concat.String())
	}

	// Build the union of imports: everything go-jsonschema emitted +
	// everything the caller said the appended sections need. Dedupe on
	// (alias, path).
	type importKey struct{ alias, path string }
	union := make(map[importKey]struct{})
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("malformed import %q: %w", imp.Path.Value, err)
		}
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		union[importKey{alias: alias, path: path}] = struct{}{}
	}
	for _, p := range imports {
		union[importKey{path: p}] = struct{}{}
	}
	for _, ap := range aliasedImports {
		union[importKey{alias: ap[0], path: ap[1]}] = struct{}{}
	}

	// Strip every existing import decl from the AST; we're about to
	// replace them with a single consolidated block.
	newDecls := make([]ast.Decl, 0, len(file.Decls))
	for _, decl := range file.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		newDecls = append(newDecls, decl)
	}

	if len(union) > 0 {
		keys := make([]importKey, 0, len(union))
		for k := range union {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].path != keys[j].path {
				return keys[i].path < keys[j].path
			}
			return keys[i].alias < keys[j].alias
		})
		specs := make([]ast.Spec, 0, len(keys))
		for _, k := range keys {
			spec := &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(k.path)}}
			if k.alias != "" {
				spec.Name = &ast.Ident{Name: k.alias}
			}
			specs = append(specs, spec)
		}
		// Lparen non-zero forces the grouped `import ( ... )` form; the
		// actual position value doesn't matter once go/format runs.
		consolidated := &ast.GenDecl{
			Tok:    token.IMPORT,
			Lparen: token.Pos(1),
			Specs:  specs,
			Rparen: token.Pos(1),
		}
		file.Decls = append([]ast.Decl{consolidated}, newDecls...)
	} else {
		file.Decls = newDecls
	}

	// Print the rewritten AST, then gofmt for consistent spacing.
	// printer alone respects positions; gofmt smoothes the result.
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		return nil, fmt.Errorf("print combined AST: %w", err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt combined output: %w\n--- printed source ---\n%s", err, buf.String())
	}
	return formatted, nil
}
