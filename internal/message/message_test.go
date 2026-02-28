package message

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
)

func TestParseMessageFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantTS   string
		wantFrom string
		wantTo   string
	}{
		{
			name:     "normal",
			filename: "20260201-022121-from-orchestrator-to-worker.md",
			wantTS:   "20260201-022121",
			wantFrom: "orchestrator",
			wantTo:   "worker",
		},
		{
			name:     "short timestamp",
			filename: "12345-from-a-to-b.md",
			wantTS:   "12345",
			wantFrom: "a",
			wantTo:   "b",
		},
		{
			name:     "hyphenated names",
			filename: "20260201-022121-from-node-alpha-to-node-beta.md",
			wantTS:   "20260201-022121",
			wantFrom: "node-alpha",
			wantTo:   "node-beta",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseMessageFilename(tt.filename)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Timestamp != tt.wantTS {
				t.Errorf("Timestamp: got %q, want %q", info.Timestamp, tt.wantTS)
			}
			if info.From != tt.wantFrom {
				t.Errorf("From: got %q, want %q", info.From, tt.wantFrom)
			}
			if info.To != tt.wantTo {
				t.Errorf("To: got %q, want %q", info.To, tt.wantTo)
			}
		})
	}
}

func TestParseMessageFilename_Invalid(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{"no extension", "20260201-from-a-to-b"},
		{"wrong extension", "20260201-from-a-to-b.txt"},
		{"missing from marker", "20260201-to-b.md"},
		{"missing to marker", "20260201-from-a.md"},
		{"empty from", "20260201-from--to-b.md"},
		{"empty to", "20260201-from-a-to-.md"},
		{"empty timestamp", "-from-a-to-b.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMessageFilename(tt.filename)
			if err == nil {
				t.Errorf("expected error for %q, got nil", tt.filename)
			}
		})
	}
}

func TestDeliverMessage(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Create inbox for known recipient
	recipientInbox := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(recipientInbox, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Place a message in post/
	filename := "20260201-030000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("test message"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Issue #33: nodes map now uses session-prefixed keys
	nodes := map[string]discovery.NodeInfo{
		"test:worker":       {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
		"test:orchestrator": {PaneID: "%2", SessionName: "test", SessionDir: sessionDir},
	}
	adjacency := map[string][]string{
		"orchestrator": {"worker"},
		"worker":       {"orchestrator"},
	}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}
	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker()); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify file moved to inbox
	inboxPath := filepath.Join(recipientInbox, filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Errorf("message not delivered to inbox: %v", err)
	}
	// Verify removed from post/
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message still in post/ after delivery")
	}
}

func TestDeliverMessage_InvalidRecipient(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Place a message for unknown recipient
	filename := "20260201-030000-from-orchestrator-to-unknown-node.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("test message"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Issue #33: nodes map now uses session-prefixed keys
	nodes := map[string]discovery.NodeInfo{
		"test:worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
	}
	adjacency := map[string][]string{
		"orchestrator": {"worker"},
	}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}
	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker()); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify moved to dead-letter/
	deadPath := filepath.Join(sessionDir, "dead-letter", filename)
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter: %v", err)
	}
}

func TestRouting_Allowed(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Create inbox for worker
	recipientInbox := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(recipientInbox, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Place a message in post/
	filename := "20260201-040000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("test message"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Issue #33: nodes map now uses session-prefixed keys
	nodes := map[string]discovery.NodeInfo{
		"test:worker":       {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
		"test:orchestrator": {PaneID: "%2", SessionName: "test", SessionDir: sessionDir},
	}
	// Define edge: orchestrator <-> worker
	adjacency := map[string][]string{
		"orchestrator": {"worker"},
		"worker":       {"orchestrator"},
	}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker()); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify delivered to inbox
	inboxPath := filepath.Join(recipientInbox, filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Errorf("message not delivered to inbox: %v", err)
	}
	// Verify removed from post/
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message still in post/ after delivery")
	}
	// Verify NOT in dead-letter/
	deadPath := filepath.Join(sessionDir, "dead-letter", filename)
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Error("message should not be in dead-letter/ (routing was allowed)")
	}
}

func TestRouting_Denied(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Place a message in post/
	filename := "20260201-040000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("test message"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Issue #33: nodes map now uses session-prefixed keys
	nodes := map[string]discovery.NodeInfo{
		"test:worker":       {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
		"test:orchestrator": {PaneID: "%2", SessionName: "test", SessionDir: sessionDir},
	}
	// No edge defined between orchestrator and worker
	adjacency := map[string][]string{}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker()); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify moved to dead-letter/
	deadPath := filepath.Join(sessionDir, "dead-letter", filename)
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter: %v", err)
	}
	// Verify removed from post/
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message still in post/ after delivery")
	}
}

func TestRouting_PostmanAlwaysAllowed(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Create inbox for worker
	recipientInbox := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(recipientInbox, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Place a message from "postman"
	filename := "20260201-040000-from-postman-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("test message"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Issue #33: nodes map now uses session-prefixed keys
	nodes := map[string]discovery.NodeInfo{
		"test:worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
	}
	// No edge defined for postman
	adjacency := map[string][]string{}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker()); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify delivered to inbox (postman is always allowed)
	inboxPath := filepath.Join(recipientInbox, filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Errorf("message not delivered to inbox: %v", err)
	}
	// Verify NOT in dead-letter/
	deadPath := filepath.Join(sessionDir, "dead-letter", filename)
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Error("message should not be in dead-letter/ (postman is always allowed)")
	}
}

