package message

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
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
		{
			// 64-char from field: "a" + 63 "a" chars = 64 total (#299)
			name:     "64-char node name (boundary accept)",
			filename: "12345-from-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-to-b.md",
			wantTS:   "12345",
			wantFrom: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantTo:   "b",
		},
		{
			name:     "session-prefixed recipient",
			filename: "20260201-022121-from-orchestrator-to-review-session:worker.md",
			wantTS:   "20260201-022121",
			wantFrom: "orchestrator",
			wantTo:   "review-session:worker",
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
		// 65-char from field: "a" + 64 "a" chars = 65 total, exceeds 64-char cap (#299)
		{"65-char node name (boundary reject)", "12345-from-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-to-b.md"},
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

func TestGenerateFilename_InvalidNodeSegments(t *testing.T) {
	tests := []struct {
		name      string
		sender    string
		recipient string
	}{
		{
			name:      "invalid sender",
			sender:    "messenger_alt",
			recipient: "worker",
		},
		{
			name:      "invalid recipient",
			sender:    "messenger",
			recipient: "worker_alt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GenerateFilename("20260328-121000", tt.sender, tt.recipient, "test-session")
			if err == nil {
				t.Fatal("expected invalid node name error, got nil")
			}
			if !strings.Contains(err.Error(), "invalid node name") {
				t.Fatalf("expected invalid node name error, got: %v", err)
			}
		})
	}
}

func TestGenerateFilename_RoundTripSessionPrefixedSender(t *testing.T) {
	tests := []struct {
		name      string
		sender    string
		recipient string
	}{
		{
			name:      "session-prefixed sender with hyphenated session name",
			sender:    "qa-to-prod:orchestrator",
			recipient: "worker",
		},
		{
			name:      "session-prefixed sender with tilde-prefixed session name",
			sender:    "~ops:orchestrator",
			recipient: "worker",
		},
		{
			name:      "session-prefixed sender and recipient with reserved markers",
			sender:    "qa-to-prod:orchestrator",
			recipient: "review-to-prod:worker",
		},
		{
			name:      "session-prefixed sender and recipient with tilde-prefixed session names",
			sender:    "~ops:orchestrator",
			recipient: "~review:worker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename, err := GenerateFilename("20260328-121000", tt.sender, tt.recipient, "local-session")
			if err != nil {
				t.Fatalf("GenerateFilename failed: %v", err)
			}

			info, err := ParseMessageFilename(filename)
			if err != nil {
				t.Fatalf("ParseMessageFilename failed: %v", err)
			}

			if info.Timestamp != "20260328-121000" {
				t.Fatalf("Timestamp: got %q, want %q", info.Timestamp, "20260328-121000")
			}
			if info.From != tt.sender {
				t.Fatalf("From: got %q, want %q (filename=%q)", info.From, tt.sender, filename)
			}
			if info.To != tt.recipient {
				t.Fatalf("To: got %q, want %q (filename=%q)", info.To, tt.recipient, filename)
			}
		})
	}
}

func TestDeliverMessage(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "test") // basename must match session name in nodes map
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Create inbox for known recipient
	recipientInbox := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(recipientInbox, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Place a message in post/ (with valid frontmatter for envelope validation, Issue #161)
	filename := "20260201-030000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: worker\n  timestamp: 2026-02-01T03:00:00Z\n---\n\ntest message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
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
	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
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

	manager := journal.NewManager("test-ctx", 31337)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)

	// Place a message for unknown recipient (with valid frontmatter for envelope validation, Issue #161)
	filename := "20260201-030000-from-orchestrator-to-unknown-node.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: unknown-node\n  timestamp: 2026-02-01T03:00:00Z\n  input_request_id: ireq_deadletter_123\n---\n\ntest message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
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
	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify moved to dead-letter/ with unknown-recipient suffix
	deadPath := filepath.Join(sessionDir, "dead-letter", "20260201-030000-from-orchestrator-to-unknown-node-dl-unknown-recipient.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter: %v", err)
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("journal.Replay failed: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("journal.Replay returned %d events, want 3", len(events))
	}
	if events[2].Type != projection.MailboxProjectionDeadLetteredEventType {
		t.Fatalf("events[2].Type = %q, want mailbox_projection_dead_lettered", events[2].Type)
	}
	var payload journal.MailboxEventPayload
	if err := json.Unmarshal(events[2].Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload) failed: %v", err)
	}
	if payload.MessageID != filename || payload.From != "orchestrator" || payload.To != "unknown-node" {
		t.Fatalf("payload identifiers = %#v, want original message/from/to", payload)
	}
	if payload.InputRequestID != "ireq_deadletter_123" {
		t.Fatalf("payload.InputRequestID = %q, want ireq_deadletter_123", payload.InputRequestID)
	}
	if payload.FailureReason != "unknown-recipient" {
		t.Fatalf("payload.FailureReason = %q, want unknown-recipient", payload.FailureReason)
	}
	if payload.Path != filepath.Join("dead-letter", filepath.Base(deadPath)) || payload.SourcePath != filepath.Join("post", filename) {
		t.Fatalf("payload paths = %q/%q, want dead-letter path and original post path", payload.Path, payload.SourcePath)
	}
}

