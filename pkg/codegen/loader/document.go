// Package loader parses an AsyncAPI 3.x YAML document into a typed
// in-memory representation. M1 only models the slice of the spec the tracer
// needs: enough to find each message's payload $ref and resolve it to a
// filesystem path. Channels, parameters, bindings, and operations are
// modelled as opaque-enough placeholders so they round-trip through YAML
// without losing data; M2/M3 widen the typed surface as the lowerer
// (pkg/codegen/lower) starts using more fields.
package loader

// Document is the root of an AsyncAPI 3.x spec.
type Document struct {
	AsyncAPI           string                `yaml:"asyncapi"`
	Info               Info                  `yaml:"info"`
	DefaultContentType string                `yaml:"defaultContentType,omitempty"`
	Channels           map[string]*Channel   `yaml:"channels,omitempty"`
	Operations         map[string]*Operation `yaml:"operations,omitempty"`
	Components         *Components           `yaml:"components,omitempty"`
	// XAAPICodegen carries aapi-codegen-specific configuration declared
	// inside the spec via the `x-aapi-codegen` extension. The extension
	// is the canonical place to put cross-tree $ref → import mappings
	// (plan §6 / Q1) — colocates spec content with the tooling
	// directives that interpret it. CLI / config-YAML still work as
	// overrides for migration, but the spec is the source of truth.
	XAAPICodegen *XExtension `yaml:"x-aapi-codegen,omitempty"`

	// SourcePath is the absolute filesystem path the document was loaded
	// from. Set by Load(); used by RefResolver to resolve relative $refs.
	SourcePath string `yaml:"-"`
}

// XExtension is the typed shape of the spec-level `x-aapi-codegen`
// extension. Add fields as new declarations move into the spec.
type XExtension struct {
	SchemaPackages []SchemaPackageMapping `yaml:"schema-packages,omitempty"`
	// OmitValidation opts out of generated UnmarshalJSON methods for
	// this spec. Useful when consumers hand-write their own
	// UnmarshalJSON (e.g. wire-compat shims for legacy PascalCase
	// publishers). Either source — spec extension or config — can opt
	// out; the merge is OR semantics (Generate's mergeOmitValidation).
	OmitValidation bool `yaml:"omit-validation,omitempty"`
}

// Components is the AsyncAPI 3.x `components` object. Models the
// subset aapi-codegen consumes: shared JSON Schema types
// (`components.schemas`) and shared message wrappers
// (`components.messages`). Other components (parameters,
// operationBindings, ...) get added when fixtures need them.
type Components struct {
	Schemas  map[string]any      `yaml:"schemas,omitempty"`
	Messages map[string]*Message `yaml:"messages,omitempty"`
}

// SchemaPackageMapping ties a JSON-Schema $id to the Go package its
// types live in. Declared either in the spec's x-aapi-codegen extension
// (canonical) or in the optional aapi-codegen.config.yaml (legacy /
// migration override). Aliasing is mandatory because two canonical
// schemas commonly share a last path segment (`v1`/`v1`) and Go's
// import-alias derivation would otherwise collide.
//
// This type duplicates the codegen.SchemaPackageMapping shape on
// purpose — the loader can't import codegen without a dependency
// cycle. Generate() converts loader.SchemaPackageMapping →
// codegen.SchemaPackageMapping at the boundary.
type SchemaPackageMapping struct {
	ID      string `yaml:"id"`
	Package string `yaml:"package"`
	Alias   string `yaml:"alias"`
}

type Info struct {
	Title       string `yaml:"title"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

type Channel struct {
	Address     string                 `yaml:"address"`
	Title       string                 `yaml:"title,omitempty"`
	Description string                 `yaml:"description,omitempty"`
	Parameters  map[string]*Parameter  `yaml:"parameters,omitempty"`
	Bindings    map[string]any `yaml:"bindings,omitempty"`
	Messages    map[string]*Message    `yaml:"messages,omitempty"`
}

type Parameter struct {
	Description string `yaml:"description,omitempty"`
	// Schema, enum etc. live here when the spec needs them. M1 doesn't.
}

type Message struct {
	// Ref is set when this message slot is a `$ref` to
	// `#/components/messages/X` rather than an inline declaration. The
	// resolver (see ResolveMessageRefs) follows the ref, copies the
	// component's fields into this struct, and sets CanonicalName.
	// Spec authors can't mix Ref with inline fields — the resolver
	// rejects that.
	Ref         string  `yaml:"$ref,omitempty"`
	Name        string  `yaml:"name,omitempty"`
	Title       string  `yaml:"title,omitempty"`
	Summary     string  `yaml:"summary,omitempty"`
	ContentType string  `yaml:"contentType,omitempty"`
	Payload     Payload `yaml:"payload"`

	// CanonicalName is set by the resolver to the
	// `components.messages.<Name>` key when this message was resolved
	// from a component ref. The materializer uses it as the synthetic-
	// file basename so multiple channel-level references to the same
	// component dedupe to one synthetic schema (one Go type). Empty
	// for inline messages — the materializer falls back to the
	// channel-level message key in that case.
	CanonicalName string `yaml:"-"`
}

// Payload is either an inline schema or a $ref to an external one. M1 only
// handles the $ref form (every reference spec uses it). When Ref is empty
// the lowerer can fall back to interpreting the rest of the YAML as an
// inline schema; M1 doesn't need that path.
type Payload struct {
	Ref string `yaml:"$ref,omitempty"`
	// Inline schema fields (type/properties/etc.) get parsed into this map
	// for now. Promoted to typed fields when M2 needs them.
	Inline map[string]any `yaml:",inline"`
}

type Operation struct {
	Action  string                  `yaml:"action"` // "send" | "receive"
	Channel OperationChannelRef     `yaml:"channel"`
	Title   string                  `yaml:"title,omitempty"`
	Summary string                  `yaml:"summary,omitempty"`
	Messages []OperationMessageRef  `yaml:"messages,omitempty"`
}

// OperationChannelRef is the typed shape of `operations.X.channel.$ref`.
type OperationChannelRef struct {
	Ref string `yaml:"$ref"`
}

// OperationMessageRef is the typed shape of items in `operations.X.messages`.
type OperationMessageRef struct {
	Ref string `yaml:"$ref"`
}
