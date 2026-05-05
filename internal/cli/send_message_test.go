package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRunSendMessage_BasicFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := RunSendMessage([]string{"--to", "worker", "--body", "hello"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("unexpected flag-parse error: %v", err)
	}
}

func TestRunSendMessage_FlagHelpOmitsHiddenAndRemovedFlags(t *testing.T) {
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{"-h"})
	})
	if err == nil {
		t.Fatal("RunSendMessage(-h) = nil, want help error")
	}
	if !strings.Contains(err.Error(), "flag: help requested") {
		t.Fatalf("RunSendMessage(-h) error = %v, want help requested", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage of send:") {
		t.Fatalf("stderr missing help header: %q", stderr)
	}
	if strings.Contains(stderr, "--context-id") {
		t.Fatalf("stderr still exposes hidden context override: %q", stderr)
	}
	for _, removed := range []string{"--config", "--json", "--params", "--idempotency-key", "--session"} {
		if strings.Contains(stderr, removed) {
			t.Fatalf("stderr still exposes hidden/removed flag %s: %q", removed, stderr)
		}
	}
}

func TestRunSendMessage_FromFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := RunSendMessage([]string{"--to", "worker", "--body", "hello", "--from", "orchestrator"})
	if err == nil {
		t.Fatal("expected unknown-flag error for --from, got nil")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -from") {
		t.Fatalf("expected unknown --from flag error, got: %v", err)
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

func TestRunSendMessage_InvalidReplyToRejectedBeforeWriting(t *testing.T) {
	tests := []struct {
		name    string
		replyTo string
		want    string
	}{
		{
			name:    "path",
			replyTo: "../previous.md",
			want:    "not a path",
		},
		{
			name:    "multi_token",
			replyTo: "previous message.md",
			want:    "single message id token",
		},
		{
			name:    "bad_filename",
			replyTo: "previous.md",
			want:    "valid message id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("POSTMAN_HOME", tmpDir)
			err := RunSendMessage([]string{
				"--to", "worker",
				"--body", "hello",
				"--reply-to", tt.replyTo,
			})
			if err == nil {
				t.Fatal("RunSendMessage() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RunSendMessage() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestRunSendMessage_InvalidSatisfiesObligationIDRejectedBeforeWriting(t *testing.T) {
	tests := []struct {
		name         string
		obligationID string
		want         string
	}{
		{
			name:         "path",
			obligationID: "../obl_123",
			want:         "path separators",
		},
		{
			name:         "multi_token",
			obligationID: "obl 123",
			want:         "whitespace",
		},
		{
			name: "empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("POSTMAN_HOME", tmpDir)
			args := []string{
				"--to", "worker",
				"--body", "hello",
				"--satisfies-obligation-id", tt.obligationID,
			}
			err := RunSendMessage(args)
			if tt.obligationID == "" {
				if err != nil && strings.Contains(err.Error(), "--satisfies-obligation-id") {
					t.Fatalf("RunSendMessage() error = %v, want no obligation validation error", err)
				}
				return
			}
			if err == nil {
				t.Fatal("RunSendMessage() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RunSendMessage() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestRunSendMessage_ReplyPolicyFlagsAreMutuallyExclusive(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := RunSendMessage([]string{
		"--to", "worker",
		"--body", "hello",
		"--no-reply",
		"--reply-required",
	})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("RunSendMessage() error = %v, want mutually exclusive", err)
	}
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

func TestSendMessage_MissingSender(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["orchestrator --- worker"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-missing-sender",
		"--to", "worker",
		"--body", "hello",
	})
	if err == nil {
		t.Fatal("expected missing sender error, got nil")
	}
	if !strings.Contains(err.Error(), "missing sender") {
		t.Fatalf("expected missing sender error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "\"messenger\"") {
		t.Fatalf("expected missing sender error to name messenger, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-missing-sender", "test-session"))
}

func TestSendMessage_MissingReceiver(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- orchestrator"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-missing-receiver",
		"--to", "worker",
		"--body", "hello",
	})
	if err == nil {
		t.Fatal("expected missing receiver error, got nil")
	}
	if !strings.Contains(err.Error(), "missing receiver") {
		t.Fatalf("expected missing receiver error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "\"worker\"") {
		t.Fatalf("expected missing receiver error to name worker, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-missing-receiver", "test-session"))
}

func TestSendMessage_InvalidEdge(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- orchestrator --- worker"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-invalid-edge",
		"--to", "worker",
		"--body", "hello",
	})
	if err == nil {
		t.Fatal("expected edge violation error, got nil")
	}
	if !strings.Contains(err.Error(), "edge violation") {
		t.Fatalf("expected edge violation error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "allowed recipients: orchestrator") {
		t.Fatalf("expected edge violation error to name allowed recipients, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-invalid-edge", "test-session"))
}

func TestSendMessage_AllowsSessionPrefixedGraphKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["test-session:messenger --- review-session:worker"]

["test-session:messenger"]
role = "messenger"

["review-session:worker"]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-prefixed-graph",
		"--to", "review-session:worker",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-prefixed-graph", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-review-session:worker.md") {
		t.Fatalf("post filename missing session-prefixed recipient: %q", entries[0].Name())
	}
}

func TestSendMessage_AllowsMixedSenderGraphKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = [
  "messenger --- orchestrator",
  "test-session:messenger --- review-session:worker",
]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

["test-session:messenger"]
role = "messenger"

["review-session:worker"]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-mixed-sender-graph",
		"--to", "review-session:worker",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-mixed-sender-graph", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-review-session:worker.md") {
		t.Fatalf("post filename missing session-prefixed recipient: %q", entries[0].Name())
	}
}

func TestSendMessage_PrefixedRecipientRequiresExplicitGraphKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["test-session:messenger --- worker"]

[messenger]
role = "messenger"

["test-session:messenger"]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-prefixed-recipient-needs-explicit-edge",
		"--to", "review-session:worker",
		"--body", "hello",
	})
	if err == nil {
		t.Fatal("expected missing receiver error, got nil")
	}
	if !strings.Contains(err.Error(), "missing receiver") {
		t.Fatalf("expected missing receiver error, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-prefixed-recipient-needs-explicit-edge", "test-session"))
}