func TestDeadLetterFailureReason(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "extracts reason from dead-letter basename",
			path: "dead-letter/20260201-030000-from-orchestrator-to-worker-dl-routing-denied.md",
			want: "routing-denied",
		},
		{
			name: "uses final marker when original basename contains marker",
			path: "dead-letter/message-dl-original-dl-unknown-recipient.md",
			want: "unknown-recipient",
		},
		{
			name: "missing marker",
			path: "dead-letter/message.md",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deadLetterFailureReason(tt.path); got != tt.want {
				t.Fatalf("deadLetterFailureReason(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestSendDeadLetterNotification_UsesPublicRecoveryCommand(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	deadLetterBasename := "20260201-030000-from-orchestrator-to-worker-dl-routing-denied.md"
	sendDeadLetterNotification(
		sessionDir,
		"test-ctx",
		"review:orchestrator",
		"routing denied",
		"20260201-030000-from-orchestrator-to-worker.md",
		deadLetterBasename,
	)

	inboxDir := filepath.Join(sessionDir, "inbox", "orchestrator")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", inboxDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("dead-letter notification count = %d, want 1", len(entries))
	}

	data, err := os.ReadFile(filepath.Join(inboxDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(notification): %v", err)
	}
	content := string(data)
	for _, stale := range []string{
		"tmux-a2a-postman read",
		"--dead-letters",
		"--resend-oldest",
		`tmux-a2a-postman send --to <node> --body "<message>"`,
		"tmux-a2a-postman send --to <node> --body-stdin < corrected-message.md",
		"tmux-a2a-postman send --to <node> --body-file corrected-message.md",
		"tmux-a2a-postman send --to <node> <<'POSTMAN_BODY'",
		"tmux-a2a-postman send --to <node> --message-file corrected-message.md",
	} {
		if strings.Contains(content, stale) {
			t.Fatalf("dead-letter notification still contains stale recovery surface %q: %s", stale, content)
		}
	}
	for _, want := range []string{
		"tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'",
		"<corrected message>",
		"POSTMAN_BODY",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("dead-letter notification missing safe send recovery command %q: %s", want, content)
		}
	}
	if !strings.Contains(content, filepath.Join(sessionDir, "dead-letter", deadLetterBasename)) {
		t.Fatalf("dead-letter notification missing dead-letter path: %s", content)
	}
}

func TestDeliverMessage_ExplicitUnknownRecipientSession(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "test")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	filename := "20260201-030000-from-orchestrator-to-missing-session:worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: missing-session:worker\n  timestamp: 2026-02-01T03:00:00Z\n---\n\ntest message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"test:orchestrator": {PaneID: "%2", SessionName: "test", SessionDir: sessionDir},
		"test:worker":       {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
	}
	adjacency := map[string][]string{
		"orchestrator": {"missing-session:worker"},
	}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(sessionDir, "dead-letter", "20260201-030000-from-orchestrator-to-missing-session:worker-dl-unknown-session.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in unknown-session dead-letter: %v", err)
	}
}

func TestDeliverMessage_CrossSessionExplicitRecipient(t *testing.T) {
	sourceSessionDir := filepath.Join(t.TempDir(), "sender-session")
	if err := config.CreateSessionDirs(sourceSessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs source failed: %v", err)
	}
	recipientSessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(recipientSessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs recipient failed: %v", err)
	}

	filename := "20260201-030000-from-orchestrator-to-review-session:worker.md"
	postPath := filepath.Join(sourceSessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: review-session:worker\n  timestamp: 2026-02-01T03:00:00Z\n---\n\ntest message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"sender-session:orchestrator": {PaneID: "%2", SessionName: "sender-session", SessionDir: sourceSessionDir},
		"review-session:worker":       {PaneID: "%1", SessionName: "review-session", SessionDir: recipientSessionDir},
	}
	adjacency := map[string][]string{
		"orchestrator": {"review-session:worker"},
	}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	inboxPath := filepath.Join(recipientSessionDir, "inbox", "worker", filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Fatalf("cross-session message not delivered to simple-name inbox: %v", err)
	}

	if _, err := os.Stat(filepath.Join(recipientSessionDir, "inbox", "review-session:worker", filename)); !os.IsNotExist(err) {
		t.Fatalf("unexpected session-prefixed inbox artifact: %v", err)
	}
}

func TestRouting_Allowed(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "test") // basename must match session name in nodes map
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Create inbox for worker
	recipientInbox := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(recipientInbox, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Place a message in post/ (with valid frontmatter for envelope validation, Issue #161)
	filename := "20260201-040000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: worker\n  timestamp: 2026-02-01T04:00:00Z\n---\n\ntest message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
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
	sessionDir := filepath.Join(t.TempDir(), "test") // basename must match session name in nodes map
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Place a message in post/ (with valid frontmatter for envelope validation, Issue #161)
	filename := "20260201-040000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: worker\n  timestamp: 2026-02-01T04:00:00Z\n---\n\ntest message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify moved to dead-letter/ with routing-denied suffix
	deadPath := filepath.Join(sessionDir, "dead-letter", "20260201-040000-from-orchestrator-to-worker-dl-routing-denied.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter: %v", err)
	}
	// Verify removed from post/
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message still in post/ after delivery")
	}
}

