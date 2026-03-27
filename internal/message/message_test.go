package message

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
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
		{
			// 64-char from field: "a" + 63 "a" chars = 64 total (#299)
			name:     "64-char node name (boundary accept)",
			filename: "12345-from-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-to-b.md",
			wantTS:   "12345",
			wantFrom: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantTo:   "b",
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
	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
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

	// Place a message for unknown recipient (with valid frontmatter for envelope validation, Issue #161)
	filename := "20260201-030000-from-orchestrator-to-unknown-node.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: orchestrator\n  to: unknown-node\n  timestamp: 2026-02-01T03:00:00Z\n---\n\ntest message\n"
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
	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify moved to dead-letter/ with unknown-recipient suffix
	deadPath := filepath.Join(sessionDir, "dead-letter", "20260201-030000-from-orchestrator-to-unknown-node-dl-unknown-recipient.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter: %v", err)
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
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

func TestRouting_PostmanAlwaysAllowed(t *testing.T) {
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(sessionDir, "dead-letter", "badname-dl-parse-error.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter: %v", err)
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

	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, isSessionEnabled, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(senderDir, "dead-letter", "20260201-030000-from-alice-to-bob-dl-session-disabled.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not in dead-letter (recipient session disabled): %v", err)
	}
}

func TestDeliverMessage_CrossSessionPing(t *testing.T) {
	// Verify Bug 1 fix: a ping file written by SendPingToNode to the target session's
	// post/ dir must reach the inbox, not be dead-lettered as a forged sender.
	// sourceSessionName == "messenger" != daemonSession == "local-daemon",
	// but isSessionEnabled("messenger") == true, so the guard must pass.
	tmpDir := t.TempDir()
	messengerDir := filepath.Join(tmpDir, "messenger") // basename = sourceSessionName
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

	// messenger is enabled; local-daemon is the daemon's own session
	isSessionEnabled := func(s string) bool { return s == "messenger" }

	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, isSessionEnabled, nil, idle.NewIdleTracker(), "local-daemon"); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	inboxPath := filepath.Join(messengerDir, "inbox", "orchestrator", filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Errorf("ping not delivered to inbox: %v", err)
	}

	deadLetterGlob := filepath.Join(messengerDir, "dead-letter", "*forged*")
	matches, _ := filepath.Glob(deadLetterGlob)
	if len(matches) > 0 {
		t.Errorf("ping was incorrectly dead-lettered as forged sender: %v", matches)
	}
}

func TestDeliverMessage_DisabledSessionPing(t *testing.T) {
	// Verify Bug 3 scenario: a from=postman file arriving in a disabled session's post/ dir
	// must be dead-lettered as forged sender. isSessionEnabled returns false, so the
	// forged-sender guard at message.go:313 fires before ResolveNodeName is reached.
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

	// messenger is disabled; local-daemon is the daemon's own session
	isSessionEnabled := func(s string) bool { return false }

	_ = DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, isSessionEnabled, nil, idle.NewIdleTracker(), "local-daemon")

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

	err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), "")
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
params:
  contextId: test-ctx
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
	err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, func(s string) bool { return true }, nil, idleTracker, "")
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

// TestDeliverMessage_ForeignSession verifies that F4 dead-letters messages
// addressed to a recipient in a foreign (non-daemon, non-enabled) session.
func TestDeliverMessage_ForeignSession(t *testing.T) {
	// F4: Verify that a recipient in a foreign (non-daemon, non-enabled) session is dead-lettered.
	// Setup: daemon owns "own-session". Sender (postman) delivers to bob, who somehow appears
	// in knownNodes under "own-session" but nodeInfo.SessionName resolves to "foreign-session"
	// (simulating stale knownNodes after a session was previously enabled then not).
	senderDir := filepath.Join(t.TempDir(), "own-session") // basename matches daemonSession
	if err := config.CreateSessionDirs(senderDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}
	recipientDir := t.TempDir()

	filename := "20260201-040000-from-postman-to-bob.md"
	postPath := filepath.Join(senderDir, "post", filename)
	// postman sender bypasses adjacency and envelope checks
	content := "---\nparams:\n  contextId: test-ctx\n  from: postman\n  to: bob\n  timestamp: 2026-02-01T04:00:00Z\n---\n\ncontent\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// bob's NodeInfo has SessionName "foreign-session" (a stale cross-session entry in knownNodes).
	// "own-session:bob" key means same-session lookup succeeds, but nodeInfo reveals the actual session.
	nodes := map[string]discovery.NodeInfo{
		"own-session:bob": {PaneID: "%2", SessionName: "foreign-session", SessionDir: recipientDir},
	}
	adjacency := map[string][]string{}
	cfg := &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}

	// foreign-session is not enabled; daemonSession = "own-session"
	isSessionEnabled := func(s string) bool { return s == "own-session" }
	if err := DeliverMessage(postPath, "test-ctx", nodes, nil, adjacency, cfg, isSessionEnabled, nil, idle.NewIdleTracker(), "own-session"); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	deadPath := filepath.Join(senderDir, "dead-letter", "20260201-040000-from-postman-to-bob-dl-foreign-session.md")
	if _, err := os.Stat(deadPath); err != nil {
		t.Errorf("message not dead-lettered with dlSuffixForeignSession: %v", err)
	}
}