func TestPONG_Handling(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Place a PONG message (to postman)
	filename := "20260201-050000-from-worker-to-postman.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("PONG"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Issue #33: nodes map now uses session-prefixed keys
	nodes := map[string]discovery.NodeInfo{
		"test:worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
	}
	adjacency := map[string][]string{}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker()); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify moved to read/ (not inbox or dead-letter)
	readPath := filepath.Join(sessionDir, "read", filename)
	if _, err := os.Stat(readPath); err != nil {
		t.Errorf("PONG not in read/: %v", err)
	}
	// Verify removed from post/
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message still in post/ after delivery")
	}
	// Verify NOT in inbox/
	inboxPath := filepath.Join(sessionDir, "inbox", "postman", filename)
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Error("PONG should not be in inbox/")
	}
	// Verify NOT in dead-letter/
	deadPath := filepath.Join(sessionDir, "dead-letter", filename)
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Error("PONG should not be in dead-letter/")
	}
}

func TestScanInboxMessages(t *testing.T) {
	t.Run("valid messages returned", func(t *testing.T) {
		dir := t.TempDir()
		filename := "20260201-030000-from-orchestrator-to-worker.md"
		if err := os.WriteFile(filepath.Join(dir, filename), []byte("content"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		msgs := ScanInboxMessages(dir)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if msgs[0].From != "orchestrator" || msgs[0].To != "worker" {
			t.Errorf("unexpected message fields: %+v", msgs[0])
		}
	})

	t.Run("non-md file skipped", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "20260201-030000-from-a-to-b.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		msgs := ScanInboxMessages(dir)
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages for non-.md file, got %d", len(msgs))
		}
	})

	t.Run("invalid filename skipped", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "not-a-valid-message.md"), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		msgs := ScanInboxMessages(dir)
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages for invalid filename, got %d", len(msgs))
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		msgs := ScanInboxMessages(dir)
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages for empty dir, got %d", len(msgs))
		}
	})

	t.Run("missing directory", func(t *testing.T) {
		msgs := ScanInboxMessages("/nonexistent/path/that/does/not/exist")
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages for missing dir, got %d", len(msgs))
		}
	})
}

func TestDeliverMessage_ParseError(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	// Filename with no "-from-" marker triggers parse error
	filename := "badname.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{}
	adjacency := map[string][]string{}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker()); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(sessionDir, "dead-letter", filename)
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter: %v", err)
	}
}

func TestDeliverMessage_RecipientSessionDisabled(t *testing.T) {
	senderDir := t.TempDir()
	if err := config.CreateSessionDirs(senderDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}
	recipientDir := t.TempDir()

	filename := "20260201-030000-from-alice-to-bob.md"
	postPath := filepath.Join(senderDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"sess-a:alice": {PaneID: "%1", SessionName: "sess-a", SessionDir: senderDir},
		"sess-b:bob":   {PaneID: "%2", SessionName: "sess-b", SessionDir: recipientDir},
	}
	adjacency := map[string][]string{
		"alice": {"bob"},
	}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	isSessionEnabled := func(s string) bool {
		return s != "sess-b"
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, isSessionEnabled, nil, idle.NewIdleTracker()); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(senderDir, "dead-letter", filename)
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter (recipient session disabled): %v", err)
	}
}

func TestDeliverMessage_FileAlreadyGone(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	// Valid filename format but file is never created
	postPath := filepath.Join(sessionDir, "post", "20260201-030000-from-alice-to-bob.md")

	nodes := map[string]discovery.NodeInfo{}
	adjacency := map[string][]string{}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker())
	if err != nil {
		t.Fatalf("expected nil for already-gone file, got: %v", err)
	}
}

// TestPostmanMessage_NoHoldingState verifies that postman → node messages
// do not cause false "holding" state (Issue #87).
func TestPostmanMessage_NoHoldingState(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	cfg := &config.Config{
		Edges:                []string{"postman -- worker"},
		Nodes:                map[string]config.NodeConfig{"worker": {}},
		NotificationTemplate: "test notification",
		TmuxTimeout:          1.0,
	}

	adjacency := map[string][]string{
		"postman": {"worker"},
		"worker":  {"postman"},
	}

	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	// Create postman → worker message in post/
	filename := "20260209-120000-from-postman-to-worker.md"
	content := `---
method: message/send
params:
  contextId: test-ctx
  taskId: 12345
  from: postman
  to: worker
  timestamp: 2026-02-09T12:00:00+09:00
---

## Content

PING from postman
`
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Setup nodes
	nodes := map[string]discovery.NodeInfo{
		"test-session:worker": {
			PaneID:      "%100",
			SessionName: "test-session",
			SessionDir:  sessionDir,
		},
	}

	idleTracker := idle.NewIdleTracker()

	// Deliver message
	err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(s string) bool { return true }, nil, idleTracker)
	if err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify: UpdateReceiveActivity was NOT called for worker
	// (Because info.From == "postman", UpdateReceiveActivity is skipped)
	nodeKey := "test-session:worker"
	activity := idleTracker.GetNodeStates()[nodeKey]

	// activity.LastReceived should be zero (not updated)
	if !activity.LastReceived.IsZero() {
		t.Errorf("LastReceived should be zero for postman → worker message, got %v", activity.LastReceived)
	}

	// Verify NOT holding (IsHoldingBall should return false)
	if idleTracker.IsHoldingBall(nodeKey) {
		t.Error("worker should NOT be holding ball after postman message")
	}
}