func TestDeliverMessage_PostmanGenericPathDeadLettered(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "test") // basename must match session name in nodes map
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), "test"); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	inboxPath := filepath.Join(recipientInbox, filename)
	if _, err := os.Stat(inboxPath); err == nil {
		t.Fatalf("generic from=postman file should not reach inbox: %s", inboxPath)
	}

	deadPath := filepath.Join(sessionDir, "dead-letter", "20260201-040000-from-postman-to-worker-dl-forged-sender.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Fatalf("generic from=postman file not dead-lettered as forged sender: %v", err)
	}
}

func TestPONG_Handling(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	// Place a PONG message (to postman) — with valid frontmatter for envelope validation (Issue #161)
	filename := "20260201-050000-from-worker-to-postman.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: worker\n  to: postman\n  timestamp: 2026-02-01T05:00:00Z\n---\n\nPONG\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify removed from post/
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message still in post/ after delivery")
	}
	// Verify dead-lettered (postman is unknown recipient after explicit PONG removal)
	deadPath := filepath.Join(sessionDir, "dead-letter", "20260201-050000-from-worker-to-postman-dl-unknown-recipient.md")
	if _, err := os.Stat(deadPath); os.IsNotExist(err) {
		t.Error("PONG should be in dead-letter/")
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
		if msgs[0].Filename != filename {
			t.Errorf("Filename: got %q, want %q", msgs[0].Filename, filename)
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(sessionDir, "dead-letter", "badname-dl-parse-error.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter: %v", err)
	}
}

func TestDeliverMessage_ParseErrorRejectsSymlinkedDeadLetterDir(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	escapedDir := filepath.Join(tmpDir, "escaped-dead-letter")
	if err := os.MkdirAll(escapedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll escapedDir failed: %v", err)
	}

	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	if err := os.Remove(deadLetterDir); err != nil {
		t.Fatalf("Remove dead-letter dir failed: %v", err)
	}
	if err := os.Symlink(escapedDir, deadLetterDir); err != nil {
		t.Fatalf("Symlink dead-letter dir failed: %v", err)
	}

	filename := "badname.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}
	err := DeliverMessage(postPath, "test-ctx", map[string]discovery.NodeInfo{}, map[string][]string{}, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), "")
	if err == nil {
		t.Fatal("expected symlink rejection error, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection error, got: %v", err)
	}

	if _, err := os.Stat(postPath); err != nil {
		t.Fatalf("post file should remain in place after rejection: %v", err)
	}

	escapedPath := filepath.Join(escapedDir, "badname-dl-parse-error.md")
	if _, err := os.Stat(escapedPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected escaped dead-letter artifact: %v", err)
	}
}

func TestMoveToDeadLetterRejectsSymlinkDestination(t *testing.T) {
	tmpDir := t.TempDir()
	deadLetterDir := filepath.Join(tmpDir, "dead-letter")
	if err := os.MkdirAll(deadLetterDir, 0o755); err != nil {
		t.Fatalf("MkdirAll deadLetterDir failed: %v", err)
	}

	srcPath := filepath.Join(tmpDir, "message.md")
	if err := os.WriteFile(srcPath, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile srcPath failed: %v", err)
	}

	escapedTarget := filepath.Join(tmpDir, "escaped.md")
	dstPath := filepath.Join(deadLetterDir, "message-dl-parse-error.md")
	if err := os.Symlink(escapedTarget, dstPath); err != nil {
		t.Fatalf("Symlink dstPath failed: %v", err)
	}

	err := moveToDeadLetter(srcPath, dstPath)
	if err == nil {
		t.Fatal("expected symlink rejection error, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection error, got: %v", err)
	}

	if _, err := os.Stat(srcPath); err != nil {
		t.Fatalf("source file should remain after rejection: %v", err)
	}

	info, err := os.Lstat(dstPath)
	if err != nil {
		t.Fatalf("Lstat dstPath failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination symlink was replaced unexpectedly: mode=%v", info.Mode())
	}

	if _, err := os.Stat(escapedTarget); !os.IsNotExist(err) {
		t.Fatalf("unexpected escaped target artifact: %v", err)
	}
}

