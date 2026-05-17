package loader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// identifierKeyRegexp matches valid Go-identifier-shaped names used as
// filename bases during materialization (components.schemas.<X>,
// components.messages.<X>, channels.*.messages.<X>). Rejecting non-
// matching keys at load time keeps untrusted YAML strings out of
// filepath.Join, which would otherwise allow path traversal via keys
// like "../foo" or silent subdirectory creation via "foo/bar".
var identifierKeyRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ErrUnsupportedRef is returned when a $ref points outside the slice of
// AsyncAPI we currently model. M1 only resolves payload $refs that point to
// JSON Schema files on disk; AsyncAPI-internal $refs (e.g.
// `#/components/messages/X`) are deferred to M2.
var ErrUnsupportedRef = errors.New("unsupported $ref shape")

// Load parses the AsyncAPI document at specPath. The returned Document has
// SourcePath populated so payload $refs can be resolved against it.
func Load(specPath string) (*Document, error) {
	abs, err := filepath.Abs(specPath)
	if err != nil {
		return nil, fmt.Errorf("resolve spec path: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	doc := &Document{SourcePath: abs}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, fmt.Errorf("parse spec %s: %w", abs, err)
	}
	if !strings.HasPrefix(doc.AsyncAPI, "3.") {
		return nil, fmt.Errorf("aapi-codegen requires asyncapi 3.x, got %q", doc.AsyncAPI)
	}
	if err := validateIdentifierKeys(doc); err != nil {
		return nil, fmt.Errorf("parse spec %s: %w", abs, err)
	}
	return doc, nil
}

// validateIdentifierKeys rejects spec keys that are used as
// materialization filenames (components.schemas.<X>,
// components.messages.<X>, channels.*.messages.<X>) when they would
// produce unsafe filesystem paths. Catches keys like "../foo" (path
// traversal) and "foo/bar" (silent subdirectory creation) at load time,
// so the error names the offending YAML location rather than surfacing
// later as a confusing materialize failure.
func validateIdentifierKeys(d *Document) error {
	if d.Components != nil {
		for name := range d.Components.Schemas {
			if err := validateOneKey(name, "components.schemas"); err != nil {
				return err
			}
		}
		for name := range d.Components.Messages {
			if err := validateOneKey(name, "components.messages"); err != nil {
				return err
			}
		}
	}
	for chName, ch := range d.Channels {
		for msgName := range ch.Messages {
			if err := validateOneKey(msgName, fmt.Sprintf("channels.%q.messages", chName)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOneKey(name, location string) error {
	if !identifierKeyRegexp.MatchString(name) {
		return fmt.Errorf("%s key %q is not a valid identifier: must match %s (used as a filename during materialization)",
			location, name, identifierKeyRegexp.String())
	}
	return nil
}

// PayloadSchemaPaths walks every channel.messages.*.payload.$ref in doc,
// resolves each ref against the spec file's directory, dedupes, and returns
// absolute filesystem paths. Refs that contain a fragment (e.g. `foo.json#/X`)
// have the fragment stripped — we hand whole files to go-jsonschema.
//
// Internal AsyncAPI refs (`#/components/...`) are not yet supported and
// surface as ErrUnsupportedRef.
func (d *Document) PayloadSchemaPaths() ([]string, error) {
	specDir := filepath.Dir(d.SourcePath)
	seen := make(map[string]struct{})
	var out []string
	for chanName, ch := range d.Channels {
		for msgName, msg := range ch.Messages {
			// Cross-file message $ref → imported Go type (v0.5+). No
			// payload schema to feed to go-jsonschema; the producer's
			// already-generated package supplies the type.
			if msg.Ref != "" {
				continue
			}
			ref := msg.Payload.Ref
			if ref == "" {
				return nil, fmt.Errorf("channel %q message %q: inline payload schemas not yet supported (M1)", chanName, msgName)
			}
			if strings.HasPrefix(ref, "#/") {
				return nil, fmt.Errorf("channel %q message %q: %w (internal ref %q)", chanName, msgName, ErrUnsupportedRef, ref)
			}
			path := ref
			if i := strings.Index(path, "#"); i >= 0 {
				path = path[:i]
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(specDir, path)
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return nil, fmt.Errorf("channel %q message %q: resolve %q: %w", chanName, msgName, ref, err)
			}
			if _, dup := seen[abs]; dup {
				continue
			}
			seen[abs] = struct{}{}
			out = append(out, abs)
		}
	}
	// v0.5+: a pure consumer-import spec (every message resolved via
	// cross-file $ref) legitimately has no local payload schemas. The
	// caller (codegen.Generate) handles the empty-schemas case by
	// skipping the go-jsonschema invocation and emitting only the
	// publisher/subscriber + parameter sections.
	return out, nil
}
