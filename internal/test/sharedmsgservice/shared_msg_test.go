// Acceptance test for the components.messages feature.
//
// Asserts the dedup contract: two channels referencing the same
// components.messages entry must produce ONE payload Go type plus
// TWO Send publisher methods, both taking the shared type.
package sharedmsgservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentsMessages_DedupAndPublish(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "types.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "sharedv1",
		"-o", outFile,
		"internal/test/sharedmsgservice/shared-msg-v1.source.asyncapi.yaml",
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	// Dedup contract: StatusChangePayload appears as a type EXACTLY
	// once. Two declarations would mean the shared component
	// materialized twice — a regression of the dedup behaviour.
	if n := strings.Count(src, "type StatusChangePayload struct"); n != 1 {
		t.Errorf("type StatusChangePayload declared %d times, want 1 (cross-channel component dedup):\n%s", n, src)
	}

	// Both operations get a Send method, and both take the shared
	// payload type.
	for _, want := range []string{
		"func (p *Publisher) SendPrimary(",
		"func (p *Publisher) SendShadow(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing publisher method %q\n%s", want, src)
		}
	}
	if n := strings.Count(src, "msg StatusChangePayload"); n != 2 {
		t.Errorf("expected 2 Send methods taking msg StatusChangePayload, got %d:\n%s", n, src)
	}

	// Each operation's publisher targets its own exchange — the shared
	// payload doesn't collapse the channels themselves.
	for _, want := range []string{
		`"PrimaryStatus"`,
		`"ShadowStatus"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing exchange string %s\n%s", want, src)
		}
	}

	// Build the generated code in a bare standalone module — proves
	// it has no missing imports or types after dedup.
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_sharedmsg_test\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write tmp go.mod: %v", err)
	}

	// Drop publisher assertions into the tmp module to exercise the
	// runtime behaviour end-to-end.
	if err := os.WriteFile(filepath.Join(tmp, "shared_publisher_test.go"),
		[]byte(publisherAssertions), 0o600); err != nil {
		t.Fatalf("write publisher assertions: %v", err)
	}

	runGo(t, tmp, []string{"test", "./..."})
}

// publisherAssertions is the per-channel publisher behaviour check.
// Stored as a string literal (rather than a .txt sibling) because
// it's short and self-contained for this fixture.
const publisherAssertions = `package sharedv1

import (
	"context"
	"strings"
	"testing"
)

type fakeTransport struct {
	exchange   string
	routingKey string
	body       []byte
}

func (f *fakeTransport) Publish(_ context.Context, exchange, routingKey string, body []byte) error {
	f.exchange = exchange
	f.routingKey = routingKey
	f.body = append(f.body[:0], body...)
	return nil
}

func TestPublisher_SendPrimary(t *testing.T) {
	ft := &fakeTransport{}
	p := NewPublisher(ft)
	msg := StatusChangePayload{ID: "ord-1", NewStatus: "paid"}
	if err := p.SendPrimary(context.Background(), "Order", msg); err != nil {
		t.Fatalf("SendPrimary: %v", err)
	}
	if ft.exchange != "PrimaryStatus" {
		t.Errorf("exchange: got %q, want PrimaryStatus", ft.exchange)
	}
	if ft.routingKey != "Order" {
		t.Errorf("routingKey: got %q, want Order", ft.routingKey)
	}
	if !strings.Contains(string(ft.body), ` + "`" + `"id":"ord-1"` + "`" + `) {
		t.Errorf("body should contain camelCase id: %s", ft.body)
	}
}

func TestPublisher_SendShadow(t *testing.T) {
	ft := &fakeTransport{}
	p := NewPublisher(ft)
	msg := StatusChangePayload{ID: "ord-1", NewStatus: "paid"}
	if err := p.SendShadow(context.Background(), "Order", msg); err != nil {
		t.Fatalf("SendShadow: %v", err)
	}
	if ft.exchange != "ShadowStatus" {
		t.Errorf("exchange: got %q, want ShadowStatus", ft.exchange)
	}
	// Shadow channel address is "shadow.{entityType}" — the literal
	// prefix is what distinguishes the two exchanges' routing.
	if ft.routingKey != "shadow.Order" {
		t.Errorf("routingKey: got %q, want shadow.Order", ft.routingKey)
	}
}
`

// --- helpers (mirror the other fixture packages) ---

func repoRootFromTestDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// .../aapi-codegen/internal/test/sharedmsgservice → .../aapi-codegen
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