func TestDeliverSystemMessageDirectQueueFullSkipsDeadLetterDir(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	recipientInbox := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(recipientInbox, 0o700); err != nil {
		t.Fatalf("MkdirAll recipientInbox failed: %v", err)
	}
	for i := range inboxQueueCap {
		name := filepath.Join(recipientInbox, fmt.Sprintf("20260201-0300%02d-from-daemon-to-worker.md", i))
		if err := os.WriteFile(name, []byte("queued"), 0o600); err != nil {
			t.Fatalf("WriteFile inbox fixture %d failed: %v", i, err)
		}
	}

	escapedDir := filepath.Join(tmpDir, "escaped-dead-letter")
	if err := os.MkdirAll(escapedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll escapedDir failed: %v", err)
	}

	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	if err := os.Remove(deadLetterDir); err != nil {
		t.Fatalf("Remove dead-letter dir failed: %v", err)
	}
	if err := os.Symlink(escapedDir, deadLetterDir); err != nil {
		t.Fatalf("Symlink dead-letter dir failed: %v", err)
	}

	nodeInfo := discovery.NodeInfo{PaneID: "%1", SessionName: "test", SessionDir: sessionDir}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}
	err := DeliverSystemMessageDirect(
		"20260201-040000-from-daemon-to-worker.md",
		nodeInfo,
		"worker",
		"daemon",
		"test-ctx",
		"system content",
		cfg,
		map[string][]string{},
		map[string]discovery.NodeInfo{},
		map[string]bool{},
	)
	if err != nil {
		t.Fatalf("expected queue-full direct delivery to stay undelivered without touching dead-letter, got: %v", err)
	}

	escapedPath := filepath.Join(escapedDir, "20260201-040000-from-daemon-to-worker-dl-queue-full.md")
	if _, err := os.Stat(escapedPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected escaped dead-letter artifact: %v", err)
	}
}

func TestDeliverMessage_RecipientSessionDisabled(t *testing.T) {
	// After F2, cross-session bare-name routing is removed. This test verifies that a recipient
	// whose NodeInfo.SessionName is disabled gets dead-lettered. alice sends to bob; both keys
	// are in sess-a (so same-session lookup works), but bob's NodeInfo records SessionName "sess-b"
	// which is disabled. The sender's session "sess-a" is enabled; the recipient check fires.
	senderDir := filepath.Join(t.TempDir(), "sess-a") // basename must match session name in nodes map
	if err := config.CreateSessionDirs(senderDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	filename := "20260201-030000-from-alice-to-bob.md"
	postPath := filepath.Join(senderDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: alice\n  to: bob\n  timestamp: 2026-02-01T03:00:00Z\n---\n\ncontent\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"sess-a:alice": {PaneID: "%1", SessionName: "sess-a", SessionDir: senderDir},
		// bob's key is in sess-a so same-session lookup finds it, but NodeInfo.SessionName is "sess-b"
		"sess-a:bob": {PaneID: "%2", SessionName: "sess-b", SessionDir: t.TempDir()},
	}
	adjacency := map[string][]string{
		"alice": {"bob"},
	}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	// sess-a (sender) is enabled; sess-b (recipient's recorded session) is disabled.
	isSessionEnabled := func(s string) bool { return s == "sess-a" }

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, isSessionEnabled, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(senderDir, "dead-letter", "20260201-030000-from-alice-to-bob-dl-session-disabled.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter (recipient session disabled): %v", err)
	}
}

func TestDeliverMessage_SameSessionDaemonAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	daemonDir := filepath.Join(tmpDir, "daemon-session")
	if err := config.CreateSessionDirs(daemonDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	filename := "20260301-120000-from-daemon-to-orchestrator.md"
	postPath := filepath.Join(daemonDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: daemon\n  to: orchestrator\n  timestamp: 2026-03-01T12:00:00Z\n---\n\nALERT\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"daemon-session:orchestrator": {PaneID: "%10", SessionName: "daemon-session", SessionDir: daemonDir},
	}
	adjacency := map[string][]string{}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return false }, nil, idle.NewIdleTracker(), "daemon-session"); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	inboxPath := filepath.Join(daemonDir, "inbox", "orchestrator", filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Errorf("daemon message not delivered to inbox: %v", err)
	}

	deadLetterGlob := filepath.Join(daemonDir, "dead-letter", "*forged*")
	matches, _ := filepath.Glob(deadLetterGlob)
	if len(matches) > 0 {
		t.Errorf("daemon message was incorrectly dead-lettered as forged sender: %v", matches)
	}
}

func TestDeliverMessage_DisabledSessionPostmanDeadLettered(t *testing.T) {
	tmpDir := t.TempDir()
	messengerDir := filepath.Join(tmpDir, "messenger")
	if err := config.CreateSessionDirs(messengerDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	filename := "20260301-120000-from-postman-to-orchestrator.md"
	postPath := filepath.Join(messengerDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: postman\n  to: orchestrator\n  timestamp: 2026-03-01T12:00:00Z\n---\n\nPING\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"messenger:orchestrator": {PaneID: "%10", SessionName: "messenger", SessionDir: messengerDir},
	}
	adjacency := map[string][]string{}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	_ = DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return false }, nil, idle.NewIdleTracker(), "local-daemon")

	inboxPath := filepath.Join(messengerDir, "inbox", "orchestrator", filename)
	if _, err := os.Stat(inboxPath); err == nil {
		t.Errorf("ping should NOT be delivered to inbox for disabled session, but found: %s", inboxPath)
	}

	deadLetterGlob := filepath.Join(messengerDir, "dead-letter", "*forged*")
	matches, _ := filepath.Glob(deadLetterGlob)
	if len(matches) == 0 {
		t.Errorf("expected ping to be dead-lettered as forged sender, but found no forged entries in dead-letter/")
	}
}

