package cli

import (
	"bytes"
	"encoding/json"
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
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/runtimecontext"
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

func TestRunPopWithContextWritesJSONToConfiguredStdout(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "ctx-pop-context"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	filename := "20260414-032800-from-orchestrator-to-worker.md"
	inboxPath := filepath.Join(inboxDir, filename)
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.WriteFile(inboxPath, []byte(messageFixture("orchestrator", "worker", "context stdout payload")), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	var stdout bytes.Buffer
	err := runPopWithContext(commandContext{
		stdout: &stdout,
		resolveInboxPath: func(args []string) (string, error) {
			if strings.Join(args, " ") != "--context-id "+contextID {
				t.Fatalf("resolveInboxPath args = %#v", args)
			}
			return inboxDir, nil
		},
		loadConfig: func(path string) (*config.Config, error) {
			if path != "" {
				t.Fatalf("config path = %q, want empty", path)
			}
			return config.DefaultConfig(), nil
		},
		contextOwnsSession: func(baseDir, resolvedContextID, sessionName string) bool {
			if baseDir != tmpDir || resolvedContextID != contextID || sessionName != "test-session" {
				t.Fatalf("ownership args = %q/%q/%q", baseDir, resolvedContextID, sessionName)
			}
			return false
		},
	}, []string{"--context-id", contextID})
	if err != nil {
		t.Fatalf("runPopWithContext: %v", err)
	}
	payload := decodePopMessageOutputForTest(t, stdout.String())
	if payload.MarkdownPath != filepath.Join(sessionDir, "read", filename) {
		t.Fatalf("MarkdownPath = %q, want read path", payload.MarkdownPath)
	}
	if payload.SubmitPath != projection.SubmitPathPost {
		t.Fatalf("SubmitPath = %q, want %q (non-owner direct fallback)", payload.SubmitPath, projection.SubmitPathPost)
	}
	if !strings.Contains(readPopArchiveForTest(t, payload), "context stdout payload") {
		t.Fatalf("archived body missing expected payload")
	}
}

func TestRunPopWithContextUsesDaemonSubmitDependencyWithoutDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "ctx-pop-submit-context"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	filename := "20260414-032800-from-orchestrator-to-worker.md"

	var stdout bytes.Buffer
	var gotRequest projection.DaemonSubmitRequest
	err := runPopWithContext(commandContext{
		stdout: &stdout,
		resolveInboxPath: func(args []string) (string, error) {
			return inboxDir, nil
		},
		loadConfig: func(path string) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
		contextOwnsSession: func(baseDir, resolvedContextID, sessionName string) bool {
			return true
		},
		roundTripDaemonSubmit: func(gotSessionDir string, request projection.DaemonSubmitRequest, timeout time.Duration) (projection.DaemonSubmitResponse, error) {
			if gotSessionDir != sessionDir {
				t.Fatalf("sessionDir = %q, want %q", gotSessionDir, sessionDir)
			}
			gotRequest = request
			return projection.DaemonSubmitResponse{
				Command:      request.Command,
				Filename:     filename,
				Content:      messageFixture("orchestrator", "worker", "daemon dependency payload"),
				UnreadBefore: 1,
			}, nil
		},
	}, []string{"--context-id", contextID})
	if err != nil {
		t.Fatalf("runPopWithContext: %v", err)
	}
	if gotRequest.Command != projection.DaemonSubmitPop || gotRequest.Node != "worker" {
		t.Fatalf("daemon request = %#v, want pop for worker", gotRequest)
	}
	payload := decodePopMessageOutputForTest(t, stdout.String())
	if payload.MarkdownPath != filepath.Join(sessionDir, "read", filename) {
		t.Fatalf("MarkdownPath = %q, want inferred daemon read path", payload.MarkdownPath)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("SubmitPath = %q, want %q (daemon-submit path)", payload.SubmitPath, projection.SubmitPathDaemon)
	}
}

func TestRunPop_EmptyDaemonPopReportsSubmitPath(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "ctx-pop-empty-daemon"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")

	var stdout bytes.Buffer
	err := runPopWithContext(commandContext{
		stdout: &stdout,
		resolveInboxPath: func(args []string) (string, error) {
			return inboxDir, nil
		},
		loadConfig: func(path string) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
		contextOwnsSession: func(baseDir, resolvedContextID, sessionName string) bool {
			return true
		},
		roundTripDaemonSubmit: func(gotSessionDir string, request projection.DaemonSubmitRequest, timeout time.Duration) (projection.DaemonSubmitResponse, error) {
			return projection.DaemonSubmitResponse{
				Command: request.Command,
				Empty:   true,
			}, nil
		},
	}, []string{"--context-id", contextID})
	if err != nil {
		t.Fatalf("runPopWithContext: %v", err)
	}
	var payload popEmptyOutput
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout.String(), err)
	}
	if payload.Status != "empty" {
		t.Fatalf("Status = %q, want empty", payload.Status)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("SubmitPath = %q, want %q (daemon-owned empty pop)", payload.SubmitPath, projection.SubmitPathDaemon)
	}
}

