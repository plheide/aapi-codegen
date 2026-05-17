// Package consumerservice_test is the v0.2 acceptance test: the
// generator emits a Handler interface, Subscriber struct, and
// Subscribe<MessageName> method per `action: receive` operation; the
// SubscribeTransport interface + ErrDrop sentinel are present; ack
// semantics map handler return → transport observation as documented.
package consumerservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestV02_ReceiveOperations runs aapi-codegen against the consumerv1
// fixture and asserts the generated file declares the expected
// subscriber surface, then exercises it in a tmp module against a fake
// transport.
func TestV02_ReceiveOperations(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "consumerv1.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "consumerv1",
		"-o", outFile,
		"internal/test/consumerservice/consumerv1.source.asyncapi.yaml",
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	// Subscriber surface: interface, struct, constructor, ErrDrop, plus
	// the per-message Handler interface and Subscribe method.
	for _, want := range []string{
		"type SubscribeTransport interface",
		"var ErrDrop = errors.New",
		"type Subscriber struct",
		"func NewSubscriber(transport SubscribeTransport) *Subscriber",
		"type WorkItemHandler interface",
		"HandleWorkItem(ctx context.Context, msg WorkItem) error",
		"func (s *Subscriber) SubscribeWorkItem(",
		"type DeadLetterHandler interface",
		"HandleDeadLetter(ctx context.Context, msg DeadLetter) error",
		"func (s *Subscriber) SubscribeDeadLetter(",
		// Templated queue name interpolates the tenant arg:
		`queueName := tenant + ".work"`,
		// Static queue name appears as a literal:
		`queueName := "dlq.consumer-service"`,
		// Poison payloads get joined with ErrDrop:
		"errors.Join(ErrDrop, err)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, src)
		}
	}

	// Coexistence: publisher half of the spec also emits.
	for _, want := range []string{
		"type Transport interface",
		"func (p *Publisher) SendReport(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing publisher half %q", want)
		}
	}

	// Tmp module + assertions, exercise the whole surface.
	assertions, err := os.ReadFile("consumer_assertions.txt")
	if err != nil {
		t.Fatalf("read consumer_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "consumer_test.go"), assertions, 0o600); err != nil {
		t.Fatalf("write consumer_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_v02_test\n\ngo 1.26\n"), 0o600); err != nil {
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