// helper: build a minimal BindingRegistry with one active binding for nodeName.
func makeRegistry(nodeName string, active bool, permittedSenders []string) *binding.BindingRegistry {
	return &binding.BindingRegistry{
		Bindings: []binding.Binding{
			{
				ChannelID:        "chan-01",
				NodeName:         nodeName,
				ContextID:        "ctx-01",
				Active:           active,
				PermittedSenders: permittedSenders,
			},
		},
	}
}

func TestDeliverToPhonyNode_Success(t *testing.T) {
	baseDir := t.TempDir()
	reg := makeRegistry("worker", true, []string{"orchestrator"})
	msg := Message{Body: "hello phony", MessageID: "msg-1", SenderID: "orchestrator"}

	if err := DeliverToPhonyNode(baseDir, "ctx-01", "worker", "orchestrator", reg, msg); err != nil {
		t.Fatalf("DeliverToPhonyNode failed: %v", err)
	}

	inboxDir := filepath.Join(baseDir, "ctx-01", "phony", "worker", "inbox")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("inbox dir missing: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 inbox file, got %d", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(inboxDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading inbox file: %v", err)
	}
	if string(data) != msg.Body {
		t.Errorf("inbox body: got %q, want %q", string(data), msg.Body)
	}
}

func TestDeliverToPhonyNode_RoutingDenied(t *testing.T) {
	baseDir := t.TempDir()
	reg := makeRegistry("worker", true, []string{"orchestrator"})
	msg := Message{Body: "unauthorized", SenderID: "intruder"}

	if err := DeliverToPhonyNode(baseDir, "ctx-01", "worker", "intruder", reg, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// dead-letter must exist
	dlDir := filepath.Join(baseDir, "ctx-01", "phony", "worker", "dead-letter")
	entries, err := os.ReadDir(dlDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 dead-letter file, dir err=%v entries=%d", err, len(entries))
	}
	// inbox must be empty
	inboxDir := filepath.Join(baseDir, "ctx-01", "phony", "worker", "inbox")
	if _, err := os.Stat(inboxDir); !os.IsNotExist(err) {
		t.Error("inbox should not exist when routing is denied")
	}
	// verify JSON reason
	data, _ := os.ReadFile(filepath.Join(dlDir, entries[0].Name()))
	var rec phonyDeadLetterRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal dead-letter: %v", err)
	}
	if rec.Reason != "routing_denied" {
		t.Errorf("reason: got %q, want %q", rec.Reason, "routing_denied")
	}
	if rec.SchemaVersion != 1 {
		t.Errorf("schema_version: got %d, want 1", rec.SchemaVersion)
	}
}

func TestDeliverToPhonyNode_DefaultDeny_AbsentKey(t *testing.T) {
	baseDir := t.TempDir()
	// Registry has no binding for "worker"
	reg := &binding.BindingRegistry{Bindings: []binding.Binding{}}
	msg := Message{Body: "hello", SenderID: "orchestrator"}

	if err := DeliverToPhonyNode(baseDir, "ctx-01", "worker", "orchestrator", reg, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dlDir := filepath.Join(baseDir, "ctx-01", "phony", "worker", "dead-letter")
	entries, err := os.ReadDir(dlDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 dead-letter, got err=%v n=%d", err, len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(dlDir, entries[0].Name()))
	var rec phonyDeadLetterRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Reason != "routing_denied" {
		t.Errorf("reason: got %q, want routing_denied", rec.Reason)
	}
}

func TestDeliverToPhonyNode_DefaultDeny_EmptyList(t *testing.T) {
	baseDir := t.TempDir()
	reg := makeRegistry("worker", true, []string{}) // empty permitted_senders
	msg := Message{Body: "hello", SenderID: "orchestrator"}

	if err := DeliverToPhonyNode(baseDir, "ctx-01", "worker", "orchestrator", reg, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dlDir := filepath.Join(baseDir, "ctx-01", "phony", "worker", "dead-letter")
	entries, err := os.ReadDir(dlDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 dead-letter, got err=%v n=%d", err, len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(dlDir, entries[0].Name()))
	var rec phonyDeadLetterRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Reason != "routing_denied" {
		t.Errorf("reason: got %q, want routing_denied", rec.Reason)
	}
}

func TestDeliverToPhonyNode_ChannelUnbound(t *testing.T) {
	baseDir := t.TempDir()
	reg := makeRegistry("worker", false, []string{"orchestrator"}) // active=false
	msg := Message{Body: "hello", SenderID: "orchestrator"}

	if err := DeliverToPhonyNode(baseDir, "ctx-01", "worker", "orchestrator", reg, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dlDir := filepath.Join(baseDir, "ctx-01", "phony", "worker", "dead-letter")
	entries, err := os.ReadDir(dlDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 dead-letter, got err=%v n=%d", err, len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(dlDir, entries[0].Name()))
	var rec phonyDeadLetterRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Reason != "channel_unbound" {
		t.Errorf("reason: got %q, want channel_unbound", rec.Reason)
	}
}

func TestDeliverToPhonyNode_FilenameInvariant(t *testing.T) {
	// sender_id with path traversal chars; filename must not contain those bytes
	baseDir := t.TempDir()
	reg := makeRegistry("worker", true, []string{"orchestrator"})
	// Try to inject "/" and ".." via sender_id and channel_id (via body)
	msg := Message{
		Body:      "../../../etc/passwd",
		MessageID: "msg/../traversal",
		SenderID:  "orchestrator/../evil",
	}
	// routing will pass (sender param is "orchestrator")
	if err := DeliverToPhonyNode(baseDir, "ctx-01", "worker", "orchestrator", reg, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inboxDir := filepath.Join(baseDir, "ctx-01", "phony", "worker", "inbox")
	entries, err := os.ReadDir(inboxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 inbox file, err=%v n=%d", err, len(entries))
	}
	name := entries[0].Name()
	if strings.Contains(name, "/") || strings.Contains(name, "..") || strings.Contains(name, "evil") || strings.Contains(name, "passwd") {
		t.Errorf("filename contains unsafe bytes: %q", name)
	}
}

// TestDeliverMessage_PhonyDispatch verifies that DeliverMessage routes messages
// to phony nodes via dispatchPhonyNode, before ResolveNodeName is called (#306).
func TestDeliverMessage_PhonyDispatch(t *testing.T) {
	baseDir := t.TempDir()
	// sessionDir provides the post/ directory; basename is used as source session name.
	sessionDir := filepath.Join(t.TempDir(), "own-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
	}

	filename := "20260201-030000-from-orchestrator-to-channel-a.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: ctx-01\n  from: orchestrator\n  to: channel-a\n  timestamp: 2026-02-01T03:00:00Z\n---\n\nphony message\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Phony nodes use bare keys (no session: prefix) so dispatchPhonyNode can match
	// info.To before ResolveNodeName is called.
	nodes := map[string]discovery.NodeInfo{
		"channel-a": {IsPhony: true},
	}
	reg := makeRegistry("channel-a", true, []string{"orchestrator"})
	cfg := &config.Config{
		BaseDir:     baseDir,
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}

	if err := DeliverMessage(postPath, "ctx-01", nodes, reg, map[string][]string{}, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}

	// Verify phony inbox received the message.
	inboxDir := filepath.Join(baseDir, "ctx-01", "phony", "channel-a", "inbox")
	entries, err := os.ReadDir(inboxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 phony inbox file, got err=%v n=%d", err, len(entries))
	}
	// Verify post/ file was removed by dispatchPhonyNode.
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("post/ file should be removed after phony dispatch")
	}
}
