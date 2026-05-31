package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/testfixture"
)

func benchmarkLoadedDaemonSubmitSession(b *testing.B) testfixture.LoadedSession {
	b.Helper()

	fixture := testfixture.BuildLoadedSession(b, testfixture.DefaultLoadedSessionConfig())
	manager := journal.NewManager("ctx-loaded-submit", os.Getpid())
	journal.InstallProcessManager(manager)
	b.Cleanup(journal.ClearProcessManager)
	if err := manager.Bootstrap(fixture.SessionDir, fixture.SessionName, fixture.Now.Add(time.Second)); err != nil {
		b.Fatalf("Bootstrap: %v", err)
	}

	return fixture
}

func BenchmarkProcessDaemonSubmitRequest_LoadedSessionSend(b *testing.B) {
	fixture := benchmarkLoadedDaemonSubmitSession(b)
	b.ReportAllocs()

	b.ResetTimer()
	reportLoadedDaemonSubmitMetrics(b, fixture)
	for i := 0; i < b.N; i++ {
		requestID := fmt.Sprintf("bench-send-%d", i)
		filename := fmt.Sprintf("20260521-100000-r%04x-from-orchestrator-to-worker.md", i%0xffff)
		requestPath, err := projection.WriteDaemonSubmitRequest(fixture.SessionDir, projection.DaemonSubmitRequest{
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
		_ = os.Remove(projection.DaemonSubmitResponsePath(fixture.SessionDir, requestID))
		_ = os.Remove(filepath.Join(fixture.SessionDir, "post", filename))
	}
}

func BenchmarkProcessDaemonSubmitRequest_LoadedSessionEmptyPop(b *testing.B) {
	fixture := benchmarkLoadedDaemonSubmitSession(b)
	b.ReportAllocs()

	b.ResetTimer()
	reportLoadedDaemonSubmitMetrics(b, fixture)
	for i := 0; i < b.N; i++ {
		requestID := fmt.Sprintf("bench-empty-pop-%d", i)
		requestPath, err := projection.WriteDaemonSubmitRequest(fixture.SessionDir, projection.DaemonSubmitRequest{
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
		_ = os.Remove(projection.DaemonSubmitResponsePath(fixture.SessionDir, requestID))
	}
}

func BenchmarkProcessDaemonSubmitRequest_LoadedSessionPop(b *testing.B) {
	fixture := benchmarkLoadedDaemonSubmitSession(b)
	b.ReportAllocs()

	inboxDir := filepath.Join(fixture.SessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		b.Fatalf("MkdirAll inbox: %v", err)
	}

	b.ResetTimer()
	reportLoadedDaemonSubmitMetrics(b, fixture)
	for i := 0; i < b.N; i++ {
		requestID := fmt.Sprintf("bench-pop-%d", i)
		filename := fmt.Sprintf("20260521-110000-r%04x-from-orchestrator-to-worker.md", i%0xffff)
		if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte("benchmark pop\n"), 0o600); err != nil {
			b.Fatalf("WriteFile inbox: %v", err)
		}
		requestPath, err := projection.WriteDaemonSubmitRequest(fixture.SessionDir, projection.DaemonSubmitRequest{
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
		_ = os.Remove(projection.DaemonSubmitResponsePath(fixture.SessionDir, requestID))
	}
}

func reportLoadedDaemonSubmitMetrics(b *testing.B, fixture testfixture.LoadedSession) {
	b.Helper()
	b.ReportMetric(float64(fixture.MailboxEventRecords), "mailbox_events")
	b.ReportMetric(float64(fixture.ReadArchiveRecords), "read_archives")
	b.ReportMetric(float64(fixture.DaemonSubmitRequests), "daemon_submit_requests")
	b.ReportMetric(float64(fixture.DaemonSubmitResponses), "daemon_submit_responses")
	b.ReportMetric(float64(fixture.MessageBodyBytes), "message_body_bytes")
}
