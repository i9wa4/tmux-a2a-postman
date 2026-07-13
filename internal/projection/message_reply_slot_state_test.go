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

func verdictContent(from, to, messageID, verdict, verdictOf, body string) string {
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: none\n" +
		"  verdict: " + verdict + "\n" +
		"  verdictOf: " + verdictOf + "\n" +
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

func TestProjectMessageInputRequestState_ReplayFixturesRebuildOpenFilledAndUncertainStates(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name                    string
		appendReply             func(t *testing.T, writer *journal.Writer, now time.Time)
		wantInputRequiredCount  int
		wantWaitingOnInputCount int
	}{
		{
			name:                    "open required request stays visible after replay",
			wantInputRequiredCount:  1,
			wantWaitingOnInputCount: 1,
		},
		{
			name: "exact fill closes the replayed request",
			appendReply: func(t *testing.T, writer *journal.Writer, now time.Time) {
				reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_replay_123", "DONE")
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(5*time.Second))
			},
			wantInputRequiredCount:  0,
			wantWaitingOnInputCount: 0,
		},
		{
			name: "missing exact fill target keeps the replayed request open",
			appendReply: func(t *testing.T, writer *journal.Writer, now time.Time) {
				reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_missing", "DONE")
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(5*time.Second))
			},
			wantInputRequiredCount:  1,
			wantWaitingOnInputCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionDir := t.TempDir()

			writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
			if err != nil {
				t.Fatalf("OpenShadowWriter() error = %v", err)
			}

			request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_replay_123", "", "please work")
			appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
			appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))
			appendInputRequestMailboxEvent(t, writer, MailboxProjectionReadEventType, "m1.md", "orchestrator", "worker", request, now.Add(3*time.Second))

			if tt.appendReply != nil {
				tt.appendReply(t, writer, now)
			}

			events, err := journal.Replay(sessionDir)
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}
			if len(events) == 0 {
				t.Fatal("Replay() returned no events, want persisted durable facts")
			}

			got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
			if err != nil {
				t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
			}
			if !ok {
				t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
			}
			if got.InputRequiredCounts["worker"] != tt.wantInputRequiredCount {
				t.Fatalf("worker action required = %d, want %d", got.InputRequiredCounts["worker"], tt.wantInputRequiredCount)
			}
			if got.WaitingOnInputCounts["orchestrator"] != tt.wantWaitingOnInputCount {
				t.Fatalf("orchestrator waiting = %d, want %d", got.WaitingOnInputCounts["orchestrator"], tt.wantWaitingOnInputCount)
			}
			if tt.wantInputRequiredCount == 1 {
				if len(got.InputRequired) != 1 || got.InputRequired[0].InputRequestID != "ireq_replay_123" {
					t.Fatalf("input required details = %#v, want replayed ireq_replay_123 left open", got.InputRequired)
				}
				if got.InputRequired[0].OpenedEventID == "" || got.InputRequired[0].ReadEventID == "" {
					t.Fatalf("input required detail = %#v, want replayable opened/read event ids", got.InputRequired[0])
				}
			}
		})
	}
}

func TestProjectMessageInputRequestState_ProjectRequestSatisfaction(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	filledRequest := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_filled", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", filledRequest, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", filledRequest, now.Add(2*time.Second))
	filledReply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "m1.md", "", "ireq_filled", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", filledReply, now.Add(10*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", filledReply, now.Add(11*time.Second))

	openRequest := inputRequestContentWithExact("orchestrator", "worker", "m3.md", "required", "", "ireq_open", "", "please also do this")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "orchestrator", "worker", openRequest, now.Add(20*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m3.md", "orchestrator", "worker", openRequest, now.Add(30*time.Second))

	got, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestStateAt() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestStateAt() ok = false, want true")
	}

	worker := got.RequestSatisfaction["worker"]
	if worker.OpenedCount != 2 || worker.FilledCount != 1 || worker.OpenCount != 1 || worker.StaleOpenCount != 1 {
		t.Fatalf("worker request satisfaction = %#v, want opened=2 filled=1 open=1 stale=1", worker)
	}
	if worker.AverageTimeToFillSeconds != 8 {
		t.Fatalf("worker average time to fill = %d, want 8", worker.AverageTimeToFillSeconds)
	}
	if worker.LongestOpenAgeSeconds != 3670 {
		t.Fatalf("worker longest open age = %d, want 3670", worker.LongestOpenAgeSeconds)
	}
	if worker.StaleAfterSeconds != 3600 {
		t.Fatalf("worker stale threshold = %d, want 3600", worker.StaleAfterSeconds)
	}
}

