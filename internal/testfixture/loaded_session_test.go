package testfixture

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestBuildLoadedSessionCreatesReproducibleSmallFixture(t *testing.T) {
	fixture := BuildLoadedSession(t, LoadedSessionConfig{
		ContextID:             "ctx-test",
		SessionName:           "review",
		Nodes:                 []string{"worker", "critic"},
		MailboxEventRecords:   16,
		ReadArchiveRecords:    4,
		PostRecords:           2,
		DeadLetterRecords:     1,
		StatusSnapshots:       2,
		DaemonSubmitRequests:  3,
		DaemonSubmitResponses: 5,
		MessageBodyBytes:      128,
		Now:                   time.Date(2026, time.May, 21, 10, 0, 0, 0, time.UTC),
	})

	events, err := journal.Replay(fixture.SessionDir)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got, want := len(events), 2+fixture.MailboxEventRecords+fixture.StatusSnapshots; got != want {
		t.Fatalf("journal record count = %d, want %d", got, want)
	}

	mailbox, ok, err := projection.ProjectMailboxProjection(fixture.SessionDir)
	if err != nil {
		t.Fatalf("ProjectMailboxProjection: %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxProjection ok = false, want true")
	}
	if got, want := len(mailbox.Post), fixture.PostRecords; got != want {
		t.Fatalf("post projection count = %d, want %d", got, want)
	}
	if got, want := len(mailbox.Read), fixture.ReadArchiveRecords; got != want {
		t.Fatalf("read projection count = %d, want %d", got, want)
	}
	if got, want := len(mailbox.DeadLetter), fixture.DeadLetterRecords; got != want {
		t.Fatalf("dead-letter projection count = %d, want %d", got, want)
	}

	statusSnapshot, ok, err := projection.ProjectSessionStatus(fixture.SessionDir)
	if err != nil {
		t.Fatalf("ProjectSessionStatus: %v", err)
	}
	if !ok {
		t.Fatal("ProjectSessionStatus ok = false, want true")
	}
	if statusSnapshot.SessionName != fixture.SessionName {
		t.Fatalf("status snapshot session = %q, want %q", statusSnapshot.SessionName, fixture.SessionName)
	}

	requests, err := os.ReadDir(projection.DaemonSubmitRequestsDir(fixture.SessionDir))
	if err != nil {
		t.Fatalf("ReadDir(requests): %v", err)
	}
	if got, want := len(requests), fixture.DaemonSubmitRequests; got != want {
		t.Fatalf("daemon-submit request snapshots = %d, want %d", got, want)
	}
	responses, err := os.ReadDir(projection.DaemonSubmitResponsesDir(fixture.SessionDir))
	if err != nil {
		t.Fatalf("ReadDir(responses): %v", err)
	}
	if got, want := len(responses), fixture.DaemonSubmitResponses; got != want {
		t.Fatalf("daemon-submit response snapshots = %d, want %d", got, want)
	}

	readFiles, err := filepath.Glob(filepath.Join(fixture.SessionDir, "read", "*.md"))
	if err != nil {
		t.Fatalf("Glob(read): %v", err)
	}
	if got, want := len(readFiles), fixture.ReadArchiveRecords; got != want {
		t.Fatalf("read archive files = %d, want %d", got, want)
	}
}
