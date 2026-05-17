// Package consumerimportservice_test is the v0.5 acceptance test for
// cross-file message $ref → imported Go type:
//   - Generate the producer's package (OrderMessage + Publisher).
//   - Generate the consumer-view's package, which references the
//     producer's OrderMessage via cross-file $ref.
//   - Assert the generated Subscriber's handler signature uses the
//     IMPORTED OrderMessage type, qualified with the producer-side
//     import alias.
//   - Compile + exercise both packages together against a fake
//     transport; the same Go value flows through Publisher and
//     Subscriber without conversion.
package consumerimportservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestV05_CrossFileMessageRef(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	// Generate the producer package.
	producerDir := filepath.Join(tmp, "producerv1")
	if err := os.MkdirAll(producerDir, 0o700); err != nil {
		t.Fatalf("mkdir producerv1: %v", err)
	}
	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "producerv1",
		"-o", filepath.Join(producerDir, "types.gen.go"),
		"internal/test/consumerimportservice/producer/producerv1.source.asyncapi.yaml",
	})

	// Generate the consumer-view package.
	consumerDir := filepath.Join(tmp, "consumerv1")
	if err := os.MkdirAll(consumerDir, 0o700); err != nil {
		t.Fatalf("mkdir consumerv1: %v", err)
	}
	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "consumerv1",
		"-o", filepath.Join(consumerDir, "types.gen.go"),
		"internal/test/consumerimportservice/consumer/consumerv1.source.asyncapi.yaml",
	})

	src, err := os.ReadFile(filepath.Join(consumerDir, "types.gen.go"))
	if err != nil {
		t.Fatalf("read consumer types.gen.go: %v", err)
	}
	body := string(src)

	for _, want := range []string{
		// The producer's package is imported under the declared alias.
		`producerv1 "aapicodegen_v05_test/producerv1"`,
		// Handler interface references the IMPORTED type.
		"HandleOrderMessage(ctx context.Context, msg producerv1.OrderMessage) error",
		// Subscribe wrapper unmarshals into the imported type.
		"var msg producerv1.OrderMessage",
		// Method names stay local (no producer-side qualification).
		"func (s *Subscriber) SubscribeOrderMessage(",
		"type OrderMessageHandler interface",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("generated consumer file missing %q\n--- file ---\n%s", want, body)
		}
	}

	// Consumer must NOT declare its own OrderMessage — the whole point
	// of the message-packages mapping is to delegate to the producer's
	// type. Duplication here would defeat the design.
	if strings.Contains(body, "type OrderMessage struct") {
		t.Errorf("consumer declared its own OrderMessage — should be imported from producer:\n%s", body)
	}

	// Compile both packages together + run a smoke test that the same
	// OrderMessage value flows through Publisher and Subscriber.
	dropAssertions(t, tmp)
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_v05_test\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write tmp go.mod: %v", err)
	}
	runGo(t, tmp, []string{"test", "./..."})
}

// TestV05_MissingMessagePackageMapping proves the lowerer errors with
// a clear pointer when a cross-file message $ref has no mapping —
// catches the "spec author forgot to declare message-packages" case at
// codegen time rather than producing broken Go.
func TestV05_MissingMessagePackageMapping(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	// Same as the consumer fixture but with the message-packages block
	// stripped. The cross-file $ref then has nothing to resolve to.
	specPath := filepath.Join(tmp, "spec.source.asyncapi.yaml")
	specBody := `asyncapi: 3.1.0
info: { title: missing-mapping, version: 1.0.0 }
defaultContentType: application/json
channels:
  c:
    address: '{tenant}'
    parameters: { tenant: { description: x } }
    bindings:
      amqp:
        is: queue
        queue: { name: '{tenant}', durable: true }
        bindingVersion: 0.3.0
    messages:
      OrderMessage:
        $ref: '/nonexistent/producer.yaml#/channels/orderDispatch/messages/OrderMessage'
operations:
  consume:
    action: receive
    channel: { $ref: '#/channels/c' }
    messages: [ { $ref: '#/channels/c/messages/OrderMessage' } ]
`
	if err := os.WriteFile(specPath, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	outFile := filepath.Join(tmp, "out.gen.go")
	cmd := exec.Command("go", "run", "./cmd/aapi-codegen", "-package", "x", "-o", outFile, specPath)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected codegen to fail without a message-packages mapping; got success and:\n%s", out)
	}
	if !strings.Contains(string(out), "message-packages") {
		t.Errorf("error should mention `message-packages` mapping; got:\n%s", out)
	}
}

func dropAssertions(t *testing.T, tmp string) {
	t.Helper()
	body, err := os.ReadFile("import_assertions.txt")
	if err != nil {
		t.Fatalf("read import_assertions.txt: %v", err)
	}
	// The assertions live in the consumer package and exercise the
	// imported type end-to-end.
	dst := filepath.Join(tmp, "consumerv1", "import_test.go")
	if err := os.WriteFile(dst, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", dst, err)
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
