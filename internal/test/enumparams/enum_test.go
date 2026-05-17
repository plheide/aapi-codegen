// Package enumparams_test is the v0.4 acceptance test for typed
// channel parameters: parameters with `schema.type: string` + an
// `enum` list lower to typed Go enum types, dedupe across channels,
// and surface in both Publisher and Subscriber signatures.
package enumparams_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestV04_TypedEnumParameters(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "enumparamsv1.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "enumparamsv1",
		"-o", outFile,
		"internal/test/enumparams/enumparamsv1.source.asyncapi.yaml",
	})

	src, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	body := string(src)

	for _, want := range []string{
		"type JobType string",
		`JobTypeBuild    JobType = "build"`,
		`JobTypeDeploy   JobType = "deploy"`,
		`JobTypeRollback JobType = "rollback"`,
		// Publisher signature uses the enum type, not string.
		"jobType JobType,",
		// Routing-key builder wraps the enum in string() so `+` type-checks.
		`routingKey := tenant + "." + string(jobType)`,
		// Subscriber uses the enum type for the queue parameter.
		`queueName := string(jobType) + ".results"`,
		// Other (non-enum) parameter stays string-typed.
		"tenant string,",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, body)
		}
	}

	// Dedup: JobType is referenced by two channels (jobDispatch +
	// jobResultQueue) but the type + const block must appear exactly once.
	if n := strings.Count(body, "type JobType string"); n != 1 {
		t.Errorf("JobType declared %d times, want 1 (lowerer should dedup)", n)
	}

	// Runtime exercise.
	assertions, err := os.ReadFile("enum_assertions.txt")
	if err != nil {
		t.Fatalf("read enum_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "enum_test.go"), assertions, 0o600); err != nil {
		t.Fatalf("write enum_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_v04_test\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write tmp go.mod: %v", err)
	}
	runGo(t, tmp, []string{"test", "./..."})
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