func TestSendMessage_AllowsSameSessionFullGraphKeyForBareRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["test-session:messenger --- test-session:worker"]

["test-session:messenger"]
role = "messenger"

["test-session:worker"]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-full-same-session-graph",
		"--to", "worker",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-full-same-session-graph", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-worker.md") {
		t.Fatalf("post filename missing bare recipient: %q", entries[0].Name())
	}
}

func TestSendMessage_AllowsBareGraphKeyForSameSessionPrefixedRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-bare-graph-same-session-prefixed-recipient",
		"--to", "test-session:worker",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-bare-graph-same-session-prefixed-recipient", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-test-session:worker.md") {
		t.Fatalf("post filename missing session-prefixed recipient: %q", entries[0].Name())
	}
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

func TestRunSendMessage_IdempotencyKeyFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := RunSendMessage([]string{"--to", "worker", "--body", "hello", "--idempotency-key", "key-abc-123"})
	if err == nil {
		t.Fatal("expected unknown-flag error for --idempotency-key, got nil")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -idempotency-key") {
		t.Fatalf("expected unknown --idempotency-key flag error, got: %v", err)
	}
}

func TestRunSendMessage_DraftTemplateExpandsReplyCommand(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = "send --to <recipient>"
draft_template = "{reply_command}"
message_footer = ""
edges = ["orchestrator --- worker"]

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "orchestrator")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-draft-reply",
		"--to", "worker",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-draft-reply", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	if !strings.Contains(string(draftContent), "send --to worker") {
		t.Fatalf("draft content missing reply command: %q", string(draftContent))
	}
}

func TestRunSendMessage_DraftTemplatePreservesMultilineReplyCommand(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = """
tmux-a2a-postman send
  --to <recipient>
  --body "<your message>"
"""
draft_template = "{reply_command}"
message_footer = ""
edges = ["orchestrator --- worker"]

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "orchestrator")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-draft-multiline",
		"--to", "worker",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-draft-multiline", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	want := "tmux-a2a-postman send\n  --to worker\n  --body \"<your message>\""
	if !strings.Contains(string(draftContent), want) {
		t.Fatalf("draft content missing preserved multiline reply command:\n%s", string(draftContent))
	}
}

