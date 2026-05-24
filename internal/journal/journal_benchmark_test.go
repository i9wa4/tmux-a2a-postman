package journal_test

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/testfixture"
)

func BenchmarkReplay_LoadedSession10000MailboxEvents500ReadArchives(b *testing.B) {
	fixture := testfixture.BuildLoadedSession(b, testfixture.DefaultLoadedSessionConfig())
	b.ReportAllocs()

	b.ResetTimer()
	reportLoadedSessionMetrics(b, fixture)
	for i := 0; i < b.N; i++ {
		events, err := journal.Replay(fixture.SessionDir)
		if err != nil {
			b.Fatalf("Replay: %v", err)
		}
		if len(events) == 0 {
			b.Fatal("Replay returned no events")
		}
	}
}

func reportLoadedSessionMetrics(b *testing.B, fixture testfixture.LoadedSession) {
	b.Helper()
	b.ReportMetric(float64(fixture.MailboxEventRecords), "mailbox_events")
	b.ReportMetric(float64(fixture.ReadArchiveRecords), "read_archives")
	b.ReportMetric(float64(fixture.StatusSnapshots), "status_snapshots")
	b.ReportMetric(float64(fixture.DaemonSubmitResponses), "daemon_submit_responses")
	b.ReportMetric(float64(fixture.MessageBodyBytes), "message_body_bytes")
}
