package message

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
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

	nodes := map[string]discovery.NodeInfo{"worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir}}
	adjacency := map[string][]string{
		"orchestrator": {"worker"},
		"worker":       {"orchestrator"},
	}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}
	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg); err != nil {
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

	nodes := map[string]discovery.NodeInfo{"worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir}}
	adjacency := map[string][]string{
		"orchestrator": {"worker"},
	}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}
	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg); err != nil {
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

	nodes := map[string]discovery.NodeInfo{"worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir}}
	// Define edge: orchestrator <-> worker
	adjacency := map[string][]string{
		"orchestrator": {"worker"},
		"worker":       {"orchestrator"},
	}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg); err != nil {
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

	nodes := map[string]discovery.NodeInfo{"worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir}}
	// No edge defined between orchestrator and worker
	adjacency := map[string][]string{}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg); err != nil {
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

	nodes := map[string]discovery.NodeInfo{"worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir}}
	// No edge defined for postman
	adjacency := map[string][]string{}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg); err != nil {
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

	nodes := map[string]discovery.NodeInfo{"worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir}}
	adjacency := map[string][]string{}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg); err != nil {
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
