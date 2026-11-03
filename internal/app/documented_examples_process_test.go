package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDocumentedMinimalFirstExchange(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	f := newRebuiltCLIFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "scripts/validation/notification-smoke.py", "--postman", f.bin,
		"--agent", "fixture", "--case", "rally", "--terminal", "both", "--timeout", "10",
		"--postman-md", "testdata/config/minimal.md", "--output", filepath.Join(f.dir, "documented-first-exchange.json"))
	cmd.Dir = repositorySource(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("documented config with actual recipient-owned POP/full-read/reply: %v\n%s", err, out)
	}
}
