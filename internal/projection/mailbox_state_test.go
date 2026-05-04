package projection

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func appendMailboxStateEvent(t *testing.T, writer *journal.Writer, eventType string, visibility journal.Visibility, to, messageID, relativePath string, occurredAt time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, visibility, journal.MailboxEventPayload{
		MessageID: messageID,
		To:        to,
		Path:      relativePath,
	}, occurredAt); err != nil {
		t.Fatalf("AppendEvent(%s %s): %v", eventType, messageID, err)
	}
}

func TestProjectMailboxState_ReplaysUnreadCountsForCurrentGeneration(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 10, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	workerMessageID := "20260414-031001-r1111-from-orchestrator-to-worker.md"
	criticMessageID := "20260414-031002-r2222-from-orchestrator-to-critic.md"
	appendMailboxStateEvent(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, "worker", workerMessageID, filepath.Join("inbox", "worker", workerMessageID), now.Add(time.Second))
	appendMailboxStateEvent(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, "review:critic", criticMessageID, filepath.Join("inbox", "critic", criticMessageID), now.Add(2*time.Second))
	appendMailboxStateEvent(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, "worker", workerMessageID, filepath.Join("read", workerMessageID), now.Add(3*time.Second))

	got, ok, err := ProjectMailboxState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMailboxState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxState() ok = false, want true")
	}
	if got.InboxCounts["worker"] != 0 {
		t.Fatalf("worker unread = %d, want 0", got.InboxCounts["worker"])
	}
	if got.InboxCounts["critic"] != 1 {
		t.Fatalf("critic unread = %d, want 1", got.InboxCounts["critic"])
	}
}

func TestProjectMailboxState_FallsBackWhenHistoryIsMissing(t *testing.T) {
	sessionDir := t.TempDir()

	got, ok, err := ProjectMailboxState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMailboxState() error = %v", err)
	}
	if ok {
		t.Fatalf("ProjectMailboxState() ok = true, want false with %#v", got)
	}
}

func TestProjectMailboxState_FallsBackWhenHistoryIsIncomplete(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 20, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	messageID := "20260414-032001-r1111-from-orchestrator-to-worker.md"
	appendMailboxStateEvent(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, "worker", messageID, filepath.Join("read", messageID), now.Add(time.Second))

	got, ok, err := ProjectMailboxState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMailboxState() error = %v", err)
	}
	if ok {
		t.Fatalf("ProjectMailboxState() ok = true, want false with %#v", got)
	}
}

func TestProjectMailboxState_IgnoresDuplicateReadAfterDelivery(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 25, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	messageID := "20260414-032501-r1111-from-orchestrator-to-worker.md"
	appendMailboxStateEvent(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, "worker", messageID, filepath.Join("inbox", "worker", messageID), now.Add(time.Second))
	appendMailboxStateEvent(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, "worker", messageID, filepath.Join("read", messageID), now.Add(2*time.Second))
	appendMailboxStateEvent(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, "worker", messageID, filepath.Join("read", messageID), now.Add(3*time.Second))

	got, ok, err := ProjectMailboxState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMailboxState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxState() ok = false, want true")
	}
	if got.InboxCounts["worker"] != 0 {
		t.Fatalf("worker unread = %d, want 0", got.InboxCounts["worker"])
	}
}

func TestProjectMailboxState_IgnoresDelayedDuplicateReadAfterNewDelivery(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 27, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	firstMessageID := "20260414-032701-r1111-from-orchestrator-to-worker.md"
	secondMessageID := "20260414-032702-r2222-from-orchestrator-to-worker.md"
	appendMailboxStateEvent(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, "worker", firstMessageID, filepath.Join("inbox", "worker", firstMessageID), now.Add(time.Second))
	appendMailboxStateEvent(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, "worker", firstMessageID, filepath.Join("read", firstMessageID), now.Add(2*time.Second))
	appendMailboxStateEvent(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, "worker", secondMessageID, filepath.Join("inbox", "worker", secondMessageID), now.Add(3*time.Second))
	appendMailboxStateEvent(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, "worker", firstMessageID, filepath.Join("read", firstMessageID), now.Add(4*time.Second))

	got, ok, err := ProjectMailboxState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMailboxState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxState() ok = false, want true")
	}
	if got.InboxCounts["worker"] != 1 {
		t.Fatalf("worker unread = %d, want 1", got.InboxCounts["worker"])
	}
}

func TestProjectMailboxState_IgnoresUndeliveredReadWithoutDecrementingUnread(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 28, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	unreadMessageID := "20260414-032801-r1111-from-orchestrator-to-worker.md"
	undeliveredMessageID := "20260414-032802-r2222-from-orchestrator-to-worker.md"
	appendMailboxStateEvent(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, "worker", unreadMessageID, filepath.Join("inbox", "worker", unreadMessageID), now.Add(time.Second))
	appendMailboxStateEvent(t, writer, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, "worker", undeliveredMessageID, filepath.Join("read", undeliveredMessageID), now.Add(2*time.Second))

	got, ok, err := ProjectMailboxState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMailboxState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxState() ok = false, want true")
	}
	if got.InboxCounts["worker"] != 1 {
		t.Fatalf("worker unread = %d, want 1", got.InboxCounts["worker"])
	}
}

func TestProjectMailboxState_ReplaysAcrossLeaseResume(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter(first) error = %v", err)
	}
	workerMessageID := "20260414-033001-r1111-from-orchestrator-to-worker.md"
	criticMessageID := "20260414-033002-r2222-from-orchestrator-to-critic.md"
	appendMailboxStateEvent(t, writer, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, "worker", workerMessageID, filepath.Join("inbox", "worker", workerMessageID), now.Add(time.Second))

	resumedWriter, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 202, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("OpenShadowWriter(second) error = %v", err)
	}
	appendMailboxStateEvent(t, resumedWriter, MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, "critic", criticMessageID, filepath.Join("inbox", "critic", criticMessageID), now.Add(3*time.Second))
	appendMailboxStateEvent(t, resumedWriter, MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, "worker", workerMessageID, filepath.Join("read", workerMessageID), now.Add(4*time.Second))

	got, ok, err := ProjectMailboxState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMailboxState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxState() ok = false, want true")
	}
	if got.InboxCounts["worker"] != 0 {
		t.Fatalf("worker unread = %d, want 0", got.InboxCounts["worker"])
	}
	if got.InboxCounts["critic"] != 1 {
		t.Fatalf("critic unread = %d, want 1", got.InboxCounts["critic"])
	}
}
