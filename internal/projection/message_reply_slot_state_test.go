package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func inputRequestContent(from, to, messageID, replyPolicy, replyTo, body string) string {
	return inputRequestContentWithExact(from, to, messageID, replyPolicy, replyTo, "", "", body)
}

func inputRequestContentWithExact(from, to, messageID, replyPolicy, replyTo, inputRequestID, fillsInputRequestID, body string) string {
	replyToLine := ""
	if replyTo != "" {
		replyToLine = "  replyTo: " + replyTo + "\n"
	}
	inputRequestIDLine := ""
	if inputRequestID != "" {
		inputRequestIDLine = "  input_request_id: " + inputRequestID + "\n"
	}
	fillsInputRequestIDLine := ""
	if fillsInputRequestID != "" {
		fillsInputRequestIDLine = "  fills_input_request_id: " + fillsInputRequestID + "\n"
	}
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		replyToLine +
		inputRequestIDLine +
		fillsInputRequestIDLine +
		"---\n\n" + body + "\n"
}

func appendInputRequestMailboxEvent(t *testing.T, writer *journal.Writer, eventType string, messageID, from, to, content string, now time.Time) {
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

func TestProjectMessageInputRequestState_RepliesResolveRequiredMessages(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 20, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionReadEventType, "m1.md", "orchestrator", "worker", request, now.Add(3*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required after read = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting after send = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
	if len(got.InputRequired) != 1 {
		t.Fatalf("action required details = %#v, want one detail", got.InputRequired)
	}
	action := got.InputRequired[0]
	if action.Direction != "inbound" || action.MessageID != "m1.md" || action.Sender != "orchestrator" || action.Recipient != "worker" || action.ReplyPolicy != "required" {
		t.Fatalf("action detail = %#v, want inbound m1 orchestrator->worker required", action)
	}
	if action.OpenedAt != now.Add(2*time.Second).Format(time.RFC3339Nano) || action.OpenedAtSource != MailboxProjectionDeliveredEventType {
		t.Fatalf("action opened evidence = %#v, want delivered timestamp/source", action)
	}
	if action.ReadAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("action read_at = %q, want read timestamp", action.ReadAt)
	}
	if len(got.WaitingOnInput) != 1 {
		t.Fatalf("waiting details = %#v, want one detail", got.WaitingOnInput)
	}
	waiting := got.WaitingOnInput[0]
	if waiting.Direction != "outbound" || waiting.MessageID != "m1.md" || waiting.Sender != "orchestrator" || waiting.Recipient != "worker" || waiting.ReplyPolicy != "required" {
		t.Fatalf("waiting detail = %#v, want outbound m1 orchestrator->worker required", waiting)
	}
	if waiting.OpenedAt != now.Add(time.Second).Format(time.RFC3339Nano) || waiting.OpenedAtSource != MailboxProjectionPostConsumedEventType {
		t.Fatalf("waiting opened evidence = %#v, want post-consumed timestamp/source", waiting)
	}
	if waiting.ReadAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("waiting read_at = %q, want recipient read timestamp", waiting.ReadAt)
	}

	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(5*time.Second))

	got, ok, err = ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() after reply error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() after reply ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required after reply = %d, want 0", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting after reply = %d, want 0", got.WaitingOnInputCounts["orchestrator"])
	}
	if got.InfoUnreadCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator info unread = %d, want 1", got.InfoUnreadCounts["orchestrator"])
	}
	if len(got.InputRequired) != 0 || len(got.WaitingOnInput) != 0 {
		t.Fatalf("input request details after reply = action:%#v waiting:%#v, want empty", got.InputRequired, got.WaitingOnInput)
	}
}

func TestProjectMessageInputRequestState_ReplyWithoutReplyToDoesNotResolve(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 25, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ExactFillResolvesRequiredMessage(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_123", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_123", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required = %d, want 0", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ExactFillWithMatchingReplyToResolvesRequiredMessage(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 15, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_123", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "m1.md", "", "ireq_123", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required = %d, want 0", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ExactInputRequestIgnoresReplyToFallback(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 30, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_123", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_MismatchedReplyToFailsExactClose(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 45, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_123", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "other.md", "", "ireq_123", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ReplyToMissDoesNotResolve(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 26, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "missing.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_TracksMultipleRecipients(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	workerRequest := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	criticRequest := inputRequestContent("orchestrator", "critic", "m2.md", "required", "", "please review")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", workerRequest, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", workerRequest, now.Add(2*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "orchestrator", "critic", criticRequest, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "orchestrator", "critic", criticRequest, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 2 {
		t.Fatalf("orchestrator waiting = %d, want 2", got.WaitingOnInputCounts["orchestrator"])
	}
	if got.InputRequiredCounts["worker"] != 1 || got.InputRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts = %#v, want worker=1 critic=1", got.InputRequiredCounts)
	}

	workerReply := inputRequestContent("worker", "orchestrator", "m3.md", "none", "m1.md", "ACK")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "worker", "orchestrator", workerReply, now.Add(5*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m3.md", "worker", "orchestrator", workerReply, now.Add(6*time.Second))

	got, ok, err = ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() after reply error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() after reply ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting after one reply = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
	if got.InputRequiredCounts["worker"] != 0 || got.InputRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts after one reply = %#v, want worker=0 critic=1", got.InputRequiredCounts)
	}
}

func TestProjectMessageInputRequestState_ReplyToDoesNotMatchSessionQualifiedRecipientBySimpleName(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 50, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "remote:worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "remote:worker", request, now.Add(time.Second))
	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(2*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ReplyToMatchesSessionQualifiedParticipant(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 51, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "remote:worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "remote:worker", request, now.Add(time.Second))
	reply := inputRequestContent("remote:worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "remote:worker", "orchestrator", reply, now.Add(2*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_SkipsIncompleteMailboxEvents(t *testing.T) {
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
	content := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", content, now.Add(2*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", content, now.Add(3*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_KeysInputRequestsByMessageAndRecipient(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 40, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	workerRequest := inputRequestContent("orchestrator", "worker", "broadcast.md", "required", "", "please work")
	criticRequest := inputRequestContent("orchestrator", "critic", "broadcast.md", "required", "", "please review")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "broadcast.md", "orchestrator", "worker", workerRequest, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "broadcast.md", "orchestrator", "worker", workerRequest, now.Add(2*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "broadcast.md", "orchestrator", "critic", criticRequest, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "broadcast.md", "orchestrator", "critic", criticRequest, now.Add(4*time.Second))

	workerReply := inputRequestContent("worker", "orchestrator", "worker-reply.md", "none", "broadcast.md", "ACK")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "worker-reply.md", "worker", "orchestrator", workerReply, now.Add(5*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "worker-reply.md", "worker", "orchestrator", workerReply, now.Add(6*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
	if got.InputRequiredCounts["worker"] != 0 || got.InputRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts = %#v, want worker=0 critic=1", got.InputRequiredCounts)
	}
}
