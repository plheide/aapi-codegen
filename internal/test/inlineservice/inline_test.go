// Package inlineservice_test is the M5.5/Q4 acceptance test:
// inline message payloads + components.schemas materialize correctly,
// the generated package contains the right type names, and the result
// builds.
package inlineservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestQ4_InlinePayloadsAndComponents drives aapi-codegen against the
// inlinev1 fixture and asserts that:
//   - inline payloads materialize to Go types named after the message
//     key by default ("InlineMessage"),
//   - an explicit `title` on the payload overrides ("InlineCancelMsg"),
//   - components.schemas types materialize once and are shared
//     ("Tag"), with #/components/schemas/Tag refs resolving to it,
//   - the generated package builds standalone (no missing types, no
//     stray imports).
func TestQ4_InlinePayloadsAndComponents(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "types.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "inlinev1",
		"-o", outFile,
		"internal/test/inlineservice/inlinev1.source.asyncapi.yaml",
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	// Type names: derived from message keys, with explicit title override.
	for _, want := range []string{
		"type InlineMessage struct",      // message key → Go type name
		"type InlineCancelMsg struct",    // explicit title overrides message key
		"type Tag struct",                // components.schemas entry
		"type InlineAuditMessage struct", // inline payload with cross-tree file $ref
		"type AuditID struct",            // target of that cross-tree $ref — proves
		// rewriteRefs absolutised the relative path so go-jsonschema could
		// follow it from the synthetic tmp file. Regression guard for the
		// "inline-payload + cross-tree file ref" case faservices specs use.
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, src)
		}
	}

	// Tag must appear ONCE (not duplicated per payload that references it).
	if n := strings.Count(src, "type Tag struct"); n != 1 {
		t.Errorf("type Tag declared %d times, want 1 (components.schemas should dedupe)\n--- file ---\n%s", n, src)
	}

	// Both payloads should reference the shared Tag type.
	if !strings.Contains(src, "Tags []Tag") {
		t.Errorf("InlineMessage.Tags should be []Tag (referencing components.schemas.Tag)\n--- file ---\n%s", src)
	}
	if !strings.Contains(src, "Tag *Tag") && !strings.Contains(src, "Tag Tag ") {
		t.Errorf("InlineCancelMsg.Tag should reference Tag (component)\n--- file ---\n%s", src)
	}

	// Publisher emission still works for inline-payload messages.
	for _, want := range []string{
		"func (p *Publisher) SendInline(",
		"func (p *Publisher) SendInlineCancel(",
		"func (p *Publisher) SendInlineAudit(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing publisher method %q", want)
		}
	}

	// Drop the runtime assertions and exercise the generated package
	// for real — earlier this test only `go build`-ed, which would
	// silently pass any semantically-broken-but-syntactically-valid
	// publisher emission.
	assertionsBody, err := os.ReadFile("inline_assertions.txt")
	if err != nil {
		t.Fatalf("read inline_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "inline_test.go"), assertionsBody, 0o600); err != nil {
		t.Fatalf("write inline_test.go into tmp module: %v", err)
	}

	// Tmp module is a bare standalone (no aapi-codegen runtime dep,
	// since Q2 dropped wire-compat shim emission).
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_q4_test\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write tmp go.mod: %v", err)
	}
	runGo(t, tmp, []string{"test", "./..."})
}

// --- test helpers (mirrored from widgetservice/golden_test.go because
// Go test packages can't share types across directories). ---

func repoRootFromTestDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// .../aapi-codegen/internal/test/inlineservice → .../aapi-codegen
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
