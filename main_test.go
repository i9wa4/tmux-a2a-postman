package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestMain_Version(t *testing.T) {
	// Build the binary with a known revision
	cmd := exec.Command("go", "build", "-ldflags", "-X main.revision=test123", "-o", "postman_test_bin", ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	defer func() { _ = os.Remove("postman_test_bin") }()

	// Run version subcommand
	out, err := exec.Command("./postman_test_bin", "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version subcommand failed: %v\n%s", err, out)
	}

	expected := "postman dev (rev: test123)\n"
	if string(out) != expected {
		t.Errorf("got %q, want %q", string(out), expected)
	}
}

func TestMain_NoArgs(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "postman_test_bin", ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	defer func() { _ = os.Remove("postman_test_bin") }()

	out, err := exec.Command("./postman_test_bin").CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}

	expected := "postman: not yet implemented\n"
	if string(out) != expected {
		t.Errorf("got %q, want %q", string(out), expected)
	}
}
