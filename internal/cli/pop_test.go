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
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	assertPopPayloadArchive(t, payload, content)
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
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	assertPopPayloadArchive(t, payload, content)
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
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	archived := readPopArchiveForTest(t, payload)
	if strings.Contains(archived, "Next steps: Reply with tmux-a2a-postman send --to review-session:orchestrator --body \"<your message>\"") {
		t.Fatalf("archived content still contains hard-coded next steps reply hint:\n%s", archived)
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
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	assertPopPayloadArchive(t, payload, content)
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
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	if payload.MarkdownPath == "" {
		t.Fatal("payload.MarkdownPath is empty")
	}
}

func TestRunPop_TildeShortensHomeMarkdownPathAndKeepsAbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	baseDir := filepath.Join(homeDir, ".local", "state", "tmux-a2a-postman")
	t.Setenv("HOME", homeDir)
	installFakeTmuxForCLI(t, baseDir, "test-session", "worker")

	contextID := "ctx-pop-home-path"
	messageFile := "20260415-010104-from-orchestrator-to-worker.md"
	inboxDir := filepath.Join(baseDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	content := messageFixture("orchestrator", "worker", "Home path payload")
	if err := os.WriteFile(filepath.Join(inboxDir, messageFile), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	wantDisplayPath := filepath.Join("~", ".local", "state", "tmux-a2a-postman", contextID, "test-session", "read", messageFile)
	if payload.MarkdownPath != wantDisplayPath {
		t.Fatalf("payload.MarkdownPath = %q, want %q", payload.MarkdownPath, wantDisplayPath)
	}
	wantAbsolutePath := filepath.Join(baseDir, contextID, "test-session", "read", messageFile)
	if payload.MarkdownAbsolutePath != wantAbsolutePath {
		t.Fatalf("payload.MarkdownAbsolutePath = %q, want %q", payload.MarkdownAbsolutePath, wantAbsolutePath)
	}
	assertPopPayloadArchive(t, payload, content)
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
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-15T01:01:03Z\n  replyPolicy: required\n  reply_to: 20260415-010000-from-worker-to-orchestrator.md\n---\n\nReview this\n"
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
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	params := popFrontmatterParamsForTest(t, payload)
	if params["replyPolicy"] != "required" {
		t.Fatalf("frontmatter params replyPolicy = %#v, want required", params["replyPolicy"])
	}
}

func TestRunPop_RequiresCompleteArchivedBodyReadBeforeHandlingDecision(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-body-read-required"
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260512-010103-from-postman-to-worker.md"
	longFiller := strings.Repeat("filler line that could consume a bounded stdout window\n", 256)
	lateInstruction := "LATE RECIPIENT INSTRUCTION: handle this message only after the full archived body is available."
	content := "---\nparams:\n" +
		"  from: postman\n" +
		"  to: worker\n" +
		"  messageId: " + filename + "\n" +
		"  replyPolicy: none\n" +
		"  messageType: ping\n" +
		"  timestamp: 2026-05-12T01:01:03Z\n" +
		"---\n\n" +
		"# Ping\n\n" +
		longFiller +
		lateInstruction + "\n"
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
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	if strings.Contains(stdout, lateInstruction) {
		t.Fatalf("pop JSON leaked late body instruction instead of requiring archive read:\n%s", stdout)
	}
	if !payload.ArchivedBodyReadRequired {
		t.Fatal("payload.ArchivedBodyReadRequired = false, want true")
	}
	for _, want := range []string{
		"complete archived Markdown body",
		"before any handling",
		"routing",
		"reply",
		"status decision",
		"no-action or no-op decision",
		"messageType",
		"replyPolicy",
		"truncated command output is not a complete read",
	} {
		if !strings.Contains(payload.ArchivedBodyReadInstruction, want) {
			t.Fatalf("ArchivedBodyReadInstruction missing %q: %q", want, payload.ArchivedBodyReadInstruction)
		}
	}
	if payload.ReplyPolicy != "none" {
		t.Fatalf("ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}
	params := popFrontmatterParamsForTest(t, payload)
	if params["messageType"] != "ping" {
		t.Fatalf("frontmatter params messageType = %#v, want ping", params["messageType"])
	}
	archived := readPopArchiveForTest(t, payload)
	if !strings.Contains(archived, lateInstruction) {
		t.Fatalf("archived body missing late instruction:\n%s", archived)
	}
	if archived != content {
		t.Fatalf("archived content changed:\n got %q\nwant %q", archived, content)
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
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	if payload.MarkdownPath != filepath.Join(sessionDir, "read", filename) {
		t.Fatalf("payload.MarkdownPath = %q, want inferred daemon read path", payload.MarkdownPath)
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

func TestRunPop_ReportsMessageIDAndExactInputRequestFields(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")
	contextID := "ctx-pop-exact-input-request"
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
		"  input_request_id: ireq_123\n" +
		"  fills_input_request_id: ireq_prev\n" +
		"  input_request_set_id: ireqset_1\n" +
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
	if payload.MessageID != messageFile {
		t.Fatalf("payload.MessageID = %q, want %q", payload.MessageID, messageFile)
	}
	if payload.InputRequestID != "ireq_123" {
		t.Fatalf("payload.InputRequestID = %q, want ireq_123", payload.InputRequestID)
	}
	if payload.FillsInputRequestID != "ireq_prev" {
		t.Fatalf("payload.FillsInputRequestID = %q, want ireq_prev", payload.FillsInputRequestID)
	}
	if payload.InputRequestSetID != "ireqset_1" {
		t.Fatalf("payload.InputRequestSetID = %q, want ireqset_1", payload.InputRequestSetID)
	}
	if payload.BranchID != "branch_1" || payload.CompletionRule != "all" {
		t.Fatalf("group fields = %q/%q, want branch_1/all", payload.BranchID, payload.CompletionRule)
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

func assertPopPayloadOmitsInlineMarkdown(t *testing.T, stdout string) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	for _, removed := range []string{"id", "body", "content", "body_available", "body_reference", "body_bytes", "body_omitted_reason"} {
		if _, ok := raw[removed]; ok {
			t.Fatalf("pop output still includes %q: %s", removed, stdout)
		}
	}
}

func assertPopPayloadArchive(t *testing.T, payload popMessageOutput, want string) {
	t.Helper()
	if got := readPopArchiveForTest(t, payload); got != want {
		t.Fatalf("archived content changed:\n got %q\nwant %q", got, want)
	}
}

func readPopArchiveForTest(t *testing.T, payload popMessageOutput) string {
	t.Helper()
	path := payload.MarkdownPath
	if payload.MarkdownAbsolutePath != "" {
		path = payload.MarkdownAbsolutePath
	}
	if path == "" {
		t.Fatal("payload.MarkdownPath and payload.MarkdownAbsolutePath are empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile pop archive path: %v", err)
	}
	return string(data)
}

func popFrontmatterParamsForTest(t *testing.T, payload popMessageOutput) map[string]any {
	t.Helper()
	params, ok := payload.Frontmatter["params"].(map[string]any)
	if !ok {
		t.Fatalf("payload.Frontmatter params = %#v, want object", payload.Frontmatter["params"])
	}
	return params
}
