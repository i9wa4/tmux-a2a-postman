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
