package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestProjectMailboxState_ReplaysUnreadCountsForCurrentGeneration(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 10, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if _, err := writer.AppendEvent("compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, map[string]string{
		"to": "worker",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(delivered worker): %v", err)
	}
	if _, err := writer.AppendEvent("compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, map[string]string{
		"to": "review:critic",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("AppendEvent(delivered critic): %v", err)
	}
	if _, err := writer.AppendEvent("compatibility_mailbox_read", journal.VisibilityOperatorVisible, map[string]string{
		"to": "worker",
	}, now.Add(3*time.Second)); err != nil {
		t.Fatalf("AppendEvent(read worker): %v", err)
	}

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
	if _, err := writer.AppendEvent("compatibility_mailbox_read", journal.VisibilityOperatorVisible, map[string]string{
		"to": "worker",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(read worker): %v", err)
	}

	got, ok, err := ProjectMailboxState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMailboxState() error = %v", err)
	}
	if ok {
		t.Fatalf("ProjectMailboxState() ok = true, want false with %#v", got)
	}
}

func TestProjectMailboxState_ReplaysAcrossLeaseResume(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 3, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter(first) error = %v", err)
	}
	if _, err := writer.AppendEvent("compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, map[string]string{
		"to": "worker",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(first delivery): %v", err)
	}

	resumedWriter, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 202, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("OpenShadowWriter(second) error = %v", err)
	}
	if _, err := resumedWriter.AppendEvent("compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, map[string]string{
		"to": "critic",
	}, now.Add(3*time.Second)); err != nil {
		t.Fatalf("AppendEvent(second delivery): %v", err)
	}
	if _, err := resumedWriter.AppendEvent("compatibility_mailbox_read", journal.VisibilityOperatorVisible, map[string]string{
		"to": "worker",
	}, now.Add(4*time.Second)); err != nil {
		t.Fatalf("AppendEvent(second read): %v", err)
	}

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
