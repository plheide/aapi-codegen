// Package validatedparams_test is the v0.4.1 acceptance test for
// pattern-validated channel parameters: parameters with `schema.pattern`
// lower to typed wrappers with NewX/MustX constructors. Also exercises
// the `omit-validation: true` off-switch: when set, pattern wrappers
// are skipped and the parameter falls back to plain string.
package validatedparams_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestV041_PatternValidatedParameters(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "validatedparamsv1.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "validatedparamsv1",
		"-o", outFile,
		"internal/test/validatedparams/validatedparamsv1.source.asyncapi.yaml",
	})

	src, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	body := string(src)

	for _, want := range []string{
		"type DataPartitionID string",
		"var dataPartitionIDPattern = regexp.MustCompile(`^slb-\\d{5}$`)",
		"func NewDataPartitionID(v string) (DataPartitionID, error)",
		"func MustDataPartitionID(v string) DataPartitionID",
		// Publisher signature uses the typed wrapper.
		"dataPartitionID DataPartitionID,",
		`routingKey := string(dataPartitionID) + "." + workflowName`,
		// Subscriber too.
		`queueName := string(dataPartitionID) + ".results"`,
		// Un-typed param stays string.
		"workflowName string,",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, body)
		}
	}

	// Dedup: DataPartitionID is referenced by two channels but the
	// type + var + constructors must appear exactly once.
	if n := strings.Count(body, "type DataPartitionID string"); n != 1 {
		t.Errorf("DataPartitionID declared %d times, want 1 (lowerer should dedup)", n)
	}
	if n := strings.Count(body, "func NewDataPartitionID("); n != 1 {
		t.Errorf("NewDataPartitionID declared %d times, want 1", n)
	}

	// Runtime exercise.
	assertions, err := os.ReadFile("validated_assertions.txt")
	if err != nil {
		t.Fatalf("read validated_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "validated_test.go"), assertions, 0o600); err != nil {
		t.Fatalf("write validated_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_v041_test\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write tmp go.mod: %v", err)
	}
	runGo(t, tmp, []string{"test", "./..."})
}

// TestV041_OmitValidationFallsBackToString proves that adding
// `x-aapi-codegen.omit-validation: true` to the spec drops the
// pattern wrapper entirely — the parameter reverts to `string`.
// Enum-typed params (different fixture, v0.4 path) are unaffected;
// only pattern wrappers respect the knob.
func TestV041_OmitValidationFallsBackToString(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	// Build a one-off spec that flips omit-validation on. Borrows the
	// pattern from the main fixture; only the spec extension differs.
	specPath := filepath.Join(tmp, "spec.source.asyncapi.yaml")
	specBody := `asyncapi: 3.1.0
info:
  title: omit-validation test
  version: 1.0.0
defaultContentType: application/json
x-aapi-codegen:
  omit-validation: true
channels:
  c:
    address: '{dataPartitionID}'
    parameters:
      dataPartitionID:
        schema:
          type: string
          pattern: '^slb-\d{5}$'
    bindings:
      amqp:
        is: routingKey
        exchange: { name: X, type: direct }
        bindingVersion: 0.3.0
    messages:
      M:
        name: M
        contentType: application/json
        payload:
          type: object
          required: [id]
          additionalProperties: false
          properties: { id: { type: string } }
operations:
  send:
    action: send
    channel: { $ref: '#/channels/c' }
    messages: [ { $ref: '#/channels/c/messages/M' } ]
`
	if err := os.WriteFile(specPath, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	outFile := filepath.Join(tmp, "out.gen.go")
	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "omittedv1",
		"-o", outFile,
		specPath,
	})
	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(body)
	if strings.Contains(s, "type DataPartitionID string") {
		t.Errorf("omit-validation should drop the typed wrapper:\n%s", s)
	}
	if strings.Contains(s, "regexp.MustCompile") {
		t.Errorf("omit-validation should skip regex compile:\n%s", s)
	}
	if !strings.Contains(s, "dataPartitionID string,") {
		t.Errorf("parameter should fall back to plain string under omit-validation:\n%s", s)
	}
}

func repoRootFromTestDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func runGo(t *testing.T, dir string, args []string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s in %s failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}