func TestDeliverMessage_ForeignEnabledSessionForgedPostman(t *testing.T) {
	tmpDir := t.TempDir()
	foreignDir := filepath.Join(tmpDir, "foreign-session")
	if err := config.CreateSessionDirs(foreignDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	filename := "20260301-120000-from-postman-to-orchestrator.md"
	postPath := filepath.Join(foreignDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("forged payload"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"foreign-session:orchestrator": {PaneID: "%10", SessionName: "foreign-session", SessionDir: foreignDir},
	}
	adjacency := map[string][]string{}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	isSessionEnabled := func(s string) bool { return s == "foreign-session" }

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, isSessionEnabled, nil, idle.NewIdleTracker(), "local-daemon"); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	inboxPath := filepath.Join(foreignDir, "inbox", "orchestrator", filename)
	if _, err := os.Stat(inboxPath); err == nil {
		t.Fatalf("forged from=postman message should not reach inbox: %s", inboxPath)
	}

	deadPath := filepath.Join(foreignDir, "dead-letter", "20260301-120000-from-postman-to-orchestrator-dl-forged-sender.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Fatalf("forged from=postman message not dead-lettered: %v", err)
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

	err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), "")
	if err != nil {
		t.Fatalf("expected nil for already-gone file, got: %v", err)
	}
}

