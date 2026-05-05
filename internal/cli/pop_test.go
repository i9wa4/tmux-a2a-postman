package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRunPop_ContextIDFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	stdout, _, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", "test-ctx-123"})
	})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("--context-id not defined in RunPop: %v", err)
	}
	if stdout != "" {
		var payload popEmptyOutput
		if decodeErr := json.Unmarshal([]byte(stdout), &payload); decodeErr != nil {
			t.Fatalf("json.Unmarshal(%q): %v", stdout, decodeErr)
		}
	}
}

func TestRunPop_HelpHidesContextIDFlag(t *testing.T) {
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"-h"})
	})
	if err == nil {
		t.Fatal("RunPop(-h) = nil, want help error")
	}
	if !strings.Contains(err.Error(), "flag: help requested") {
		t.Fatalf("RunPop(-h) error = %v, want help requested", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage of pop:") {
		t.Fatalf("stderr missing help header: %q", stderr)
	}
	if strings.Contains(stderr, "--context-id") {
		t.Fatalf("stderr still exposes hidden context override: %q", stderr)
	}
	for _, removed := range []string{"--config", "--peek", "--file", "--params"} {
		if strings.Contains(stderr, removed) {
			t.Fatalf("stderr still exposes removed/hidden flag %s: %q", removed, stderr)
		}
	}
}

func TestRunPop_RequeuedMessagePreservesOriginalPayload(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")
	contextID := "ctx-pop-requeued"
	messageFile := "20260328-101503-from-orchestrator-to-worker.md"
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	readDir := filepath.Join(tmpDir, contextID, "test-session", "read")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}

	content := messageFixture("orchestrator", "worker", "Requeued original payload")
	inboxPath := filepath.Join(inboxDir, messageFile)
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.Content != content {
		t.Fatalf("payload.Content changed:\n got %q\nwant %q", payload.Content, content)
	}
	if payload.Body != "Requeued original payload" {
		t.Fatalf("payload.Body = %q, want original payload", payload.Body)
	}
	readPath := filepath.Join(readDir, messageFile)
	archived, err := os.ReadFile(readPath)
	if err != nil {
		t.Fatalf("ReadFile read: %v", err)
	}
	if string(archived) != content {
		t.Fatalf("archived content changed:\n got %q\nwant %q", archived, content)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("inbox file still present or wrong error: %v", err)
	}
}

func TestRunPop_RestoredArchivedMessagePreservesCanonicalReadTracking(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")
	contextID := "ctx-pop-restored"
	messageFile := "20260328-101504-from-orchestrator-to-worker.md"
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	readDir := filepath.Join(tmpDir, contextID, "test-session", "read")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}

	content := messageFixture("orchestrator", "worker", "Archived original payload")
	readPath := filepath.Join(readDir, messageFile)
	inboxPath := filepath.Join(inboxDir, messageFile)
	if err := os.WriteFile(readPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile read: %v", err)
	}
	canonicalReadTime := time.Date(2026, time.March, 28, 10, 15, 4, 0, time.UTC)
	if err := os.Chtimes(readPath, canonicalReadTime, canonicalReadTime); err != nil {
		t.Fatalf("Chtimes read: %v", err)
	}
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}
	restoredCopyTime := canonicalReadTime.Add(2 * time.Minute)
	if err := os.Chtimes(inboxPath, restoredCopyTime, restoredCopyTime); err != nil {
		t.Fatalf("Chtimes inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.Content != content {
		t.Fatalf("payload.Content changed:\n got %q\nwant %q", payload.Content, content)
	}
	if payload.Body != "Archived original payload" {
		t.Fatalf("payload.Body = %q, want archived payload", payload.Body)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("restored inbox copy still present or wrong error: %v", err)
	}
	readInfo, err := os.Stat(readPath)
	if err != nil {
		t.Fatalf("Stat read: %v", err)
	}
	if !readInfo.ModTime().Equal(canonicalReadTime) {
		t.Fatalf("canonical read modtime changed: got %s want %s", readInfo.ModTime().UTC().Format(time.RFC3339), canonicalReadTime.Format(time.RFC3339))
	}
	archived, err := os.ReadFile(readPath)
	if err != nil {
		t.Fatalf("ReadFile read: %v", err)
	}
	if string(archived) != content {
		t.Fatalf("read content changed:\n got %q\nwant %q", archived, content)
	}
}

func TestRunPop_DoesNotAppendHardCodedReplyHint(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")
	contextID := "ctx-pop-no-hardcoded-footer"
	messageFile := "20260407-220000-from-review-session:orchestrator-to-worker.md"
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	content := messageFixture("review-session:orchestrator", "worker", "Cross-session payload")
	inboxPath := filepath.Join(inboxDir, messageFile)
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.Body != "Cross-session payload" {
		t.Fatalf("payload.Body = %q, want popped payload", payload.Body)
	}
	if strings.Contains(payload.Content, "Next steps: Reply with tmux-a2a-postman send --to review-session:orchestrator --body \"<your message>\"") {
		t.Fatalf("payload.Content still contains hard-coded next steps reply hint:\n%s", payload.Content)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, contextID, "test-session", "read", messageFile)); err != nil {
		t.Fatalf("archived file missing: %v", err)
	}
}

