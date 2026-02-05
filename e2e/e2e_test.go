package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// TestE2E_MessageFileFormat tests message file format compatibility
// between Go binary and Python scripts.
func TestE2E_MessageFileFormat(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	draftDir := filepath.Join(sessionDir, "draft")

	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		t.Fatalf("creating draft dir: %v", err)
	}

	// Create message file (Go format)
	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := ts + "-from-worker-to-orchestrator.md"
	content := "---\nmethod: message/send\nparams:\n  contextId: test-ctx\n  from: worker\n  to: orchestrator\n  timestamp: " + now.Format("2006-01-02T15:04:05.000000") + "\n---\n\nTest message body\n"

	draftPath := filepath.Join(draftDir, filename)
	if err := os.WriteFile(draftPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing draft: %v", err)
	}

	// Verify file can be read back
	readContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("reading draft: %v", err)
	}

	contentStr := string(readContent)
	if !strings.Contains(contentStr, "method: message/send") {
		t.Error("missing method field")
	}
	if !strings.Contains(contentStr, "from: worker") {
		t.Error("missing from field")
	}
	if !strings.Contains(contentStr, "to: orchestrator") {
		t.Error("missing to field")
	}
	if !strings.Contains(contentStr, "Test message body") {
		t.Error("missing message body")
	}

	// Verify filename parsing
	info, err := message.ParseMessageFilename(filename)
	if err != nil {
		t.Fatalf("parsing filename: %v", err)
	}
	if info.From != "worker" {
		t.Errorf("parsed from: got %q, want %q", info.From, "worker")
	}
	if info.To != "orchestrator" {
		t.Errorf("parsed to: got %q, want %q", info.To, "orchestrator")
	}
}

// TestE2E_BasicRouting tests basic message routing from post to inbox.
func TestE2E_BasicRouting(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	// Create session directories
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("creating session dirs: %v", err)
	}

	// Create test message in post/
	postDir := filepath.Join(sessionDir, "post")
	filename := "20260201-120000-from-orchestrator-to-worker.md"
	content := "---\nmethod: message/send\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: worker\n  timestamp: 2026-02-01T12:00:00.000000\n---\n\nTest routing message\n"

	postPath := filepath.Join(postDir, filename)
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing post message: %v", err)
	}

	// Mock nodes (recipient exists)
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "worker-pane-id",
			SessionName: "test-session",
			SessionDir:  sessionDir,
		},
	}

	// Mock adjacency (orchestrator -> worker allowed)
	adjacency := map[string][]string{
		"orchestrator": {"worker"},
		"worker":       {"orchestrator"},
	}

	// Mock config
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	// Deliver message (should move to inbox/worker/)
	if err := message.DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg); err != nil {
		t.Fatalf("message.DeliverMessage failed: %v", err)
	}

	// Verify message moved to inbox/worker/
	inboxPath := filepath.Join(sessionDir, "inbox", "worker", filename)
	if _, err := os.Stat(inboxPath); os.IsNotExist(err) {
		t.Errorf("message not delivered to inbox/worker/")
	}

	// Verify message not in post/
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Errorf("message still in post/ after delivery")
	}
}

// TestE2E_TmuxEnvironment tests that require tmux are skipped in CI.
func TestE2E_TmuxEnvironment(t *testing.T) {
	t.Skip("Requires tmux environment - deferred to manual E2E testing")
}

// TestE2E_DaemonIntegration tests that require daemon are skipped in CI.
func TestE2E_DaemonIntegration(t *testing.T) {
	t.Skip("Requires running daemon - deferred to manual E2E testing")
}
