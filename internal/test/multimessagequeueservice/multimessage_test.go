// Package multimessagequeueservice_test is the v0.7 acceptance test: two
// receive operations that share one queue (distinct fixed routing keys,
// distinct message types) generate ONE combined Subscribe<Queue> method
// that binds both keys and dispatches by routing key — not two competing
// per-message Subscribers.
package multimessagequeueservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestV07_SharedQueueMultipleMessageTypes(t *testing.T) {
	repoRoot := repoRootFromTestDir(t)
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "multimessagev1.gen.go")

	runGo(t, repoRoot, []string{
		"run", "./cmd/aapi-codegen",
		"-package", "multimessagev1",
		"-o", outFile,
		"internal/test/multimessagequeueservice/multimessagev1.source.asyncapi.yaml",
	})

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	for _, want := range []string{
		// One combined method named after the shared queue.
		"func (s *Subscriber) SubscribeEventsWorker(ctx context.Context, handler EventsWorkerHandler) error",
		// Combined handler interface embeds the per-message handlers.
		"type EventsWorkerHandler interface {",
		"type CreatedEventHandler interface {",
		"type UpdatedEventHandler interface {",
		// Queue name from x-aapi-codegen.queue.name; both binding keys
		// (addresses), in alphabetical operation order.
		`queueName := "events-worker"`,
		`bindingKeys := []string{"events.created", "events.updated"}`,
		// Dispatch is by routing key, not trial-unmarshal.
		"switch routingKey {",
		`case "events.created":`,
		`case "events.updated":`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file missing %q\n--- file ---\n%s", want, src)
		}
	}

	// Must NOT emit a per-message Subscribe for a shared-queue message —
	// that's the pre-v0.7 shape that would compete on one queue.
	for _, unwanted := range []string{
		"func (s *Subscriber) SubscribeCreatedEvent(",
		"func (s *Subscriber) SubscribeUpdatedEvent(",
	} {
		if strings.Contains(src, unwanted) {
			t.Errorf("generated file unexpectedly contains %q (shared-queue messages must not get their own Subscribe)\n--- file ---\n%s", unwanted, src)
		}
	}

	// Tmp module + runtime assertions against a recording transport.
	assertions, err := os.ReadFile("multimessage_assertions.txt")
	if err != nil {
		t.Fatalf("read multimessage_assertions.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "multimessage_test.go"), assertions, 0o600); err != nil {
		t.Fatalf("write multimessage_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"),
		[]byte("module aapicodegen_v07_test\n\ngo 1.26\n"), 0o600); err != nil {
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
