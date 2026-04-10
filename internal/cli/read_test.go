package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRead_DeadLettersFlag(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	err := RunRead([]string{"--dead-letters"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("unexpected flag-parse error: %v", err)
	}
}

func TestRunRead_ArchivedFlag(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := RunRead([]string{"--archived"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("unexpected flag-parse error: %v", err)
	}
}

func TestRunRead_ArchivedSessionPrefixedRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "review-session", "worker")
	contextID := "ctx-read-archived-prefixed"
	readDir := filepath.Join(tmpDir, contextID, "review-session", "read")
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		t.Fatalf("MkdirAll readDir: %v", err)
	}

	filename := "20260328-123500-from-orchestrator-to-review-session:worker.md"
	content := messageFixture("orchestrator", "review-session:worker", "Archived cross-session payload")
	if err := os.WriteFile(filepath.Join(readDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile archived message: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunRead([]string{"--archived", "--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunRead --archived: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, filename) {
		t.Fatalf("archived listing missing session-prefixed recipient file: stdout=%q", stdout)
	}
}

func TestRunRead_DeadLettersFileRejectsPermanentFailure(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "review-session", "worker")
	contextID := "ctx-read-dead-file"
	sessionDir := filepath.Join(tmpDir, contextID, "review-session")
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	postDir := filepath.Join(sessionDir, "post")
	if err := os.MkdirAll(deadLetterDir, 0o700); err != nil {
		t.Fatalf("MkdirAll deadLetterDir: %v", err)
	}
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		t.Fatalf("MkdirAll postDir: %v", err)
	}

	filename := "20260328-123500-from-orchestrator-to-worker-dl-routing-denied.md"
	deadLetterPath := filepath.Join(deadLetterDir, filename)
	if err := os.WriteFile(deadLetterPath, []byte("content"), 0o600); err != nil {
		t.Fatalf("WriteFile dead-letter: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunRead([]string{"--dead-letters", "--file", filename, "--context-id", contextID})
	})
	if err == nil {
		t.Fatal("expected permanent resend rejection, got nil")
	}
	if !strings.Contains(err.Error(), "not resendable") || !strings.Contains(err.Error(), "routing-denied") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty on rejection: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr should be empty on rejection: %q", stderr)
	}

	if _, err := os.Stat(deadLetterPath); err != nil {
		t.Fatalf("dead-letter file should remain after rejection: %v", err)
	}
	resentPath := filepath.Join(postDir, "20260328-123500-from-orchestrator-to-worker.md")
	if _, err := os.Stat(resentPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected resent post artifact: %v", err)
	}
}

func TestRunRead_DeadLettersResendOldestRejectsPermanentFailure(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "review-session", "worker")
	contextID := "ctx-read-dead-oldest"
	sessionDir := filepath.Join(tmpDir, contextID, "review-session")
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	postDir := filepath.Join(sessionDir, "post")
	if err := os.MkdirAll(deadLetterDir, 0o700); err != nil {
		t.Fatalf("MkdirAll deadLetterDir: %v", err)
	}
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		t.Fatalf("MkdirAll postDir: %v", err)
	}

	filename := "20260328-123400-from-orchestrator-to-worker-dl-envelope-mismatch.md"
	deadLetterPath := filepath.Join(deadLetterDir, filename)
	if err := os.WriteFile(deadLetterPath, []byte("content"), 0o600); err != nil {
		t.Fatalf("WriteFile dead-letter: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunRead([]string{"--dead-letters", "--resend-oldest", "--context-id", contextID})
	})
	if err == nil {
		t.Fatal("expected permanent resend rejection, got nil")
	}
	if !strings.Contains(err.Error(), "not resendable") || !strings.Contains(err.Error(), "envelope-mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty on rejection: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr should be empty on rejection: %q", stderr)
	}

	if _, err := os.Stat(deadLetterPath); err != nil {
		t.Fatalf("dead-letter file should remain after rejection: %v", err)
	}
	resentPath := filepath.Join(postDir, "20260328-123400-from-orchestrator-to-worker.md")
	if _, err := os.Stat(resentPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected resent post artifact: %v", err)
	}
}