func TestRunSendMessage_MessageFooterUsesRecipientReachability(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = "tmux-a2a-postman send --to <recipient> --body \"<your message>\""
draft_template = "# Content\n\n"
message_footer = """You can talk to: {can_talk_to}
Reply: {reply_command}
"""
edges = ["messenger --- orchestrator --- boss"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

[boss]
role = "boss"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-footer-recipient",
		"--to", "orchestrator",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-footer-recipient", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	content := string(draftContent)
	if !strings.Contains(content, "You can talk to: messenger, boss") {
		t.Fatalf("footer missing recipient reachability:\n%s", content)
	}
	if strings.Contains(content, "You can talk to: orchestrator") {
		t.Fatalf("footer still contains sender reachability:\n%s", content)
	}
	if !strings.Contains(content, "Reply: tmux-a2a-postman send --to messenger") {
		t.Fatalf("footer missing recipient-scoped reply command:\n%s", content)
	}
	if strings.Contains(content, "Reply: tmux-a2a-postman send --to orchestrator") {
		t.Fatalf("footer still contains sender-scoped reply command:\n%s", content)
	}
}

func TestRunSendMessage_MessageFooterUsesSessionPrefixedRecipientReachability(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = "tmux-a2a-postman send --to <recipient> --body \"<your message>\""
draft_template = "# Content\n\n"
message_footer = """You can talk to: {can_talk_to}
Reply: {reply_command}
"""
edges = ["messenger --- review-session:orchestrator --- boss"]

[messenger]
role = "messenger"

["review-session:orchestrator"]
role = "orchestrator"

[boss]
role = "boss"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-footer-prefixed-recipient",
		"--to", "review-session:orchestrator",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-footer-prefixed-recipient", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-review-session:orchestrator.md") {
		t.Fatalf("post filename missing session-prefixed recipient: %q", entries[0].Name())
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	content := string(draftContent)
	if !strings.Contains(content, "You can talk to: messenger, boss") {
		t.Fatalf("footer missing session-prefixed recipient reachability:\n%s", content)
	}
	if strings.Contains(content, "You can talk to: review-session:orchestrator") {
		t.Fatalf("footer unexpectedly used recipient key instead of neighbor list:\n%s", content)
	}
	if !strings.Contains(content, "Reply: tmux-a2a-postman send --to messenger") {
		t.Fatalf("footer missing recipient-scoped reply command:\n%s", content)
	}
}

func TestRunSendMessage_DefaultMessageFooterUsesConfiguredReplyCommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = "custom-reply --context {context_id} --to <recipient>"
draft_template = "# Content\n\n"
edges = ["messenger --- orchestrator"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := RunSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-default-footer-reply",
		"--to", "orchestrator",
		"--body", "hello",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-default-footer-reply", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	content := string(draftContent)
	if !strings.Contains(content, "Reply: custom-reply --context ctx-default-footer-reply --to messenger") {
		t.Fatalf("default footer missing configured reply command:\n%s", content)
	}
	if strings.Contains(content, "Reply: tmux-a2a-postman send --to <receiver>") {
		t.Fatalf("default footer still contains hard-coded placeholder reply command:\n%s", content)
	}
}

func TestRunSendMessage_StoresReplyPolicyMetadataAndReplyToFooter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-reply-policy",
			"--to", "worker",
			"--body", "please do this",
			"--reply-required",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.ObligationID == "" {
		t.Fatal("payload.ObligationID is empty, want generated obligation id")
	}
	if err := envelope.ValidateObligationToken(payload.ObligationID); err != nil {
		t.Fatalf("payload.ObligationID = %q is invalid: %v", payload.ObligationID, err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-reply-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"messageId: " + payload.Sent,
		"replyPolicy: required",
		"obligation_id: " + payload.ObligationID,
		"Reply: tmux-a2a-postman send --to messenger --body \"<your message>\" --satisfies-obligation-id " + payload.ObligationID + " --reply-to " + payload.Sent,
		"Add --reply-required only when your reply needs a response.",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
	if strings.Contains(string(content), "satisfies_obligation_id:") {
		t.Fatalf("content contains unexpected satisfies_obligation_id:\n%s", string(content))
	}
}

func TestRunSendMessage_StoresSatisfiesObligationIDForExactReply(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	const obligationID = "obl_exact_123"
	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-exact-reply",
			"--to", "messenger",
			"--body", "DONE",
			"--reply-to", "20260503-090000-sabcd-r1234-from-messenger-to-worker.md",
			"--satisfies-obligation-id", obligationID,
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ObligationID != "" {
		t.Fatalf("payload.ObligationID = %q, want empty for no-reply exact reply", payload.ObligationID)
	}
	if payload.SatisfiesObligationID != obligationID {
		t.Fatalf("payload.SatisfiesObligationID = %q, want %q", payload.SatisfiesObligationID, obligationID)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-exact-reply", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"replyPolicy: none",
		"replyTo: 20260503-090000-sabcd-r1234-from-messenger-to-worker.md",
		"satisfies_obligation_id: " + obligationID,
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
	if strings.Contains(string(content), "\n  obligation_id:") {
		t.Fatalf("content contains unexpected obligation_id:\n%s", string(content))
	}
}

func TestRunSendMessage_DefaultStoresNoReplyPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-default-reply-policy",
			"--to", "worker",
			"--body", "plain update",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}
}

func TestRunSendMessage_StrictMessageTypeStoresRequiredPolicy(t *testing.T) {
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
  timestamp: {timestamp}
  messageType: status_request
  obligation_id: {obligation_id}
---

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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-message-type-policy",
			"--to", "worker",
			"--body", "current status?",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.ObligationID == "" {
		t.Fatal("payload.ObligationID is empty, want generated obligation id")
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-message-type-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"messageType: status_request",
		"replyPolicy: required",
		"obligation_id: " + payload.ObligationID,
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
}

func TestRunSendMessage_StrictMessageTypeOverridesGeneratedReplyPolicyPlaceholder(t *testing.T) {
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
  messageType: status_request
---

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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-message-type-placeholder-policy",
			"--to", "worker",
			"--body", "current status?",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-message-type-placeholder-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"messageType: status_request",
		"replyPolicy: required",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
	if strings.Contains(string(content), "replyPolicy: none") {
		t.Fatalf("content kept generated replyPolicy none:\n%s", string(content))
	}
	assertNoGeneratedReplyPolicyMarker(t, string(content), "post content")
}

func TestRunSendMessage_StrictMessageTypeOverridesSpacedGeneratedReplyPolicyPlaceholder(t *testing.T) {
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
  replyPolicy:  {reply_policy}
  timestamp: {timestamp}
  messageType: status_request
---

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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-message-type-spaced-placeholder-policy",
			"--to", "worker",
			"--body", "current status?",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
}

func TestRunSendMessage_StrictMessageTypePreservesExplicitNoReplyWhenPlaceholderIsNotInParams(t *testing.T) {
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
  replyPolicy: none
  timestamp: {timestamp}
  messageType: status_request
---

replyPolicy: {reply_policy}
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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-explicit-no-reply-body-placeholder",
			"--to", "worker",
			"--body", "current status?",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}
}

func TestRunSendMessage_StrictMessageTypePreservesExplicitPolicyWithPlaceholderAlias(t *testing.T) {
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
  replyPolicy: required
  reply_obligation: {reply_policy}
  timestamp: {timestamp}
  messageType: status_update
---

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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-explicit-policy-placeholder-alias",
			"--to", "worker",
			"--body", "please reply",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-explicit-policy-placeholder-alias", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"replyPolicy: required",
		"reply_obligation: required",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
}

func TestRunSendMessage_StrictMessageTypePreservesExplicitNoReplyWithPlaceholderAlias(t *testing.T) {
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
  replyPolicy: none
  reply_obligation: {reply_policy}
  timestamp: {timestamp}
  messageType: status_request
---

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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-explicit-no-reply-placeholder-alias",
			"--to", "worker",
			"--body", "status-only",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-explicit-no-reply-placeholder-alias", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"replyPolicy: none",
		"reply_obligation: none",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
}

func TestRunSendMessage_PreservesShellExpandedExplicitReplyPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
allow_shell_templates = true
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: $(printf required)
  timestamp: {timestamp}
  messageType: status_update
---

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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-shell-expanded-reply-policy",
			"--to", "worker",
			"--body", "please reply",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-shell-expanded-reply-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), "replyPolicy: required") {
		t.Fatalf("content missing expanded replyPolicy:\n%s", string(content))
	}
}

