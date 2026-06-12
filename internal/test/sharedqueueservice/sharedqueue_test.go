// Package sharedqueueservice_test is the v0.6 acceptance test: a
// routingKey-mode consumer channel whose x-aapi-codegen.queue.name
// differs from the channel address generates a Subscribe that passes
// the queue name and the address-derived binding key to the transport
// as separate values.
package sharedqueueservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestV06_SharedQueueDistinctBindingKey(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "sharedqueuev1.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "sharedqueuev1",
		"-o", outFile,
		"internal/test/sharedqueueservice/sharedqueuev1.source.asyncapi.yaml",
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	for _, want := range []string{
		// The v0.6 transport contract.
		"Subscribe(ctx context.Context, queueName string, bindingKeys []string, handler func(ctx context.Context, routingKey string, body []byte) error) error",
		// Queue name comes from x-aapi-codegen.queue.name (literal,
		// distinct from the address)...
		`queueName := "shared-status-worker"`,
		// ...while the binding key comes from the channel address.
		`bindingKeys := []string{"status.events"}`,
		// Parameterless channel still emits the method (the tmp-module
		// runtime test below proves the (ctx, handler)-only signature by
		// compiling a two-arg call).
		"func (s *Subscriber) SubscribeStatusEvent(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, src)
		}
	}

	// Tmp module + runtime assertions against a recording transport.
	assertions, err := os.ReadFile("sharedqueue_assertions.txt")
	if err != nil {
		t.Fatalf("read sharedqueue_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "sharedqueue_test.go"), assertions, 0o600); err != nil {
		t.Fatalf("write sharedqueue_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_v06_test\n\ngo 1.26\n"), 0o600); err != nil {
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
