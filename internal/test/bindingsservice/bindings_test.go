// Package bindingsservice_test is the v0.3 acceptance test for
// SendOption + spec-declared AMQP message/operation bindings.
package bindingsservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestV03_BindingsAndSendOptions(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "bindingsv1.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "bindingsv1",
		"-o", outFile,
		"internal/test/bindingsservice/bindingsv1.source.asyncapi.yaml",
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	// v0.3 surface: PublishProperties, SendOption + With* helpers,
	// Publish signature with the properties arg.
	for _, want := range []string{
		"type PublishProperties struct",
		"type SendOption func(*PublishProperties)",
		"func WithContentType(",
		"func WithContentEncoding(",
		"func WithMessageType(",
		"func WithPriority(v uint8) SendOption",
		"func WithExpirationMillis(",
		"Publish(ctx context.Context, exchange, routingKey string, body []byte, props PublishProperties) error",
		"opts ...SendOption,",
		// Spec defaults inlined into SendJob.
		`ContentEncoding: "utf-8"`,
		`MessageType:     "jobs.dispatch.v1"`,
		"Priority:        priorityPtr(5)",
		`Expiration:      "60000"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, src)
		}
	}

	// SendNotify has no bindings: only ContentType default should land.
	if !strings.Contains(src, `func (p *Publisher) SendNotify(`) {
		t.Fatalf("SendNotify missing")
	}
	notifyBlock := extractFunc(src, "SendNotify")
	if !strings.Contains(notifyBlock, `ContentType: "application/json"`) {
		t.Errorf("SendNotify should default ContentType from defaultContentType:\n%s", notifyBlock)
	}
	for _, leaked := range []string{"MessageType", "ContentEncoding", "Priority", "Expiration"} {
		if strings.Contains(notifyBlock, leaked+":") {
			t.Errorf("SendNotify should NOT default %s (no spec binding):\n%s", leaked, notifyBlock)
		}
	}

	// Tmp module + runtime assertions.
	assertions, err := os.ReadFile("bindings_assertions.txt")
	if err != nil {
		t.Fatalf("read bindings_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "bindings_test.go"), assertions, 0o600); err != nil {
		t.Fatalf("write bindings_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_v03_test\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write tmp go.mod: %v", err)
	}
	runGo(t, tmp, []string{"test", "./..."})
}

// extractFunc returns the substring of src from the line declaring
// funcName through the next top-level closing brace. Crude but
// sufficient for grepping a single generated Send method body.
func extractFunc(src, funcName string) string {
	needle := "func (p *Publisher) " + funcName + "("
	start := strings.Index(src, needle)
	if start < 0 {
		return ""
	}
	// Find the matching closing brace at column 0.
	tail := src[start:]
	depth := 0
	for i, r := range tail {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return tail[:i+1]
			}
		}
	}
	return tail
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
