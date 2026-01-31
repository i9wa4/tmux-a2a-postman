package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain_Version(t *testing.T) {
	cmd := exec.Command("go", "build", "-ldflags", "-X main.revision=test123", "-o", "postman_test_bin", ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	defer func() { _ = os.Remove("postman_test_bin") }()

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
	if err == nil {
		t.Fatal("expected non-zero exit code for no args")
	}

	if len(out) == 0 {
		t.Error("expected usage message on stderr, got empty output")
	}
}

func TestSessionLock_Acquire(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "postman.lock")

	// Normal acquire
	lock1, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("first lock acquire failed: %v", err)
	}

	// Double-acquire should fail
	_, err = NewSessionLock(lockPath)
	if err == nil {
		t.Fatal("expected error on double acquire, got nil")
	}

	_ = lock1.Release()
}

func TestSessionLock_Release(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "postman.lock")

	lock1, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("first lock acquire failed: %v", err)
	}

	if err := lock1.Release(); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// Re-acquire after release should succeed
	lock2, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	_ = lock2.Release()
}

func TestResolveBaseDir(t *testing.T) {
	// Test default
	origVal := os.Getenv("POSTMAN_HOME")
	defer func() {
		if origVal != "" {
			_ = os.Setenv("POSTMAN_HOME", origVal)
		} else {
			_ = os.Unsetenv("POSTMAN_HOME")
		}
	}()

	_ = os.Unsetenv("POSTMAN_HOME")
	if got := resolveBaseDir(); got != ".postman" {
		t.Errorf("default: got %q, want %q", got, ".postman")
	}

	// Test POSTMAN_HOME priority
	_ = os.Setenv("POSTMAN_HOME", "/tmp/custom-postman")
	if got := resolveBaseDir(); got != "/tmp/custom-postman" {
		t.Errorf("POSTMAN_HOME: got %q, want %q", got, "/tmp/custom-postman")
	}
}

func TestCreateSessionDirs(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	if err := createSessionDirs(sessionDir); err != nil {
		t.Fatalf("createSessionDirs failed: %v", err)
	}

	expectedDirs := []string{"inbox", "post", "draft", "read", "dead-letter"}
	for _, d := range expectedDirs {
		path := filepath.Join(sessionDir, d)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("directory %q not created: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
}
