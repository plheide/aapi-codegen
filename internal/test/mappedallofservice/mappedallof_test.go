// Package mappedallofservice_test covers a payload composed with allOf
// from a header schema that schema-packages maps to another Go package,
// under unevaluatedProperties: false. Up to go-jsonschema v0.28.0 the
// generated file imported the mapped package without using it, so it
// didn't compile, and unevaluatedProperties went unchecked.
package mappedallofservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllOfMemberFromMappedPackage(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	// The mapped package's stand-in, so that an import of it resolves.
	commonHeader, err := os.ReadFile("commonv1_header.txt")
	if err != nil {
		t.Fatalf("read commonv1_header.txt: %v", err)
	}
	writeFile(t, filepath.Join(tmp, "commonv1", "header.go"), commonHeader)

	// aapi-codegen doesn't create the output's directory.
	orderDir := filepath.Join(tmp, "orderv1")
	if err := os.MkdirAll(orderDir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", orderDir, err)
	}
	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "orderv1",
		"-o", filepath.Join(orderDir, "orderv1.gen.go"),
		"internal/test/mappedallofservice/mappedallofv1.source.asyncapi.yaml",
	})

	assertions, err := os.ReadFile("mappedallof_assertions.txt")
	if err != nil {
		t.Fatalf("read mappedallof_assertions.txt: %v", err)
	}
	writeFile(t, filepath.Join(orderDir, "mappedallof_test.go"), assertions)
	writeFile(t, filepath.Join(tmp, "go.mod"), []byte("module aapicodegen_mappedallof_test\n\ngo 1.26\n"))

	// Builds the generated package and runs the runtime assertions.
	runGo(t, tmp, []string{"test", "./..."})
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
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
