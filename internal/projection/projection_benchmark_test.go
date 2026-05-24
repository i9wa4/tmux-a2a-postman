package projection_test

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/testfixture"
)

func BenchmarkProjectMailboxProjection_LoadedSession10000MailboxEvents500ReadArchives(b *testing.B) {
	fixture := testfixture.BuildLoadedSession(b, testfixture.DefaultLoadedSessionConfig())
	b.ReportAllocs()

	b.ResetTimer()
	reportLoadedSessionMetrics(b, fixture)
	for i := 0; i < b.N; i++ {
		projected, ok, err := projection.ProjectMailboxProjection(fixture.SessionDir)
		if err != nil {
			b.Fatalf("ProjectMailboxProjection: %v", err)
		}
		if !ok {
			b.Fatal("ProjectMailboxProjection ok = false")
		}
		if len(projected.Read) != fixture.ReadArchiveRecords {
			b.Fatalf("read projection count = %d, want %d", len(projected.Read), fixture.ReadArchiveRecords)
		}
	}
}

func BenchmarkProjectMailboxState_LoadedSession10000MailboxEvents500ReadArchives(b *testing.B) {
	fixture := testfixture.BuildLoadedSession(b, testfixture.DefaultLoadedSessionConfig())
	b.ReportAllocs()

	b.ResetTimer()
	reportLoadedSessionMetrics(b, fixture)
	for i := 0; i < b.N; i++ {
		projected, ok, err := projection.ProjectMailboxState(fixture.SessionDir, fixture.SessionName)
		if err != nil {
			b.Fatalf("ProjectMailboxState: %v", err)
		}
		if !ok {
			b.Fatal("ProjectMailboxState ok = false")
		}
		if len(projected.InboxCounts) == 0 {
			b.Fatal("ProjectMailboxState returned no inbox counts")
		}
	}
}

func BenchmarkProjectSessionStatus_LoadedSession10000MailboxEvents8Snapshots(b *testing.B) {
	fixture := testfixture.BuildLoadedSession(b, testfixture.DefaultLoadedSessionConfig())
	b.ReportAllocs()

	b.ResetTimer()
	reportLoadedSessionMetrics(b, fixture)
	for i := 0; i < b.N; i++ {
		projected, ok, err := projection.ProjectSessionStatus(fixture.SessionDir)
		if err != nil {
			b.Fatalf("ProjectSessionStatus: %v", err)
		}
		if !ok {
			b.Fatal("ProjectSessionStatus ok = false")
		}
		if projected.SessionName != fixture.SessionName {
			b.Fatalf("session name = %q, want %q", projected.SessionName, fixture.SessionName)
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
