package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func obligationContent(from, to, messageID, replyPolicy, replyTo, body string) string {
	replyToLine := ""
	if replyTo != "" {
		replyToLine = "  replyTo: " + replyTo + "\n"
	}
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		replyToLine +
		"---\n\n" + body + "\n"
}

func appendObligationMailboxEvent(t *testing.T, writer *journal.Writer, eventType string, messageID, from, to, content string, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: messageID,
		From:      from,
		To:        to,
		Content:   content,
	}, now); err != nil {
		t.Fatalf("AppendEvent(%s, %s): %v", eventType, messageID, err)
	}
}

func TestProjectMessageObligationState_RepliesResolveRequiredMessages(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 20, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := obligationContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendObligationMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionReadEventType, "m1.md", "orchestrator", "worker", request, now.Add(3*time.Second))

	got, ok, err := ProjectMessageObligationState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageObligationState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageObligationState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required after read = %d, want 1", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting after send = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}

	reply := obligationContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendObligationMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(5*time.Second))

	got, ok, err = ProjectMessageObligationState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageObligationState() after reply error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageObligationState() after reply ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required after reply = %d, want 0", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting after reply = %d, want 0", got.WaitingOnReplyCounts["orchestrator"])
	}
	if got.InfoUnreadCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator info unread = %d, want 1", got.InfoUnreadCounts["orchestrator"])
	}
}

func TestProjectMessageObligationState_TracksMultipleRecipients(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	workerRequest := obligationContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	criticRequest := obligationContent("orchestrator", "critic", "m2.md", "required", "", "please review")
	appendObligationMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", workerRequest, now.Add(time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", workerRequest, now.Add(2*time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "orchestrator", "critic", criticRequest, now.Add(3*time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "orchestrator", "critic", criticRequest, now.Add(4*time.Second))

	got, ok, err := ProjectMessageObligationState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageObligationState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageObligationState() ok = false, want true")
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 2 {
		t.Fatalf("orchestrator waiting = %d, want 2", got.WaitingOnReplyCounts["orchestrator"])
	}
	if got.ActionRequiredCounts["worker"] != 1 || got.ActionRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts = %#v, want worker=1 critic=1", got.ActionRequiredCounts)
	}

	workerReply := obligationContent("worker", "orchestrator", "m3.md", "none", "m1.md", "ACK")
	appendObligationMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "worker", "orchestrator", workerReply, now.Add(5*time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m3.md", "worker", "orchestrator", workerReply, now.Add(6*time.Second))

	got, ok, err = ProjectMessageObligationState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageObligationState() after reply error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageObligationState() after reply ok = false, want true")
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting after one reply = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
	if got.ActionRequiredCounts["worker"] != 0 || got.ActionRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts after one reply = %#v, want worker=0 critic=1", got.ActionRequiredCounts)
	}
}

func TestProjectMessageObligationState_KeysObligationsByMessageAndRecipient(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 40, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	workerRequest := obligationContent("orchestrator", "worker", "broadcast.md", "required", "", "please work")
	criticRequest := obligationContent("orchestrator", "critic", "broadcast.md", "required", "", "please review")
	appendObligationMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "broadcast.md", "orchestrator", "worker", workerRequest, now.Add(time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "broadcast.md", "orchestrator", "worker", workerRequest, now.Add(2*time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "broadcast.md", "orchestrator", "critic", criticRequest, now.Add(3*time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "broadcast.md", "orchestrator", "critic", criticRequest, now.Add(4*time.Second))

	workerReply := obligationContent("worker", "orchestrator", "worker-reply.md", "none", "broadcast.md", "ACK")
	appendObligationMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "worker-reply.md", "worker", "orchestrator", workerReply, now.Add(5*time.Second))
	appendObligationMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "worker-reply.md", "worker", "orchestrator", workerReply, now.Add(6*time.Second))

	got, ok, err := ProjectMessageObligationState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageObligationState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageObligationState() ok = false, want true")
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
	if got.ActionRequiredCounts["worker"] != 0 || got.ActionRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts = %#v, want worker=0 critic=1", got.ActionRequiredCounts)
	}
}
