package messagecollisionservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMaterialize_KeyCollision_NoOverwrite drives the regression
// fixture where a components.messages and an inline channel message
// share the same channel-level key (`SharedMsg`). Before the
// materializer placed the two into distinct subdirs (components/messages/
// vs messages/), the second materialize silently overwrote the first
// — only one Go type was emitted. The fix should produce both
// ComponentSharedMsg and InlineSharedMsg in the output.
func TestMaterialize_KeyCollision_NoOverwrite(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, "..", "..", ".."))

	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "types.gen.go")

	cmd := exec.Command("go",
		"run", "./cmd/aapi-codegen",
		"-package", "collisionv1",
		"-o", outFile,
		"internal/test/messagecollisionservice/collision-v1.source.asyncapi.yaml",
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("aapi-codegen failed: %v\n%s", err, out)
	}

	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	src := string(body)

	// Both types must be present. If the materialize collision is back,
	// one of the two payloads will have overwritten the other on disk
	// and only one type will appear here.
	if !strings.Contains(src, "type ComponentSharedMsg struct") {
		t.Errorf("missing type ComponentSharedMsg; generated:\n%s", src)
	}
	if !strings.Contains(src, "type InlineSharedMsg struct") {
		t.Errorf("missing type InlineSharedMsg; generated:\n%s", src)
	}

	// Sanity: the field unique to each schema must appear in the
	// right type. Field names appear in struct-tag form. Catches a
	// regression where the two schemas got swapped or merged.
	if !strings.Contains(src, `ComponentField string `+"`json:\"componentField\"`") {
		t.Errorf("missing ComponentSharedMsg.ComponentField; generated:\n%s", src)
	}
	if !strings.Contains(src, `InlineField string `+"`json:\"inlineField\"`") {
		t.Errorf("missing InlineSharedMsg.InlineField; generated:\n%s", src)
	}
}