func TestRunSendMessage_PreservesShellGeneratedReplyPolicyLine(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
allow_shell_templates = true
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: {reply_policy}
$(printf '  reply_obligation: none')
  timestamp: {timestamp}
  messageType: status_request
---

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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-shell-generated-reply-policy-line",
			"--to", "worker",
			"--body", "status only",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-shell-generated-reply-policy-line", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), "reply_obligation: none") {
		t.Fatalf("content missing shell-generated reply_obligation:\n%s", string(content))
	}
	assertNoGeneratedReplyPolicyMarker(t, string(content), "post content")
}

func TestRunSendMessage_PreservesLastExplicitPolicyWhenShellAddsPolicyFields(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
allow_shell_templates = true
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: $(printf 'required\n  replyPolicy: none')
  reply_obligation: required
  timestamp: {timestamp}
  messageType: status_update
---

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

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-shell-added-reply-policy-fields",
			"--to", "worker",
			"--body", "please reply",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-shell-added-reply-policy-fields", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), "reply_obligation: required") {
		t.Fatalf("content missing reply_obligation required:\n%s", string(content))
	}
	assertNoGeneratedReplyPolicyMarker(t, string(content), "post content")
}

func TestRunSendMessage_ReplyPolicyFlagsUpdatePlaceholderAliases(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPolicy  string
		wantContext string
	}{
		{
			name:        "no reply",
			args:        []string{"--no-reply"},
			wantPolicy:  "none",
			wantContext: "ctx-flag-no-reply-placeholder-alias",
		},
		{
			name:        "reply required",
			args:        []string{"--reply-required"},
			wantPolicy:  "required",
			wantContext: "ctx-flag-reply-required-placeholder-alias",
		},
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
  reply_obligation: {reply_policy}
  timestamp: {timestamp}
---

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

			args := []string{
				"--config", configPath,
				"--context-id", tt.wantContext,
				"--to", "worker",
				"--body", "flag-controlled policy",
			}
			args = append(args, tt.args...)
			stdout, _, err := captureCommandOutput(t, func() error {
				return RunSendMessage(args)
			})
			if err != nil {
				t.Fatalf("RunSendMessage: %v", err)
			}
			payload := decodeSendOutputForTest(t, stdout)
			if payload.ReplyPolicy != tt.wantPolicy {
				t.Fatalf("payload.ReplyPolicy = %q, want %q", payload.ReplyPolicy, tt.wantPolicy)
			}

			content, err := os.ReadFile(filepath.Join(tmpDir, tt.wantContext, "test-session", "post", payload.Sent))
			if err != nil {
				t.Fatalf("ReadFile post: %v", err)
			}
			if !strings.Contains(string(content), "reply_obligation: "+tt.wantPolicy) {
				t.Fatalf("content missing reply_obligation %q:\n%s", tt.wantPolicy, string(content))
			}
		})
	}
}

