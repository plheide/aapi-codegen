// Package widgetservice_test is the aapi-codegen acceptance suite.
//
// One test per milestone:
//
//   - TestM1_GenerateAndBuild     — payload generation + build (M1)
//   - TestM2_2_CrossTreeImport    — cross-tree $ref → import mapping (M2.2)
//   - TestM3_Publishers           — typed publishers per send op (M3)
//   - TestM4_TopicExchange        — second spec, topic exchange (M4, fixture in ../notificationservice)
//
// (The original M2.1 wire-compat shim emission was removed in M5.5/Q2 —
// generated types now stay clean; users hand-write a sibling compat.go
// when they need legacy PascalCase compatibility.)
package widgetservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	specRelPath  = "widget-v1.source.asyncapi.yaml"
	targetType   = "type WidgetCancelMessage struct"
	tmpModulePkg = "widgetv1"
)

// TestM1_GenerateAndBuild runs the aapi-codegen CLI against the fixture
// spec and asserts the generated file declares WidgetCancelMessage and
// builds.
func TestM1_GenerateAndBuild(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "types.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", tmpModulePkg,
		"-o", outFile,
		filepath.Join("internal/test/widgetservice", specRelPath),
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if !strings.Contains(string(body), targetType) {
		t.Fatalf("generated file missing %q\n--- file ---\n%s", targetType, body)
	}

	writeTmpModule(t, tmp)
	runGo(t, tmp, []string{"build", "./..."})
}

// TestM3_Publishers exercises the publisher emission end-to-end: drop
// publisher_assertions.txt into the tmp module, then run `go test`. The
// assertions construct a Publisher over a fakeTransport and check the
// observed (exchange, routingKey, body) for each `action: send`
// operation.
func TestM3_Publishers(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", tmpModulePkg,
		"-o", filepath.Join(tmp, "types.gen.go"),
		filepath.Join("internal/test/widgetservice", specRelPath),
	})

	writeTmpModule(t, tmp)
	dropAssertions(t, tmp, "publisher_assertions.txt", "publisher_test.go")

	runGo(t, tmp, []string{"test", "./..."})
}

// TestM4_TopicExchange drives a second synthetic spec (notification
// fixture, topic exchange, single-parameter templated address). The
// thesis: M3's pipeline generalises with no generator code changes —
// only the spec and the assertions differ.
func TestM4_TopicExchange(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "notificationv1",
		"-o", filepath.Join(tmp, "types.gen.go"),
		"internal/test/notificationservice/notification-v1.source.asyncapi.yaml",
	})

	writeTmpModule(t, tmp)

	// Sanity: the generated publisher must reference the right
	// exchange string at the call to transport.Publish — that's the
	// only externally-observable byte that the fake transport later
	// asserts on. (The earlier check for the literal "topic exchange"
	// substring was just grepping a doc comment, not behaviour; the
	// real topic-vs-direct distinction is invisible to the publisher
	// since both go through the same Publish(exchange, key, body) call.)
	generated, err := os.ReadFile(filepath.Join(tmp, "types.gen.go"))
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if !strings.Contains(string(generated), `"NotificationExchange"`) {
		t.Errorf("generated file should publish to NotificationExchange as a string literal:\n%s", generated)
	}

	// notification_assertions.txt lives under the notification fixture;
	// path is relative to this test's cwd (widgetservice).
	dropAssertionsFrom(t, tmp,
		"../notificationservice/notification_assertions.txt",
		"notification_test.go")

	runGo(t, tmp, []string{"test", "./..."})
}

// TestM2_2_CrossTreeImport drives the cross-tree $ref → import path,
// declared via the spec's `x-aapi-codegen.schema-packages` extension
// (M5.5/Q1). No CLI -config is needed: the spec is the source of truth
// for the mapping. Acceptance is structural — the generated widgetv1
// must import commonv1 instead of inlining MessageHeader, and the
// commonv1 + widgetv1 pair must build together.
func TestM2_2_CrossTreeImport(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()

	// commonv1 is hand-written for now (aapi-codegen does not yet
	// generate non-AsyncAPI packages — M7+ scope).
	commonHeaderBody, err := os.ReadFile("commonv1_header.txt")
	if err != nil {
		t.Fatalf("read commonv1_header.txt: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "commonv1"), 0o700); err != nil {
		t.Fatalf("mkdir commonv1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "commonv1", "header.go"), commonHeaderBody, 0o600); err != nil {
		t.Fatalf("write commonv1/header.go: %v", err)
	}

	// Generate widgetv1 into a subdirectory so import paths are
	// unambiguous (aapicodegen_acceptance_test/widgetv1 vs commonv1).
	widgetDir := filepath.Join(tmp, "widgetv1")
	if err := os.MkdirAll(widgetDir, 0o700); err != nil {
		t.Fatalf("mkdir widgetv1: %v", err)
	}

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "widgetv1",
		"-o", filepath.Join(widgetDir, "types.gen.go"),
		"internal/test/widgetservice/widget-v1-crosstree.source.asyncapi.yaml",
	})

	// Verify the generated file imports commonv1 (the spec extension's
	// schema-package mapping took effect) and does not declare its own
	// MessageHeader.
	generated, err := os.ReadFile(filepath.Join(widgetDir, "types.gen.go"))
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if !strings.Contains(string(generated), `commonv1 "aapicodegen_acceptance_test/commonv1"`) {
		t.Errorf("generated file should import commonv1; got:\n%s", generated)
	}
	if strings.Contains(string(generated), "type MessageHeader struct") {
		t.Errorf("generated file should NOT declare MessageHeader (should be imported); got:\n%s", generated)
	}
	if !strings.Contains(string(generated), "commonv1.MessageHeader") {
		t.Errorf("generated WidgetMessage.Header field should reference commonv1.MessageHeader; got:\n%s", generated)
	}

	writeTmpModule(t, tmp)
	runGo(t, tmp, []string{"build", "./..."})
}

// dropAssertions copies an assertions .txt file from the cwd into the
// tmp module under the destination *_test.go name. The .txt extension
// keeps the host package from compiling them — the symbols they
// reference only exist after generation.
func dropAssertions(t *testing.T, tmp, srcRel, dstName string) {
	t.Helper()
	dropAssertionsFrom(t, tmp, srcRel, dstName)
}

// dropAssertionsFrom is like dropAssertions but lets the caller spell
// out a path that escapes the cwd (e.g. ../notificationservice/...).
func dropAssertionsFrom(t *testing.T, tmp, srcRel, dstName string) {
	t.Helper()
	body, err := os.ReadFile(srcRel)
	if err != nil {
		t.Fatalf("read %s: %v", srcRel, err)
	}
	if err := os.WriteFile(filepath.Join(tmp, dstName), body, 0o600); err != nil {
		t.Fatalf("write %s into tmp module: %v", dstName, err)
	}
}

// writeTmpModule writes a go.mod for the tmp acceptance module.
// Generated code has no external aapi-codegen dependency, so the tmp
// module is a bare standalone with only stdlib imports — no replace
// directive needed.
func writeTmpModule(t *testing.T, dir string) {
	t.Helper()
	body := []byte(`module aapicodegen_acceptance_test

go 1.26
`)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), body, 0o600); err != nil {
		t.Fatalf("write tmp go.mod: %v", err)
	}
}

func repoRootFromTestDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// .../aapi-codegen/internal/test/widgetservice → .../aapi-codegen
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