func TestRunPop_EmptyDirectPopReportsSubmitPath(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "ctx-pop-empty-direct"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")

	var stdout bytes.Buffer
	err := runPopWithContext(commandContext{
		stdout: &stdout,
		resolveInboxPath: func(args []string) (string, error) {
			return inboxDir, nil
		},
		loadConfig: func(path string) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
		contextOwnsSession: func(baseDir, resolvedContextID, sessionName string) bool {
			return false
		},
	}, []string{"--context-id", contextID})
	if err != nil {
		t.Fatalf("runPopWithContext: %v", err)
	}
	var payload popEmptyOutput
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout.String(), err)
	}
	if payload.Status != "empty" {
		t.Fatalf("Status = %q, want empty", payload.Status)
	}
	if payload.SubmitPath != projection.SubmitPathPost {
		t.Fatalf("SubmitPath = %q, want %q (non-owner direct fallback empty pop)", payload.SubmitPath, projection.SubmitPathPost)
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

func TestRunPop_IncludesFilesystemSessionDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-session-diagnostics"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	workerInbox := filepath.Join(sessionDir, "inbox", "worker")
	criticInbox := filepath.Join(sessionDir, "inbox", "critic")
	for _, dir := range []string{workerInbox, criticInbox, filepath.Join(sessionDir, "post"), filepath.Join(sessionDir, "dead-letter")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(workerInbox, "20260415-010201-from-orchestrator-to-worker.md"), []byte(messageFixture("orchestrator", "worker", "diagnostics payload")), 0o600); err != nil {
		t.Fatalf("WriteFile worker inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(criticInbox, "20260415-010202-from-worker-to-critic.md"), []byte(messageFixture("worker", "critic", "critic backlog")), 0o600); err != nil {
		t.Fatalf("WriteFile critic inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "post", "20260415-010203-from-worker-to-critic.md"), []byte("post backlog"), 0o600); err != nil {
		t.Fatalf("WriteFile post: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "dead-letter", "20260415-010204-from-worker-to-missing-dl-unknown-recipient.md"), []byte("dead letter"), 0o600); err != nil {
		t.Fatalf("WriteFile dead-letter: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.SessionDiagnostics == nil {
		t.Fatalf("SessionDiagnostics missing: %s", stdout)
	}
	diag := *payload.SessionDiagnostics
	if diag.Source != "filesystem" {
		t.Fatalf("diagnostics source = %q, want filesystem", diag.Source)
	}
	if diag.UnreadInboxCount != 1 || diag.PostCount != 1 || diag.DeadLetterCount != 1 {
		t.Fatalf("queue diagnostics = %#v, want unread=1 post=1 dead-letter=1", diag)
	}
	if diag.ActiveTaskCount != 0 || diag.UnclosedRequiredRequestCount != 0 {
		t.Fatalf("fallback diagnostics inferred task state without projection: %#v", diag)
	}
}

func TestPopSessionDiagnosticsUsesInputRequestProjection(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "ctx-pop-projection-diagnostics"
	sessionName := "test-session"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}

	now := time.Date(2026, time.April, 15, 1, 5, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, contextID, sessionName, 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	content := sessionStatusMessageContent("critic", "worker", "m1.md", map[string]string{
		"replyPolicy":      "required",
		"input_request_id": "ireq_123",
	}, "please reply")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, "m1.md", "critic", "worker", content, now.Add(time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "m1.md", "critic", "worker", content, now.Add(2*time.Second))

	diagnostics := popSessionDiagnosticsForSession(sessionDir)
	if diagnostics == nil {
		t.Fatal("popSessionDiagnosticsForSession returned nil")
		return
	}
	diag := *diagnostics
	if diag.Source != "projection" {
		t.Fatalf("diagnostics source = %q, want projection", diag.Source)
	}
	if diag.InputRequiredCount != 1 || diag.WaitingOnInputCount != 1 || diag.UnclosedRequiredRequestCount != 1 || diag.ActiveTaskCount != 1 {
		t.Fatalf("input-request diagnostics = %#v, want one unique open request with two directional views", diag)
	}
}

func TestUniqueOpenInputRequestCountDedupesFallbackMessageRecipient(t *testing.T) {
	state := projection.MessageInputRequestState{
		InputRequired: []projection.InputRequestDetail{
			{MessageID: "m1.md", Recipient: "worker"},
			{MessageID: "m2.md", Recipient: "worker"},
		},
		WaitingOnInput: []projection.InputRequestDetail{
			{MessageID: "m1.md", Recipient: "worker"},
			{MessageID: "m1.md", Recipient: "critic"},
			{MessageID: "m3.md", Recipient: "worker"},
		},
	}

	if got := uniqueOpenInputRequestCount(state); got != 4 {
		t.Fatalf("uniqueOpenInputRequestCount() = %d, want 4", got)
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
	if err := config.WriteSessionPIDFile(filepath.Join(sessionDir, "postman.pid"), os.Getpid()); err != nil {
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

func TestRunPop_IncludesRuntimeContextSummaryWhenSnapshotReferenced(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")
	receiverLaunchCommand := "/usr/bin/codex --yolo --add-dir /receiver/internal --model gpt-5.5"
	t.Setenv("TMUX_A2A_POSTMAN_LAUNCH_COMMAND", receiverLaunchCommand)

	contextID := "ctx-pop-runtime-context"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260520-010103-from-orchestrator-to-worker.md"
	capturedAt := time.Date(2026, time.May, 20, 1, 1, 3, 0, time.UTC)
	senderLaunchCommand := "/usr/bin/codex --yolo --add-dir /sender/internal --model gpt-5.5"
	snapshot := runtimecontext.BuildSnapshot(runtimecontext.BuildOptions{
		Now:         capturedAt,
		Scope:       "sender",
		ContextID:   contextID,
		MessageID:   filename,
		TmuxSession: "test-session",
		Node:        "orchestrator",
		PaneID:      "%42",
		CWD:         filepath.Join(tmpDir, "workspace"),
		Runtime:     runtimecontext.RuntimeMetadata{LaunchCommand: senderLaunchCommand},
	})
	if _, err := runtimecontext.SaveSnapshot(sessionDir, snapshot); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  messageId: " + filename + "\n" +
		"  timestamp: 2026-05-20T01:01:03Z\n" +
		"  runtimeContextId: " + snapshot.SnapshotID + "\n" +
		"  runtimeContextScope: sender\n" +
		"  runtimeContextCapturedAt: " + snapshot.CapturedAt + "\n" +
		"  runtimeContextHash: " + snapshot.ContentHash + "\n" +
		"---\n\n" +
		"## Sender Message\n\n---\n\nRead the archived body before acting.\n"
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
	receiptPath := payload.PopReceiptAbsolutePath
	if receiptPath == "" {
		receiptPath = payload.PopReceiptPath
	}
	if receiptPath == "" {
		t.Fatalf("payload missing pop receipt path: %#v", payload)
	}
	if filepath.Base(receiptPath) != "20260520-010103-from-orchestrator-to-worker.pop.json" {
		t.Fatalf("receipt path = %q, want filename.pop.json", receiptPath)
	}
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("ReadFile pop receipt: %v", err)
	}
	receiptInfo, err := os.Stat(receiptPath)
	if err != nil {
		t.Fatalf("Stat pop receipt: %v", err)
	}
	if got := receiptInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("pop receipt permissions = %v, want 0600", got)
	}
	if string(receiptBytes) != stdout {
		t.Fatalf("pop receipt did not persist exact stdout:\nreceipt=%s\nstdout=%s", string(receiptBytes), stdout)
	}
	if payload.RuntimeContext == nil {
		t.Fatalf("payload.RuntimeContext is nil; stdout=%s", stdout)
	}
	if payload.RuntimeContext.Semantics != "metadata_not_instructions" {
		t.Fatalf("runtime context semantics = %q", payload.RuntimeContext.Semantics)
	}
	if payload.RuntimeContext.Fields.Role != "orchestrator" {
		t.Fatalf("runtime context role = %q, want orchestrator", payload.RuntimeContext.Fields.Role)
	}
	if payload.RuntimeContext.YouWereLaunchedWith != senderLaunchCommand {
		t.Fatalf("runtime context launch command = %q, want %q", payload.RuntimeContext.YouWereLaunchedWith, senderLaunchCommand)
	}
	if payload.RuntimeContext.Fields.Runtime == nil || payload.RuntimeContext.Fields.Runtime.LaunchCommand != senderLaunchCommand {
		t.Fatalf("runtime context fields runtime = %#v, want launch command", payload.RuntimeContext.Fields.Runtime)
	}
	if payload.ReceiverRuntimeContext == nil {
		t.Fatalf("payload.ReceiverRuntimeContext is nil; stdout=%s", stdout)
	}
	if payload.ReceiverRuntimeContext.Scope != "receiver" {
		t.Fatalf("receiver runtime context scope = %q, want receiver", payload.ReceiverRuntimeContext.Scope)
	}
	if payload.ReceiverRuntimeContext.Fields.Role != "worker" {
		t.Fatalf("receiver runtime context role = %q, want worker", payload.ReceiverRuntimeContext.Fields.Role)
	}
	if payload.ReceiverRuntimeContext.YouWereLaunchedWith != receiverLaunchCommand {
		t.Fatalf("receiver launch command = %q, want %q", payload.ReceiverRuntimeContext.YouWereLaunchedWith, receiverLaunchCommand)
	}
	if payload.ReceiverRuntimeContext.Fields.Runtime == nil || payload.ReceiverRuntimeContext.Fields.Runtime.LaunchCommand != receiverLaunchCommand {
		t.Fatalf("receiver runtime fields = %#v, want launch command", payload.ReceiverRuntimeContext.Fields.Runtime)
	}
	if payload.ReceiverRuntimeContext.Fields.Runtime.AddDir != nil {
		t.Fatalf("receiver runtime add_dir = %#v, want omitted", payload.ReceiverRuntimeContext.Fields.Runtime.AddDir)
	}
	if !strings.Contains(stdout, `"you_were_launched_with"`) {
		t.Fatalf("pop JSON missing you_were_launched_with key:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"receiver_runtime_context"`) {
		t.Fatalf("pop JSON missing receiver_runtime_context key:\n%s", stdout)
	}
	if payload.RuntimeContext.ContentHash != snapshot.ContentHash {
		t.Fatalf("runtime context hash = %q, want %q", payload.RuntimeContext.ContentHash, snapshot.ContentHash)
	}
	if payload.RuntimeContext.ArchivedContextPath == "" {
		t.Fatalf("runtime context archived paths missing: %#v", payload.RuntimeContext)
	}
	if payload.RuntimeContext.ArchivedContextAbsolutePath != "" {
		t.Fatalf("default runtime context summary exposed absolute path: %#v", payload.RuntimeContext)
	}
	if !payload.ArchivedBodyReadRequired {
		t.Fatal("payload.ArchivedBodyReadRequired = false, want true")
	}
	assertPopPayloadOmitsInlineMarkdown(t, stdout)
	if strings.Contains(stdout, "Read the archived body before acting.") {
		t.Fatalf("pop JSON leaked sender body despite runtime summary:\n%s", stdout)
	}
}

func TestRunPop_BoundsRuntimeContextSummaryAndOmitsAbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-runtime-context-bounded"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260520-010105-from-orchestrator-to-worker.md"
	snapshot := runtimecontext.Snapshot{
		SchemaVersion: runtimecontext.SchemaVersion,
		Semantics:     runtimecontext.SemanticsMetadataNotInstructions,
		SnapshotID:    "rctx_bounded",
		CapturedAt:    "2026-05-20T01:01:05Z",
		Scope:         "sender",
		ContextID:     contextID,
		MessageID:     filename,
		TmuxSession:   "test-session",
		Node:          "orchestrator",
		CWD:           strings.Repeat("/very/long/workspace/", 700),
		Runtime: &runtimecontext.RuntimeMetadata{
			Name:    strings.Repeat("codex", 400),
			Model:   strings.Repeat("model", 400),
			Profile: strings.Repeat("profile", 400),
		},
		Freshness: runtimecontext.Freshness{State: "fresh", AgeSeconds: 0},
		Redaction: runtimecontext.Redaction{Rules: []string{"secret_patterns", "control_characters", "max_string_bytes"}},
	}
	snapshot.ContentHash = runtimecontext.ContentHash(snapshot)
	if _, err := runtimecontext.SaveSnapshot(sessionDir, snapshot); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  messageId: " + filename + "\n" +
		"  timestamp: 2026-05-20T01:01:05Z\n" +
		"  runtimeContextId: " + snapshot.SnapshotID + "\n" +
		"  runtimeContextHash: " + snapshot.ContentHash + "\n" +
		"---\n\nPayload\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	var raw struct {
		RuntimeContext json.RawMessage `json:"runtime_context"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("json.Unmarshal raw pop output: %v", err)
	}
	if len(raw.RuntimeContext) == 0 {
		t.Fatalf("runtime_context missing from pop output: %s", stdout)
	}
	if len(raw.RuntimeContext) > 4096 {
		t.Fatalf("runtime_context length = %d, want <= 4096", len(raw.RuntimeContext))
	}
	if bytes.Contains(raw.RuntimeContext, []byte("archived_context_absolute_path")) {
		t.Fatalf("runtime_context leaked absolute path field: %s", raw.RuntimeContext)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.RuntimeContext == nil {
		t.Fatalf("RuntimeContext is nil: %s", stdout)
	}
	if !payload.RuntimeContext.Redaction.Truncated {
		t.Fatalf("Redaction.Truncated = false, want true: %#v", payload.RuntimeContext.Redaction)
	}
	if payload.RuntimeContext.ArchivedContextAbsolutePath != "" {
		t.Fatalf("ArchivedContextAbsolutePath = %q, want omitted", payload.RuntimeContext.ArchivedContextAbsolutePath)
	}
}

func TestRunPop_RuntimeContextHashMismatchIsExposed(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-runtime-context-hash-mismatch"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260520-010106-from-orchestrator-to-worker.md"
	snapshot := runtimecontext.BuildSnapshot(runtimecontext.BuildOptions{
		Now:       time.Date(2026, time.May, 20, 1, 1, 6, 0, time.UTC),
		Scope:     "sender",
		ContextID: contextID,
		MessageID: filename,
		Node:      "orchestrator",
	})
	if _, err := runtimecontext.SaveSnapshot(sessionDir, snapshot); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  messageId: " + filename + "\n" +
		"  timestamp: 2026-05-20T01:01:06Z\n" +
		"  runtimeContextId: " + snapshot.SnapshotID + "\n" +
		"  runtimeContextHash: sha256:" + strings.Repeat("0", 64) + "\n" +
		"---\n\nPayload\n"
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
	if payload.RuntimeContext != nil {
		t.Fatalf("RuntimeContext = %#v, want nil on hash mismatch", payload.RuntimeContext)
	}
	if !strings.Contains(payload.RuntimeContextError, "runtime_context_hash_mismatch") {
		t.Fatalf("RuntimeContextError = %q, want hash mismatch", payload.RuntimeContextError)
	}
	if !payload.ArchivedBodyReadRequired {
		t.Fatal("ArchivedBodyReadRequired = false, want true")
	}
}

func TestRunPop_RuntimeContextMissingSnapshotIsExposed(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-runtime-context-missing"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260520-010107-from-orchestrator-to-worker.md"
	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  messageId: " + filename + "\n" +
		"  timestamp: 2026-05-20T01:01:07Z\n" +
		"  runtimeContextId: rctx_missing\n" +
		"---\n\nPayload\n"
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
	if payload.RuntimeContext != nil {
		t.Fatalf("RuntimeContext = %#v, want nil for missing snapshot", payload.RuntimeContext)
	}
	if !strings.Contains(payload.RuntimeContextError, "runtime_context_unavailable") || !strings.Contains(payload.RuntimeContextError, "not found") {
		t.Fatalf("RuntimeContextError = %q, want unavailable/not found", payload.RuntimeContextError)
	}
}

func TestRunPop_IncludesRuntimeContextForCrossSessionDeliveredMessage(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "ctx-pop-runtime-context-cross-session"
	senderSession := "sender-session"
	recipientSession := "recipient-session"
	senderSessionDir := filepath.Join(tmpDir, contextID, senderSession)
	recipientSessionDir := filepath.Join(tmpDir, contextID, recipientSession)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["sender-session:messenger --- recipient-session:worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	installFakeTmuxForCLI(t, tmpDir, senderSession, "messenger")
	sendStdout, sendStderr, err := captureSendHeredocWithBody(t, "cross-session body", []string{
		"--config", configPath,
		"--context-id", contextID,
		"--to", recipientSession + ":worker",
	})
	if err != nil {
		t.Fatalf("RunSendHeredoc: %v\nstderr=%s", err, sendStderr)
	}
	sendPayload := decodeSendOutputForTest(t, sendStdout)
	postPath := filepath.Join(senderSessionDir, "post", sendPayload.Sent)
	postContent, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	metadata, err := envelope.ParseMetadata(string(postContent))
	if err != nil {
		t.Fatalf("ParseMetadata post: %v", err)
	}
	if metadata.RuntimeContextID == "" || metadata.RuntimeContextHash == "" {
		t.Fatalf("runtime context metadata missing: %#v", metadata)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		t.Fatalf("ParseEdges: %v", err)
	}
	knownNodes := map[string]discovery.NodeInfo{
		senderSession + ":messenger": {
			SessionName: senderSession,
			SessionDir:  senderSessionDir,
		},
		recipientSession + ":worker": {
			SessionName: recipientSession,
			SessionDir:  recipientSessionDir,
		},
	}
	if err := message.DeliverMessage(postPath, contextID, knownNodes, adjacency, cfg, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	installFakeTmuxForCLI(t, tmpDir, recipientSession, "worker")
	popStdout, popStderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--config", configPath, "--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, popStderr)
	}
	payload := decodePopMessageOutputForTest(t, popStdout)
	if payload.RuntimeContext == nil {
		t.Fatalf("RuntimeContext is nil for cross-session pop: %s", popStdout)
	}
	if payload.RuntimeContextError != "" {
		t.Fatalf("RuntimeContextError = %q, want empty", payload.RuntimeContextError)
	}
	if payload.RuntimeContext.ContentHash != metadata.RuntimeContextHash {
		t.Fatalf("runtime context hash = %q, want %q", payload.RuntimeContext.ContentHash, metadata.RuntimeContextHash)
	}
	if payload.RuntimeContext.Fields.Role != "messenger" {
		t.Fatalf("runtime context role = %q, want messenger", payload.RuntimeContext.Fields.Role)
	}
	wantPathPart := filepath.Join(contextID, "snapshot", "runtime-context", metadata.RuntimeContextID+".json")
	if !strings.Contains(payload.RuntimeContext.ArchivedContextPath, wantPathPart) {
		t.Fatalf("ArchivedContextPath = %q, want context-level path containing %q", payload.RuntimeContext.ArchivedContextPath, wantPathPart)
	}
}

func TestRunPop_RuntimeContextNoneOmitsSummary(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-pop-runtime-context-none"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260520-010104-from-orchestrator-to-worker.md"
	snapshot := runtimecontext.BuildSnapshot(runtimecontext.BuildOptions{
		Now:       time.Date(2026, time.May, 20, 1, 1, 4, 0, time.UTC),
		Scope:     "sender",
		ContextID: contextID,
		MessageID: filename,
		Node:      "orchestrator",
	})
	if _, err := runtimecontext.SaveSnapshot(sessionDir, snapshot); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  messageId: " + filename + "\n" +
		"  timestamp: 2026-05-20T01:01:04Z\n" +
		"  runtimeContextId: " + snapshot.SnapshotID + "\n" +
		"---\n\nPayload\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunPop([]string{"--context-id", contextID, "--runtime-context", "none"})
	})
	if err != nil {
		t.Fatalf("RunPop: %v\nstderr=%s", err, stderr)
	}
	payload := decodePopMessageOutputForTest(t, stdout)
	if payload.RuntimeContext != nil {
		t.Fatalf("RuntimeContext = %#v, want nil", payload.RuntimeContext)
	}
	if payload.ReceiverRuntimeContext != nil {
		t.Fatalf("ReceiverRuntimeContext = %#v, want nil", payload.ReceiverRuntimeContext)
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
