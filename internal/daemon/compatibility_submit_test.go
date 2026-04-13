package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestProcessCompatibilitySubmitRequest_SendWritesPostFile(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	requestPath, err := projection.WriteCompatibilitySubmitRequest(sessionDir, projection.CompatibilitySubmitRequest{
		RequestID: "req-send",
		Command:   projection.CompatibilitySubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  "20260414-033100-from-orchestrator-to-worker.md",
		Content:   "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:31:00Z\n---\n\nsubmit payload\n",
	})
	if err != nil {
		t.Fatalf("WriteCompatibilitySubmitRequest: %v", err)
	}

	if err := processCompatibilitySubmitRequest(requestPath); err != nil {
		t.Fatalf("processCompatibilitySubmitRequest: %v", err)
	}

	postPath := filepath.Join(sessionDir, "post", "20260414-033100-from-orchestrator-to-worker.md")
	got, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("ReadFile postPath: %v", err)
	}
	if string(got) != "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:31:00Z\n---\n\nsubmit payload\n" {
		t.Fatalf("post payload changed:\n got %q", string(got))
	}

	response, err := projection.ReadCompatibilitySubmitResponse(projection.CompatibilitySubmitResponsePath(sessionDir, "req-send"))
	if err != nil {
		t.Fatalf("ReadCompatibilitySubmitResponse: %v", err)
	}
	if response.Filename != "20260414-033100-from-orchestrator-to-worker.md" {
		t.Fatalf("response.Filename = %q", response.Filename)
	}
}

func TestProcessCompatibilitySubmitRequest_PopArchivesUnreadMessage(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	oldest := "20260414-033200-from-orchestrator-to-worker.md"
	newest := "20260414-033201-from-orchestrator-to-worker.md"
	if err := os.WriteFile(filepath.Join(inboxDir, oldest), []byte("---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\noldest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile oldest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, newest), []byte("---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:01Z\n---\n\nnewest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile newest: %v", err)
	}

	requestPath, err := projection.WriteCompatibilitySubmitRequest(sessionDir, projection.CompatibilitySubmitRequest{
		RequestID: "req-pop",
		Command:   projection.CompatibilitySubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteCompatibilitySubmitRequest: %v", err)
	}

	if err := processCompatibilitySubmitRequest(requestPath); err != nil {
		t.Fatalf("processCompatibilitySubmitRequest: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sessionDir, "read", oldest)); err != nil {
		t.Fatalf("archived read file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, oldest)); !os.IsNotExist(err) {
		t.Fatalf("oldest inbox file still present or wrong error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, newest)); err != nil {
		t.Fatalf("newest inbox file missing: %v", err)
	}

	response, err := projection.ReadCompatibilitySubmitResponse(projection.CompatibilitySubmitResponsePath(sessionDir, "req-pop"))
	if err != nil {
		t.Fatalf("ReadCompatibilitySubmitResponse: %v", err)
	}
	if response.Filename != oldest {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, oldest)
	}
	if response.UnreadBefore != 2 {
		t.Fatalf("response.UnreadBefore = %d, want 2", response.UnreadBefore)
	}
}
