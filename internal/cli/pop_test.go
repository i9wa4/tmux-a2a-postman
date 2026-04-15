package cli

import (
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
	err := RunPop([]string{"--context-id", "test-ctx-123"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("--context-id not defined in RunPop: %v", err)
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
	if !strings.Contains(stdout, "Requeued original payload") {
		t.Fatalf("stdout %q does not contain original payload", stdout)
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
	if !strings.Contains(stdout, "Archived original payload") {
		t.Fatalf("stdout %q does not contain archived payload", stdout)
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

func TestRunPop_FileReadsAcrossContextsNonDestructively(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	currentContextID := "ctx-pop-current"
	otherContextID := "ctx-pop-other"
	filename := "20260330-101505-from-orchestrator-to-worker.md"
	content := messageFixture("orchestrator", "worker", "Cross-context payload")

	currentInboxDir := filepath.Join(tmpDir, currentContextID, "test-session", "inbox", "worker")
	otherInboxDir := filepath.Join(tmpDir, otherContextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(currentInboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll current inbox: %v", err)
	}
	if err := os.MkdirAll(otherInboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll other inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, currentContextID, "test-session", "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}

	targetPath := filepath.Join(otherInboxDir, filename)
	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile target inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--file", filename})
	})
	if err != nil {
		t.Fatalf("RunPop --file: %v\nstderr=%s", err, stderr)
	}
	if stdout != content {
		t.Fatalf("stdout changed payload:\n got %q\nwant %q", stdout, content)
	}

	remaining, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target inbox: %v", err)
	}
	if string(remaining) != content {
		t.Fatalf("target inbox content changed:\n got %q\nwant %q", remaining, content)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, otherContextID, "test-session", "read", filename)); !os.IsNotExist(err) {
		t.Fatalf("unexpected archived copy or wrong error: %v", err)
	}
}

func TestRunPop_FileHonorsExplicitContextID(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	currentContextID := "ctx-pop-bound"
	otherContextID := "ctx-pop-leak"
	filename := "20260330-101506-from-orchestrator-to-worker.md"
	content := messageFixture("orchestrator", "worker", "Leaked payload")

	currentInboxDir := filepath.Join(tmpDir, currentContextID, "test-session", "inbox", "worker")
	otherInboxDir := filepath.Join(tmpDir, otherContextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(currentInboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll current inbox: %v", err)
	}
	if err := os.MkdirAll(otherInboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll other inbox: %v", err)
	}

	targetPath := filepath.Join(otherInboxDir, filename)
	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile target inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", currentContextID, "--file", filename})
	})
	if err == nil {
		t.Fatal("RunPop --context-id --file unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "not found in any inbox/ directory") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout leaked payload: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr unexpectedly wrote output: %q", stderr)
	}

	remaining, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target inbox: %v", err)
	}
	if string(remaining) != content {
		t.Fatalf("target inbox content changed:\n got %q\nwant %q", remaining, content)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, otherContextID, "test-session", "read", filename)); !os.IsNotExist(err) {
		t.Fatalf("unexpected archived copy or wrong error: %v", err)
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
	if !strings.Contains(stdout, "Cross-session payload") {
		t.Fatalf("stdout %q does not contain popped payload", stdout)
	}
	if strings.Contains(stdout, "Next steps: Reply with tmux-a2a-postman send --to review-session:orchestrator --body \"<your message>\"") {
		t.Fatalf("stdout still contains hard-coded next steps reply hint:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Remaining: 0 unread") {
		t.Fatalf("stderr missing unread count update:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, contextID, "test-session", "read", messageFile)); err != nil {
		t.Fatalf("archived file missing: %v", err)
	}
}

func TestRunPop_RendersConfiguredReadContextOnDefaultPop(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-read-context"
	configPath := filepath.Join(tmpDir, "postman.toml")
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"orchestrator -- worker\"]\nread_context_mode = \"pieces\"\nread_context_pieces = [\"node\", \"cwd\"]\nread_context_heading = \"Local Runtime Context\"\n\n[orchestrator]\nrole = \"orchestrator\"\n\n[worker]\nrole = \"worker\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	messageFile := "20260415-010101-from-orchestrator-to-worker.md"
	if err := os.WriteFile(filepath.Join(inboxDir, messageFile), []byte(messageFixture("orchestrator", "worker", "Primary payload")), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--config", configPath, "--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "Primary payload") {
		t.Fatalf("stdout missing original payload:\n%s", stdout)
	}
	if !strings.Contains(stdout, "## Local Runtime Context") {
		t.Fatalf("stdout missing read-context heading:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- node: worker") {
		t.Fatalf("stdout missing node piece:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- cwd: "+cwd) {
		t.Fatalf("stdout missing cwd piece:\n%s", stdout)
	}
}

func TestRunPop_JSONDoesNotRenderConfiguredReadContext(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-read-context-json"
	configPath := filepath.Join(tmpDir, "postman.toml")
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"orchestrator -- worker\"]\nread_context_mode = \"pieces\"\nread_context_pieces = [\"node\", \"cwd\"]\n\n[orchestrator]\nrole = \"orchestrator\"\n\n[worker]\nrole = \"worker\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "20260415-010102-from-orchestrator-to-worker.md"), []byte(messageFixture("orchestrator", "worker", "JSON payload")), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--config", configPath, "--context-id", contextID, "--json"})
	})
	if err != nil {
		t.Fatalf("RunPop --json: %v\nstderr=%s", err, stderr)
	}
	if strings.Contains(stdout, "Local Runtime Context") {
		t.Fatalf("stdout leaked read-context block into json mode:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"body":"JSON payload"`) {
		t.Fatalf("stdout missing json payload body:\n%s", stdout)
	}
}

func TestRunPop_UsesCompatibilitySubmitWhenDaemonOwnsSession(t *testing.T) {
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
		[]byte("[postman]\nedges = [\"orchestrator -- worker\"]\njournal_health_cutover_enabled = true\njournal_compatibility_cutover_enabled = true\nread_context_mode = \"pieces\"\nread_context_pieces = [\"node\"]\n\n[orchestrator]\nrole = \"orchestrator\"\n\n[worker]\nrole = \"worker\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	mode, err := config.ResolveJournalCutoverMode(cfg)
	if err != nil {
		t.Fatalf("ResolveJournalCutoverMode: %v", err)
	}
	if mode != config.JournalCutoverCompatibilityFirst {
		t.Fatalf("cutover mode = %q, want %q", mode, config.JournalCutoverCompatibilityFirst)
	}
	if !config.ContextOwnsSession(tmpDir, contextID, "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}
	filename := "20260414-032800-from-orchestrator-to-worker.md"
	originalInboxPath := filepath.Join(inboxDir, filename)
	if err := os.WriteFile(originalInboxPath, []byte(messageFixture("orchestrator", "worker", "original unread payload")), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	requestSeen := make(chan projection.CompatibilitySubmitRequest, 1)
	go func() {
		requestPath, request := awaitCompatibilitySubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		if _, err := projection.WriteCompatibilitySubmitResponse(sessionDir, projection.CompatibilitySubmitResponse{
			RequestID:    request.RequestID,
			Command:      request.Command,
			HandledAt:    time.Now().UTC().Format(time.RFC3339),
			Filename:     filename,
			Content:      messageFixture("orchestrator", "worker", "compatibility pop payload"),
			UnreadBefore: 1,
		}); err != nil {
			t.Errorf("WriteCompatibilitySubmitResponse: %v", err)
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
	if request.Command != projection.CompatibilitySubmitPop {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.CompatibilitySubmitPop)
	}
	if request.Node != "worker" {
		t.Fatalf("request.Node = %q, want %q", request.Node, "worker")
	}
	if !strings.Contains(stdout, "compatibility pop payload") {
		t.Fatalf("stdout %q does not contain compatibility payload", stdout)
	}
	if !strings.Contains(stdout, "## Local Runtime Context") || !strings.Contains(stdout, "- node: worker") {
		t.Fatalf("stdout missing compatibility-submit read-context block:\n%s", stdout)
	}
	if !strings.Contains(stderr, "[1/1 unread]") || !strings.Contains(stderr, "Remaining: 0 unread") {
		t.Fatalf("stderr missing unread counters:\n%s", stderr)
	}
	if _, err := os.Stat(originalInboxPath); err != nil {
		t.Fatalf("compatibility submit path should not mutate inbox directly in CLI test: %v", err)
	}
}