func TestRunSendMessage_NoReplyFlagStoresNoReplyPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-no-reply-policy",
			"--to", "worker",
			"--body", "status-only update",
			"--no-reply",
			"--reply-to", "20260503-090000-sabcd-r1234-from-worker-to-messenger.md",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}
	if payload.ReplyTo != "20260503-090000-sabcd-r1234-from-worker-to-messenger.md" {
		t.Fatalf("payload.ReplyTo = %q, want previous message id", payload.ReplyTo)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-no-reply-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"replyPolicy: none",
		"replyTo: 20260503-090000-sabcd-r1234-from-worker-to-messenger.md",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
}

func TestRunSendMessage_DefaultJSONReportsQueuedWhenOnlyLocalHandoffIsConfirmed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-send-queued-json",
			"--to", "worker",
			"--body", "hello queued json",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	var payload struct {
		Sent       string                `json:"sent"`
		Status     string                `json:"status"`
		SubmitPath projection.SubmitPath `json:"submit_path"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if payload.Sent == "" {
		t.Fatalf("payload.Sent = empty, want filename")
	}
	if payload.Status != string(sendStatusQueued) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusQueued)
	}
	if payload.SubmitPath != projection.SubmitPathPost {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathPost)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-queued-json", "test-session", "post")
	postEntries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(postEntries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(postEntries))
	}
	if postEntries[0].Name() != payload.Sent {
		t.Fatalf("post filename = %q, want %q", postEntries[0].Name(), payload.Sent)
	}
}

func TestRunSendMessage_DefaultJSONReportsProcessedWhenDaemonConsumesDirectPost(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-direct-processed", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	ownerSessionDir := filepath.Join(tmpDir, "ctx-send-direct-processed", "owner-session")
	if err := os.MkdirAll(ownerSessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll ownerSessionDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ownerSessionDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}

	go func() {
		postDir := filepath.Join(sessionDir, "post")
		filename := awaitMarkdownFile(t, postDir, time.Second)
		inboxDir := filepath.Join(sessionDir, "inbox", "worker")
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			t.Errorf("MkdirAll inboxDir: %v", err)
			return
		}
		if err := os.Rename(filepath.Join(postDir, filename), filepath.Join(inboxDir, filename)); err != nil {
			t.Errorf("Rename post to inbox: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-send-direct-processed",
			"--to", "worker",
			"--body", "hello processed json",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	var payload struct {
		Sent       string                `json:"sent"`
		Status     string                `json:"status"`
		SubmitPath projection.SubmitPath `json:"submit_path"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.SubmitPath != projection.SubmitPathPost {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathPost)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "inbox", "worker", payload.Sent)); err != nil {
		t.Fatalf("Stat delivered inbox file: %v", err)
	}
}

