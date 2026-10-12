// Package sharedmsgqueueservice_test covers receive operations that share
// a message type: two routing keys on one shared queue carry the same
// AuditEvent, and a third channel delivers it from its own queue. Before
// the fix, the generator declared AuditEventHandler once per operation and
// wrote Go that didn't compile.
package sharedmsgqueueservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedMessageTypeAcrossReceiveOperations(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "sharedmsgqueuev1.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "sharedmsgqueuev1",
		"-o", outFile,
		"internal/test/sharedmsgqueueservice/sharedmsgqueuev1.source.asyncapi.yaml",
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	for _, want := range []string{
		// The shared queue gets one combined subscriber; both keys share
		// one case because they carry the same type.
		"func (s *Subscriber) SubscribeAuditLog(ctx context.Context, handler AuditLogHandler) error",
		`queueName := "audit-log"`,
		`bindingKeys := []string{"orders.cancelled", "orders.created"}`,
		`case "orders.cancelled", "orders.created":`,
		// The archive queue keeps its single-queue Subscribe.
		`queueName := "audit-archive"`,
		`bindingKeys := []string{"orders.archived"}`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, src)
		}
	}
	// Each declaration exactly once.
	for decl, want := range map[string]int{
		"type AuditEventHandler interface":          1,
		"type AuditLogHandler interface":            1,
		"func (s *Subscriber) SubscribeAuditEvent(": 1,
		"\tAuditEventHandler\n":                     1, // embedded once in AuditLogHandler
	} {
		if n := strings.Count(src, decl); n != want {
			t.Errorf("%q appears %d times, want %d\n--- file ---\n%s", decl, n, want, src)
		}
	}

	// Tmp module + runtime assertions against a recording transport.
	assertions, err := os.ReadFile("sharedmsgqueue_assertions.txt")
	if err != nil {
		t.Fatalf("read sharedmsgqueue_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "sharedmsgqueue_test.go"), assertions, 0o600); err != nil {
		t.Fatalf("write sharedmsgqueue_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_sharedmsg_test\n\ngo 1.26\n"), 0o600); err != nil {
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
