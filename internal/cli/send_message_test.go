package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
)

func TestRunSendMessage_BasicFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := RunSendMessage([]string{"--to", "worker", "--body", "hello"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("unexpected flag-parse error: %v", err)
	}
}

func TestRunSendMessage_FromFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := RunSendMessage([]string{"--to", "worker", "--body", "hello", "--from", "orchestrator"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("--from not defined in RunSendMessage: %v", err)
	}
	if err == nil {
		t.Fatalf("expected error (--bindings required with --from), got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "bindings") {
		t.Errorf("expected error mentioning 'bindings', got: %v", err)
	}
}

func TestRunSendMessage_InvalidFromNodeName(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	bindingsFile := filepath.Join(tmpDir, "bindings.json")
	if err := os.WriteFile(bindingsFile, []byte(`[]`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := RunSendMessage([]string{
		"--to", "worker", "--body", "hello",
		"--from", "../escape", "--bindings", bindingsFile,
	})
	if err == nil {
		t.Fatal("expected error for invalid --from node name, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") && !strings.Contains(err.Error(), "invalid value") {
		t.Errorf("expected 'invalid node name' or 'invalid value', got: %v", err)
	}
}

func TestRunSendMessage_InvalidToNodeName(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-invalid-to",
		"--to", "worker_alt",
		"--body", "hello",
	})
	if err == nil {
		t.Fatal("expected invalid --to node name error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-invalid-to", "test-session"))
}

func TestRunSendMessage_InvalidAutoDetectedPaneTitle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger_alt")

	err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-invalid-pane",
		"--to", "worker",
		"--body", "hello",
	})
	if err == nil {
		t.Fatal("expected invalid auto-detected pane title error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-invalid-pane", "test-session"))
}

func TestResolveInboxPath_InvalidAutoDetectedPaneTitle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker_alt")

	_, err := cliutil.ResolveInboxPath([]string{
		"--config", configPath,
		"--context-id", "ctx-resolve-invalid-pane",
	})
	if err == nil {
		t.Fatal("expected invalid auto-detected pane title error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}
}

func TestRunSendMessage_IdempotencyKeyFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := RunSendMessage([]string{"--to", "worker", "--body", "hello", "--idempotency-key", "key-abc-123"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("--idempotency-key not defined in RunSendMessage: %v", err)
	}
}