func TestRunSendMessage_ReturnsErrorWhenDaemonDeadLettersDirectPost(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-direct-dead-letter", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	ownerSessionDir := filepath.Join(tmpDir, "ctx-send-direct-dead-letter", "owner-session")
	if err := os.MkdirAll(ownerSessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll ownerSessionDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ownerSessionDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}

	go func() {
		postDir := filepath.Join(sessionDir, "post")
		filename := awaitMarkdownFile(t, postDir, time.Second)
		deadLetterDir := filepath.Join(sessionDir, "dead-letter")
		if err := os.MkdirAll(deadLetterDir, 0o700); err != nil {
			t.Errorf("MkdirAll deadLetterDir: %v", err)
			return
		}
		dst := filepath.Join(deadLetterDir, strings.TrimSuffix(filename, ".md")+"-dl-routing-denied.md")
		if err := os.Rename(filepath.Join(postDir, filename), dst); err != nil {
			t.Errorf("Rename post to dead-letter: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-send-direct-dead-letter",
			"--to", "worker",
			"--body", "hello dead letter",
		})
	})
	if err == nil {
		t.Fatal("RunSendMessage() = nil, want dead-letter error")
	}
	if !strings.Contains(err.Error(), "message dead-lettered:") {
		t.Fatalf("RunSendMessage() error = %v, want dead-letter wording", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunSendMessage_UsesDaemonSubmitWhenDaemonOwnsSession(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-submit", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, "ctx-send-submit", "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	go func() {
		requestPath, request := awaitDaemonSubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		postDir := filepath.Join(sessionDir, "post")
		if err := os.MkdirAll(postDir, 0o700); err != nil {
			t.Errorf("MkdirAll postDir: %v", err)
			return
		}
		postPath := filepath.Join(postDir, request.Filename)
		if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
			t.Errorf("WriteFile postPath: %v", err)
			return
		}
		if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("Remove requestPath: %v", err)
		}
		inboxDir := filepath.Join(sessionDir, "inbox", "worker")
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			t.Errorf("MkdirAll inboxDir: %v", err)
			return
		}
		if err := os.Rename(postPath, filepath.Join(inboxDir, request.Filename)); err != nil {
			t.Errorf("Rename post to inbox: %v", err)
		}
		if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
			Filename:  request.Filename,
		}); err != nil {
			t.Errorf("WriteDaemonSubmitResponse: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-send-submit",
			"--to", "worker",
			"--body", "hello through submit",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitSend {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitSend)
	}
	if !strings.Contains(request.Filename, "-to-worker.md") {
		t.Fatalf("request filename missing recipient: %q", request.Filename)
	}
	if !strings.Contains(request.Content, "hello through submit") {
		t.Fatalf("request content missing body:\n%s", request.Content)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != request.Filename {
		t.Fatalf("payload.Sent = %q, want %q", payload.Sent, request.Filename)
	}
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.ContextID != "ctx-send-submit" {
		t.Fatalf("payload.ContextID = %q, want ctx-send-submit", payload.ContextID)
	}
	if payload.Session != "test-session" {
		t.Fatalf("payload.Session = %q, want test-session", payload.Session)
	}
	if payload.From != "messenger" {
		t.Fatalf("payload.From = %q, want messenger", payload.From)
	}
	if payload.To != "worker" {
		t.Fatalf("payload.To = %q, want worker", payload.To)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
	postEntries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err == nil && len(postEntries) != 0 {
		t.Fatalf("direct post write bypassed daemon submit: found %d post entries", len(postEntries))
	}
}

func TestRunSendMessage_UsesDaemonSubmitForOwnedSessionInLegacyMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-submit-legacy", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, "ctx-send-submit-legacy", "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	serveDone := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		requestsDir := projection.DaemonSubmitRequestsDir(sessionDir)
		for {
			entries, err := os.ReadDir(requestsDir)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
						continue
					}
					requestPath := filepath.Join(requestsDir, entry.Name())
					request, readErr := projection.ReadDaemonSubmitRequest(requestPath)
					if readErr != nil {
						serveDone <- fmt.Errorf("ReadDaemonSubmitRequest(%s): %w", requestPath, readErr)
						return
					}
					requestSeen <- request
					postDir := filepath.Join(sessionDir, "post")
					if err := os.MkdirAll(postDir, 0o700); err != nil {
						serveDone <- fmt.Errorf("MkdirAll postDir: %w", err)
						return
					}
					postPath := filepath.Join(postDir, request.Filename)
					if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
						serveDone <- fmt.Errorf("WriteFile postPath: %w", err)
						return
					}
					if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
						serveDone <- fmt.Errorf("Remove requestPath: %w", err)
						return
					}
					inboxDir := filepath.Join(sessionDir, "inbox", "worker")
					if err := os.MkdirAll(inboxDir, 0o700); err != nil {
						serveDone <- fmt.Errorf("MkdirAll inboxDir: %w", err)
						return
					}
					if err := os.Rename(postPath, filepath.Join(inboxDir, request.Filename)); err != nil {
						serveDone <- fmt.Errorf("Rename post to inbox: %w", err)
						return
					}
					if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
						RequestID: request.RequestID,
						Command:   request.Command,
						HandledAt: time.Now().UTC().Format(time.RFC3339),
						Filename:  request.Filename,
					}); err != nil {
						serveDone <- fmt.Errorf("WriteDaemonSubmitResponse: %w", err)
						return
					}
					serveDone <- nil
					return
				}
			}
			if time.Now().After(deadline) {
				serveDone <- fmt.Errorf("timed out waiting for daemon submit request in %s", requestsDir)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-send-submit-legacy",
			"--to", "worker",
			"--body", "hello through submit in legacy mode",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	if serveErr := <-serveDone; serveErr != nil {
		t.Fatal(serveErr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitSend {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitSend)
	}
	if !strings.Contains(request.Content, "hello through submit in legacy mode") {
		t.Fatalf("request content missing body:\n%s", request.Content)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.Session != "test-session" {
		t.Fatalf("payload.Session = %q, want test-session", payload.Session)
	}
	if payload.ContextID != "ctx-send-submit-legacy" {
		t.Fatalf("payload.ContextID = %q, want ctx-send-submit-legacy", payload.ContextID)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
	postEntries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err == nil && len(postEntries) != 0 {
		t.Fatalf("direct post write bypassed daemon submit: found %d post entries", len(postEntries))
	}
}

func TestRunSendMessage_DefaultJSONUsesDaemonSubmitWhenDaemonOwnsSession(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-submit-json", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, "ctx-send-submit-json", "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	go func() {
		requestPath, request := awaitDaemonSubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		postDir := filepath.Join(sessionDir, "post")
		if err := os.MkdirAll(postDir, 0o700); err != nil {
			t.Errorf("MkdirAll postDir: %v", err)
			return
		}
		postPath := filepath.Join(postDir, request.Filename)
		if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
			t.Errorf("WriteFile postPath: %v", err)
			return
		}
		if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("Remove requestPath: %v", err)
		}
		inboxDir := filepath.Join(sessionDir, "inbox", "worker")
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			t.Errorf("MkdirAll inboxDir: %v", err)
			return
		}
		if err := os.Rename(postPath, filepath.Join(inboxDir, request.Filename)); err != nil {
			t.Errorf("Rename post to inbox: %v", err)
		}
		if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
			Filename:  request.Filename,
		}); err != nil {
			t.Errorf("WriteDaemonSubmitResponse: %v", err)
		}
	}()

	replyTo := "20260503-090000-sabcd-r1234-from-worker-to-messenger.md"
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-send-submit-json",
			"--to", "worker",
			"--body", "hello through submit json",
			"--reply-required",
			"--reply-to", replyTo,
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitSend {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitSend)
	}
	if !strings.Contains(request.Filename, "-to-worker.md") {
		t.Fatalf("request filename missing recipient: %q", request.Filename)
	}
	if !strings.Contains(request.Content, "hello through submit json") {
		t.Fatalf("request content missing body:\n%s", request.Content)
	}
	for _, want := range []string{
		"replyPolicy: required",
		"replyTo: " + replyTo,
	} {
		if !strings.Contains(request.Content, want) {
			t.Fatalf("request content missing %q:\n%s", want, request.Content)
		}
	}
	assertNoGeneratedReplyPolicyMarker(t, request.Content, "daemon submit request content")
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != request.Filename {
		t.Fatalf("payload.Sent = %q, want %q", payload.Sent, request.Filename)
	}
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.ContextID != "ctx-send-submit-json" {
		t.Fatalf("payload.ContextID = %q, want ctx-send-submit-json", payload.ContextID)
	}
	if payload.Session != "test-session" {
		t.Fatalf("payload.Session = %q, want test-session", payload.Session)
	}
	if payload.From != "messenger" {
		t.Fatalf("payload.From = %q, want messenger", payload.From)
	}
	if payload.To != "worker" {
		t.Fatalf("payload.To = %q, want worker", payload.To)
	}
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.ReplyTo != replyTo {
		t.Fatalf("payload.ReplyTo = %q, want %q", payload.ReplyTo, replyTo)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
	postEntries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err == nil && len(postEntries) != 0 {
		t.Fatalf("direct post write bypassed daemon submit: found %d post entries", len(postEntries))
	}
}

