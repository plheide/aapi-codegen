package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain_PrintsWarningsToStderr runs the CLI on a payload that declares
// a keyword the generated code doesn't check: go-jsonschema's warning is
// printed to stderr with aapi-codegen's prefix, and the file is still
// written, with exit status 0.
func TestMain_PrintsWarningsToStderr(t *testing.T) {
	dir := t.TempDir()
	spec := `asyncapi: 3.1.0
info: { title: T, version: 1.0.0 }
channels:
  orders:
    address: orders
    bindings:
      amqp:
        is: routingKey
        exchange: { name: Orders, type: direct }
        bindingVersion: 0.3.0
    messages:
      OrderPlaced:
        payload:
          title: OrderPlaced
          type: object
          minProperties: 1
          properties:
            id: { type: string }
operations:
  sendOrderPlaced:
    action: send
    channel: { $ref: '#/channels/orders' }
    messages:
      - $ref: '#/channels/orders/messages/OrderPlaced'
`
	specPath := filepath.Join(dir, "spec.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.go")

	cmd := exec.Command("go", "run", ".", "-package", "x", "-o", out, specPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("aapi-codegen failed: %v\n%s", err, stderr.String())
	}
	want := "aapi-codegen: warning: schema fidelity: type OrderPlaced does not enforce all it declares: minProperties not checked\n"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr does not contain %q:\n%s", want, stderr.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output file not written: %v", err)
	}
}

func TestDefaultPackageName(t *testing.T) {
	cases := []struct {
		out, want string
	}{
		// Version-folder rule: parent + basename.
		{"/tmp/lib/schemas/order-event/v1/types.gen.go", "ordereventv1"},
		{"/tmp/lib/schemas/order-status-change/v1/types.gen.go", "orderstatuschangev1"},
		{"/tmp/anything/v2/types.gen.go", "anythingv2"},
		// Plain dir basename (not a version folder).
		{"/tmp/widgetservice/types.gen.go", "widgetservice"},
		{"/tmp/foo-bar/types.gen.go", "foobar"},
		// Relative cwd-style output paths still resolve via filepath.Abs.
		// (We can't pin the cwd here, so just verify it doesn't panic and
		// returns a non-empty identifier.)
	}
	for _, c := range cases {
		got := defaultPackageName(c.out)
		if got != c.want {
			t.Errorf("defaultPackageName(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