func TestProjectMessageInputRequestState_ProjectDeadLetteredRequestSatisfaction(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	deadLetteredRequest := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_deadlettered", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeadLetteredEventType, "m1.md", "orchestrator", "worker", deadLetteredRequest, now.Add(time.Second))

	got, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestStateAt() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestStateAt() ok = false, want true")
	}

	worker := got.RequestSatisfaction["worker"]
	if worker.OpenedCount != 1 || worker.FilledCount != 0 || worker.OpenCount != 1 || worker.DeadLetteredCount != 1 || worker.StaleOpenCount != 1 {
		t.Fatalf("worker request satisfaction = %#v, want opened=1 filled=0 open=1 dead_lettered=1 stale=1", worker)
	}
	if worker.LongestOpenAgeSeconds != 3699 {
		t.Fatalf("worker longest open age = %d, want 3699", worker.LongestOpenAgeSeconds)
	}
}

func TestProjectVerdictDebtState_OutgoingVerdictStampClearsDebt(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_verdict", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	fill := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_verdict", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", fill, now.Add(2*time.Second))

	before, ok, err := ProjectVerdictDebtState(sessionDir, "review", now.Add(10*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectVerdictDebtState(before) error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectVerdictDebtState(before) ok = false, want true")
	}
	if before.Requesters["orchestrator"].UnstampedCount != 1 {
		t.Fatalf("before debt = %#v, want one unstamped fill", before.Requesters["orchestrator"])
	}

	verdict := verdictContent("orchestrator", "worker", "m3.md", "pass", "ireq_verdict", "accepted")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "orchestrator", "worker", verdict, now.Add(3*time.Second))

	after, ok, err := ProjectVerdictDebtState(sessionDir, "review", now.Add(10*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectVerdictDebtState(after) error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectVerdictDebtState(after) ok = false, want true")
	}
	if after.Requesters["orchestrator"].UnstampedCount != 0 {
		t.Fatalf("after debt = %#v, want verdict stamp to clear debt", after.Requesters["orchestrator"])
	}
}

func TestInputRequestMetadataFromPayloadUsesDurableMetadataFallbacks(t *testing.T) {
	meta := inputRequestMetadataFromPayload(journal.MailboxEventPayload{
		ContextID:           "ctx-replay",
		MessageID:           "m1.md",
		From:                "orchestrator",
		To:                  "worker",
		ReplyPolicy:         "required",
		ReplyTo:             "previous.md",
		MessageType:         "task",
		Timestamp:           "2026-05-10T08:00:00Z",
		InputRequestID:      "ireq_123",
		FillsInputRequestID: "ireq_prev",
		InputRequestSetID:   "ireqset_1",
		BranchID:            "branch_1",
		CompletionRule:      "all",
	})

	if meta.ContextID != "ctx-replay" || meta.MessageID != "m1.md" || meta.From != "orchestrator" || meta.To != "worker" {
		t.Fatalf("identity metadata = %#v, want durable payload fallbacks", meta)
	}
	if meta.ReplyPolicy != "required" || meta.ReplyTo != "previous.md" || meta.MessageType != "task" || meta.Timestamp != "2026-05-10T08:00:00Z" {
		t.Fatalf("lifecycle metadata = %#v, want durable payload fallbacks", meta)
	}
	if meta.InputRequestID != "ireq_123" || meta.FillsInputRequestID != "ireq_prev" || meta.InputRequestSetID != "ireqset_1" || meta.BranchID != "branch_1" || meta.CompletionRule != "all" {
		t.Fatalf("input request metadata = %#v, want durable payload fallbacks", meta)
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
	if action.OpenedEventID == "" {
		t.Fatalf("action opened_event_id is empty, want durable journal event id")
	}
	if action.ReadAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("action read_at = %q, want read timestamp", action.ReadAt)
	}
	if action.ReadEventID == "" {
		t.Fatalf("action read_event_id is empty, want durable journal event id")
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
	if waiting.OpenedEventID == "" {
		t.Fatalf("waiting opened_event_id is empty, want durable journal event id")
	}
	if waiting.ReadAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("waiting read_at = %q, want recipient read timestamp", waiting.ReadAt)
	}
	if waiting.ReadEventID == "" {
		t.Fatalf("waiting read_event_id is empty, want durable journal event id")
	}
	if action.OpenedEventID == waiting.OpenedEventID {
		t.Fatalf("opened event ids should point to distinct delivered/post-consumed events, got %q", action.OpenedEventID)
	}
	if action.ReadEventID != waiting.ReadEventID {
		t.Fatalf("read event ids should point to the same read event: action=%q waiting=%q", action.ReadEventID, waiting.ReadEventID)
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