// TestDaemonMessage_NoHoldingState verifies that daemon → node messages
// do not cause false reply-lag state (Issue #87).
func TestDaemonMessage_NoHoldingState(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	cfg := &config.Config{
		Edges:                []string{"daemon --- worker"},
		Nodes:                map[string]config.NodeConfig{"worker": {}},
		NotificationTemplate: "test notification",
		TmuxTimeout:          1.0,
	}

	adjacency := map[string][]string{
		"daemon": {"worker"},
		"worker": {"daemon"},
	}

	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	// Create daemon → worker message in post/
	filename := "20260209-120000-from-daemon-to-worker.md"
	content := `---
params:
  contextId: test-ctx
  from: daemon
  to: worker
  timestamp: 2026-02-09T12:00:00+09:00
---

## Content

PING from daemon
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
	err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idleTracker, "test-session")
	if err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify: UpdateReceiveActivity was NOT called for worker
	// (Because info.From == "daemon", UpdateReceiveActivity is skipped)
	nodeKey := "test-session:worker"
	activity := idleTracker.GetNodeStates()[nodeKey]

	// activity.LastReceived should be zero (not updated)
	if !activity.LastReceived.IsZero() {
		t.Errorf("LastReceived should be zero for daemon → worker message, got %v", activity.LastReceived)
	}

	if !activity.LastSent.IsZero() {
		t.Errorf("LastSent should be zero for daemon message, got %v", activity.LastSent)
	}
}

// TestDeliverMessage_ForeignSession verifies that F4 dead-letters messages
// addressed to a recipient in a foreign (non-daemon, non-enabled) session.
func TestDeliverMessage_ForeignSession(t *testing.T) {
	// F4: Verify that a recipient in a foreign (non-daemon, non-enabled) session is dead-lettered.
	// Setup: daemon owns "own-session". Sender alice delivers to bob, who somehow appears
	// in knownNodes under "own-session" but nodeInfo.SessionName resolves to "foreign-session"
	// (simulating stale knownNodes after a session was previously enabled then not).
	senderDir := filepath.Join(t.TempDir(), "own-session") // basename matches daemonSession
	if err := config.CreateSessionDirs(senderDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}
	recipientDir := t.TempDir()

	filename := "20260201-040000-from-alice-to-bob.md"
	postPath := filepath.Join(senderDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: alice\n  to: bob\n  timestamp: 2026-02-01T04:00:00Z\n---\n\ncontent\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// bob's NodeInfo has SessionName "foreign-session" (a stale cross-session entry in knownNodes).
	// "own-session:bob" key means same-session lookup succeeds, but nodeInfo reveals the actual session.
	nodes := map[string]discovery.NodeInfo{
		"own-session:alice": {PaneID: "%1", SessionName: "own-session", SessionDir: senderDir},
		"own-session:bob":   {PaneID: "%2", SessionName: "foreign-session", SessionDir: recipientDir},
	}
	adjacency := map[string][]string{
		"alice": {"bob"},
	}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	// foreign-session is not enabled; daemonSession = "own-session"
	isSessionEnabled := func(s string) bool { return s == "own-session" }
	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, isSessionEnabled, nil, idle.NewIdleTracker(), "own-session"); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(senderDir, "dead-letter", "20260201-040000-from-alice-to-bob-dl-foreign-session.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not dead-lettered with dlSuffixForeignSession: %v", err)
	}
}

func TestDeliverMessage_ProjectLocalEdgeViolationWarningTemplateIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	fakeHome := filepath.Join(tmpDir, "home")
	projectDir := filepath.Join(fakeHome, "project")
	localConfigDir := filepath.Join(projectDir, ".tmux-a2a-postman")
	xdgConfigHome := filepath.Join(tmpDir, "xdg")
	xdgConfigDir := filepath.Join(xdgConfigHome, "tmux-a2a-postman")
	sessionDir := filepath.Join(tmpDir, "test")

	if err := os.MkdirAll(xdgConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll xdgConfigDir failed: %v", err)
	}
	if err := os.MkdirAll(localConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll localConfigDir failed: %v", err)
	}
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	xdgConfig := `
[postman]
allow_shell_templates = true

[worker]
role = "worker"

[orchestrator]
role = "orchestrator"
`
	if err := os.WriteFile(filepath.Join(xdgConfigDir, "postman.toml"), []byte(xdgConfig), 0o644); err != nil {
		t.Fatalf("WriteFile XDG config failed: %v", err)
	}

	localConfig := `
[postman]
edge_violation_warning_template = "Routing denied $(printf project-local-edge-warning)"
`
	if err := os.WriteFile(filepath.Join(localConfigDir, "postman.toml"), []byte(localConfig), 0o644); err != nil {
		t.Fatalf("WriteFile local config failed: %v", err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	t.Chdir(projectDir)

	cfg, err := config.LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	filename := "20260201-040000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: worker\n  timestamp: 2026-02-01T04:00:00Z\n---\n\ntest message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile postPath failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"test:worker":       {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
		"test:orchestrator": {PaneID: "%2", SessionName: "test", SessionDir: sessionDir},
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, map[string][]string{}, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	warningMatches, err := filepath.Glob(filepath.Join(sessionDir, "inbox", "orchestrator", "*-from-postman-to-orchestrator.md"))
	if err != nil {
		t.Fatalf("Glob warningMatches failed: %v", err)
	}
	if len(warningMatches) == 0 {
		t.Fatal("routing-denied warning file not found in sender inbox")
	}

	warningBody, err := os.ReadFile(warningMatches[0])
	if err != nil {
		t.Fatalf("ReadFile warning failed: %v", err)
	}
	if strings.Contains(string(warningBody), "project-local-edge-warning") {
		t.Fatalf("project-local edge violation warning template was applied: %q", string(warningBody))
	}
}

func TestDeliverMessage_RoutingDeniedWarningIncludesReplyCommand(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "test")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	filename := "20260201-040000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: worker\n  timestamp: 2026-02-01T04:00:00Z\n---\n\ntest message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"test:worker":       {PaneID: "%1", SessionName: "test", SessionDir: sessionDir},
		"test:orchestrator": {PaneID: "%2", SessionName: "test", SessionDir: sessionDir},
	}
	cfg := &config.Config{
		EnterDelay:                   0.1,
		TmuxTimeout:                  1.0,
		ReplyCommand:                 "send-heredoc --to <recipient>",
		EdgeViolationWarningTemplate: "Reply: {reply_command}",
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, map[string][]string{}, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	warningDir := filepath.Join(sessionDir, "inbox", "orchestrator")
	entries, err := os.ReadDir(warningDir)
	if err != nil {
		t.Fatalf("ReadDir warningDir failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("warning entry count = %d, want 1", len(entries))
	}

	warningBody, err := os.ReadFile(filepath.Join(warningDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile warning failed: %v", err)
	}
	if !strings.Contains(string(warningBody), "send-heredoc --to <recipient>") {
		t.Fatalf("warning missing reply command: %q", string(warningBody))
	}
}

func TestDeliverMessage_AppendsShadowJournalDeliveredEvent(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	manager := journal.NewManager("test-ctx", 31337)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)

	filename := "20260414-173500-r1234-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	replyTo := "20260414-173000-r0001-from-worker-to-orchestrator.md"
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: worker\n  messageId: " + filename + "\n  replyPolicy: required\n  replyTo: " + replyTo + "\n  messageType: task\n  timestamp: 2026-04-14T17:35:00Z\n  input_request_id: ireq_delivery_123\n---\n\nshadow delivery\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile postPath failed: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"review-session:worker":       {PaneID: "%1", SessionName: "review-session", SessionDir: sessionDir},
		"review-session:orchestrator": {PaneID: "%2", SessionName: "review-session", SessionDir: sessionDir},
	}
	cfg := &config.Config{}
	adjacency := map[string][]string{"orchestrator": {"worker"}}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("journal.Replay failed: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("journal.Replay returned %d events, want 4", len(events))
	}
	if events[2].Type != projection.MailboxProjectionPostConsumedEventType {
		t.Fatalf("events[2].Type = %q, want mailbox_projection_post_consumed", events[2].Type)
	}
	if events[3].Type != projection.MailboxProjectionDeliveredEventType {
		t.Fatalf("events[3].Type = %q, want mailbox_projection_delivered", events[3].Type)
	}
	var payload map[string]string
	if err := json.Unmarshal(events[3].Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload) failed: %v", err)
	}
	if payload["path"] != filepath.Join("inbox", "worker", filename) {
		t.Fatalf("payload[path] = %q, want %q", payload["path"], filepath.Join("inbox", "worker", filename))
	}
	if payload["content"] != content {
		t.Fatalf("payload[content] = %q, want %q", payload["content"], content)
	}
	for key, want := range map[string]string{
		"context_id":       "test-ctx",
		"message_id":       filename,
		"reply_policy":     "required",
		"reply_to":         replyTo,
		"message_type":     "task",
		"timestamp":        "2026-04-14T17:35:00Z",
		"input_request_id": "ireq_delivery_123",
	} {
		if payload[key] != want {
			t.Fatalf("payload[%s] = %q, want %q", key, payload[key], want)
		}
	}
}

func TestDeliverSystemMessageDirect_AppendsShadowJournalDeliveredEvent(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	manager := journal.NewManager("test-ctx", 31337)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)

	nodeInfo := discovery.NodeInfo{
		PaneID:      "%1",
		SessionName: "review-session",
		SessionDir:  sessionDir,
	}
	cfg := &config.Config{}

	if err := DeliverSystemMessageDirect(
		"20260414-173600-r5678-from-postman-to-worker.md",
		nodeInfo,
		"worker",
		"postman",
		"test-ctx",
		"system delivery",
		cfg,
		map[string][]string{},
		map[string]discovery.NodeInfo{"review-session:worker": nodeInfo},
		map[string]bool{},
	); err != nil {
		t.Fatalf("DeliverSystemMessageDirect failed: %v", err)
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("journal.Replay failed: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("journal.Replay returned %d events, want 3", len(events))
	}
	if events[2].Type != projection.MailboxProjectionDeliveredEventType {
		t.Fatalf("events[2].Type = %q, want mailbox_projection_delivered", events[2].Type)
	}
	var payload map[string]string
	if err := json.Unmarshal(events[2].Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload) failed: %v", err)
	}
	if payload["from"] != "postman" || payload["to"] != "worker" {
		t.Fatalf("payload = %#v, want from=postman to=worker", payload)
	}
}

func TestApprovalDecisionFromContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    journal.ApprovalDecision
		ok      bool
	}{
		{
			name: "approved",
			content: "---\nparams:\n  from: critic\n  to: orchestrator\n" +
				"  thread_id: thread-review-01\n---\n\nAPPROVED: looks good\n",
			want: journal.ApprovalDecisionApproved,
			ok:   true,
		},
		{
			name: "not approved",
			content: "---\nparams:\n  from: critic\n  to: orchestrator\n" +
				"  thread_id: thread-review-01\n---\n\nNOT APPROVED: missing verification\n",
			want: journal.ApprovalDecisionRejected,
			ok:   true,
		},
		{
			name: "plain body is not a decision",
			content: "---\nparams:\n  from: critic\n  to: orchestrator\n" +
				"  thread_id: thread-review-01\n---\n\nPlease revise this.\n",
			ok: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := approvalDecisionFromContent(tt.content)
			if ok != tt.ok {
				t.Fatalf("approvalDecisionFromContent() ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("approvalDecisionFromContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeliverMessage_AppendsReplayableApprovalEventsForCrossSessionThread(t *testing.T) {
	mainSessionDir := filepath.Join(t.TempDir(), "main")
	if err := config.CreateSessionDirs(mainSessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs(main) failed: %v", err)
	}
	reviewSessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(reviewSessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs(review) failed: %v", err)
	}

	manager := journal.NewManager("test-ctx", 31337)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)

	nodes := map[string]discovery.NodeInfo{
		"main:orchestrator":     {PaneID: "%1", SessionName: "main", SessionDir: mainSessionDir},
		"review-session:critic": {PaneID: "%2", SessionName: "review-session", SessionDir: reviewSessionDir},
	}
	adjacency := map[string][]string{
		"orchestrator":          {"review-session:critic"},
		"review-session:critic": {"main:orchestrator"},
	}
	cfg := &config.Config{}
	threadID := "thread-review-01"

	requestFilename := "20260414-173500-r1234-from-orchestrator-to-review-session:critic.md"
	requestPath := filepath.Join(mainSessionDir, "post", requestFilename)
	requestContent := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: review-session:critic\n  thread_id: " + threadID + "\n  timestamp: 2026-04-14T17:35:00Z\n---\n\nPlease review the implementation.\n"
	if err := os.WriteFile(requestPath, []byte(requestContent), 0o644); err != nil {
		t.Fatalf("WriteFile(requestPath) failed: %v", err)
	}

	if err := DeliverMessage(requestPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage(request) failed: %v", err)
	}

	decisionFilename := "20260414-173501-r5678-from-review-session:critic-to-main:orchestrator.md"
	decisionPath := filepath.Join(reviewSessionDir, "post", decisionFilename)
	decisionContent := "---\nparams:\n  contextId: test-ctx\n  from: review-session:critic\n  to: main:orchestrator\n  thread_id: " + threadID + "\n  timestamp: 2026-04-14T17:35:01Z\n---\n\nAPPROVED: verification passed.\n"
	if err := os.WriteFile(decisionPath, []byte(decisionContent), 0o644); err != nil {
		t.Fatalf("WriteFile(decisionPath) failed: %v", err)
	}

	if err := DeliverMessage(decisionPath, "test-ctx", nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage(decision) failed: %v", err)
	}

	for _, sessionDir := range []string{mainSessionDir, reviewSessionDir} {
		projected, ok, err := projection.ProjectThreadApproval(sessionDir)
		if err != nil {
			t.Fatalf("ProjectThreadApproval(%s) error = %v", sessionDir, err)
		}
		if !ok {
			t.Fatalf("ProjectThreadApproval(%s) ok = false, want true", sessionDir)
		}

		thread, ok := projected.Threads[threadID]
		if !ok {
			t.Fatalf("ProjectThreadApproval(%s) missing thread %q in %#v", sessionDir, threadID, projected.Threads)
		}
		if thread.Requester != "orchestrator" {
			t.Fatalf("thread requester = %q, want orchestrator", thread.Requester)
		}
		if thread.Reviewer != "critic" {
			t.Fatalf("thread reviewer = %q, want critic", thread.Reviewer)
		}
		if thread.Status != projection.ApprovalStatusApproved {
			t.Fatalf("thread status = %q, want %q", thread.Status, projection.ApprovalStatusApproved)
		}
		if thread.RequestMessageID != requestFilename {
			t.Fatalf("thread request message = %q, want %q", thread.RequestMessageID, requestFilename)
		}
		if thread.DecisionMessageID != decisionFilename {
			t.Fatalf("thread decision message = %q, want %q", thread.DecisionMessageID, decisionFilename)
		}
	}
}

// TestDeliverNotificationWithRetry_RetryUsesRefreshedPaneID verifies that when
// the first delivery attempt fails and knownNodes has a fresh PaneID, the retry
// uses the refreshed PaneID.
func TestDeliverNotificationWithRetry_RetryUsesRefreshedPaneID(t *testing.T) {
	var callCount int
	var gotPaneIDs []string

	adapter := controlplane.TmuxHandAdapter{
		ProbeRuntime: func(string) (string, error) { return "bash", nil },
		SendToPane: func(paneID string, _ string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
			callCount++
			gotPaneIDs = append(gotPaneIDs, paneID)
			if callCount == 1 {
				return fmt.Errorf("no such pane: %s", paneID)
			}
			return nil
		},
	}

	target := controlplane.Target{
		ActorID:     "worker",
		RunID:       "test:worker",
		SessionName: "test",
		Brain:       controlplane.Brain{Runtime: "bash"},
		Hand:        controlplane.HandAttachment{Kind: controlplane.HandKindTmux, Address: "%stale"},
	}
	delivery := controlplane.PaneDelivery{BypassCooldown: true}
	knownNodes := map[string]discovery.NodeInfo{
		"test:worker": {PaneID: "%fresh", SessionName: "test"},
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	deliverNotificationWithRetry(adapter, target, delivery, "test:worker", knownNodes, "test-msg.md")

	if callCount != 2 {
		t.Errorf("adapter.Deliver called %d times, want 2", callCount)
	}
	if len(gotPaneIDs) < 2 {
		t.Fatalf("expected 2 pane ID calls, got %d", len(gotPaneIDs))
	}
	if gotPaneIDs[0] != "%stale" {
		t.Errorf("first attempt pane = %q, want %%stale", gotPaneIDs[0])
	}
	if gotPaneIDs[1] != "%fresh" {
		t.Errorf("retry pane = %q, want %%fresh", gotPaneIDs[1])
	}
	if strings.Contains(buf.String(), "pane notification failed") {
		t.Errorf("unexpected WARNING on successful retry: %s", buf.String())
	}
}

// TestDeliverNotificationWithRetry_BothAttemptsFail_LogsWarning verifies that
// when both delivery attempts fail, a WARNING is logged with node, pane, and session.
func TestDeliverNotificationWithRetry_BothAttemptsFail_LogsWarning(t *testing.T) {
	var callCount int

	adapter := controlplane.TmuxHandAdapter{
		ProbeRuntime: func(string) (string, error) { return "bash", nil },
		SendToPane: func(_ string, _ string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
			callCount++
			return fmt.Errorf("pane not found")
		},
	}

	target := controlplane.Target{
		ActorID:     "worker",
		RunID:       "test:worker",
		SessionName: "test",
		Brain:       controlplane.Brain{Runtime: "bash"},
		Hand:        controlplane.HandAttachment{Kind: controlplane.HandKindTmux, Address: "%gone"},
	}
	delivery := controlplane.PaneDelivery{BypassCooldown: true}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	deliverNotificationWithRetry(adapter, target, delivery, "test:worker", nil, "test-fail.md")

	if callCount != 2 {
		t.Errorf("adapter.Deliver called %d times, want 2", callCount)
	}
	logOut := buf.String()
	if !strings.Contains(logOut, "pane notification failed") {
		t.Errorf("expected WARNING containing 'pane notification failed', got: %s", logOut)
	}
	if !strings.Contains(logOut, "test:worker") {
		t.Errorf("expected node name in WARNING, got: %s", logOut)
	}
	if !strings.Contains(logOut, "msg=test-fail.md") {
		t.Errorf("expected msg= in WARNING, got: %s", logOut)
	}
}
