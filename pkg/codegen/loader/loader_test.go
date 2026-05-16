package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_RejectsPathTraversalKeys ensures untrusted YAML keys can't
// escape the materialization tmp dir via "../foo"-style names or
// silently create subdirectories via "foo/bar"-style names. The
// validation runs at Load time so the error names the YAML location,
// not a downstream tmp path.
func TestLoad_RejectsPathTraversalKeys(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		wantSub string // substring the error message should contain
	}{
		{
			name: "components.schemas key with path traversal",
			spec: `asyncapi: 3.1.0
info: { title: X, version: 1.0.0 }
components:
  schemas:
    "../etc/passwd":
      type: object
`,
			wantSub: `components.schemas key "../etc/passwd"`,
		},
		{
			name: "components.messages key with slash",
			spec: `asyncapi: 3.1.0
info: { title: X, version: 1.0.0 }
components:
  messages:
    "foo/bar":
      payload: { type: object }
`,
			wantSub: `components.messages key "foo/bar"`,
		},
		{
			name: "channel message key with leading dot",
			spec: `asyncapi: 3.1.0
info: { title: X, version: 1.0.0 }
channels:
  ch:
    address: a
    messages:
      ".hidden":
        payload: { type: object }
`,
			wantSub: `messages key ".hidden"`,
		},
		{
			name: "channel message key starting with digit",
			spec: `asyncapi: 3.1.0
info: { title: X, version: 1.0.0 }
channels:
  ch:
    address: a
    messages:
      "1bad":
        payload: { type: object }
`,
			wantSub: `messages key "1bad"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			specPath := filepath.Join(tmp, "spec.yaml")
			if err := os.WriteFile(specPath, []byte(tc.spec), 0o600); err != nil {
				t.Fatalf("write spec: %v", err)
			}
			_, err := Load(specPath)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error should contain %q; got %v", tc.wantSub, err)
			}
		})
	}
}

// TestLoad_AcceptsValidKeys is the positive control: identifier-shaped
// keys load cleanly. Prevents the regex from being silently too strict.
func TestLoad_AcceptsValidKeys(t *testing.T) {
	spec := `asyncapi: 3.1.0
info: { title: X, version: 1.0.0 }
components:
  schemas:
    Foo: { type: object }
    BarBaz: { type: object }
    _Hidden: { type: object }
channels:
  myChannel:
    address: a
    messages:
      MsgOne: { payload: { type: object } }
`
	tmp := t.TempDir()
	specPath := filepath.Join(tmp, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := Load(specPath); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
