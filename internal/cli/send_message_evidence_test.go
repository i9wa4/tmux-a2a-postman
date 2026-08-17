package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	messagedelivery "github.com/i9wa4/tmux-a2a-postman/internal/message"
)

func TestRunSendMessage_CustomTemplateOwnedBoundaryDrivesEvidenceGate(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantDeadLetter bool
	}{
		{name: "wrapper done sender nonclaim", body: "Status: still working\n"},
		{name: "sender done", body: "DONE: complete\n", wantDeadLetter: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)
			t.Setenv("HOME", tmpDir)
			t.Setenv("XDG_CONFIG_HOME", tmpDir)
			configPath := filepath.Join(tmpDir, "postman.toml")
			configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: {reply_policy}
  timestamp: {timestamp}
  tmuxSession: {session_name}
---

# Message

DONE: wrapper-generated text

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
			if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
				t.Fatalf("WriteFile config: %v", err)
			}
			installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

			ctxID := "ctx-custom-boundary"
			sessionDir := filepath.Join(tmpDir, ctxID, "test-session")
			manager := journal.NewManager(ctxID, os.Getpid())
			journal.InstallProcessManager(manager)
			t.Cleanup(journal.ClearProcessManager)

			stdout, _, err := captureCommandOutput(t, func() error {
				return runSendHeredocWithBody(t, tt.body, []string{
					"--config", configPath,
					"--context-id", ctxID,
					"--to", "worker",
				})
			})
			if err != nil {
				t.Fatalf("RunSendMessage: %v", err)
			}
			payload := decodeSendOutputForTest(t, stdout)
			if err := config.CreateSessionDirs(sessionDir); err != nil {
				t.Fatalf("CreateSessionDirs: %v", err)
			}
			if err := manager.Bootstrap(sessionDir, "test-session", time.Date(2026, 7, 13, 10, 0, 1, 0, time.UTC)); err != nil {
				t.Fatalf("journal bootstrap failed: %v", err)
			}
			postPath := filepath.Join(sessionDir, "post", payload.Sent)
			contentBytes, err := os.ReadFile(postPath)
			if err != nil {
				t.Fatalf("ReadFile post: %v", err)
			}
			senderBody, exact := envelope.SenderBodyFromContent(string(contentBytes))
			if !exact {
				t.Fatalf("SenderBodyFromContent() exact = false; content:\n%s", contentBytes)
			}
			if !strings.HasPrefix(senderBody, tt.body) {
				t.Fatalf("SenderBodyFromContent() = %q, want prefix %q", senderBody, tt.body)
			}

			nodes := map[string]discovery.NodeInfo{
				"test-session:worker":    {PaneID: "%1", SessionName: "test-session", SessionDir: sessionDir},
				"test-session:messenger": {PaneID: "%2", SessionName: "test-session", SessionDir: sessionDir},
			}
			adjacency := map[string][]string{
				"messenger": {"worker"},
				"worker":    {"messenger"},
			}
			cfg := &config.Config{
				EnterDelay:                  0.1,
				TmuxTimeout:                 1.0,
				EvidencePresenceGateEnabled: true,
				EvidencePresenceGateAfter:   "2026-07-13T10:00:00Z",
			}
			if err := messagedelivery.DeliverMessage(postPath, ctxID, nodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
				t.Fatalf("DeliverMessage failed: %v", err)
			}
			inboxPath := filepath.Join(sessionDir, "inbox", "worker", payload.Sent)
			matches, err := filepath.Glob(filepath.Join(sessionDir, "dead-letter", "*missing-evidence*"))
			if err != nil {
				t.Fatalf("Glob failed: %v", err)
			}
			if tt.wantDeadLetter {
				if len(matches) != 1 {
					t.Fatalf("missing-evidence dead letters = %d, want 1: %v", len(matches), matches)
				}
				if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
					t.Fatalf("message delivered despite sender claim: %v", err)
				}
				return
			}
			if len(matches) != 0 {
				t.Fatalf("missing-evidence dead letters = %d, want 0: %v", len(matches), matches)
			}
			if _, err := os.Stat(inboxPath); err != nil {
				t.Fatalf("message not delivered to inbox: %v", err)
			}
		})
	}
}

