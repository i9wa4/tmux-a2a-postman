package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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
