// Package paramqueueservice_test covers queue names that use the channel
// address's parameters but differ from the address, such as
// "{tenant}.audit-log" on the address "{tenant}.orders.created". Before the
// fix, the generator wrote such a name as a quoted literal, braces included,
// so the generated code compiled and declared the wrong queue.
package paramqueueservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueueNameFromAddressParameters(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "paramqueuev1.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "paramqueuev1",
		"-o", outFile,
		"internal/test/paramqueueservice/paramqueuev1.source.asyncapi.yaml",
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	for _, want := range []string{
		// Every address parameter in the queue name.
		`queueName := tenant + ".audit-log"`,
		`bindingKeys := []string{tenant + ".orders.created"}`,
		// Only the typed parameter, with its string() cast as in the key.
		`queueName := "archive." + string(region)`,
		`bindingKeys := []string{string(region) + "." + tenant + ".orders.archived"}`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, src)
		}
	}
	if strings.Contains(src, `queueName := "{`) {
		t.Errorf("generated file still quotes a queue name's parameter placeholder\n--- file ---\n%s", src)
	}

	// Tmp module + runtime assertions against a recording transport.
	assertions, err := os.ReadFile("paramqueue_assertions.txt")
	if err != nil {
		t.Fatalf("read paramqueue_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "paramqueue_test.go"), assertions, 0o600); err != nil {
		t.Fatalf("write paramqueue_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_paramqueue_test\n\ngo 1.26\n"), 0o600); err != nil {
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
	if testing.Verbose() && len(out) > 0 {
		t.Logf("go %s output:\n%s", strings.Join(args, " "), out)
	}
}