func TestRunSendMessage_DaemonSubmitResolvesGeneratedReplyPolicyMarker(t *testing.T) {
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
  messageType: status_request
---

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

	sessionDir := filepath.Join(tmpDir, "ctx-send-submit-marker-resolution", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, "ctx-send-submit-marker-resolution", "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	go func() {
		requestPath, request := awaitDaemonSubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		postDir := filepath.Join(sessionDir, "post")
		if err := os.MkdirAll(postDir, 0o700); err != nil {
			t.Errorf("MkdirAll postDir: %v", err)
			return
		}
		postPath := filepath.Join(postDir, request.Filename)
		if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
			t.Errorf("WriteFile postPath: %v", err)
			return
		}
		if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("Remove requestPath: %v", err)
		}
		inboxDir := filepath.Join(sessionDir, "inbox", "worker")
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			t.Errorf("MkdirAll inboxDir: %v", err)
			return
		}
		if err := os.Rename(postPath, filepath.Join(inboxDir, request.Filename)); err != nil {
			t.Errorf("Rename post to inbox: %v", err)
		}
		if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
			Filename:  request.Filename,
		}); err != nil {
			t.Errorf("WriteDaemonSubmitResponse: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendMessage([]string{
			"--config", configPath,
			"--context-id", "ctx-send-submit-marker-resolution",
			"--to", "worker",
			"--body", "current status?",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitSend {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitSend)
	}
	for _, want := range []string{
		"messageType: status_request",
		"replyPolicy: required",
	} {
		if !strings.Contains(request.Content, want) {
			t.Fatalf("request content missing %q:\n%s", want, request.Content)
		}
	}
	assertNoGeneratedReplyPolicyMarker(t, request.Content, "daemon submit request content")
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != request.Filename {
		t.Fatalf("payload.Sent = %q, want %q", payload.Sent, request.Filename)
	}
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
}

