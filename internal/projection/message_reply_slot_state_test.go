package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func replySlotContent(from, to, messageID, replyPolicy, replyTo, body string) string {
	return replySlotContentWithExact(from, to, messageID, replyPolicy, replyTo, "", "", body)
}

func replySlotContentWithExact(from, to, messageID, replyPolicy, replyTo, replySlotID, fillsReplySlotID, body string) string {
	replyToLine := ""
	if replyTo != "" {
		replyToLine = "  replyTo: " + replyTo + "\n"
	}
	replySlotIDLine := ""
	if replySlotID != "" {
		replySlotIDLine = "  reply_slot_id: " + replySlotID + "\n"
	}
	fillsReplySlotIDLine := ""
	if fillsReplySlotID != "" {
		fillsReplySlotIDLine = "  fills_reply_slot_id: " + fillsReplySlotID + "\n"
	}
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		replyToLine +
		replySlotIDLine +
		fillsReplySlotIDLine +
		"---\n\n" + body + "\n"
}

func appendReplySlotMailboxEvent(t *testing.T, writer *journal.Writer, eventType string, messageID, from, to, content string, now time.Time) {
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

func TestProjectMessageReplySlotState_RepliesResolveRequiredMessages(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 20, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionReadEventType, "m1.md", "orchestrator", "worker", request, now.Add(3*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required after read = %d, want 1", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting after send = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}

	reply := replySlotContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(5*time.Second))

	got, ok, err = ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() after reply error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() after reply ok = false, want true")
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

func TestProjectMessageReplySlotState_ReplyWithoutReplyToDoesNotResolve(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 25, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := replySlotContent("worker", "orchestrator", "m2.md", "none", "", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_ExactFillResolvesRequiredMessage(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContentWithExact("orchestrator", "worker", "m1.md", "required", "", "rslot_123", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := replySlotContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "rslot_123", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required = %d, want 0", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_ExactFillWithMatchingReplyToResolvesRequiredMessage(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 15, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContentWithExact("orchestrator", "worker", "m1.md", "required", "", "rslot_123", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := replySlotContentWithExact("worker", "orchestrator", "m2.md", "none", "m1.md", "", "rslot_123", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required = %d, want 0", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_ExactReplySlotIgnoresReplyToFallback(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 30, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContentWithExact("orchestrator", "worker", "m1.md", "required", "", "rslot_123", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := replySlotContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_MismatchedReplyToFailsExactClose(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 45, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContentWithExact("orchestrator", "worker", "m1.md", "required", "", "rslot_123", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := replySlotContentWithExact("worker", "orchestrator", "m2.md", "none", "other.md", "", "rslot_123", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_ReplyToMissDoesNotResolve(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 26, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := replySlotContent("worker", "orchestrator", "m2.md", "none", "missing.md", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_TracksMultipleRecipients(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	workerRequest := replySlotContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	criticRequest := replySlotContent("orchestrator", "critic", "m2.md", "required", "", "please review")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", workerRequest, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", workerRequest, now.Add(2*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "orchestrator", "critic", criticRequest, now.Add(3*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "orchestrator", "critic", criticRequest, now.Add(4*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 2 {
		t.Fatalf("orchestrator waiting = %d, want 2", got.WaitingOnReplyCounts["orchestrator"])
	}
	if got.ActionRequiredCounts["worker"] != 1 || got.ActionRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts = %#v, want worker=1 critic=1", got.ActionRequiredCounts)
	}

	workerReply := replySlotContent("worker", "orchestrator", "m3.md", "none", "m1.md", "ACK")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "worker", "orchestrator", workerReply, now.Add(5*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m3.md", "worker", "orchestrator", workerReply, now.Add(6*time.Second))

	got, ok, err = ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() after reply error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() after reply ok = false, want true")
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting after one reply = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
	if got.ActionRequiredCounts["worker"] != 0 || got.ActionRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts after one reply = %#v, want worker=0 critic=1", got.ActionRequiredCounts)
	}
}

func TestProjectMessageReplySlotState_ReplyToDoesNotMatchSessionQualifiedRecipientBySimpleName(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 50, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContent("orchestrator", "remote:worker", "m1.md", "required", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "remote:worker", request, now.Add(time.Second))
	reply := replySlotContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(2*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_ReplyToMatchesSessionQualifiedParticipant(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 51, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := replySlotContent("orchestrator", "remote:worker", "m1.md", "required", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "remote:worker", request, now.Add(time.Second))
	reply := replySlotContent("remote:worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "remote:worker", "orchestrator", reply, now.Add(2*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_SkipsIncompleteMailboxEvents(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 55, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	if _, err := writer.AppendEvent(MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, map[string]string{
		"to": "worker",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(incomplete delivered): %v", err)
	}
	content := replySlotContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", content, now.Add(2*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", content, now.Add(3*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.ActionRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.ActionRequiredCounts["worker"])
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
}

func TestProjectMessageReplySlotState_KeysReplySlotsByMessageAndRecipient(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 40, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	workerRequest := replySlotContent("orchestrator", "worker", "broadcast.md", "required", "", "please work")
	criticRequest := replySlotContent("orchestrator", "critic", "broadcast.md", "required", "", "please review")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "broadcast.md", "orchestrator", "worker", workerRequest, now.Add(time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "broadcast.md", "orchestrator", "worker", workerRequest, now.Add(2*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "broadcast.md", "orchestrator", "critic", criticRequest, now.Add(3*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "broadcast.md", "orchestrator", "critic", criticRequest, now.Add(4*time.Second))

	workerReply := replySlotContent("worker", "orchestrator", "worker-reply.md", "none", "broadcast.md", "ACK")
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "worker-reply.md", "worker", "orchestrator", workerReply, now.Add(5*time.Second))
	appendReplySlotMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "worker-reply.md", "worker", "orchestrator", workerReply, now.Add(6*time.Second))

	got, ok, err := ProjectMessageReplySlotState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageReplySlotState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageReplySlotState() ok = false, want true")
	}
	if got.WaitingOnReplyCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnReplyCounts["orchestrator"])
	}
	if got.ActionRequiredCounts["worker"] != 0 || got.ActionRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts = %#v, want worker=0 critic=1", got.ActionRequiredCounts)
	}
}
