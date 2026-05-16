package loader

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Materialize prepares a document with inline payloads and/or
// `components.schemas` for go-jsonschema consumption by writing
// synthetic .schema.json files into tmpDir. After this call, every
// message that previously had an inline payload has a Payload.Ref
// pointing at one of these synthetic files — so the rest of the
// pipeline (PayloadSchemaPaths, the lowerer's title-reading,
// go-jsonschema's DoFile) can process them without knowing whether the
// original spec used inline or external payloads.
//
// Layout (mirrors the AsyncAPI spec's own hierarchy so collisions
// between an inline channel message and a components.messages entry
// sharing a key are structural, not name-mangled):
//
//	tmpDir/
//	  components/
//	    <SchemaName>.schema.json        # one per components.schemas entry
//	    messages/
//	      <MsgName>.schema.json         # one per components.messages payload
//	  messages/
//	    <MsgKey>.schema.json            # one per channel-level inline message
//
// `#/components/schemas/X` refs in any payload are rewritten to
// relative file paths so go-jsonschema's filesystem-based resolver
// picks them up. The relative path depends on where the referring
// file lives — components/messages/Foo.schema.json refs `../X.schema.json`,
// messages/Foo.schema.json refs `../components/X.schema.json`,
// components/Foo.schema.json refs sibling `X.schema.json`.
//
// Caller is responsible for cleaning up tmpDir (typically via
// os.RemoveAll in a defer next to os.MkdirTemp).
func (d *Document) Materialize(tmpDir string) error {
	if !d.hasInlineContent() {
		return nil
	}
	componentsDir := filepath.Join(tmpDir, "components")
	componentMessagesDir := filepath.Join(componentsDir, "messages")
	messagesDir := filepath.Join(tmpDir, "messages")
	for _, dir := range []string{componentsDir, componentMessagesDir, messagesDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Components first: payloads' refs point at component files, so
	// the files must exist by the time go-jsonschema tries to resolve.
	if d.Components != nil {
		for _, name := range sortedKeys(d.Components.Schemas) {
			schema := asMap(d.Components.Schemas[name])
			if schema == nil {
				return fmt.Errorf("components.schemas.%s: not an object", name)
			}
			withTitle := injectTitle(schema, name)
			rewriteComponentRefs(withTitle, componentsDir, componentsDir)
			path := filepath.Join(componentsDir, name+".schema.json")
			if err := writeJSON(path, withTitle); err != nil {
				return err
			}
		}
	}

	// Messages: walk every channel's messages, materialize any inline
	// payloads, point Payload.Ref at the materialized file.
	//
	// Shared-message dedup: messages with CanonicalName (set by the
	// resolver when they came from components.messages) materialize
	// once into componentMessagesDir/<CanonicalName>.schema.json.
	// Subsequent channel slots referencing the same component re-point
	// their Payload.Ref at the already-written file — go-jsonschema
	// then emits a single Go type, observably honouring the dedup
	// contract.
	//
	// Dedup key is the file path: inline-vs-component live in different
	// directories, so a key collision between `components.messages.Foo`
	// and a channel-level inline `Foo` is structurally impossible —
	// they materialize to distinct paths under distinct subdirs.
	materialized := make(map[string]bool)
	for _, chName := range sortedKeys(d.Channels) {
		ch := d.Channels[chName]
		for _, msgName := range sortedKeys(ch.Messages) {
			msg := ch.Messages[msgName]
			if msg.Payload.Ref != "" {
				// `#/components/schemas/X` refs in an already-resolved
				// Payload.Ref (typical when a components.messages entry
				// declared its payload as a $ref into components.schemas
				// and the resolver copied that Ref into a channel slot)
				// must be translated to the file path the components
				// loop above wrote. go-jsonschema can't dereference
				// AsyncAPI-internal pointers; only filesystem $refs.
				if strings.HasPrefix(msg.Payload.Ref, componentsRefPrefix) {
					name := msg.Payload.Ref[len(componentsRefPrefix):]
					msg.Payload.Ref = filepath.Join(componentsDir, name+".schema.json")
				}
				// Other ref forms (external files, absolute paths)
				// are left alone — go-jsonschema's filesystem
				// resolver handles them.
				continue
			}
			if len(msg.Payload.Inline) == 0 {
				return fmt.Errorf("channel %q message %q: payload has neither $ref nor inline content", chName, msgName)
			}
			// Pick the file directory + title source. Component-derived
			// messages go under components/messages/ (CanonicalName as
			// filename, so two channels referencing the same component
			// dedup to one file). Inline messages go under messages/
			// (channel-level key as filename, today's behaviour).
			var fileDir, titleSource string
			if msg.CanonicalName != "" {
				fileDir = componentMessagesDir
				titleSource = msg.CanonicalName
			} else {
				fileDir = messagesDir
				titleSource = msgName
			}
			path := filepath.Join(fileDir, titleSource+".schema.json")
			if materialized[path] {
				msg.Payload.Ref = path
				msg.Payload.Inline = nil
				continue
			}
			schema := copyMap(msg.Payload.Inline)
			withTitle := injectTitle(schema, titleSource)
			rewriteComponentRefs(withTitle, fileDir, componentsDir)
			if err := writeJSON(path, withTitle); err != nil {
				return err
			}
			materialized[path] = true
			msg.Payload.Ref = path
			msg.Payload.Inline = nil
		}
	}
	return nil
}

func (d *Document) hasInlineContent() bool {
	if d.Components != nil && len(d.Components.Schemas) > 0 {
		return true
	}
	for _, ch := range d.Channels {
		for _, msg := range ch.Messages {
			if msg.Payload.Ref == "" && len(msg.Payload.Inline) > 0 {
				return true
			}
		}
	}
	return false
}

// injectTitle returns schema with a `title` key set to defaultTitle if
// no title was present. Spec-author-provided title wins (per Q4
// resolved decision: payload Go type name = message key by default,
// inline title overrides).
func injectTitle(schema map[string]any, defaultTitle string) map[string]any {
	if _, has := schema["title"]; has {
		return schema
	}
	schema["title"] = defaultTitle
	return schema
}

// rewriteComponentRefs walks the schema tree (in place) and rewrites
// `$ref` strings of the form `#/components/schemas/X` to relative file
// paths pointing into componentsDir, computed from fromDir. External-
// file refs (`./...`, `../...`, http://...) and other internal refs
// (`#/$defs/...`) are left untouched.
//
// fromDir is the directory the file being rewritten will live in;
// componentsDir is where the target component schemas live. The
// relative path is derived once via filepath.Rel and threaded down the
// recursion as componentsRel — all refs in a single file share the
// same base.
func rewriteComponentRefs(node any, fromDir, componentsDir string) {
	componentsRel, err := filepath.Rel(fromDir, componentsDir)
	if err != nil {
		// fromDir and componentsDir are always tmpDir-rooted siblings
		// in our usage, so filepath.Rel can't fail in practice. The
		// fallback (no rewriting) is safer than panicking — caller
		// will see the original ref and at worst get a clearer
		// "ref not resolved" error from go-jsonschema.
		return
	}
	rewriteComponentRefsRecursive(node, componentsRel)
}

func rewriteComponentRefsRecursive(node any, componentsRel string) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			if k == "$ref" {
				if s, ok := v.(string); ok {
					n[k] = rewriteOneRef(s, componentsRel)
				}
				continue
			}
			rewriteComponentRefsRecursive(v, componentsRel)
		}
	case []any:
		for _, item := range n {
			rewriteComponentRefsRecursive(item, componentsRel)
		}
	}
}

const componentsRefPrefix = "#/components/schemas/"

func rewriteOneRef(ref, componentsRel string) string {
	if !strings.HasPrefix(ref, componentsRefPrefix) {
		return ref
	}
	name := ref[len(componentsRefPrefix):]
	return filepath.Join(componentsRel, name+".schema.json")
}

// asMap accepts either map[string]any (the standard yaml.v3 inline
// representation) or map[any]any (older yaml decoders) and returns the
// canonical map[string]any form. yaml.v3 with `,inline` should always
// give map[string]any; the type switch is defensive.
func asMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, vv := range m {
			if ks, ok := k.(string); ok {
				out[ks] = vv
			}
		}
		return out
	}
	return nil
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

func writeJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