func TestPerformCLINotification_SkippedWhenPaneEmpty(t *testing.T) {
	var called bool
	fn := func(_ string, _ string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
		called = true
		return nil
	}
	status := performCLINotification("", "msg", 0, 0, 1, true, 0, 0, fn)
	if status != cliNotifySkipped {
		t.Errorf("status = %q, want %q", status, cliNotifySkipped)
	}
	if called {
		t.Error("sendToPaneFunc should not be called when paneID is empty")
	}
}

func TestPerformCLINotification_OKOnSuccess(t *testing.T) {
	var gotPaneID string
	fn := func(paneID string, _ string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
		gotPaneID = paneID
		return nil
	}
	status := performCLINotification("%1", "msg", 0, 0, 1, true, 0, 0, fn)
	if status != cliNotifyOK {
		t.Errorf("status = %q, want %q", status, cliNotifyOK)
	}
	if gotPaneID != "%1" {
		t.Errorf("gotPaneID = %q, want %%1", gotPaneID)
	}
}

func TestPerformCLINotification_FailedOnError(t *testing.T) {
	fn := func(_ string, _ string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
		return fmt.Errorf("tmux error")
	}
	status := performCLINotification("%gone", "msg", 0, 0, 1, true, 0, 0, fn)
	if status != cliNotifyFailed {
		t.Errorf("status = %q, want %q", status, cliNotifyFailed)
	}
}

func TestWriteSendResult_ProcessedWithNotifyOK(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return writeSendResult("test.md", sendStatusProcessed, cliNotifyOK)
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != "test.md" {
		t.Errorf("payload.Sent = %q, want test.md", payload.Sent)
	}
	if payload.Notify != "OK" {
		t.Errorf("payload.Notify = %q, want OK", payload.Notify)
	}
}

func TestWriteSendResult_ProcessedWithNotifyFailed(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return writeSendResult("test.md", sendStatusProcessed, cliNotifyFailed)
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Notify != "FAILED" {
		t.Errorf("payload.Notify = %q, want FAILED", payload.Notify)
	}
}

func TestWriteSendResult_ProcessedWithNotifySkipped(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return writeSendResult("test.md", sendStatusProcessed, cliNotifySkipped)
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Notify != "SKIPPED" {
		t.Errorf("payload.Notify = %q, want SKIPPED", payload.Notify)
	}
}

func TestWriteSendResult_QueuedHasNoNotifySuffix(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return writeSendResult("test.md", sendStatusQueued, cliNotifyNone)
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Status != string(sendStatusQueued) {
		t.Errorf("payload.Status = %q, want %q", payload.Status, sendStatusQueued)
	}
	if payload.Notify != "" {
		t.Errorf("payload.Notify = %q, want empty", payload.Notify)
	}
}

func decodeSendOutputForTest(t *testing.T, stdout string) sendOutput {
	t.Helper()
	assertNoGeneratedReplyPolicyMarker(t, stdout, "send stdout")
	var payload sendOutput
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if strings.Contains(stdout, "Sent: ") || strings.Contains(stdout, "Queued: ") {
		t.Fatalf("stdout unexpectedly used human output: %q", stdout)
	}
	return payload
}

func assertNoGeneratedReplyPolicyMarker(t *testing.T, value, label string) {
	t.Helper()
	if strings.Contains(value, "__TMUX_A2A_POSTMAN_GENERATED_REPLY_POLICY_") {
		t.Fatalf("%s leaked generated reply policy marker:\n%s", label, value)
	}
	if strings.Contains(value, "__TMUX_A2A_POSTMAN_GENERATED_OBLIGATION_ID_") {
		t.Fatalf("%s leaked generated obligation id marker:\n%s", label, value)
	}
}