func TestRunSendHeredoc_SupportedPathEvidenceGateClassifiesWrappedSenderBody(t *testing.T) {
	cases := []struct {
		name           string
		body           string
		evidenceFlags  bool
		wantDeadLetter bool
	}{
		{name: "done missing evidence", body: "DONE: complete", wantDeadLetter: true},
		{name: "pass missing evidence", body: "PASS: verified", wantDeadLetter: true},
		{name: "approved missing evidence", body: "APPROVED: accepted", wantDeadLetter: true},
		{name: "done complete evidence", body: "DONE: complete", evidenceFlags: true},
		{name: "pass complete evidence", body: "PASS: verified", evidenceFlags: true},
		{name: "approved complete evidence", body: "APPROVED: accepted", evidenceFlags: true},
		{name: "non claim missing evidence control", body: "Status: still working"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)
			t.Setenv("HOME", tmpDir)
			t.Setenv("XDG_CONFIG_HOME", tmpDir)

			configPath := filepath.Join(tmpDir, "postman.toml")
			configContent := `[postman]
edges = ["worker --- orchestrator"]

[worker]
role = "worker"

[orchestrator]
role = "orchestrator"
`
			if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
				t.Fatalf("WriteFile config: %v", err)
			}
			installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

			sessionDir := filepath.Join(tmpDir, "ctx-supported-evidence", "test-session")
			if err := config.CreateSessionDirs(sessionDir); err != nil {
				t.Fatalf("CreateSessionDirs: %v", err)
			}
			if err := config.WriteSessionPIDFile(filepath.Join(sessionDir, "postman.pid"), os.Getpid()); err != nil {
				t.Fatalf("WriteFile postman.pid: %v", err)
			}

			manager := journal.NewManager("ctx-supported-evidence", os.Getpid())
			journal.InstallProcessManager(manager)
			t.Cleanup(journal.ClearProcessManager)
			if err := manager.Bootstrap(sessionDir, "test-session", time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)); err != nil {
				t.Fatalf("journal bootstrap failed: %v", err)
			}
			var deliveryNotifications int
			restoreNotificationObserver := messagedelivery.SetDeliveryNotificationObserverForTest(func(messagedelivery.DeliveryNotificationObservation) {
				deliveryNotifications++
			})
			t.Cleanup(restoreNotificationObserver)

			deliverDone := make(chan error, 1)
			go func() {
				postDir := filepath.Join(sessionDir, "post")
				filename := awaitMarkdownFile(t, postDir, time.Second)
				postPath := filepath.Join(postDir, filename)
				content, err := os.ReadFile(postPath)
				if err != nil {
					deliverDone <- fmt.Errorf("ReadFile postPath: %w", err)
					return
				}
				senderBody, ok := envelope.SenderBodyFromContent(string(content))
				if !ok {
					deliverDone <- fmt.Errorf("SenderBodyFromContent(post content) ok = false; content:\n%s", content)
					return
				}
				if !strings.HasPrefix(senderBody, tc.body) {
					deliverDone <- fmt.Errorf("SenderBodyFromContent(post content) = %q, want prefix %q", senderBody, tc.body)
					return
				}

				nodes := map[string]discovery.NodeInfo{
					"test-session:worker":       {PaneID: "%1", SessionName: "test-session", SessionDir: sessionDir},
					"test-session:orchestrator": {PaneID: "%2", SessionName: "test-session", SessionDir: sessionDir},
				}
				adjacency := map[string][]string{
					"worker":       {"orchestrator"},
					"orchestrator": {"worker"},
				}
				deliveryCfg := &config.Config{
					EnterDelay:                  0.1,
					TmuxTimeout:                 1,
					EvidencePresenceGateEnabled: true,
					EvidencePresenceGateAfter:   "2026-07-13T10:00:00Z",
				}
				if err := messagedelivery.DeliverMessage(postPath, "ctx-supported-evidence", nodes, adjacency, deliveryCfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
					deliverDone <- fmt.Errorf("DeliverMessage: %w", err)
					return
				}
				deliverDone <- nil
			}()

			args := []string{
				"--config", configPath,
				"--context-id", "ctx-supported-evidence",
				"--to", "orchestrator",
			}
			if tc.evidenceFlags {
				evidenceRoot := createEvidenceRootForSendTest(t)
				args = append(args,
					"--evidence-command", "go test ./...",
					"--evidence-cwd", evidenceRoot,
					"--evidence-env-allowlist", "PATH,HOME",
					"--evidence-timeout-seconds", "120",
					"--evidence-side-effect-class", "read-only",
					"--evidence-artifact", "reports/test.json",
					"--evidence-hash", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				)
			}

			stdout, stderr, err := captureCommandOutput(t, func() error {
				return runSendHeredocWithBody(t, tc.body, args)
			})
			if deliverErr := <-deliverDone; deliverErr != nil {
				t.Fatal(deliverErr)
			}
			if tc.wantDeadLetter {
				if deliveryNotifications != 0 {
					t.Fatalf("delivery notifications = %d, want 0 for rejected message", deliveryNotifications)
				}
				if err == nil {
					t.Fatal("RunSendHeredoc() error = nil, want missing-evidence dead letter")
				}
				if !strings.Contains(err.Error(), "missing-evidence") {
					t.Fatalf("RunSendHeredoc() error = %v, want missing-evidence", err)
				}
				if stdout != "" {
					t.Fatalf("stdout = %q, want empty on dead letter", stdout)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunSendHeredoc: %v\nstderr=%s", err, stderr)
			}
			payload := decodeSendOutputForTest(t, stdout)
			if payload.Status != string(sendStatusProcessed) {
				t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
			}
			if payload.Notify != "" {
				t.Fatalf("payload.Notify = %q, want empty because daemon delivery owns the recipient hint", payload.Notify)
			}
			if deliveryNotifications != 1 {
				t.Fatalf("delivery notifications = %d, want exactly 1", deliveryNotifications)
			}
			if _, statErr := os.Stat(filepath.Join(sessionDir, "inbox", "orchestrator", payload.Sent)); statErr != nil {
				t.Fatalf("Stat delivered inbox file: %v", statErr)
			}
			deadLetterMatches, globErr := filepath.Glob(filepath.Join(sessionDir, "dead-letter", "*missing-evidence*"))
			if globErr != nil {
				t.Fatalf("Glob dead-letter: %v", globErr)
			}
			if len(deadLetterMatches) != 0 {
				t.Fatalf("missing-evidence dead letters = %d, want 0: %v", len(deadLetterMatches), deadLetterMatches)
			}
		})
	}
}
