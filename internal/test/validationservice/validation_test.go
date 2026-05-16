// Acceptance test for Option B (validation knob).
//
// Three sub-tests prove:
//   - DefaultEnforced: with no config, go-jsonschema's UnmarshalJSON
//     methods are emitted; Unmarshal rejects JSON missing required
//     fields with a clear error.
//   - OptOutViaConfig: -config with omit-validation: true suppresses
//     UnmarshalJSON; Unmarshal accepts the same missing-field JSON
//     (plain Go json behaviour).
//   - OptOutViaSpecExtension: the x-aapi-codegen.omit-validation spec
//     extension achieves the same opt-out without a config file.
package validationservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	specRel     = "validation-v1.source.asyncapi.yaml"
	specRelOmit = "validation-v1-omit.source.asyncapi.yaml"
)

func TestValidation_DefaultEnforced(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "validationv1",
		"-o", filepath.Join(tmp, "types.gen.go"),
		"internal/test/validationservice/" + specRel,
	})

	src := mustRead(t, filepath.Join(tmp, "types.gen.go"))
	if !strings.Contains(src, "func (j *PingMessage) UnmarshalJSON(") {
		t.Errorf("default mode should emit PingMessage.UnmarshalJSON; got:\n%s", src)
	}

	writeBareModule(t, tmp, "validation_default")
	dropAssertions(t, tmp, defaultAssertions)
	runGo(t, tmp, []string{"test", "./..."})
}

func TestValidation_OptOutViaConfig(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "types.gen.go")
	configPath := filepath.Join(tmp, "aapi-codegen.config.yaml")
	if err := os.WriteFile(configPath, []byte(
		"package: validationv1\n"+
			"output: "+outFile+"\n"+
			"omit-validation: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-config", configPath,
		"internal/test/validationservice/" + specRel,
	})

	src := mustRead(t, outFile)
	if strings.Contains(src, "func (j *PingMessage) UnmarshalJSON(") {
		t.Errorf("opt-out-via-config should NOT emit PingMessage.UnmarshalJSON; got:\n%s", src)
	}

	writeBareModule(t, tmp, "validation_optout_config")
	dropAssertions(t, tmp, optOutAssertions)
	runGo(t, tmp, []string{"test", "./..."})
}

func TestValidation_OptOutViaSpecExtension(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "validationv1",
		"-o", filepath.Join(tmp, "types.gen.go"),
		"internal/test/validationservice/" + specRelOmit,
	})

	src := mustRead(t, filepath.Join(tmp, "types.gen.go"))
	if strings.Contains(src, "func (j *PingMessage) UnmarshalJSON(") {
		t.Errorf("opt-out-via-spec-extension should NOT emit PingMessage.UnmarshalJSON; got:\n%s", src)
	}

	writeBareModule(t, tmp, "validation_optout_ext")
	dropAssertions(t, tmp, optOutAssertions)
	runGo(t, tmp, []string{"test", "./..."})
}

// defaultAssertions is the runtime check for the default mode:
// Unmarshal of JSON missing a required field returns an error.
const defaultAssertions = `package validationv1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnmarshal_MissingRequiredErrors(t *testing.T) {
	// Missing required field "msg".
	in := []byte(` + "`" + `{"id":"x"}` + "`" + `)
	var m PingMessage
	err := json.Unmarshal(in, &m)
	if err == nil {
		t.Fatal("expected error for missing required field 'msg'")
	}
	if !strings.Contains(err.Error(), "msg") {
		t.Errorf("error should name missing field 'msg': %v", err)
	}
}

func TestUnmarshal_AdditionalPropertiesRejected(t *testing.T) {
	in := []byte(` + "`" + `{"id":"x","msg":"hi","extra":"nope"}` + "`" + `)
	var m PingMessage
	err := json.Unmarshal(in, &m)
	if err == nil {
		t.Fatal("expected error for additionalProperties (extra key)")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Errorf("error should name extra field: %v", err)
	}
}
`

// optOutAssertions is the runtime check for both opt-out paths: the
// same missing-field JSON that errored under defaults must now succeed
// (plain Go json behaviour — no UnmarshalJSON method to enforce).
const optOutAssertions = `package validationv1

import (
	"encoding/json"
	"testing"
)

func TestUnmarshal_OptOut_NoValidation(t *testing.T) {
	// Missing required field "msg" — but no UnmarshalJSON to enforce.
	in := []byte(` + "`" + `{"id":"x"}` + "`" + `)
	var m PingMessage
	if err := json.Unmarshal(in, &m); err != nil {
		t.Fatalf("opt-out mode should accept missing-field JSON: %v", err)
	}
	if m.ID != "x" {
		t.Errorf("ID: got %q, want x", m.ID)
	}
}
`

// --- helpers ---

func mustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func writeBareModule(t *testing.T, dir, name string) {
	t.Helper()
	body := "module " + name + "\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatalf("write tmp go.mod: %v", err)
	}
}

func dropAssertions(t *testing.T, dir, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "validation_test.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write assertions: %v", err)
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
	if testing.Verbose() && len(out) > 0 {
		t.Logf("go %s output:\n%s", strings.Join(args, " "), out)
	}
}
