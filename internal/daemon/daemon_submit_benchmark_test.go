package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

const (
	loadedSessionJournalRecords = 10000
	loadedSessionReadFiles      = 500
)

func benchmarkLoadedDaemonSubmitSession(b *testing.B) string {
	b.Helper()

	sessionDir := filepath.Join(b.TempDir(), "loaded-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		b.Fatalf("CreateSessionDirs: %v", err)
	}

	restoreDurableWrites := journal.SetDurableWritesForTesting(false)
	b.Cleanup(restoreDurableWrites)
	manager := journal.NewManager("ctx-loaded-submit", os.Getpid())
	journal.InstallProcessManager(manager)
	b.Cleanup(journal.ClearProcessManager)
	now := time.Date(2026, time.May, 21, 9, 30, 0, 0, time.UTC)
	if err := manager.Bootstrap(sessionDir, "loaded-session", now); err != nil {
		b.Fatalf("Bootstrap: %v", err)
	}

	readDir := filepath.Join(sessionDir, "read")
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		b.Fatalf("MkdirAll read: %v", err)
	}
	for i := 0; i < loadedSessionJournalRecords; i++ {
		filename := fmt.Sprintf("20260521-%06d-r%04x-from-orchestrator-to-worker.md", i%1000000, i%0xffff)
		content := fmt.Sprintf("---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: %s\n---\n\nloaded message %d\n", filename, i)
		eventType := projection.MailboxProjectionDeliveredEventType
		relativePath := filepath.Join("inbox", "worker", filename)
		if i < loadedSessionReadFiles {
			eventType = projection.MailboxProjectionReadEventType
			relativePath = filepath.Join("read", filename)
			if err := os.WriteFile(filepath.Join(readDir, filename), []byte(content), 0o600); err != nil {
				b.Fatalf("WriteFile read fixture: %v", err)
			}
		}
		if err := journal.RecordProcessMailboxPayload(sessionDir, "loaded-session", eventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
			MessageID: filename,
			From:      "orchestrator",
			To:        "worker",
			Path:      relativePath,
			Content:   content,
		}, now.Add(time.Duration(i+1)*time.Millisecond)); err != nil {
			b.Fatalf("RecordProcessMailboxPayload: %v", err)
		}
	}
	return sessionDir
}

func BenchmarkProcessDaemonSubmitRequest_LoadedSessionSend(b *testing.B) {
	sessionDir := benchmarkLoadedDaemonSubmitSession(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		requestID := fmt.Sprintf("bench-send-%d", i)
		filename := fmt.Sprintf("20260521-100000-r%04x-from-orchestrator-to-worker.md", i%0xffff)
		requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
			RequestID: requestID,
			Command:   projection.DaemonSubmitSend,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Filename:  filename,
			Content:   "benchmark send\n",
		})
		if err != nil {
			b.Fatalf("WriteDaemonSubmitRequest: %v", err)
		}
		if _, err := processDaemonSubmitRequest(requestPath); err != nil {
			b.Fatalf("processDaemonSubmitRequest: %v", err)
		}
		_ = os.Remove(projection.DaemonSubmitResponsePath(sessionDir, requestID))
		_ = os.Remove(filepath.Join(sessionDir, "post", filename))
	}
}

func BenchmarkProcessDaemonSubmitRequest_LoadedSessionEmptyPop(b *testing.B) {
	sessionDir := benchmarkLoadedDaemonSubmitSession(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		requestID := fmt.Sprintf("bench-empty-pop-%d", i)
		requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
			RequestID: requestID,
			Command:   projection.DaemonSubmitPop,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Node:      "operator",
		})
		if err != nil {
			b.Fatalf("WriteDaemonSubmitRequest: %v", err)
		}
		if _, err := processDaemonSubmitRequest(requestPath); err != nil {
			b.Fatalf("processDaemonSubmitRequest: %v", err)
		}
		_ = os.Remove(projection.DaemonSubmitResponsePath(sessionDir, requestID))
	}
}

func BenchmarkProcessDaemonSubmitRequest_LoadedSessionPop(b *testing.B) {
	sessionDir := benchmarkLoadedDaemonSubmitSession(b)
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		b.Fatalf("MkdirAll inbox: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		requestID := fmt.Sprintf("bench-pop-%d", i)
		filename := fmt.Sprintf("20260521-110000-r%04x-from-orchestrator-to-worker.md", i%0xffff)
		if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte("benchmark pop\n"), 0o600); err != nil {
			b.Fatalf("WriteFile inbox: %v", err)
		}
		requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
			RequestID: requestID,
			Command:   projection.DaemonSubmitPop,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Node:      "worker",
		})
		if err != nil {
			b.Fatalf("WriteDaemonSubmitRequest: %v", err)
		}
		if _, err := processDaemonSubmitRequest(requestPath); err != nil {
			b.Fatalf("processDaemonSubmitRequest: %v", err)
		}
		_ = os.Remove(projection.DaemonSubmitResponsePath(sessionDir, requestID))
	}
}