func TestRunPop_PrintsOnlyStoredMessageOnDefaultPop(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-stored-message"
	configPath := filepath.Join(tmpDir, "postman.toml")
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"orchestrator --- worker\"]\n\n[orchestrator]\nrole = \"orchestrator\"\n\n[worker]\nrole = \"worker\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	messageFile := "20260415-010101-from-orchestrator-to-worker.md"
	content := messageFixture("orchestrator", "worker", "Primary payload")
	if err := os.WriteFile(filepath.Join(inboxDir, messageFile), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--config", configPath, "--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.Content != content {
		t.Fatalf("payload.Content changed:\n got %q\nwant %q", payload.Content, content)
	}
	if payload.Body != "Primary payload" {
		t.Fatalf("payload.Body = %q, want Primary payload", payload.Body)
	}
	if strings.Contains(stdout, "Local Runtime Context") {
		t.Fatalf("stdout unexpectedly rendered extra runtime block:\n%s", stdout)
	}
}

func TestRunPop_PrintsJSONMessagePayloadByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-json-payload"
	configPath := filepath.Join(tmpDir, "postman.toml")
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"orchestrator --- worker\"]\n\n[orchestrator]\nrole = \"orchestrator\"\n\n[worker]\nrole = \"worker\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "20260415-010102-from-orchestrator-to-worker.md"), []byte(messageFixture("orchestrator", "worker", "JSON payload")), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--config", configPath, "--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	if strings.Contains(stdout, "Local Runtime Context") {
		t.Fatalf("stdout leaked runtime block into json mode:\n%s", stdout)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.Body != "JSON payload" {
		t.Fatalf("payload.Body = %q, want JSON payload", payload.Body)
	}
}

func TestRunPop_ParsesEnvelopeMetadataForJSONPayload(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-envelope-metadata"
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260415-010103-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-15T01:01:03Z\n  reply_obligation: required\n  reply_to: 20260415-010000-from-worker-to-orchestrator.md\n---\n\nReview this\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.From != "orchestrator" || payload.To != "worker" {
		t.Fatalf("from/to = %q/%q, want orchestrator/worker", payload.From, payload.To)
	}
	if payload.Timestamp != "2026-04-15T01:01:03Z" {
		t.Fatalf("Timestamp = %q, want envelope timestamp", payload.Timestamp)
	}
	if payload.ReplyPolicy != "required" {
		t.Fatalf("ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.ReplyTo != "20260415-010000-from-worker-to-orchestrator.md" {
		t.Fatalf("ReplyTo = %q, want reply_to alias value", payload.ReplyTo)
	}
	if payload.Body != "Review this" {
		t.Fatalf("Body = %q, want Review this", payload.Body)
	}
}

func TestRunPop_UsesDaemonSubmitWhenDaemonOwnsSession(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-submit"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	configPath := filepath.Join(tmpDir, "postman.toml")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"orchestrator --- worker\"]\n\n[orchestrator]\nrole = \"orchestrator\"\n\n[worker]\nrole = \"worker\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, contextID, "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}
	filename := "20260414-032800-from-orchestrator-to-worker.md"
	originalInboxPath := filepath.Join(inboxDir, filename)
	if err := os.WriteFile(originalInboxPath, []byte(messageFixture("orchestrator", "worker", "original unread payload")), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	go func() {
		requestPath, request := awaitDaemonSubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID:    request.RequestID,
			Command:      request.Command,
			HandledAt:    time.Now().UTC().Format(time.RFC3339),
			Filename:     filename,
			Content:      messageFixture("orchestrator", "worker", "daemon submit pop payload"),
			UnreadBefore: 1,
		}); err != nil {
			t.Errorf("WriteDaemonSubmitResponse: %v", err)
		}
		if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("Remove requestPath: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--config", configPath, "--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitPop {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitPop)
	}
	if request.Node != "worker" {
		t.Fatalf("request.Node = %q, want %q", request.Node, "worker")
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.Body != "daemon submit pop payload" {
		t.Fatalf("payload.Body = %q, want daemon submit payload", payload.Body)
	}
	if strings.Contains(stdout, "## Local Runtime Context") {
		t.Fatalf("stdout unexpectedly rendered runtime context block:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(originalInboxPath); err != nil {
		t.Fatalf("daemon submit path should not mutate inbox directly in CLI test: %v", err)
	}
}

func TestRunPop_ReportsMessageIDAndExactObligationFields(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")
	contextID := "ctx-pop-exact-obligation"
	messageFile := "20260328-101505-from-orchestrator-to-worker.md"
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  messageId: " + messageFile + "\n" +
		"  replyPolicy: required\n" +
		"  replyTo: previous.md\n" +
		"  obligation_id: obl_123\n" +
		"  satisfies_obligation_id: obl_prev\n" +
		"  obligation_group_id: group_1\n" +
		"  branch_id: branch_1\n" +
		"  completion_rule: all\n" +
		"  timestamp: 2026-03-28T10:15:05Z\n" +
		"---\n\nExact payload\n"
	inboxPath := filepath.Join(inboxDir, messageFile)
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.ID != messageFile {
		t.Fatalf("payload.ID = %q, want %q", payload.ID, messageFile)
	}
	if payload.MessageID != messageFile {
		t.Fatalf("payload.MessageID = %q, want %q", payload.MessageID, messageFile)
	}
	if payload.ObligationID != "obl_123" {
		t.Fatalf("payload.ObligationID = %q, want obl_123", payload.ObligationID)
	}
	if payload.SatisfiesObligationID != "obl_prev" {
		t.Fatalf("payload.SatisfiesObligationID = %q, want obl_prev", payload.SatisfiesObligationID)
	}
	if payload.ObligationGroupID != "group_1" || payload.BranchID != "branch_1" || payload.CompletionRule != "all" {
		t.Fatalf("group fields = %q/%q/%q, want group_1/branch_1/all", payload.ObligationGroupID, payload.BranchID, payload.CompletionRule)
	}
}

func decodePopMessageOutputForTest(t *testing.T, stdout string) popMessageOutput {
	t.Helper()
	var payload popMessageOutput
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if payload.Status != "message" {
		t.Fatalf("payload.Status = %q, want message", payload.Status)
	}
	return payload
}
