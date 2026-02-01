package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBaseDir(t *testing.T) {
	t.Run("POSTMAN_HOME priority", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "/tmp/custom-postman")
		t.Setenv("XDG_STATE_HOME", "")
		if got := resolveBaseDir(); got != "/tmp/custom-postman" {
			t.Errorf("POSTMAN_HOME: got %q, want %q", got, "/tmp/custom-postman")
		}
	})

	t.Run("backward compat: .postman exists", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		tmpDir := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd failed: %v", err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		if err := os.Mkdir(".postman", 0o755); err != nil {
			t.Fatalf("Mkdir failed: %v", err)
		}

		if got := resolveBaseDir(); got != ".postman" {
			t.Errorf(".postman exists: got %q, want %q", got, ".postman")
		}
	})

	t.Run("XDG_STATE_HOME", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "")
		t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
		tmpDir := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd failed: %v", err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		// NOTE: .postman does NOT exist in CWD

		if got := resolveBaseDir(); got != "/tmp/xdg-state/postman" {
			t.Errorf("XDG_STATE_HOME: got %q, want %q", got, "/tmp/xdg-state/postman")
		}
	})

	t.Run("fallback to .postman", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "")
		tmpDir := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd failed: %v", err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		// NOTE: .postman does NOT exist in CWD, HOME is empty

		if got := resolveBaseDir(); got != ".postman" {
			t.Errorf("fallback: got %q, want %q", got, ".postman")
		}
	})
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
